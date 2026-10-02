package database

import (
	"context"
	"errors"
	"fmt"
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

// This fixture owns a fresh test database. It refuses an existing application
// schema before registering destructive cleanup and never loads local env files.
func phase7Database(t *testing.T, version uint) (*sqlx.DB, string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), "_test") {
		t.Fatal("Phase 7 integration tests require a PostgreSQL URL for a database ending in _test")
	}
	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping test database: %v", err)
	}
	var existing bool
	if err := db.GetContext(ctx, &existing, `SELECT to_regclass('public.users') IS NOT NULL`); err != nil {
		t.Fatalf("check fresh test database: %v", err)
	}
	if existing {
		t.Fatal("Phase 7 integration fixture refuses an existing application schema")
	}
	m, err := migrate.New("file://../../migrations", dsn)
	if err != nil {
		t.Fatalf("create test migrator: %v", err)
	}
	runErr := m.Migrate(version)
	sourceErr, databaseErr := m.Close()
	if runErr != nil || sourceErr != nil || databaseErr != nil {
		t.Fatalf("initialize schema: migrate=%v source=%v database=%v", runErr, sourceErr, databaseErr)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		// The historical migration 2 down cannot represent deleted authors.
		// All data here was created by this fixture, so clear it before rollback.
		if _, err := db.ExecContext(cleanupCtx, `TRUNCATE messages, room_members, rooms, users RESTART IDENTITY CASCADE`); err != nil {
			t.Errorf("clear owned test data: %v", err)
			return
		}
		if err := MigrateDown(dsn, "file://../../migrations"); err != nil {
			t.Errorf("roll back test schema: %v", err)
		}
	})
	return db, dsn
}

func phase7User(t *testing.T, db *sqlx.DB, name string) int64 {
	t.Helper()
	var id int64
	if err := db.Get(&id, `INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'test-hash') RETURNING id`, name, name+"@example.com"); err != nil {
		t.Fatalf("create test user: %v", err)
	}
	return id
}

func phase7Room(t *testing.T, repo chat.Repository, owner int64, name string, visibility chat.RoomVisibility) int64 {
	t.Helper()
	room := &chat.Room{Name: name, CreatedBy: owner, Visibility: visibility}
	if err := repo.CreateRoomWithMember(context.Background(), room, owner); err != nil {
		t.Fatalf("create test room: %v", err)
	}
	return room.ID
}

func phase7Message(t *testing.T, db *sqlx.DB, roomID, authorID int64, content string) int64 {
	t.Helper()
	var id int64
	if err := db.Get(&id, `INSERT INTO messages (room_id, user_id, content) VALUES ($1, $2, $3) RETURNING id`, roomID, authorID, content); err != nil {
		t.Fatalf("create test message: %v", err)
	}
	return id
}

func phase7MemberPolicy(visibility chat.RoomVisibility, member *chat.RoomMember) error {
	if member != nil {
		return nil
	}
	if visibility == chat.RoomVisibilityPrivate {
		return apperror.ErrNotFound
	}
	return apperror.ErrForbidden
}

func phase7AssertState(t *testing.T, got *chat.ReadState, roomID, cursor, unread int64) {
	t.Helper()
	if got == nil || got.RoomID != roomID || got.LastReadMessageID != cursor || got.UnreadCount != unread {
		t.Fatalf("read state = %+v; want room %d, cursor %d, unread %d", got, roomID, cursor, unread)
	}
}

func phase7AssertCode(t *testing.T, err error, code pq.ErrorCode) {
	t.Helper()
	var pgErr *pq.Error
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Fatalf("database error = %v; want PostgreSQL code %s", err, code)
	}
}

