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

func TestDataIntegrityRepositories(t *testing.T) {
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

	alice := &user.User{
		Username:     fmt.Sprintf("alice_%d", suffix),
		Email:        fmt.Sprintf("alice_%d@example.com", suffix),
		PasswordHash: "hash",
	}
	if err := users.Create(ctx, alice); err != nil {
		t.Fatalf("users.Create(alice) error = %v", err)
	}

	room := &chat.Room{Name: fmt.Sprintf("room_%d", suffix)}
	if err := rooms.CreateRoomWithMember(ctx, room, alice.ID); err != nil {
		t.Fatalf("CreateRoomWithMember() error = %v", err)
	}
	member, err := rooms.IsMember(ctx, room.ID, alice.ID)
	if err != nil || !member {
		t.Fatalf("creator membership = %v, error = %v; want true, nil", member, err)
	}

	message := &chat.Message{RoomID: room.ID, UserID: &alice.ID, Content: "retained"}
	if err := rooms.CreateMessage(ctx, message); err != nil {
		t.Fatalf("CreateMessage() error = %v", err)
	}

	if err := users.DeleteAccount(ctx, alice.ID); !errors.Is(err, apperror.ErrAccountOwnsRooms) {
		t.Fatalf("DeleteAccount(owner) error = %v, want ownership conflict", err)
	}

	bob := &user.User{
		Username:     fmt.Sprintf("bob_%d", suffix),
		Email:        fmt.Sprintf("bob_%d@example.com", suffix),
		PasswordHash: "hash",
	}
	if err := users.Create(ctx, bob); err != nil {
		t.Fatalf("users.Create(bob) error = %v", err)
	}
	bobMessage := &chat.Message{RoomID: room.ID, UserID: &bob.ID, Content: "deleted author"}
	if err := rooms.CreateMessage(ctx, bobMessage); err != nil {
		t.Fatalf("CreateMessage(bob) error = %v", err)
	}
	if err := users.DeleteAccount(ctx, bob.ID); err != nil {
		t.Fatalf("DeleteAccount(non-owner) error = %v", err)
	}

	messages, err := rooms.ListMessages(ctx, room.ID, 10, 0)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 2 || messages[0].UserID != nil || messages[0].Username != "[deleted user]" {
		t.Fatalf("deleted-author message = %+v, want nullable author and deleted label", messages)
	}

	duplicate := &user.User{
		Username:     alice.Username,
		Email:        fmt.Sprintf("different_%d@example.com", suffix),
		PasswordHash: "hash",
	}
	if err := users.Create(ctx, duplicate); !errors.Is(err, apperror.ErrConflict) {
		t.Fatalf("duplicate username error = %v, want conflict", err)
	}

	invalidRoom := &chat.Room{Name: "rollback", CreatedBy: 999999999}
	if err := rooms.CreateRoomWithMember(ctx, invalidRoom, invalidRoom.CreatedBy); err == nil {
		t.Fatal("CreateRoomWithMember() accepted a missing creator")
	}
	var roomCount int
	if err := db.GetContext(ctx, &roomCount, `SELECT COUNT(*) FROM rooms WHERE name = 'rollback'`); err != nil {
		t.Fatalf("rollback verification error = %v", err)
	}
	if roomCount != 0 {
		t.Fatalf("room count after failed creation = %d, want 0", roomCount)
	}

}
