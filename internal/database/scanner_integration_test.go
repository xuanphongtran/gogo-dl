package database

import (
	"context"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/xuanphongtran/gogo-dl/internal/attachment"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
)

func TestScannerReadyFencingAndCleanup(t *testing.T) {
	db, dsn := phase7Database(t, 10)
	ctx := context.Background()
	owner := phase7User(t, db, "scan_owner")
	chatRepo := chat.NewRepository(db)
	room := phase7Room(t, chatRepo, owner, "scanner", chat.RoomVisibilityPrivate)
	m, err := migrate.New("file://../../migrations", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Migrate(11); err != nil {
		t.Fatal(err)
	}
	repo := attachment.NewRepository(db)
	scans := attachment.NewScanRepository(db)
	queue := outbox.NewStore(db)
	row, err := repo.Reserve(ctx, room, owner, "scanner-ready-key", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", attachmentRequest(), attachmentPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Transition(ctx, room, owner, row.ID, "complete", attachmentPolicy); err != nil {
		t.Fatal(err)
	}
	job, err := queue.Claim(ctx, outbox.Scan)
	if err != nil || job == nil {
		t.Fatalf("claim err=%v", err)
	}
	var seconds float64
	if err := db.Get(&seconds, `SELECT EXTRACT(EPOCH FROM lease_until-clock_timestamp()) FROM domain_outbox_deliveries WHERE event_id=$1 AND purpose='attachment_scan'`, job.EventID); err != nil || seconds < 50 {
		t.Fatalf("lease seconds=%v err=%v", seconds, err)
	}
	prepared, err := scans.PrepareScan(ctx, job)
	if err != nil || prepared == nil {
		t.Fatalf("prepare err=%v", err)
	}
	if _, err := db.Exec(`UPDATE domain_outbox_deliveries SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1`, job.EventID); err != nil {
		t.Fatal(err)
	}
	if err := scans.FinishScan(ctx, job, "clean"); err == nil {
		t.Fatal("stale scan marked ready")
	}
	current, err := queue.Claim(ctx, outbox.Scan)
	if err != nil || current == nil {
		t.Fatal("not reclaimed")
	}
	if _, err := scans.PrepareScan(ctx, current); err != nil {
		t.Fatal(err)
	}
	if err := scans.FinishScan(ctx, current, "clean"); err != nil {
		t.Fatal(err)
	}
	ready, err := attachment.NewService(repo, nil).Get(ctx, owner, room, row.ID)
	if err != nil || ready.State != "ready" || !ready.Verified || ready.ExpiresAt == nil || time.Until(*ready.ExpiresAt) < 23*time.Hour {
		t.Fatalf("ready=%+v err=%v", ready, err)
	}
	if _, err := repo.Transition(ctx, room, owner, row.ID, "complete", attachmentPolicy); err != nil {
		t.Fatal("complete not idempotent")
	}
	if err := m.Migrate(10); err == nil {
		t.Fatal("unsafe downgrade with promoted object succeeded")
	}
	// A failed downgrade leaves golang-migrate dirty. Repair only this owned test
	// fixture to continue checking cleanup; never force production migration state.
	if err := m.Force(11); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Transition(ctx, room, owner, row.ID, "cancel", attachmentPolicy); err != nil {
		t.Fatal(err)
	}
	if err := scans.FinishScan(ctx, current, "clean"); err == nil {
		t.Fatal("completed lease reused")
	}
}
func TestScannerRejectsCancelledAndRemovedMembership(t *testing.T) {
	for _, action := range []string{"cancel", "leave"} {
		t.Run(action, func(t *testing.T) {
			db, dsn := phase7Database(t, 10)
			ctx := context.Background()
			owner := phase7User(t, db, "scan_cancel_owner")
			roomOwner := phase7User(t, db, "scan_room_owner")
			chatRepo := chat.NewRepository(db)
			room := phase7Room(t, chatRepo, roomOwner, "scan cancel", chat.RoomVisibilityPublic)
			if _, err := chatRepo.JoinPublicRoom(ctx, room, owner); err != nil {
				t.Fatal(err)
			}
			m, err := migrate.New("file://../../migrations", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if err := m.Migrate(11); err != nil {
				t.Fatal(err)
			}
			repo := attachment.NewRepository(db)
			scans := attachment.NewScanRepository(db)
			queue := outbox.NewStore(db)
			row, err := repo.Reserve(ctx, room, owner, "scanner-cancel-key", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", attachmentRequest(), attachmentPolicy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repo.Transition(ctx, room, owner, row.ID, "complete", attachmentPolicy); err != nil {
				t.Fatal(err)
			}
			job, err := queue.Claim(ctx, outbox.Scan)
			if err != nil || job == nil {
				t.Fatal("claim failed")
			}
			if _, err := scans.PrepareScan(ctx, job); err != nil {
				t.Fatal(err)
			}
			if action == "cancel" {
				if _, err := repo.Transition(ctx, room, owner, row.ID, "cancel", attachmentPolicy); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := db.Exec(`DELETE FROM room_members WHERE room_id=$1 AND user_id=$2`, room, owner); err != nil {
					t.Fatal(err)
				}
			}
			if err := scans.FinishScan(ctx, job, "clean"); err != nil {
				t.Fatal(err)
			}
			var state string
			if err := db.Get(&state, `SELECT state FROM attachments WHERE id=$1`, row.ID); err != nil || state == "ready" {
				t.Fatal("revoked/cancelled scan marked ready")
			}
		})
	}
}
