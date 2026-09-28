package database

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/xuanphongtran/gogo-dl/migrations"
)

func TestEmbeddedMigrationSource(t *testing.T) {
	files, err := os.ReadDir("../../migrations")
	if err != nil {
		t.Fatalf("read migration directory: %v", err)
	}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".sql") {
			continue
		}
		if _, err := migrations.Files.ReadFile(file.Name()); err != nil {
			t.Fatalf("embedded migration %s: %v", file.Name(), err)
		}
	}

	source, err := iofs.New(migrations.Files, ".")
	if err != nil {
		t.Fatalf("iofs.New() error = %v", err)
	}
	defer source.Close()
	version, err := source.First()
	if err != nil || version != 1 {
		t.Fatalf("embedded first migration = %d, %v; want 1", version, err)
	}
	next, err := source.Next(version)
	if err != nil || next != 2 {
		t.Fatalf("embedded next migration = %d, %v; want 2", next, err)
	}
}

func TestEmbeddedMigrationConnectionFailure(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer db.Close()
	mock.ExpectPing().WillReturnError(errors.New("connection unavailable"))

	err = MigrateUpEmbedded(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "connection unavailable") {
		t.Fatalf("MigrateUpEmbedded() error = %v, want connection failure", err)
	}
	if got := db.Stats().InUse; got != 0 {
		t.Fatalf("connections still in use after failure = %d", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}
