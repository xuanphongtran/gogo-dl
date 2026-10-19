package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/xuanphongtran/gogo-dl/internal/attachment"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestAttachmentBindingMigrationUpgradeAndEmptyDowngrade(t *testing.T) {
	db, dsn := phase7Database(t, 12)
	owner := phase7User(t, db, "binding_upgrade")
	room := phase7Room(t, chat.NewRepository(db), owner, "binding upgrade", chat.RoomVisibilityPublic)
	m, err := migrate.New("file://../../migrations", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		sourceErr, dbErr := m.Close()
		if sourceErr != nil || dbErr != nil {
			t.Errorf("close migrator: %v %v", sourceErr, dbErr)
		}
	}()
	for _, version := range []uint{13, 12, 13} {
		if err := m.Migrate(version); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := db.Get(&count, `SELECT count(*) FROM room_members WHERE room_id=$1 AND user_id=$2`, room, owner); err != nil || count != 1 {
			t.Fatalf("membership lost at schema%d: count=%d err=%v", version, count, err)
		}
	}
}

func TestAttachmentBindingDownGuardPreservesLiveBinding(t *testing.T) {
	db, _ := phase7Database(t, 13)
	owner := phase7User(t, db, "binding_down_guard")
	repo := chat.NewAttachmentRepository(db)
	room := phase7Room(t, repo, owner, "down guard", chat.RoomVisibilityPublic)
	file := boundReadyFile(t, db, room, owner)
	if _, err := chat.NewService(repo, nil).SendMessage(context.Background(), owner, room, &chat.SendMessageRequest{AttachmentIDs: []int64{file.ID}, IdempotencyKey: "binding-down-guard-key"}, ""); err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile("../../migrations/000013_message_attachments.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the actual down SQL in an explicitly rolled-back transaction:
	// no dirty migrate metadata and no production-style Force bypass are needed.
	tx, err := db.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	_, downErr := tx.Exec(string(down))
	rollbackErr := tx.Rollback()
	if rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	if downErr == nil {
		t.Fatal("down migration accepted a live file binding")
	}
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM attachments WHERE id=$1 AND state='attached' AND message_id IS NOT NULL`, file.ID); err != nil || count != 1 {
		t.Fatalf("guard damaged live binding: count=%d err=%v", count, err)
	}
}

func TestBoundQuotaSeparatesAttachedFromPendingSourceBytes(t *testing.T) {
	db, _ := phase7Database(t, 13)
	ctx := context.Background()
	owner := phase7User(t, db, "binding_quota")
	repo := chat.NewAttachmentRepository(db)
	room := phase7Room(t, repo, owner, "quota", chat.RoomVisibilityPublic)
	svc := chat.NewService(repo, nil)
	for i := 0; i < 11; i++ {
		file := boundReadyFile(t, db, room, owner)
		// Original logical size remains authoritative despite smaller output.
		if _, err := db.Exec(`UPDATE attachments SET size_bytes=$2 WHERE id=$1`, file.ID, 10<<20); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SendMessage(ctx, owner, room, &chat.SendMessageRequest{AttachmentIDs: []int64{file.ID}, IdempotencyKey: fmt.Sprintf("binding-quota-key-%02d", i)}, ""); err != nil {
			t.Fatal(err)
		}
	}
	files := attachment.NewBoundRepository(db)
	reserve := func(size int64) error {
		_, err := files.Reserve(ctx, room, owner, uuid.NewString(), strings.Repeat("a", 64), &attachment.UploadRequest{Filename: "quota.txt", ContentType: "text/plain", SizeBytes: size, SHA256: strings.Repeat("b", 64)}, attachmentPolicy)
		return err
	}
	for i := 0; i < 10; i++ {
		if err := reserve(10 << 20); err != nil {
			t.Fatalf("bound files consumed pending quota at %d: %v", i, err)
		}
	}
	if err := reserve(1); !errors.Is(err, apperror.ErrConflict) {
		t.Fatalf("pending source quota not enforced: %v", err)
	}
}
