package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// CreateRoomWithMember atomically creates a room and its creator membership.
// The repository owns the transaction because both writes are one durable
// use case and must never be reported as partially successful.
func (r *postgresRepository) CreateRoomWithMember(ctx context.Context, room *Room, userID int64) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("chat repo CreateRoomWithMember begin: %w", err)
	}

	rollback := func(opErr error) error {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return fmt.Errorf("chat repo CreateRoomWithMember rollback: %v: %w", rollbackErr, opErr)
		}
		return opErr
	}

	room.CreatedBy = userID
	row := tx.QueryRowxContext(ctx, `
		INSERT INTO rooms (name, created_by, visibility)
		VALUES ($1, $2, COALESCE(NULLIF($3, ''), 'public'))
		RETURNING id, created_at`, room.Name, room.CreatedBy, room.Visibility)
	if err := row.Scan(&room.ID, &room.CreatedAt); err != nil {
		if isPostgresConstraintCode(err, "23503") {
			return rollback(apperror.ErrNotFound)
		}
		return rollback(fmt.Errorf("chat repo CreateRoomWithMember room: %w", err))
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO room_members (room_id, user_id, role) VALUES ($1, $2, 'owner')`,
		room.ID, userID,
	); err != nil {
		return rollback(fmt.Errorf("chat repo CreateRoomWithMember member: %w", err))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("chat repo CreateRoomWithMember commit: %w", err)
	}
	return nil
}

// RemoveMember deletes durable membership and reports when the target was not
// a member. The caller performs WebSocket revocation only after this succeeds.
func (r *postgresRepository) RemoveMember(ctx context.Context, roomID, userID int64) error {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM room_members WHERE room_id = $1 AND user_id = $2 AND role <> 'owner'`,
		roomID, userID,
	)
	if err != nil {
		return fmt.Errorf("chat repo RemoveMember: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("chat repo RemoveMember rows affected: %w", err)
	}
	if affected == 0 {
		member, memberErr := r.GetMember(ctx, roomID, userID)
		if memberErr == nil && member.Role == RoomRoleOwner {
			return apperror.ErrOwnerTransfer
		}
		return apperror.ErrNotFound
	}
	return nil
}
