package database

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/internal/attachment"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
)

// This store models idempotent provider DELETE; it makes no R2 capability claim.
type recoveryObjectStore struct {
	attachment.ObjectStore
	mu             sync.Mutex
	key            string
	exists         bool
	calls, effects int
	finalCalls     int
	fail           bool
}

func (s *recoveryObjectStore) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == strings.Replace(s.key, "quarantine/", "final/", 1) {
		s.finalCalls++
		if s.fail {
			return errors.New("simulated provider outage")
		}
		return nil // No final object was created by these schema-10 fixtures.
	}
	if key != s.key {
		return errors.New("unexpected cleanup identity")
	}
	s.calls++
	if s.fail {
		return errors.New("simulated provider outage")
	}
	if s.exists {
		s.exists = false
		s.effects++
	}
	return nil
}

func waitCleanupState(t *testing.T, ctx context.Context, db *sqlx.DB, event, want string) {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var state string
		if err := db.GetContext(ctx, &state, `SELECT state FROM domain_outbox_deliveries WHERE event_id=$1 AND purpose='attachment_cleanup'`, event); err != nil {
			t.Fatal(err)
		}
		if state == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("cleanup state=%s, want %s before deadline", state, want)
		case <-tick.C:
		}
	}
}

func startRecoveryCleanup(t *testing.T, ctx context.Context, dsn string, store attachment.ObjectStore) func() {
	t.Helper()
	db, err := sqlx.ConnectContext(ctx, "postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	worker := attachment.NewWorker(attachment.NewRepository(db), outbox.NewStore(db), store)
	go func() { defer close(done); worker.Run(workerCtx) }()
	stop := sync.OnceFunc(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("cleanup worker did not stop")
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(stop)
	return stop
}

func TestCleanupWorkerRecoversAfterConnectionAndWorkerRestart(t *testing.T) {
	for _, stage := range []string{"before provider delete", "after provider delete before acknowledgement", "provider outage after restart"} {
		t.Run(stage, func(t *testing.T) {
			db, dsn := phase7Database(t, 10)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			owner := phase7User(t, db, "cleanup_recovery_owner")
			room := phase7Room(t, chat.NewRepository(db), owner, "cleanup recovery", chat.RoomVisibilityPrivate)
			// Seed notification work from the real mention producer. A separate broker
			// checkpoint models retained future relay progress; no broker runs here.
			recipient := phase7User(t, db, "cleanup_recovery_recipient")
			chatRepo := chat.NewMentionRepository(db)
			if err := chatRepo.AddMember(ctx, room, recipient); err != nil {
				t.Fatal(err)
			}
			if _, err := chat.NewService(chatRepo, nil).SendMessage(ctx, owner, room, &chat.SendMessageRequest{
				Content: "cleanup recovery fixture", MentionUserIDs: []int64{recipient}, IdempotencyKey: "cleanup-mention-key",
			}, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO domain_outbox_deliveries(event_id,purpose)
                SELECT event_id,'broker' FROM domain_outbox WHERE kind='message.mentioned' AND user_id=$1`, recipient); err != nil {
				t.Fatal(err)
			}
			repo := attachment.NewRepository(db)
			row, err := repo.Reserve(ctx, room, owner, "cleanup-recovery-key", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", attachmentRequest(), attachmentPolicy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repo.Transition(ctx, room, owner, row.ID, "complete", attachmentPolicy); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.Transition(ctx, room, owner, row.ID, "cancel", attachmentPolicy); err != nil {
				t.Fatal(err)
			}
			expireAttachment(t, db, row.ID)
			if _, err := db.ExecContext(ctx, `UPDATE domain_outbox_deliveries SET available_at=clock_timestamp()-interval '1 second' WHERE purpose='attachment_cleanup'`); err != nil {
				t.Fatal(err)
			}
			if err := repo.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			var brokerBefore int
			if err := db.GetContext(ctx, &brokerBefore, `SELECT count(*) FROM domain_outbox_deliveries WHERE purpose='broker' AND state='pending'`); err != nil || brokerBefore == 0 {
				t.Fatalf("broker baseline=%d err=%v", brokerBefore, err)
			}

			// A first process claims, then disappears without recording completion.
			oldDB, err := sqlx.ConnectContext(ctx, "postgres", dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := oldDB.Close(); err != nil {
					t.Error(err)
				}
			})
			abandoned, err := outbox.NewStore(oldDB).Claim(ctx, outbox.Cleanup)
			if err != nil || abandoned == nil {
				t.Fatalf("initial cleanup claim=%v err=%v", abandoned, err)
			}
			provider := &recoveryObjectStore{key: row.ObjectKey, exists: true}
			if stage == "after provider delete before acknowledgement" {
				if err := provider.Delete(ctx, row.ObjectKey); err != nil {
					t.Fatal(err)
				}
			}
			if err := oldDB.Close(); err != nil {
				t.Fatal(err)
			}

			// A live lease must survive the process/connection loss. Do not steal it.
			if job, err := outbox.NewStore(db).Claim(ctx, outbox.Cleanup); err != nil || job != nil {
				t.Fatalf("unexpired lease reclaimed: job=%v err=%v", job, err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE domain_outbox_deliveries SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1 AND purpose='attachment_cleanup'`, abandoned.EventID); err != nil {
				t.Fatal(err)
			}
			queue := outbox.NewStore(db)
			if err := queue.FinishCleanup(ctx, abandoned); err == nil {
				t.Fatal("expired worker acknowledged cleanup")
			}
			if err := queue.Retry(ctx, outbox.Cleanup, abandoned, "storage_unavailable"); err == nil {
				t.Fatal("expired worker rescheduled cleanup")
			}

			if stage == "provider outage after restart" {
				provider.fail = true
				stop := startRecoveryCleanup(t, ctx, dsn, provider)
				waitCleanupState(t, ctx, db, abandoned.EventID, "pending")
				stop()
				var category string
				if err := db.GetContext(ctx, &category, `SELECT last_error FROM domain_outbox_deliveries WHERE event_id=$1 AND purpose='attachment_cleanup'`, abandoned.EventID); err != nil || category != "storage_unavailable" {
					t.Fatalf("retry category=%s err=%v", category, err)
				}
				var delayed bool
				if err := db.GetContext(ctx, &delayed, `SELECT available_at>clock_timestamp() FROM domain_outbox_deliveries WHERE event_id=$1 AND purpose='attachment_cleanup'`, abandoned.EventID); err != nil || !delayed {
					t.Fatalf("retry has no backoff: delayed=%v err=%v", delayed, err)
				}
				provider.fail = false
				// Advance only fixture scheduling, not real time or the application policy.
				if _, err := db.ExecContext(ctx, `UPDATE domain_outbox_deliveries SET available_at=clock_timestamp()-interval '1 second' WHERE event_id=$1 AND purpose='attachment_cleanup'`, abandoned.EventID); err != nil {
					t.Fatal(err)
				}
			}
			stop := startRecoveryCleanup(t, ctx, dsn, provider)
			waitCleanupState(t, ctx, db, abandoned.EventID, "done")
			stop()
			if err := queue.FinishCleanup(ctx, abandoned); err == nil {
				t.Fatal("stale completion accepted after recovery")
			}
			var state, key string
			if err := db.QueryRowxContext(ctx, `SELECT state,object_key FROM attachments WHERE id=$1`, row.ID).Scan(&state, &key); err != nil || state != "deleted" || key != row.ObjectKey {
				t.Fatalf("tombstone state=%s identity preserved=%v err=%v", state, key == row.ObjectKey, err)
			}
			var heldBytes int64
			if err := db.GetContext(ctx, &heldBytes, `SELECT COALESCE(sum(size_bytes),0) FROM attachments WHERE uploader_id=$1 AND state<>'deleted'`, owner); err != nil || heldBytes != 0 {
				t.Fatalf("retained quota=%d err=%v", heldBytes, err)
			}
			var brokerAfter, scanPending, notificationPending int
			if err := db.GetContext(ctx, &brokerAfter, `SELECT count(*) FROM domain_outbox_deliveries WHERE purpose='broker' AND state='pending'`); err != nil || brokerAfter != brokerBefore {
				t.Fatalf("broker progress changed: before=%d after=%d err=%v", brokerBefore, brokerAfter, err)
			}
			if err := db.GetContext(ctx, &scanPending, `SELECT count(*) FROM domain_outbox_deliveries WHERE purpose='attachment_scan' AND state='pending'`); err != nil || scanPending != 1 {
				t.Fatalf("scan progress=%d err=%v", scanPending, err)
			}
			if err := db.GetContext(ctx, &notificationPending, `SELECT count(*) FROM domain_outbox_deliveries WHERE purpose='notification' AND state='pending'`); err != nil || notificationPending != 1 {
				t.Fatalf("notification progress=%d err=%v", notificationPending, err)
			}
			provider.mu.Lock()
			calls, effects, exists, finalCalls := provider.calls, provider.effects, provider.exists, provider.finalCalls
			provider.mu.Unlock()
			wantCalls := 1
			if stage != "before provider delete" {
				wantCalls = 2
			}
			if calls != wantCalls || effects != 1 || exists || finalCalls != 1 {
				t.Fatalf("provider calls=%d effects=%d exists=%v", calls, effects, exists)
			}
			if job, err := queue.Claim(ctx, outbox.Cleanup); err != nil || job != nil {
				t.Fatalf("completed cleanup reclaimed: job=%v err=%v", job, err)
			}
		})
	}
}
