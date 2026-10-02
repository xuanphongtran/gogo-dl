package database

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/lib/pq"
)

func TestMigrationsCleanDatabaseAndRollback(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()
	if err := MigrateUpEmbedded(context.Background(), db); err != nil {
		t.Fatalf("MigrateUpEmbedded() error = %v", err)
	}
	t.Cleanup(func() {
		if err := MigrateDown(dsn, "file://../../migrations"); err != nil {
			t.Errorf("MigrateDown() cleanup error = %v", err)
		}
	})
	if err := MigrateUpEmbedded(context.Background(), db); err != nil {
		t.Fatalf("MigrateUpEmbedded() on unchanged schema = %v", err)
	}

	var tableName string
	if err := db.QueryRow(`SELECT to_regclass('public.messages')`).Scan(&tableName); err != nil {
		t.Fatalf("verify messages table: %v", err)
	}
	if tableName != "messages" {
		t.Fatalf("messages table = %q, want messages", tableName)
	}
}

func TestEmbeddedMigrationsConcurrentStart(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	t.Cleanup(func() {
		if err := MigrateDown(dsn, "file://../../migrations"); err != nil {
			t.Errorf("MigrateDown() cleanup error = %v", err)
		}
	})

	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				results <- err
				return
			}
			defer db.Close()
			<-start
			results <- MigrateUpEmbedded(context.Background(), db)
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("concurrent MigrateUpEmbedded() error = %v", err)
		}
	}
	if t.Failed() {
		return
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()
	var version int
	var dirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatalf("migration state: %v", err)
	}
	if version != 8 || dirty {
		t.Fatalf("migration state = version %d, dirty %t; want version 8 and clean", version, dirty)
	}
}

func TestEmbeddedMigrationsRejectDirtyState(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	if err := MigrateUpEmbedded(context.Background(), db); err != nil {
		t.Fatalf("initial MigrateUpEmbedded() error = %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`UPDATE schema_migrations SET dirty = false`); err != nil {
			t.Errorf("clear test dirty state: %v", err)
		}
		if err := MigrateDown(dsn, "file://../../migrations"); err != nil {
			t.Errorf("MigrateDown() cleanup error = %v", err)
		}
	})
	if _, err := db.Exec(`UPDATE schema_migrations SET dirty = true`); err != nil {
		t.Fatalf("set test dirty state: %v", err)
	}
	var dirty migrate.ErrDirty
	if err := MigrateUpEmbedded(context.Background(), db); !errors.As(err, &dirty) {
		t.Fatalf("MigrateUpEmbedded() error = %v, want ErrDirty", err)
	}
}
