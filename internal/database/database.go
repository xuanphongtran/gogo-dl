// Package database provides helpers for connecting to PostgreSQL via sqlx
// and running schema migrations via golang-migrate.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	// file source driver for golang-migrate
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/migrations"
	// postgres driver for database/sql
	_ "github.com/lib/pq"
)

// DB wraps sqlx.DB and exposes migration helpers.
type DB struct {
	*sqlx.DB
}

// Connect opens a connection pool to PostgreSQL and verifies connectivity.
// dsn may be a PostgreSQL URL or a lib/pq key-value connection string.
func Connect(dsn string) (*DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ConnectContext(ctx, dsn)
}

// ConnectContext verifies startup connectivity within the caller's deadline.
func ConnectContext(ctx context.Context, dsn string) (*DB, error) {
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
	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("database: ping: %w", err), db.Close())
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

// MigrateUpEmbedded applies pending migrations bundled in the binary.
// The supplied pool remains owned by the caller.
func MigrateUpEmbedded(ctx context.Context, db *sql.DB) error {
	source, err := iofs.New(migrations.Files, ".")
	if err != nil {
		return fmt.Errorf("database: embedded migration source: %w", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return errors.Join(fmt.Errorf("database: migration connection: %w", err), source.Close())
	}
	restoreSession, err := boundMigrationSession(ctx, conn)
	if err != nil {
		return errors.Join(err, conn.Close(), source.Close())
	}
	driver, err := postgres.WithConnection(ctx, conn, &postgres.Config{})
	if err != nil {
		return errors.Join(fmt.Errorf("database: migration driver: %w", err), restoreSession(), conn.Close(), source.Close())
	}
	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return errors.Join(fmt.Errorf("database: embedded migrator: %w", err), restoreSession(), driver.Close(), source.Close())
	}
	stopGraceful := context.AfterFunc(ctx, func() {
		select {
		case m.GracefulStop <- true:
		default:
		}
	})

	runErr := m.Up()
	stopGraceful()
	resetErr := restoreSession()
	sourceErr, databaseErr := m.Close()
	if runErr != nil && runErr != migrate.ErrNoChange {
		err = fmt.Errorf("database: embedded migrate up: %w", runErr)
	}
	if sourceErr != nil {
		err = errors.Join(err, fmt.Errorf("database: close embedded migration source: %w", sourceErr))
	}
	if databaseErr != nil {
		err = errors.Join(err, fmt.Errorf("database: close migration connection: %w", databaseErr))
	}
	err = errors.Join(err, resetErr, ctx.Err())
	return err
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
