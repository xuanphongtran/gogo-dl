// Package database provides helpers for connecting to PostgreSQL via sqlx
// and running schema migrations via golang-migrate.
package database

import (
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	// postgres driver for golang-migrate
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	// file source driver for golang-migrate
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jmoiron/sqlx"
	// postgres driver for database/sql
	_ "github.com/lib/pq"
)

// DB wraps sqlx.DB and exposes migration helpers.
type DB struct {
	*sqlx.DB
}

// Connect opens a connection pool to PostgreSQL and verifies connectivity.
// dsn format: "host=... port=... user=... password=... dbname=... sslmode=..."
func Connect(dsn string) (*DB, error) {
	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("database: open: %w", err)
	}

	// Tune connection pool — adjust for your workload.
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(2 * time.Minute)

	// Verify the DSN is reachable.
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("database: ping: %w", err)
	}

	return &DB{db}, nil
}

// MigrateUp applies all pending up-migrations from the given directory.
// migrationsPath should be an absolute or relative path like "file://migrations".
func MigrateUp(dsn, migrationsPath string) error {
	m, err := migrate.New(migrationsPath, dsn)
	if err != nil {
		return fmt.Errorf("database: migrate.New: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("database: migrate up: %w", err)
	}
	return nil
}

// MigrateDown rolls back all applied migrations.
func MigrateDown(dsn, migrationsPath string) error {
	m, err := migrate.New(migrationsPath, dsn)
	if err != nil {
		return fmt.Errorf("database: migrate.New: %w", err)
	}
	defer m.Close()

	if err := m.Down(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("database: migrate down: %w", err)
	}
	return nil
}