func TestPhase7ReadStateSchemaUpgrade(t *testing.T) {
	db, dsn := phase7Database(t, 5)
	repo := chat.NewRepository(db)
	owner := phase7User(t, db, "phase7_upgrade_owner")
	reader := phase7User(t, db, "phase7_upgrade_reader")
	outsider := phase7User(t, db, "phase7_upgrade_outsider")
	roomID := phase7Room(t, repo, owner, "upgrade room", chat.RoomVisibilityPublic)
	if err := repo.AddMember(context.Background(), roomID, reader); err != nil {
		t.Fatalf("add reader: %v", err)
	}
	messageID := phase7Message(t, db, roomID, owner, "historical message")
	if err := MigrateUpEmbedded(context.Background(), db.DB); err != nil {
		t.Fatalf("upgrade schema 5 to 7: %v", err)
	}
	var revision int64
	var lifecycleUnset bool
	if err := db.QueryRow(`SELECT revision, edited_at IS NULL AND deleted_at IS NULL FROM messages WHERE id = $1`, messageID).Scan(&revision, &lifecycleUnset); err != nil || revision != 1 || !lifecycleUnset {
		t.Fatalf("migration 6 historical defaults: revision=%d unset=%t error=%v", revision, lifecycleUnset, err)
	}
	state, err := repo.GetReadState(context.Background(), roomID, reader, phase7MemberPolicy)
	if err != nil {
		t.Fatalf("read newly upgraded state: %v", err)
	}
	phase7AssertState(t, state, roomID, 0, 1)
	_, err = db.Exec(`INSERT INTO room_read_states (room_id, user_id, last_read_message_id) VALUES ($1, $2, -1)`, roomID, reader)
	phase7AssertCode(t, err, "23514")
	_, err = db.Exec(`INSERT INTO room_read_states (room_id, user_id, last_read_message_id) VALUES ($1, $2, 0)`, roomID, outsider)
	phase7AssertCode(t, err, "23503")
	if _, err := db.Exec(`INSERT INTO room_read_states (room_id, user_id) VALUES ($1, $2)`, roomID, reader); err != nil {
		t.Fatalf("insert default cursor: %v", err)
	}
	var cursor int64
	if err := db.Get(&cursor, `SELECT last_read_message_id FROM room_read_states WHERE room_id = $1 AND user_id = $2`, roomID, reader); err != nil || cursor != 0 {
		t.Fatalf("schema default cursor=%d error=%v", cursor, err)
	}
	_, err = db.Exec(`INSERT INTO room_read_states (room_id, user_id) VALUES ($1, $2)`, roomID, reader)
	phase7AssertCode(t, err, "23505")
	if _, _, err := repo.AdvanceReadState(context.Background(), roomID, reader, messageID, phase7MemberPolicy); err != nil {
		t.Fatalf("save upgraded cursor: %v", err)
	}
	m, err := migrate.New("file://../../migrations", dsn)
	if err != nil {
		t.Fatalf("create rollback migrator: %v", err)
	}
	runErr := m.Migrate(6)
	sourceErr, databaseErr := m.Close()
	if runErr != nil || sourceErr != nil || databaseErr != nil {
		t.Fatalf("roll back migration 7: migrate=%v source=%v database=%v", runErr, sourceErr, databaseErr)
	}
	var removed bool
	if err := db.Get(&removed, `SELECT to_regclass('public.room_read_states') IS NULL`); err != nil || !removed {
		t.Fatalf("migration 7 down removed state table=%t error=%v", removed, err)
	}
	if err := MigrateUpEmbedded(context.Background(), db.DB); err != nil {
		t.Fatalf("reapply migration 7: %v", err)
	}
	state, err = repo.GetReadState(context.Background(), roomID, reader, phase7MemberPolicy)
	if err != nil {
		t.Fatalf("read reapplied cursor: %v", err)
	}
	phase7AssertState(t, state, roomID, 0, 1)
	if _, _, err := repo.AdvanceReadState(context.Background(), roomID, reader, messageID, phase7MemberPolicy); err != nil {
		t.Fatalf("save watermark after reapply: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM messages WHERE id = $1`, messageID); err != nil {
		t.Fatalf("remove old message while retaining watermark: %v", err)
	}
	state, err = repo.GetReadState(context.Background(), roomID, reader, phase7MemberPolicy)
	if err != nil {
		t.Fatalf("read cursor after old message removal: %v", err)
	}
	phase7AssertState(t, state, roomID, messageID, 0)
}

func TestPhase7ReadStatePersistenceAndMembershipLifecycle(t *testing.T) {
	db, _ := phase7Database(t, 7)
	repo := chat.NewRepository(db)
	service := chat.NewService(repo, ws.New())
	owner := phase7User(t, db, "phase7_owner")
	reader := phase7User(t, db, "phase7_reader")
	deletedAuthor := phase7User(t, db, "phase7_deleted_author")
	roomID := phase7Room(t, repo, owner, "read room", chat.RoomVisibilityPublic)
	privateID := phase7Room(t, repo, owner, "private room", chat.RoomVisibilityPrivate)
	ctx := context.Background()
	ownMessage := phase7Message(t, db, roomID, reader, "own message")
	foreignMessage := phase7Message(t, db, privateID, owner, "wrong room")
	firstUnread := phase7Message(t, db, roomID, owner, "first unread")
	phase7Message(t, db, roomID, deletedAuthor, "retained after account deletion")
	tombstone := phase7Message(t, db, roomID, owner, "delete this content")
	if _, err := db.Exec(`DELETE FROM users WHERE id = $1`, deletedAuthor); err != nil {
		t.Fatalf("delete message author: %v", err)
	}
	if _, err := db.Exec(`UPDATE messages SET content = '', deleted_at = clock_timestamp(), deleted_by = $2 WHERE id = $1`, tombstone, owner); err != nil {
		t.Fatalf("create lifecycle-compatible tombstone: %v", err)
	}
	// Joining after this history exists must still include it at cursor zero.
	if err := repo.AddMember(ctx, roomID, reader); err != nil {
		t.Fatalf("add reader after room history exists: %v", err)
	}
	state, err := service.GetReadState(ctx, reader, roomID)
	if err != nil {
		t.Fatalf("get unset state: %v", err)
	}
	phase7AssertState(t, state, roomID, 0, 3)
	var persisted int
	if err := db.Get(&persisted, `SELECT COUNT(*) FROM room_read_states`); err != nil || persisted != 0 {
		t.Fatalf("GET should not save a cursor: rows=%d error=%v", persisted, err)
	}
	state, changed, err := repo.AdvanceReadState(ctx, roomID, reader, firstUnread, phase7MemberPolicy)
	if err != nil || !changed {
		t.Fatalf("advance state: changed=%t error=%v", changed, err)
	}
	phase7AssertState(t, state, roomID, firstUnread, 2)
	for _, retry := range []int64{firstUnread, ownMessage} {
		state, changed, err = repo.AdvanceReadState(ctx, roomID, reader, retry, phase7MemberPolicy)
		if err != nil || changed {
			t.Fatalf("retry %d: changed=%t error=%v", retry, changed, err)
		}
		phase7AssertState(t, state, roomID, firstUnread, 2)
	}
	for _, invalid := range []int64{foreignMessage, 999999999} {
		_, changed, err = repo.AdvanceReadState(ctx, roomID, reader, invalid, phase7MemberPolicy)
		if !errors.Is(err, apperror.ErrNotFound) || changed {
			t.Fatalf("invalid room message %d: changed=%t error=%v; want not found", invalid, changed, err)
		}
	}
	state, changed, err = repo.AdvanceReadState(ctx, roomID, reader, tombstone, phase7MemberPolicy)
	if err != nil || !changed {
		t.Fatalf("advance to tombstone: changed=%t error=%v", changed, err)
	}
	phase7AssertState(t, state, roomID, tombstone, 0)
	state, err = service.GetReadState(ctx, reader, roomID)
	if err != nil {
		t.Fatalf("reload durable cursor: %v", err)
	}
	phase7AssertState(t, state, roomID, tombstone, 0)
	if _, err := service.GetReadState(ctx, reader, privateID); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("private nonmember read error=%v; want not found", err)
	}
	if _, err := service.GetReadState(ctx, reader, 999999999); !errors.Is(err, apperror.ErrNotFound) {
		t.Fatalf("missing room read error=%v; want not found", err)
	}
	if err := repo.RemoveMember(ctx, roomID, reader); err != nil {
		t.Fatalf("remove reader: %v", err)
	}
	if err := db.Get(&persisted, `SELECT COUNT(*) FROM room_read_states WHERE room_id = $1 AND user_id = $2`, roomID, reader); err != nil || persisted != 0 {
		t.Fatalf("membership removal should cascade cursor: rows=%d error=%v", persisted, err)
	}
	if _, err := service.GetReadState(ctx, reader, roomID); !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("removed public member read error=%v; want forbidden", err)
	}
	if _, _, err := repo.AdvanceReadState(ctx, roomID, reader, tombstone, phase7MemberPolicy); !errors.Is(err, apperror.ErrForbidden) {
		t.Fatalf("removed public member write error=%v; want forbidden", err)
	}
	if err := repo.AddMember(ctx, roomID, reader); err != nil {
		t.Fatalf("rejoin reader: %v", err)
	}
	state, err = service.GetReadState(ctx, reader, roomID)
	if err != nil {
		t.Fatalf("read rejoined state: %v", err)
	}
	phase7AssertState(t, state, roomID, 0, 3)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.GetReadState(cancelled, reader, roomID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled GET error=%v; want context cancelled", err)
	}
	if _, _, err := repo.AdvanceReadState(cancelled, roomID, reader, firstUnread, phase7MemberPolicy); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write error=%v; want context cancelled", err)
	}
}

