package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/internal/attachment"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func attachmentPolicy(private, member bool) error {
	if member {
		return nil
	}
	if private {
		return apperror.ErrNotFound
	}
	return apperror.ErrForbidden
}
func attachmentRequest() *attachment.UploadRequest {
	return &attachment.UploadRequest{Filename: "file.txt", ContentType: "text/plain", SizeBytes: 10 << 20, SHA256: strings.Repeat("a", 64)}
}
func expireAttachment(t *testing.T, db *sqlx.DB, id int64) {
	t.Helper()
	if _, err := db.Exec(`UPDATE attachments SET created_at=clock_timestamp()-interval '20 minutes',upload_expires_at=clock_timestamp()-interval '15 minutes',cleanup_after=clock_timestamp()-interval '1 minute' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestAttachmentFoundationUpgradeAndLifecycle(t *testing.T) {
	db, dsn := phase7Database(t, 8)
	ctx := context.Background()
	owner := phase7User(t, db, "attachment_owner")
	other := phase7User(t, db, "attachment_other")
	chatRepo := chat.NewRepository(db)
	room := phase7Room(t, chatRepo, owner, "attachments", chat.RoomVisibilityPrivate)
	m, err := migrate.New("file://../../migrations", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		a, b := m.Close()
		if a != nil || b != nil {
			t.Error("close migrator failed")
		}
	}()
	if err := m.Migrate(9); err != nil {
		t.Fatal(err)
	}
	repo := attachment.NewRepository(db)
	queue := outbox.NewStore(db)
	row, err := repo.Reserve(ctx, room, owner, "attachment-key-001", strings.Repeat("b", 64), attachmentRequest(), attachmentPolicy)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := repo.Reserve(ctx, room, owner, "attachment-key-001", strings.Repeat("b", 64), attachmentRequest(), attachmentPolicy)
	if err != nil || retry.ID != row.ID || retry.ObjectKey != row.ObjectKey {
		t.Fatal("idempotent reservation changed")
	}
	if _, err := repo.Reserve(ctx, room, owner, "attachment-key-001", strings.Repeat("c", 64), attachmentRequest(), attachmentPolicy); !errors.Is(err, apperror.ErrConflict) {
		t.Fatal("conflicting retry accepted")
	}
	if _, err := repo.Get(ctx, room, other, row.ID, attachmentPolicy); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatal("private room disclosed")
	}
	if err := chatRepo.AddMember(ctx, room, other); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, room, other, row.ID, attachmentPolicy); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatal("other member inspected unbound upload")
	}
	for range 2 {
		result, err := repo.Transition(ctx, room, owner, row.ID, "complete", attachmentPolicy)
		if err != nil || result.State != "scanning" || result.Verified {
			t.Fatal("completion fabricated clean state")
		}
	}
	var count int
	if err := db.Get(&count, `SELECT COUNT(*) FROM domain_outbox WHERE kind='attachment.scan_requested'`); err != nil || count != 1 {
		t.Fatal("completion not idempotent")
	}
	for range 2 {
		if _, err := repo.Transition(ctx, room, owner, row.ID, "cancel", attachmentPolicy); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if job, err := queue.Claim(ctx, outbox.Cleanup); err != nil || job != nil {
		t.Fatal("cleanup before credential expiry")
	}
	// Cancelling cleanup does not consume the independent scan purpose.
	var state string
	if err := db.Get(&state, `SELECT state FROM domain_outbox_deliveries WHERE purpose='attachment_scan'`); err != nil || state != "pending" {
		t.Fatal("scan purpose changed")
	}
	expireAttachment(t, db, row.ID)
	if _, err := db.Exec(`UPDATE domain_outbox_deliveries SET available_at=clock_timestamp()-interval '1 second' WHERE purpose='attachment_cleanup'`); err != nil {
		t.Fatal(err)
	}
	if err := repo.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := queue.Claim(ctx, outbox.Cleanup)
	if err != nil || first == nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := db.Exec(`UPDATE domain_outbox_deliveries SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1 AND purpose='attachment_cleanup'`, first.EventID); err != nil {
		t.Fatal(err)
	}
	second, err := queue.Claim(ctx, outbox.Cleanup)
	if err != nil || second == nil || first.Token == second.Token {
		t.Fatal("lease reclaim failed")
	}
	if err := queue.FinishCleanup(ctx, first); err == nil {
		t.Fatal("stale worker acknowledged replacement lease")
	}
	if err := queue.Retry(ctx, outbox.Cleanup, first, "storage_unavailable"); err == nil {
		t.Fatal("stale worker rescheduled replacement lease")
	}
	if err := queue.FinishCleanup(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&state, `SELECT state FROM attachments WHERE id=$1`, row.ID); err != nil || state != "deleted" {
		t.Fatal("cleanup tombstone incomplete")
	}
	if err := m.Migrate(8); err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(9); err != nil {
		t.Fatal(err)
	}
	var existing bool
	if err := db.Get(&existing, `SELECT EXISTS(SELECT 1 FROM rooms WHERE id=$1)`, room); err != nil || !existing {
		t.Fatal("foundation rollback removed existing room")
	}
}

func TestAttachmentQuotaSerializesInt64Reservations(t *testing.T) {
	db, _ := phase7Database(t, 9)
	ctx := context.Background()
	// IDs beyond int32 must not overflow quota lock namespaces.
	if _, err := db.Exec(`SELECT setval('users_id_seq',3000000000); SELECT setval('rooms_id_seq',3000000000)`); err != nil {
		t.Fatal(err)
	}
	user := phase7User(t, db, "attachment_quota")
	room := phase7Room(t, chat.NewRepository(db), user, "quota", chat.RoomVisibilityPublic)
	repo := attachment.NewRepository(db)
	start := make(chan struct{})
	results := make(chan error, 11)
	var wg sync.WaitGroup
	for i := range 11 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := repo.Reserve(ctx, room, user, fmt.Sprintf("quota-request-%04d", i), strings.Repeat("a", 64), attachmentRequest(), attachmentPolicy)
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, apperror.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 10 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}

func TestAttachmentCleanupSurvivesParentDeletionAndOutboxFailure(t *testing.T) {
	db, _ := phase7Database(t, 9)
	ctx := context.Background()
	chatRepo := chat.NewRepository(db)
	owner := phase7User(t, db, "attachment_parent")
	uploader := phase7User(t, db, "attachment_uploader")
	room := phase7Room(t, chatRepo, owner, "orphan", chat.RoomVisibilityPublic)
	if err := chatRepo.AddMember(ctx, room, uploader); err != nil {
		t.Fatal(err)
	}
	repo := attachment.NewRepository(db)
	row, err := repo.Reserve(ctx, room, uploader, "orphan-request-001", strings.Repeat("a", 64), attachmentRequest(), attachmentPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE domain_outbox ADD CONSTRAINT test_reject_cleanup CHECK(kind<>'attachment.cleanup_requested')`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Transition(ctx, room, uploader, row.ID, "cancel", attachmentPolicy); err == nil {
		t.Fatal("outbox failure ignored")
	}
	var state string
	if err := db.Get(&state, `SELECT state FROM attachments WHERE id=$1`, row.ID); err != nil || state != "pending_upload" {
		t.Fatal("partial cancellation committed")
	}
	if _, err := db.Exec(`ALTER TABLE domain_outbox DROP CONSTRAINT test_reject_cleanup`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id=$1`, uploader); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM rooms WHERE id=$1`, room); err != nil {
		t.Fatal(err)
	}
	var key string
	if err := db.Get(&key, `SELECT object_key FROM attachments WHERE id=$1 AND room_id IS NULL AND uploader_id IS NULL`, row.ID); err != nil || key != row.ObjectKey {
		t.Fatal("cascade lost cleanup identity")
	}
	expireAttachment(t, db, row.ID)
	if err := repo.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	queue := outbox.NewStore(db)
	job, err := queue.Claim(ctx, outbox.Cleanup)
	if err != nil || job == nil || job.ObjectKey != key {
		t.Fatal("orphan cleanup missing")
	}
	if _, err := db.Exec(`UPDATE domain_outbox_deliveries SET attempts=8,lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1`, job.EventID); err != nil {
		t.Fatal(err)
	}
	if next, err := queue.Claim(ctx, outbox.Cleanup); err != nil || next != nil {
		t.Fatal("exhausted lease reclaimed")
	}
	if err := db.Get(&state, `SELECT state FROM domain_outbox_deliveries WHERE event_id=$1`, job.EventID); err != nil || state != "dead" {
		t.Fatal("exhausted work not dead-lettered")
	}
}
