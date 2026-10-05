package notification

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// A private schema avoids interference with the database package's migration
// tests running concurrently. Only an explicitly named test database is accepted.
func notificationDatabase(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), "_test") || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("notification tests require a PostgreSQL database ending in _test")
	}
	root, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	schema := "notification_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := root.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := root.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Error(err)
		}
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sqlx.Connect("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	files, err := filepath.Glob("../../migrations/*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(file), err)
		}
	}
	return db
}

type mentionFixture struct {
	db                              *sqlx.DB
	worker                          *Worker
	queue                           *outbox.Store
	owner, recipient, room, message int64
	job                             *outbox.Job
}

func newMentionFixture(t *testing.T) mentionFixture {
	t.Helper()
	db := notificationDatabase(t)
	f := mentionFixture{db: db, queue: outbox.NewStore(db)}
	f.worker = NewWorker(db, f.queue, nil)
	for i, dst := range []*int64{&f.owner, &f.recipient} {
		if err := db.QueryRow(`INSERT INTO users(username,email,password_hash) VALUES($1,$2,'hash') RETURNING id`, []string{"owner", "recipient"}[i], []string{"owner@example.com", "recipient@example.com"}[i]).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	repo := chat.NewMentionRepository(db)
	r := &chat.Room{Name: "mentions", CreatedBy: f.owner, Visibility: chat.RoomVisibilityPublic}
	if err := repo.CreateRoomWithMember(context.Background(), r, f.owner); err != nil {
		t.Fatal(err)
	}
	f.room = r.ID
	if err := repo.AddMember(context.Background(), f.room, f.recipient); err != nil {
		t.Fatal(err)
	}
	m, err := chat.NewService(repo, nil).SendMessage(context.Background(), f.owner, f.room, &chat.SendMessageRequest{Content: "hello", MentionUserIDs: []int64{f.recipient}, IdempotencyKey: "notification-test-key"}, "")
	if err != nil {
		t.Fatal(err)
	}
	f.message = m.ID
	f.job, err = f.queue.Claim(context.Background(), outbox.Notification)
	if err != nil || f.job == nil {
		t.Fatalf("claim: job=%v err=%v", f.job, err)
	}
	return f
}

func TestWorkerDeliveryAndInboxRecovery(t *testing.T) {
	f := newMentionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.worker.deliver(ctx, f.job); err != nil {
		t.Fatal(err)
	}
	svc := NewService(NewRepository(f.db))
	feed, err := svc.List(ctx, f.recipient, Query{})
	if err != nil || len(feed.Notifications) != 1 {
		t.Fatalf("feed=%v err=%v", feed, err)
	}
	n := feed.Notifications[0]
	if n.MessageID != f.message || n.Availability != "available" {
		t.Fatalf("notification=%+v", n)
	}
	if _, err := svc.Read(ctx, f.owner, n.ID); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("cross-owner read=%v", err)
	}
	read, err := svc.Read(ctx, f.recipient, n.ID)
	if err != nil || read.ReadAt == nil {
		t.Fatalf("read=%v err=%v", read, err)
	}
	again, err := svc.Read(ctx, f.recipient, n.ID)
	if err != nil || !again.ReadAt.Equal(*read.ReadAt) {
		t.Fatalf("retry read=%v err=%v", again, err)
	}
	var intents int
	if err := f.db.Get(&intents, `SELECT count(*) FROM domain_outbox WHERE kind='notification.created'`); err != nil || intents != 1 {
		t.Fatalf("private intents=%d err=%v", intents, err)
	}
	brokerJob, err := f.queue.Claim(ctx, "broker")
	if err != nil || brokerJob == nil || brokerJob.EventID == f.job.EventID || brokerJob.AttachmentID != 0 {
		t.Fatalf("independent private intent claim=%v err=%v", brokerJob, err)
	}
	// Simulate a recovered delivery: duplicate processing must preserve read state
	// and create neither a second feed row nor a second private intent.
	if _, err := f.db.Exec(`UPDATE domain_outbox_deliveries SET state='pending',lease_token=NULL,lease_until=NULL WHERE event_id=$1 AND purpose='notification'`, f.job.EventID); err != nil {
		t.Fatal(err)
	}
	f.job, err = f.queue.Claim(ctx, outbox.Notification)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.worker.deliver(ctx, f.job); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Get(&intents, `SELECT count(*) FROM domain_outbox WHERE kind='notification.created'`); err != nil || intents != 1 {
		t.Fatalf("duplicate intents=%d err=%v", intents, err)
	}
	var brokerState string
	if err := f.db.Get(&brokerState, `SELECT state FROM domain_outbox_deliveries WHERE event_id=$1 AND purpose='broker'`, brokerJob.EventID); err != nil || brokerState != "leased" {
		t.Fatalf("notification worker changed broker progress: %s err=%v", brokerState, err)
	}
	if _, err := f.db.Exec(`UPDATE messages SET content='',deleted_at=clock_timestamp() WHERE id=$1`, f.message); err != nil {
		t.Fatal(err)
	}
	feed, err = svc.List(ctx, f.recipient, Query{})
	if err != nil || len(feed.Notifications) != 1 || feed.Notifications[0].Availability != "deleted" {
		t.Fatalf("deleted feed=%v err=%v", feed, err)
	}
	if _, err := f.db.Exec(`DELETE FROM room_members WHERE room_id=$1 AND user_id=$2`, f.room, f.recipient); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO room_members(room_id,user_id) VALUES($1,$2)`, f.room, f.recipient); err != nil {
		t.Fatal(err)
	}
	feed, err = svc.List(ctx, f.recipient, Query{})
	if err != nil || len(feed.Notifications) != 0 {
		t.Fatalf("rejoined feed=%v err=%v", feed, err)
	}
	if _, err := svc.Read(ctx, f.recipient, n.ID); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("old-generation read=%v", err)
	}
}

func TestWorkerSuppression(t *testing.T) {
	for _, scenario := range []string{"disabled", "muted", "rejoined", "deleted", "account deleted", "room deleted", "message removed"} {
		t.Run(scenario, func(t *testing.T) {
			f := newMentionFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			svc := NewService(NewRepository(f.db))
			var err error
			switch scenario {
			case "disabled":
				v := false
				_, err = svc.GlobalPreference(ctx, f.recipient, &v)
			case "muted":
				v := true
				_, err = svc.RoomPreference(ctx, f.recipient, f.room, &v)
			case "rejoined":
				_, err = f.db.Exec(`DELETE FROM room_members WHERE room_id=$1 AND user_id=$2`, f.room, f.recipient)
				if err == nil {
					_, err = f.db.Exec(`INSERT INTO room_members(room_id,user_id) VALUES($1,$2)`, f.room, f.recipient)
				}
			case "deleted":
				_, err = f.db.Exec(`UPDATE messages SET content='',deleted_at=clock_timestamp() WHERE id=$1`, f.message)
			case "account deleted":
				_, err = f.db.Exec(`DELETE FROM users WHERE id=$1`, f.recipient)
			case "room deleted":
				_, err = f.db.Exec(`DELETE FROM rooms WHERE id=$1`, f.room)
			case "message removed":
				_, err = f.db.Exec(`DELETE FROM messages WHERE id=$1`, f.message)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.worker.deliver(ctx, f.job); err != nil {
				t.Fatal(err)
			}
			var rows int
			if err := f.db.Get(&rows, `SELECT count(*) FROM notifications`); err != nil || rows != 0 {
				t.Fatalf("notifications=%d err=%v", rows, err)
			}
			var state string
			if err := f.db.Get(&state, `SELECT state FROM domain_outbox_deliveries WHERE event_id=$1 AND purpose='notification'`, f.job.EventID); err != nil || state != "done" {
				t.Fatalf("state=%s err=%v", state, err)
			}
		})
	}
}

func TestWorkerStaleLeaseRollsBackEffect(t *testing.T) {
	f := newMentionFixture(t)
	if _, err := f.db.Exec(`UPDATE domain_outbox_deliveries SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1`, f.job.EventID); err != nil {
		t.Fatal(err)
	}
	if err := f.worker.deliver(context.Background(), f.job); err == nil {
		t.Fatal("expired lease succeeded")
	}
	var rows int
	if err := f.db.Get(&rows, `SELECT count(*) FROM notifications`); err != nil || rows != 0 {
		t.Fatalf("unfenced effect=%d err=%v", rows, err)
	}
	if err := f.db.Get(&rows, `SELECT count(*) FROM domain_outbox WHERE kind='notification.created'`); err != nil || rows != 0 {
		t.Fatalf("unfenced private intent=%d err=%v", rows, err)
	}
	job, err := f.queue.Claim(context.Background(), outbox.Notification)
	if err != nil || job == nil {
		t.Fatalf("reclaim=%v err=%v", job, err)
	}
	if err := f.worker.deliver(context.Background(), job); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerConcurrentDeliveryAndRetention(t *testing.T) {
	f := newMentionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() { <-start; results <- f.worker.deliver(ctx, f.job) }()
	}
	close(start)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent deliveries=%d", successes)
	}
	var count int
	if err := f.db.Get(&count, `SELECT count(*) FROM notifications`); err != nil || count != 1 {
		t.Fatalf("concurrent effects=%d err=%v", count, err)
	}
	if err := f.db.Get(&count, `SELECT count(*) FROM domain_outbox WHERE kind='notification.created'`); err != nil || count != 1 {
		t.Fatalf("concurrent intents=%d err=%v", count, err)
	}
	if _, err := f.db.Exec(`UPDATE notifications SET created_at=clock_timestamp()-interval '31 days'; UPDATE message_send_keys SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	feed, err := NewService(NewRepository(f.db)).List(ctx, f.recipient, Query{})
	if err != nil || len(feed.Notifications) != 0 {
		t.Fatalf("expired feed=%v err=%v", feed, err)
	}
	if err := NewRepository(f.db).Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Get(&count, `SELECT (SELECT count(*) FROM notifications)+(SELECT count(*) FROM message_send_keys)`); err != nil || count != 0 {
		t.Fatalf("retained expired rows=%d err=%v", count, err)
	}
}

func TestWorkerDoesNotLockPreferenceBeforeMembership(t *testing.T) {
	f := newMentionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := f.db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := f.db.Exec(`INSERT INTO notification_preferences(user_id) VALUES($1)`, f.recipient); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT membership_generation FROM room_members WHERE room_id=$1 AND user_id=$2 FOR UPDATE`, f.room, f.recipient); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout='200ms'`); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() { defer close(done); result <- f.worker.deliver(ctx, f.job) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("delivery did not stop after cancellation")
		}
	})
	// pg_stat_activity provides a barrier: delivery has reached its lock wait.
	for {
		var waiting bool
		if err := f.db.GetContext(ctx, &waiting, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE 'SELECT membership_generation%')`); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_preferences SET mentions_enabled=false WHERE user_id=$1`, f.recipient); err != nil {
		t.Fatalf("worker holds preference before membership: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.db.Get(&count, `SELECT count(*) FROM notifications`); err != nil || count != 0 {
		t.Fatalf("disabled effect=%d err=%v", count, err)
	}
}