func TestPhase7ReadStateConcurrentAdvanceAndRollback(t *testing.T) {
	db, _ := phase7Database(t, 7)
	repo := chat.NewRepository(db)
	owner := phase7User(t, db, "phase7_concurrent_owner")
	reader := phase7User(t, db, "phase7_concurrent_reader")
	roomID := phase7Room(t, repo, owner, "concurrent read room", chat.RoomVisibilityPublic)
	if err := repo.AddMember(context.Background(), roomID, reader); err != nil {
		t.Fatalf("add concurrent reader: %v", err)
	}
	ids := []int64{
		phase7Message(t, db, roomID, owner, "one"),
		phase7Message(t, db, roomID, owner, "two"),
		phase7Message(t, db, roomID, owner, "three"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 12)
	for i := range 12 {
		go func(messageID int64) {
			<-start
			_, _, err := repo.AdvanceReadState(ctx, roomID, reader, messageID, phase7MemberPolicy)
			results <- err
		}(ids[i%len(ids)])
	}
	close(start)
	for range 12 {
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("concurrent advance: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("concurrent advances did not finish within deadline")
		}
	}
	state, err := repo.GetReadState(ctx, roomID, reader, phase7MemberPolicy)
	if err != nil {
		t.Fatalf("read concurrent maximum: %v", err)
	}
	phase7AssertState(t, state, roomID, ids[2], 0)
	deniedID := phase7Message(t, db, roomID, owner, "must roll back")
	// A test-only durable check forces an actual write failure, not a policy error.
	if _, err := db.Exec(`ALTER TABLE room_read_states ADD CONSTRAINT test_phase7_denied_cursor CHECK (last_read_message_id <> ` + fmt.Sprint(deniedID) + `)`); err != nil {
		t.Fatalf("install forced write failure: %v", err)
	}
	_, changed, err := repo.AdvanceReadState(ctx, roomID, reader, deniedID, phase7MemberPolicy)
	phase7AssertCode(t, err, "23514")
	if changed {
		t.Fatal("failed transaction reported a changed cursor")
	}
	state, err = repo.GetReadState(ctx, roomID, reader, phase7MemberPolicy)
	if err != nil {
		t.Fatalf("read after failed write: %v", err)
	}
	phase7AssertState(t, state, roomID, ids[2], 1)
}

func TestPhase7ReadStateLocksMembershipUntilCommit(t *testing.T) {
	db, _ := phase7Database(t, 7)
	repo := chat.NewRepository(db)
	owner := phase7User(t, db, "phase7_lock_owner")
	reader := phase7User(t, db, "phase7_lock_reader")
	roomID := phase7Room(t, repo, owner, "locked read room", chat.RoomVisibilityPublic)
	if err := repo.AddMember(context.Background(), roomID, reader); err != nil {
		t.Fatalf("add lock-test reader: %v", err)
	}
	messageID := phase7Message(t, db, roomID, owner, "read while still a member")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	locked := make(chan struct{})
	release := make(chan struct{}, 1)
	defer close(release)
	result := make(chan error, 1)
	go func() {
		_, _, err := repo.AdvanceReadState(ctx, roomID, reader, messageID, func(visibility chat.RoomVisibility, member *chat.RoomMember) error {
			if err := phase7MemberPolicy(visibility, member); err != nil {
				return err
			}
			close(locked)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		result <- err
	}()
	select {
	case <-locked:
	case err := <-result:
		t.Fatalf("advance failed before policy acquired locks: %v", err)
	case <-ctx.Done():
		t.Fatal("policy did not reach locked membership within deadline")
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatalf("begin concurrent removal: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL lock_timeout = '100ms'`); err != nil {
		_ = tx.Rollback()
		t.Fatalf("set finite lock timeout: %v", err)
	}
	_, removeErr := tx.ExecContext(ctx, `DELETE FROM room_members WHERE room_id = $1 AND user_id = $2`, roomID, reader)
	rollbackErr := tx.Rollback()
	phase7AssertCode(t, removeErr, "55P03")
	if rollbackErr != nil {
		t.Fatalf("rollback blocked removal: %v", rollbackErr)
	}
	release <- struct{}{}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("commit accepted cursor: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("accepted cursor did not commit within deadline")
	}
	state, err := repo.GetReadState(ctx, roomID, reader, phase7MemberPolicy)
	if err != nil {
		t.Fatalf("read committed cursor: %v", err)
	}
	phase7AssertState(t, state, roomID, messageID, 0)
	if err := repo.RemoveMember(ctx, roomID, reader); err != nil {
		t.Fatalf("remove reader after commit: %v", err)
	}
	var rows int
	if err := db.Get(&rows, `SELECT COUNT(*) FROM room_read_states WHERE room_id = $1 AND user_id = $2`, roomID, reader); err != nil || rows != 0 {
		t.Fatalf("post-commit membership removal did not clear cursor: rows=%d error=%v", rows, err)
	}
}
