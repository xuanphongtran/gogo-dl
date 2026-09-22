package database

import (
	"database/sql"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/lib/pq"
)

func TestMigrationsUpgradeFromPreviousSchema(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}

	m, err := migrate.New("file://../../migrations", dsn)
	if err != nil {
		t.Fatalf("migrate.New() error = %v", err)
	}
	if err := m.Steps(1); err != nil {
		m.Close()
		t.Fatalf("apply historical migration: %v", err)
	}
	version, dirty, err := m.Version()
	if err != nil {
		m.Close()
		t.Fatalf("migration version after step 1: %v", err)
	}
	if version != 1 || dirty {
		m.Close()
		t.Fatalf("migration state = version %d, dirty %t; want version 1 and clean", version, dirty)
	}
	if sourceErr, databaseErr := m.Close(); sourceErr != nil || databaseErr != nil {
		t.Fatalf("close historical migrator: source=%v database=%v", sourceErr, databaseErr)
	}

	t.Cleanup(func() {
		if err := MigrateDown(dsn, "file://../../migrations"); err != nil && err != migrate.ErrNoChange {
			t.Errorf("MigrateDown() cleanup error = %v", err)
		}
	})
	if err := MigrateUp(dsn, "file://../../migrations"); err != nil {
		t.Fatalf("upgrade MigrateUp() error = %v", err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var nullable string
	if err := db.QueryRow(`
		SELECT is_nullable
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'messages' AND column_name = 'user_id'
	`).Scan(&nullable); err != nil {
		t.Fatalf("message author nullability: %v", err)
	}
	if nullable != "YES" {
		t.Fatalf("messages.user_id is_nullable = %q, want YES", nullable)
	}

	var deleteRule string
	if err := db.QueryRow(`
		SELECT rc.delete_rule
		FROM information_schema.referential_constraints rc
		WHERE rc.constraint_schema = 'public' AND rc.constraint_name = 'rooms_created_by_fkey'
	`).Scan(&deleteRule); err != nil {
		t.Fatalf("room ownership delete rule: %v", err)
	}
	if deleteRule != "RESTRICT" {
		t.Fatalf("rooms_created_by_fkey delete_rule = %q, want RESTRICT", deleteRule)
	}
}
