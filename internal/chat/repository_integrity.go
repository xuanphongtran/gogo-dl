package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"go.opentelemetry.io/otel"
)

// CreateRoomWithMember atomically creates a room and its creator membership.
// The repository owns the transaction because both writes are one durable
// use case and must never be reported as partially successful.
func (r *postgresRepository) CreateRoomWithMember(ctx context.Context, room *Room, userID int64) error {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.CreateRoomWithMember")
	defer span.End()
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
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.RemoveMember")
	defer span.End()
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
		if memberErr != nil {
			return memberErr
		}
		if member.Role == RoomRoleOwner {
			return apperror.ErrOwnerTransfer
		}
		return apperror.ErrNotFound
	}
	return nil
}

// RemoveMemberWithAuthorization keeps role reads and deletion in one transaction.
// The callback belongs to the service so persistence does not define permissions.
func (r *postgresRepository) RemoveMemberWithAuthorization(ctx context.Context, roomID, actorID, userID int64, authorize func(actor, target *RoomMember) error) (*RoomMember, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.RemoveMemberWithAuthorization")
	defer span.End()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("chat repo RemoveMemberWithAuthorization begin: %w", err)
	}
	rollback := func(opErr error) (*RoomMember, error) {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return nil, errors.Join(opErr, fmt.Errorf("chat repo RemoveMemberWithAuthorization rollback: %w", rollbackErr))
		}
		return nil, opErr
	}

	// Ownership transfers lock the room first too, keeping the lock order stable.
	var lockedRoomID int64
	if err := tx.GetContext(ctx, &lockedRoomID, `SELECT id FROM rooms WHERE id = $1 FOR UPDATE`, roomID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rollback(apperror.ErrNotFound)
		}
		return rollback(fmt.Errorf("chat repo RemoveMemberWithAuthorization room: %w", err))
	}
	var members []*RoomMember
	if err := tx.SelectContext(ctx, &members, `
		SELECT rm.room_id, rm.user_id, u.username, rm.role, rm.joined_at
		FROM room_members rm
		JOIN users u ON u.id = rm.user_id
		WHERE rm.room_id = $1 AND rm.user_id IN ($2, $3)
		ORDER BY rm.user_id
		FOR UPDATE OF rm`, roomID, actorID, userID); err != nil {
		return rollback(fmt.Errorf("chat repo RemoveMemberWithAuthorization members: %w", err))
	}
	var actor, target *RoomMember
	for _, member := range members {
		if member.UserID == actorID {
			actor = member
		}
		if member.UserID == userID {
			target = member
		}
	}
	if err := authorize(actor, target); err != nil {
		return rollback(err)
	}

	result, err := tx.ExecContext(ctx,
		`DELETE FROM room_members WHERE room_id = $1 AND user_id = $2 AND role <> 'owner'`, roomID, userID)
	if err != nil {
		return rollback(fmt.Errorf("chat repo RemoveMemberWithAuthorization delete: %w", err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return rollback(fmt.Errorf("chat repo RemoveMemberWithAuthorization rows affected: %w", err))
	}
	if affected == 0 {
		return rollback(apperror.ErrNotFound)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("chat repo RemoveMemberWithAuthorization commit: %w", err)
	}
	return target, nil
}
