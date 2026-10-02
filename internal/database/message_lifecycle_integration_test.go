package database

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func phase06Database(t *testing.T, upgrade bool) (*sqlx.DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || parsed == nil || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("Phase 6 tests require a disposable database URL with a name ending in _test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	db, err := sqlx.ConnectContext(ctx, "postgres", dsn)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() {
		// Historical downgrade cannot represent deleted authors; remove only
		// data created in this explicitly disposable test database first.
		if _, err := db.Exec(`TRUNCATE messages, room_members, rooms, users RESTART IDENTITY CASCADE`); err != nil {
			t.Errorf("clean disposable data: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
		if err := MigrateDown(dsn, "file://../../migrations"); err != nil {
			t.Errorf("clean disposable schema: %v", err)
		}
	})
	if upgrade {
		m, err := migrate.New("file://../../migrations", dsn)
		if err != nil {
			t.Fatal(err)
		}
		migrationErr := m.Migrate(5)
		sourceErr, databaseErr := m.Close()
		if migrationErr != nil || sourceErr != nil || databaseErr != nil {
			t.Fatalf("establish schema 5: %v %v %v", migrationErr, sourceErr, databaseErr)
		}
	} else if err := MigrateUpEmbedded(ctx, db.DB); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

type phase06Fixture struct {
	db                               *sqlx.DB
	ctx                              context.Context
	repo                             chat.Repository
	service                          *chat.Service
	roomID, owner, author, moderator int64
}

func newPhase06Fixture(t *testing.T, upgrade bool) *phase06Fixture {
	t.Helper()
	db, ctx := phase06Database(t, upgrade)
	f := &phase06Fixture{db: db, ctx: ctx, repo: chat.NewRepository(db)}
	for name, id := range map[string]*int64{"owner": &f.owner, "author": &f.author, "moderator": &f.moderator} {
		if err := db.QueryRowxContext(ctx, `INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'test hash') RETURNING id`, name, name+"@example.test").Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	room := &chat.Room{Name: "lifecycle", Visibility: chat.RoomVisibilityPublic}
	if err := f.repo.CreateRoomWithMember(ctx, room, f.owner); err != nil {
		t.Fatal(err)
	}
	f.roomID = room.ID
	for _, id := range []int64{f.author, f.moderator} {
		if err := f.repo.AddMember(ctx, f.roomID, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.repo.SetMemberRole(ctx, f.roomID, f.moderator, chat.RoomRoleModerator); err != nil {
		t.Fatal(err)
	}
	f.service = chat.NewService(f.repo, ws.New())
	return f
}

func (f *phase06Fixture) message(t *testing.T, content string) *chat.Message {
	t.Helper()
	msg := &chat.Message{RoomID: f.roomID, UserID: &f.author, Content: content, Username: "untrusted caller label"}
	if err := f.repo.CreateMessage(f.ctx, msg); err != nil {
		t.Fatal(err)
	}
	if msg.Username != "author" {
		t.Fatalf("created message username=%q, want authoritative author", msg.Username)
	}
	return msg
}

func TestPhase06MigrationUpgrade(t *testing.T) {
	f := newPhase06Fixture(t, true)
	var messageID int64
	if err := f.db.QueryRowxContext(f.ctx, `INSERT INTO messages (room_id, user_id, content) VALUES ($1, $2, 'legacy') RETURNING id`, f.roomID, f.author).Scan(&messageID); err != nil {
		t.Fatal(err)
	}
	if err := MigrateUpEmbedded(f.ctx, f.db.DB); err != nil {
		t.Fatal(err)
	}
	messages, err := f.repo.ListMessages(f.ctx, f.roomID, 10, 0)
	if err != nil || len(messages) != 1 {
		t.Fatalf("history=%+v %v", messages, err)
	}
	msg := messages[0]
	if msg.ID != messageID || msg.Content != "legacy" || msg.Revision != 1 || msg.EditedAt != nil || msg.DeletedAt != nil {
		t.Fatalf("backfill=%+v", msg)
	}
	for _, query := range []string{
		`UPDATE messages SET revision = 0`,
		`UPDATE messages SET deleted_at = clock_timestamp()`,
	} {
		_, err := f.db.ExecContext(f.ctx, query)
		var constraint *pq.Error
		if !errors.As(err, &constraint) || constraint.Code != "23514" {
			t.Fatalf("lifecycle constraint error=%v", err)
		}
	}
	// Target schema 5 explicitly so this still exercises migration 6 down when
	// newer migrations exist, then reapply the full chain in this disposable DB.
	m, err := migrate.New("file://../../migrations", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	stepErr := m.Migrate(5)
	sourceErr, databaseErr := m.Close()
	if stepErr != nil || sourceErr != nil || databaseErr != nil {
		t.Fatalf("down migration: %v %v %v", stepErr, sourceErr, databaseErr)
	}
	if err := MigrateUpEmbedded(f.ctx, f.db.DB); err != nil {
		t.Fatal(err)
	}
}

func TestPhase06HistoryTombstoneAndAudit(t *testing.T) {
	f := newPhase06Fixture(t, false)
	older := f.message(t, "older")
	original := f.message(t, "original")
	edited, err := f.service.EditMessage(f.ctx, f.author, f.roomID, original.ID, &chat.EditMessageRequest{Content: "edited", Revision: 1})
	if err != nil || edited.Revision != 2 || edited.EditedAt == nil || !edited.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("edit=%+v %v", edited, err)
	}
	history, err := f.repo.ListMessages(f.ctx, f.roomID, 1, 0)
	if err != nil || len(history) != 1 || history[0].Content != edited.Content || history[0].Revision != edited.Revision || !history[0].EditedAt.Equal(*edited.EditedAt) {
		t.Fatalf("edit history=%+v %v", history, err)
	}
	deleted, err := f.service.DeleteMessage(f.ctx, f.moderator, f.roomID, original.ID)
	if err != nil || deleted.Content != "" || deleted.DeletedAt == nil || deleted.Revision != 3 {
		t.Fatalf("delete=%+v %v", deleted, err)
	}
	repeated, err := f.service.DeleteMessage(f.ctx, f.author, f.roomID, original.ID)
	if err != nil || repeated.Revision != deleted.Revision || !repeated.DeletedAt.Equal(*deleted.DeletedAt) {
		t.Fatalf("retry=%+v %v", repeated, err)
	}
	var auditActor sql.NullInt64
	if err := f.db.GetContext(f.ctx, &auditActor, `SELECT deleted_by FROM messages WHERE id = $1`, original.ID); err != nil || !auditActor.Valid || auditActor.Int64 != f.moderator {
		t.Fatalf("audit actor=%+v %v", auditActor, err)
	}
	newer := f.message(t, "newer")
	history, err = f.repo.ListMessages(f.ctx, f.roomID, 1, newer.ID)
	if err != nil || len(history) != 1 || history[0].ID != deleted.ID || history[0].Content != "" || history[0].DeletedAt == nil {
		t.Fatalf("tombstone cursor=%+v %v", history, err)
	}
	history, err = f.repo.ListMessages(f.ctx, f.roomID, 1, deleted.ID)
	if err != nil || len(history) != 1 || history[0].ID != older.ID {
		t.Fatalf("older cursor=%+v %v", history, err)
	}
	if _, err := f.service.EditMessage(f.ctx, f.author, f.roomID, original.ID, &chat.EditMessageRequest{Content: "restore", Revision: 3}); !errors.Is(err, apperror.ErrMessageDeleted) {
		t.Fatalf("resurrection error=%v", err)
	}
	if _, err := f.db.ExecContext(f.ctx, `DELETE FROM users WHERE id = $1`, f.moderator); err != nil {
		t.Fatal(err)
	}
	if err := f.db.GetContext(f.ctx, &auditActor, `SELECT deleted_by FROM messages WHERE id = $1`, original.ID); err != nil || auditActor.Valid {
		t.Fatalf("nullable audit actor=%+v %v", auditActor, err)
	}
	if _, err := f.db.ExecContext(f.ctx, `DELETE FROM users WHERE id = $1`, f.author); err != nil {
		t.Fatal(err)
	}
	history, err = f.repo.ListMessages(f.ctx, f.roomID, 10, 0)
	if err != nil || history[0].UserID != nil || history[0].Username != "[deleted user]" {
		t.Fatalf("deleted author=%+v %v", history, err)
	}
	if _, err := f.service.DeleteMessage(f.ctx, f.owner, f.roomID, newer.ID); err != nil {
		t.Fatalf("owner cannot moderate deleted author: %v", err)
	}
}

func TestPhase06MutationRollbackAndWrongRoom(t *testing.T) {
	f := newPhase06Fixture(t, false)
	msg := f.message(t, "original")
	other := &chat.Room{Name: "other", Visibility: chat.RoomVisibilityPrivate}
	if err := f.repo.CreateRoomWithMember(f.ctx, other, f.author); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.DeleteMessage(f.ctx, f.author, other.ID, msg.ID); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("cross-room delete=%v", err)
	}
	if _, err := f.db.ExecContext(f.ctx, `ALTER TABLE messages ADD CONSTRAINT phase06_test_failure CHECK (content <> 'blocked')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.EditMessage(f.ctx, f.author, f.roomID, msg.ID, &chat.EditMessageRequest{Content: "blocked", Revision: 1}); err == nil {
		t.Fatal("write should fail")
	}
	history, err := f.repo.ListMessages(f.ctx, f.roomID, 10, 0)
	if err != nil || history[0].Content != "original" || history[0].Revision != 1 || history[0].EditedAt != nil {
		t.Fatalf("failed write changed state=%+v %v", history, err)
	}
	if _, err := f.service.DeleteMessage(f.ctx, f.author, 999999, msg.ID); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("missing room=%v", err)
	}
}

func runPhase06Pair(t *testing.T, ctx context.Context, actions [2]func() error) [2]error {
	t.Helper()
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, action := range actions {
		go func(action func() error) { <-start; results <- action() }(action)
	}
	close(start)
	var errors [2]error
	for i := range errors {
		select {
		case errors[i] = <-results:
		case <-ctx.Done():
			t.Fatal("concurrent mutations timed out")
		}
	}
	return errors
}

func TestPhase06ConcurrentMutations(t *testing.T) {
	f := newPhase06Fixture(t, false)
	msg := f.message(t, "original")
	edits := [2]func() error{}
	for i, content := range []string{"one", "two"} {
		edits[i] = func() error {
			_, err := f.service.EditMessage(f.ctx, f.author, f.roomID, msg.ID, &chat.EditMessageRequest{Content: content, Revision: 1})
			return err
		}
	}
	results := runPhase06Pair(t, f.ctx, edits)
	successes, conflicts := 0, 0
	for _, err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, apperror.ErrMessageRevision) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent edit results=%v", results)
	}
	identical := f.message(t, "original")
	sameEdit := func() error {
		_, err := f.service.EditMessage(f.ctx, f.author, f.roomID, identical.ID, &chat.EditMessageRequest{Content: "same", Revision: 1})
		return err
	}
	for _, err := range runPhase06Pair(t, f.ctx, [2]func() error{sameEdit, sameEdit}) {
		if err != nil {
			t.Fatal(err)
		}
	}
	var revision int64
	if err := f.db.GetContext(f.ctx, &revision, `SELECT revision FROM messages WHERE id = $1`, identical.ID); err != nil || revision != 2 {
		t.Fatalf("identical edits revision=%d %v", revision, err)
	}
	deletes := [2]func() error{}
	for i, actor := range []int64{f.author, f.moderator} {
		deletes[i] = func() error { _, err := f.service.DeleteMessage(f.ctx, actor, f.roomID, msg.ID); return err }
	}
	for _, err := range runPhase06Pair(t, f.ctx, deletes) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.GetContext(f.ctx, &revision, `SELECT revision FROM messages WHERE id = $1`, msg.ID); err != nil || revision != 3 {
		t.Fatalf("concurrent deletion revision=%d %v", revision, err)
	}
	contested := f.message(t, "original")
	race := [2]func() error{
		func() error {
			_, err := f.service.EditMessage(f.ctx, f.author, f.roomID, contested.ID, &chat.EditMessageRequest{Content: "edited", Revision: 1})
			return err
		},
		func() error {
			_, err := f.service.DeleteMessage(f.ctx, f.moderator, f.roomID, contested.ID)
			return err
		},
	}
	for _, err := range runPhase06Pair(t, f.ctx, race) {
		if err != nil && !errors.Is(err, apperror.ErrMessageDeleted) {
			t.Fatal(err)
		}
	}
	history, err := f.repo.ListMessages(f.ctx, f.roomID, 1, 0)
	if err != nil || history[0].Content != "" || history[0].DeletedAt == nil {
		t.Fatalf("edit/delete resurrected content=%+v %v", history, err)
	}
}

func TestPhase06MembershipLockProtectsAuthorization(t *testing.T) {
	f := newPhase06Fixture(t, false)
	msg := f.message(t, "original")
	locked, release := make(chan struct{}), make(chan struct{})
	results := make(chan error, 1)
	finished := false
	go func() {
		_, _, err := f.repo.DeleteMessage(f.ctx, f.roomID, f.moderator, msg.ID,
			func(_ chat.RoomVisibility, member *chat.RoomMember, _ *chat.Message) (bool, error) {
				if member == nil || member.Role != chat.RoomRoleModerator {
					return false, apperror.ErrForbidden
				}
				close(locked)
				select {
				case <-release:
					return true, nil
				case <-f.ctx.Done():
					return false, f.ctx.Err()
				}
			})
		results <- err
	}()
	// Always release and join the mutation before fixture cleanup.
	defer func() {
		close(release)
		if finished {
			return
		}
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("locked mutation: %v", err)
			}
		case <-f.ctx.Done():
			t.Error("mutation did not finish")
		}
	}()
	select {
	case <-locked:
	case err := <-results:
		finished = true
		t.Fatalf("mutation exited before lock: %v", err)
	case <-f.ctx.Done():
		t.Fatal("lock timeout")
	}
	tx, err := f.db.BeginTxx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, setupErr := tx.ExecContext(f.ctx, `SET LOCAL lock_timeout = '100ms'`)
	if setupErr != nil {
		t.Fatal(errors.Join(setupErr, tx.Rollback()))
	}
	_, updateErr := tx.ExecContext(f.ctx, `UPDATE room_members SET role = 'member' WHERE room_id = $1 AND user_id = $2`, f.roomID, f.moderator)
	rollbackErr := tx.Rollback()
	if rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	var lockErr *pq.Error
	if !errors.As(updateErr, &lockErr) || lockErr.Code != "55P03" {
		t.Fatalf("demotion should wait for authorization transaction: %v", updateErr)
	}
}
