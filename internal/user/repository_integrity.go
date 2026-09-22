package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// DeleteAccount removes a user atomically while preserving message history.
// Room ownership is checked before deletion and protected again by the
// database foreign key so a concurrent room creation cannot leave an
// ownerless room.
func (r *postgresRepository) DeleteAccount(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("user repo DeleteAccount begin: %w", err)
	}

	rollback := func(opErr error) error {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return fmt.Errorf("user repo DeleteAccount rollback: %v: %w", rollbackErr, opErr)
		}
		return opErr
	}

	var ownsRoom bool
	if err := tx.GetContext(ctx, &ownsRoom,
		`SELECT EXISTS (SELECT 1 FROM rooms WHERE created_by = $1)`, id,
	); err != nil {
		return rollback(fmt.Errorf("user repo DeleteAccount check ownership: %w", err))
	}
	if ownsRoom {
		return rollback(apperror.ErrAccountOwnsRooms)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return rollback(apperror.ErrAccountOwnsRooms)
		}
		return rollback(fmt.Errorf("user repo DeleteAccount delete: %w", err))
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return rollback(fmt.Errorf("user repo DeleteAccount rows affected: %w", err))
	}
	if affected == 0 {
		return rollback(apperror.ErrNotFound)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("user repo DeleteAccount commit: %w", err)
	}
	return nil
}

func isForeignKeyViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23503"
}
