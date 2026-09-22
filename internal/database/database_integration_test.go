package database

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

func TestMigrationsCleanDatabaseAndRollback(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}

	if err := MigrateUp(dsn, "file://../../migrations"); err != nil {
		t.Fatalf("MigrateUp() error = %v", err)
	}
	t.Cleanup(func() {
		if err := MigrateDown(dsn, "file://../../migrations"); err != nil {
			t.Errorf("MigrateDown() cleanup error = %v", err)
		}
	})

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var tableName string
	if err := db.QueryRow(`SELECT to_regclass('public.messages')`).Scan(&tableName); err != nil {
		t.Fatalf("verify messages table: %v", err)
	}
	if tableName != "messages" {
		t.Fatalf("messages table = %q, want messages", tableName)
	}
}
