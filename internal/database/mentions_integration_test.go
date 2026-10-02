package database

import (
	"context"
	"testing"

	"github.com/xuanphongtran/gogo-dl/internal/chat"
)

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
}
