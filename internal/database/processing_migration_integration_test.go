package database

import (
	"context"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/xuanphongtran/gogo-dl/internal/attachment"
	"github.com/xuanphongtran/gogo-dl/internal/chat"
)

func TestProcessingMigrationBackfillAndRollbackGuard(t *testing.T) {
	db, dsn := phase7Database(t, 11)
	ctx := context.Background()
	owner := phase7User(t, db, "processing_owner")
	room := phase7Room(t, chat.NewRepository(db), owner, "processing upgrade", chat.RoomVisibilityPublic)
	repo := attachment.NewRepository(db)
	row, err := repo.Reserve(ctx, room, owner, "processing-upgrade-key", strings.Repeat("a", 64), attachmentRequest(), attachmentPolicy)
	if err != nil {
		t.Fatal(err)
	}
	// Schema11 ready means the original bytes passed ClamAV and were promoted intact.
	if _, err := db.ExecContext(ctx, `UPDATE attachments SET state='ready',scan_started_at=clock_timestamp(),scan_completed_at=clock_timestamp() WHERE id=$1`, row.ID); err != nil {
		t.Fatal(err)
	}
	m, err := migrate.New("file://../../migrations", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Migrate(12); err != nil {
		t.Fatal(err)
	}
	var hash, engine, policy string
	var size int64
	if err := db.QueryRowxContext(ctx, `SELECT processed_sha256,processed_size_bytes,processing_engine,processing_policy FROM attachments WHERE id=$1`, row.ID).Scan(&hash, &size, &engine, &policy); err != nil {
		t.Fatal(err)
	}
	if hash != row.SHA256 || size != row.SizeBytes || engine != "clamav" || policy != "clamav-v1" {
		t.Fatal("historical ready backfill lost provenance")
	}
	if err := m.Migrate(11); err != nil {
		t.Fatal("unchanged historical ClamAV artifact cannot downgrade")
	}
	if err := m.Migrate(12); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE attachments SET processed_sha256=$2,processed_size_bytes=1,processing_engine='sanitize',processing_policy='sanitize-v1' WHERE id=$1`, row.ID, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(11); err == nil {
		t.Fatal("sanitized live artifact lost provenance on rollback")
	}
	// Recover only the owned disposable fixture after the intentional guard failure.
	if err := m.Force(12); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE attachments SET state='deleted' WHERE id=$1`, row.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(11); err != nil {
		t.Fatal("downgrade after owned cleanup not allowed")
	}
}
