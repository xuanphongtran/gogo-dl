package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// Repository defines data-access operations for the user domain.
// Using an interface keeps the service layer testable without a real DB.
type Repository interface {
	Create(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id int64) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	Update(ctx context.Context, u *User) error
	Delete(ctx context.Context, id int64) error
	DeleteAccount(ctx context.Context, id int64) error
}

// postgresRepository is the PostgreSQL implementation of Repository.
type postgresRepository struct {
	db *sqlx.DB
}

// NewRepository creates a new PostgreSQL-backed Repository.
func NewRepository(db *sqlx.DB) Repository {
	return &postgresRepository{db: db}
}

// Create inserts a new user row and fills u.ID and u.CreatedAt.
func (r *postgresRepository) Create(ctx context.Context, u *User) error {
	query := `
		INSERT INTO users (username, email, password_hash, avatar_url)
		VALUES (:username, :email, :password_hash, :avatar_url)
		RETURNING id, created_at, updated_at`

	rows, err := r.db.NamedQueryContext(ctx, query, u)
	if err != nil {
		// Detect unique constraint violations (username / email already taken).
		if isUniqueViolation(err) {
			return apperror.ErrConflict
		}
		return fmt.Errorf("user repo Create: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return fmt.Errorf("user repo Create: %w", sql.ErrNoRows)
	}
	if err := rows.Scan(&u.ID, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return fmt.Errorf("user repo Create scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("user repo Create rows: %w", err)
	}
	return nil
}

// GetByID fetches a user by primary key.
func (r *postgresRepository) GetByID(ctx context.Context, id int64) (*User, error) {
	var u User
	err := r.db.GetContext(ctx, &u, `SELECT id, username, email, password_hash, avatar_url, created_at, updated_at FROM users WHERE id = $1`, id)
	return handleGetErr(&u, err, "user repo GetByID")
}

// GetByEmail fetches a user by email address (used during login).
func (r *postgresRepository) GetByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := r.db.GetContext(ctx, &u, `SELECT id, username, email, password_hash, avatar_url, created_at, updated_at FROM users WHERE email = $1`, email)
	return handleGetErr(&u, err, "user repo GetByEmail")
}

// GetByUsername fetches a user by username.
func (r *postgresRepository) GetByUsername(ctx context.Context, username string) (*User, error) {
	var u User
	err := r.db.GetContext(ctx, &u, `SELECT id, username, email, password_hash, avatar_url, created_at, updated_at FROM users WHERE username = $1`, username)
	return handleGetErr(&u, err, "user repo GetByUsername")
}

// Update modifies username, avatar_url, and updated_at for an existing user.
func (r *postgresRepository) Update(ctx context.Context, u *User) error {
	query := `
		UPDATE users
		SET username   = :username,
		    avatar_url = :avatar_url,
		    updated_at = NOW()
		WHERE id = :id
		RETURNING updated_at`

	rows, err := r.db.NamedQueryContext(ctx, query, u)
	if err != nil {
		if isUniqueViolation(err) {
			return apperror.ErrConflict
		}
		return fmt.Errorf("user repo Update: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return apperror.ErrNotFound
	}
	if err := rows.Scan(&u.UpdatedAt); err != nil {
		return fmt.Errorf("user repo Update scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("user repo Update rows: %w", err)
	}
	return nil
}

// Delete delegates to the transactional account deletion path.
func (r *postgresRepository) Delete(ctx context.Context, id int64) error {
	return r.DeleteAccount(ctx, id)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func handleGetErr(u *User, err error, op string) (*User, error) {
	if err == nil {
		return u, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.ErrNotFound
	}
	return nil, fmt.Errorf("%s: %w", op, err)
}

// isUniqueViolation detects PostgreSQL unique constraint error code 23505.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}
