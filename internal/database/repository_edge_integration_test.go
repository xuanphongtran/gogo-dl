package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
	"github.com/xuanphongtran/gogo-dl/internal/user"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestRepositoryRoomCreationRollsBackMembershipFailure(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	if err := MigrateUp(dsn, "file://../../migrations"); err != nil {
		t.Fatalf("MigrateUp() error = %v", err)
	}

	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sqlx.Open() error = %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		t.Fatalf("db.PingContext() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`TRUNCATE messages, room_members, rooms, users RESTART IDENTITY CASCADE`)
		_ = db.Close()
		if err := MigrateDown(dsn, "file://../../migrations"); err != nil {
			t.Errorf("MigrateDown() cleanup error = %v", err)
		}
	})

	ctx := context.Background()
	users := user.NewRepository(db)
	rooms := chat.NewRepository(db)
	suffix := time.Now().UnixNano()
	owner := &user.User{
		Username:     fmt.Sprintf("owner_%d", suffix),
		Email:        fmt.Sprintf("owner_%d@example.com", suffix),
		PasswordHash: "hash",
	}
	if err := users.Create(ctx, owner); err != nil {
		t.Fatalf("users.Create() error = %v", err)
	}

	_, err = db.Exec(`
		CREATE FUNCTION test_fail_membership() RETURNS trigger
		LANGUAGE plpgsql AS $func$
		BEGIN
			RAISE EXCEPTION 'forced membership failure';
		END;
		$func$`)
	if err != nil {
		t.Fatalf("create failure function: %v", err)
	}
	_, err = db.Exec(`
		CREATE TRIGGER test_fail_membership_trigger
		BEFORE INSERT ON room_members
		FOR EACH ROW EXECUTE FUNCTION test_fail_membership()`)
	if err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	defer func() {
		_, _ = db.Exec(`DROP TRIGGER IF EXISTS test_fail_membership_trigger ON room_members`)
		_, _ = db.Exec(`DROP FUNCTION IF EXISTS test_fail_membership()`)
	}()

	room := &chat.Room{Name: fmt.Sprintf("rollback_%d", suffix), CreatedBy: owner.ID}
	if err := rooms.CreateRoomWithMember(ctx, room, owner.ID); err == nil {
		t.Fatal("CreateRoomWithMember() succeeded despite membership failure")
	}
	var count int
	if err := db.GetContext(ctx, &count, `SELECT COUNT(*) FROM rooms WHERE name = $1`, room.Name); err != nil {
		t.Fatalf("rollback verification error = %v", err)
	}
	if count != 0 {
		t.Fatalf("rooms after membership rollback = %d, want 0", count)
	}

	_, _ = db.Exec(`DROP TRIGGER IF EXISTS test_fail_membership_trigger ON room_members`)
	_, _ = db.Exec(`DROP FUNCTION IF EXISTS test_fail_membership()`)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := rooms.CreateRoomWithMember(cancelled, &chat.Room{Name: "cancelled"}, owner.ID); err == nil {
		t.Fatal("CreateRoomWithMember() ignored cancelled context")
	}
}

func TestRepositoryCursorAndConstraintContracts(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	if err := MigrateUp(dsn, "file://../../migrations"); err != nil {
		t.Fatalf("MigrateUp() error = %v", err)
	}

	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sqlx.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`TRUNCATE messages, room_members, rooms, users RESTART IDENTITY CASCADE`)
		_ = db.Close()
		if err := MigrateDown(dsn, "file://../../migrations"); err != nil {
			t.Errorf("MigrateDown() cleanup error = %v", err)
		}
	})

	ctx := context.Background()
	users := user.NewRepository(db)
	rooms := chat.NewRepository(db)
	suffix := time.Now().UnixNano()
	owner := &user.User{
		Username:     fmt.Sprintf("cursor_owner_%d", suffix),
		Email:        fmt.Sprintf("cursor_owner_%d@example.com", suffix),
		PasswordHash: "hash",
	}
	if err := users.Create(ctx, owner); err != nil {
		t.Fatalf("users.Create() error = %v", err)
	}
	room := &chat.Room{Name: fmt.Sprintf("cursor_room_%d", suffix)}
	if err := rooms.CreateRoomWithMember(ctx, room, owner.ID); err != nil {
		t.Fatalf("CreateRoomWithMember() error = %v", err)
	}
	first := &chat.Message{RoomID: room.ID, UserID: &owner.ID, Content: "first"}
	second := &chat.Message{RoomID: room.ID, UserID: &owner.ID, Content: "second"}
	if err := rooms.CreateMessage(ctx, first); err != nil {
		t.Fatalf("CreateMessage(first) error = %v", err)
	}
	if err := rooms.CreateMessage(ctx, second); err != nil {
		t.Fatalf("CreateMessage(second) error = %v", err)
	}

	older, err := rooms.ListMessages(ctx, room.ID, 1, second.ID)
	if err != nil {
		t.Fatalf("ListMessages(cursor) error = %v", err)
	}
	if len(older) != 1 || older[0].ID != first.ID {
		t.Fatalf("cursor result = %+v, want message %d", older, first.ID)
	}

	duplicateEmail := &user.User{
		Username:     fmt.Sprintf("other_%d", suffix),
		Email:        owner.Email,
		PasswordHash: "hash",
	}
	if err := users.Create(ctx, duplicateEmail); !errors.Is(err, apperror.ErrConflict) {
		t.Fatalf("duplicate email error = %v, want conflict", err)
	}
}
