package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/notification"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestMentionMigrationUpgradePreservesMembers(t *testing.T) {
	db, dsn := phase7Database(t, 9)
	owner := phase7User(t, db, "upgrade_owner")
	repo := chat.NewRepository(db)
	room := phase7Room(t, repo, owner, "upgrade", chat.RoomVisibilityPublic)
	m, err := migrate.New("file://../../migrations", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		sourceErr, databaseErr := m.Close()
		if sourceErr != nil || databaseErr != nil {
			t.Errorf("close migrator: %v %v", sourceErr, databaseErr)
		}
	}()
	for _, version := range []uint{10, 9, 10} {
		if err := m.Migrate(version); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := db.Get(&count, `SELECT count(*) FROM room_members WHERE room_id=$1 AND user_id=$2`, room, owner); err != nil || count != 1 {
			t.Fatalf("members at version %d=%d err=%v", version, count, err)
		}
		if version == 10 {
			var generation int64
			if err := db.Get(&generation, `SELECT membership_generation FROM room_members WHERE room_id=$1 AND user_id=$2`, room, owner); err != nil || generation <= 0 {
				t.Fatalf("backfill generation=%d err=%v", generation, err)
			}
		}
	}
}

func TestMentionsAtomicSendAndRetry(t *testing.T) {
	db, _ := phase7Database(t, 10)
	ctx := context.Background()
	owner := phase7User(t, db, "mention_owner")
	recipient := phase7User(t, db, "mention_recipient")
	repo := chat.NewMentionRepository(db)
	room := phase7Room(t, repo, owner, "mentions", chat.RoomVisibilityPublic)
	if err := repo.AddMember(ctx, room, recipient); err != nil {
		t.Fatal(err)
	}
	svc := chat.NewService(repo, nil)
	key := "mention-retry-key-1"
	msg, err := svc.SendMessage(ctx, owner, room, &chat.SendMessageRequest{Content: "hello", MentionUserIDs: []int64{recipient}, IdempotencyKey: key}, "")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := svc.SendMessage(ctx, owner, room, &chat.SendMessageRequest{Content: "hello", MentionUserIDs: []int64{recipient}, IdempotencyKey: key}, "")
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != msg.ID {
		t.Fatalf("retry id = %d, want %d", retry.ID, msg.ID)
	}
	var mentions, intents, keys int
	if err := db.Get(&mentions, `SELECT count(*) FROM message_mentions WHERE message_id=$1`, msg.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&intents, `SELECT count(*) FROM domain_outbox WHERE kind='message.mentioned' AND message_id=$1`, msg.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&keys, `SELECT count(*) FROM message_send_keys WHERE user_id=$1 AND request_key=$2`, owner, key); err != nil {
		t.Fatal(err)
	}
	if mentions != 1 || intents != 1 || keys != 1 {
		t.Fatalf("mentions=%d intents=%d keys=%d, want 1 each", mentions, intents, keys)
	}
	if _, err := svc.SendMessage(ctx, owner, room, &chat.SendMessageRequest{Content: "changed", MentionUserIDs: []int64{recipient}, IdempotencyKey: key}, ""); !errors.Is(err, apperror.ErrConflict) {
		t.Fatalf("different request retry: %v", err)
	}
	if _, err := svc.SendMessage(ctx, owner, room, &chat.SendMessageRequest{Content: "invalid", MentionUserIDs: []int64{recipient, 999999}, IdempotencyKey: "invalid-recipient-key"}, ""); !errors.Is(err, apperror.ErrInvalidRequest) {
		t.Fatalf("invalid recipient: %v", err)
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM messages`); err != nil || count != 1 {
		t.Fatalf("partial send count=%d err=%v", count, err)
	}
	self, err := svc.SendMessage(ctx, owner, room, &chat.SendMessageRequest{Content: "self", MentionUserIDs: []int64{owner}, IdempotencyKey: "self-mention-test-key"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&count, `SELECT count(*) FROM domain_outbox WHERE message_id=$1`, self.ID); err != nil || count != 0 {
		t.Fatalf("self notification count=%d err=%v", count, err)
	}
	if _, err := db.Exec(`UPDATE message_send_keys SET expires_at=clock_timestamp()-interval '1 second' WHERE request_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	fresh, err := svc.SendMessage(ctx, owner, room, &chat.SendMessageRequest{Content: "after expiry", MentionUserIDs: []int64{recipient}, IdempotencyKey: key}, "")
	if err != nil || fresh.ID == msg.ID {
		t.Fatalf("expired key send=%v err=%v", fresh, err)
	}
	if err := repo.TransferOwnership(ctx, room, owner, recipient); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM room_members WHERE room_id=$1 AND user_id=$2`, room, owner); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SendMessage(ctx, owner, room, &chat.SendMessageRequest{Content: "after expiry", MentionUserIDs: []int64{recipient}, IdempotencyKey: key}, ""); !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("unauthorized retry=%v", err)
	}
}

func TestMentionPreferenceGenerationAndPrivacy(t *testing.T) {
	db, _ := phase7Database(t, 10)
	ctx := context.Background()
	owner := phase7User(t, db, "preference_owner")
	member := phase7User(t, db, "preference_member")
	repo := chat.NewMentionRepository(db)
	room := phase7Room(t, repo, owner, "public", chat.RoomVisibilityPublic)
	private := phase7Room(t, repo, owner, "private", chat.RoomVisibilityPrivate)
	svc := notification.NewService(notification.NewRepository(db))
	if _, err := svc.RoomPreference(ctx, member, room, nil); !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("public non-member=%v", err)
	}
	if _, err := svc.RoomPreference(ctx, member, private, nil); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("private non-member=%v", err)
	}
	if err := repo.AddMember(ctx, room, member); err != nil {
		t.Fatal(err)
	}
	muted := true
	if _, err := svc.RoomPreference(ctx, member, room, &muted); err != nil {
		t.Fatal(err)
	}
	var oldGeneration int64
	if err := db.Get(&oldGeneration, `SELECT membership_generation FROM room_members WHERE room_id=$1 AND user_id=$2`, room, member); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM room_members WHERE room_id=$1 AND user_id=$2`, room, member); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddMember(ctx, room, member); err != nil {
		t.Fatal(err)
	}
	pref, err := svc.RoomPreference(ctx, member, room, nil)
	if err != nil || pref.Muted {
		t.Fatalf("rejoined preference=%v err=%v", pref, err)
	}
	var generation int64
	if err := db.Get(&generation, `SELECT membership_generation FROM room_members WHERE room_id=$1 AND user_id=$2`, room, member); err != nil || generation == oldGeneration {
		t.Fatalf("generation unchanged=%d err=%v", generation, err)
	}
}

