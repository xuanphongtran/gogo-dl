package database

import (
	"context"
	"testing"
	"time"

	migratedb "github.com/golang-migrate/migrate/v4/database"
)

func TestEmbeddedMigrationLockHonorsBoundedStartupAndRestoresSession(t *testing.T) {
	db, _ := phase7Database(t, 7)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var databaseName string
	if err := db.GetContext(ctx, &databaseName, "SELECT current_database()"); err != nil {
		t.Fatal(err)
	}
	key, err := migratedb.GenerateAdvisoryLockId(databaseName, "public", "schema_migrations")
	if err != nil {
		t.Fatal(err)
	}
	holder, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := holder.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
			t.Error(err)
		}
	}()
	startup, startupCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer startupCancel()
	result := make(chan error, 1)
	go func() { result <- MigrateUpEmbedded(startup, db.DB) }()
	select {
	case err := <-result:
		if err == nil {
			t.Error("migration succeeded while lock held")
		}
	case <-time.After(time.Second):
		t.Error("migration ignored startup deadline while waiting for advisory lock")
		if _, err := holder.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
			t.Error(err)
		}
		select {
		case <-result:
		case <-ctx.Done():
			t.Fatal("migration worker leaked")
		}
	}
	if db.Stats().InUse != 1 {
		t.Error("migration connection leaked")
	}
	var timeout string
	if err := db.GetContext(ctx, &timeout, "SHOW statement_timeout"); err != nil {
		t.Fatal(err)
	}
	if timeout != "0" {
		t.Fatalf("migration session timeout leaked into pool: %s", timeout)
	}
}
