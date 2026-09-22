package database

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/internal/user"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func TestEmailCanonicalizationContract(t *testing.T) {
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
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = db.Exec(`TRUNCATE messages, room_members, rooms, users RESTART IDENTITY CASCADE`)
		_ = db.Close()
		if err := MigrateDown(dsn, "file://../../migrations"); err != nil {
			t.Errorf("MigrateDown() cleanup error = %v", err)
		}
	})

	suffix := time.Now().UnixNano()
	repo := user.NewRepository(db)
	first := &user.User{Username: fmt.Sprintf("canonical_%d", suffix), Email: "CaseUser@example.com", PasswordHash: "hash"}
	if err := repo.Create(ctx, first); err != nil {
		t.Fatalf("first user create: %v", err)
	}
	second := &user.User{Username: fmt.Sprintf("canonical_two_%d", suffix), Email: "caseuser@example.com", PasswordHash: "hash"}
	if err := repo.Create(ctx, second); !errors.Is(err, apperror.ErrConflict) {
		t.Fatalf("case-insensitive duplicate error = %v, want conflict", err)
	}
	got, err := repo.GetByEmail(ctx, "CASEUSER@EXAMPLE.COM")
	if err != nil || got.ID != first.ID {
		t.Fatalf("case-insensitive lookup = %+v, error = %v", got, err)
	}
}