func TestMentionConcurrentRetryCreatesOneMessage(t *testing.T) {
	db, _ := phase7Database(t, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner := phase7User(t, db, "concurrent_owner")
	recipient := phase7User(t, db, "concurrent_recipient")
	repo := chat.NewMentionRepository(db)
	room := phase7Room(t, repo, owner, "concurrent", chat.RoomVisibilityPublic)
	if err := repo.AddMember(ctx, room, recipient); err != nil {
		t.Fatal(err)
	}
	svc := chat.NewService(repo, nil)
	start := make(chan struct{})
	type result struct {
		message *chat.Message
		err     error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			m, err := svc.SendMessage(ctx, owner, room, &chat.SendMessageRequest{Content: "hello", MentionUserIDs: []int64{recipient}, IdempotencyKey: "concurrent-retry-key"}, "")
			results <- result{m, err}
		}()
	}
	close(start)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil {
		t.Fatalf("concurrent sends: %v %v", a.err, b.err)
	}
	if a.message.ID != b.message.ID {
		t.Fatalf("duplicate messages: %d %d", a.message.ID, b.message.ID)
	}
	var count int
	if err := db.GetContext(ctx, &count, `SELECT count(*) FROM domain_outbox WHERE kind='message.mentioned'`); err != nil || count != 1 {
		t.Fatalf("concurrent intents=%d err=%v", count, err)
	}
}
