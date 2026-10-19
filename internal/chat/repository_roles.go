package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"go.opentelemetry.io/otel"
)

// SetMemberRoleWithAuthorization holds actor/target roles until the change commits.
func (r *postgresRepository) SetMemberRoleWithAuthorization(ctx context.Context, roomID, actorID, userID int64, role RoomRole, authorize func(actor, target *RoomMember) error) (changed bool, err error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.SetMemberRoleWithAuthorization")
	defer span.End()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("chat repo SetMemberRoleWithAuthorization begin: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("chat repo SetMemberRoleWithAuthorization rollback: %w", rollbackErr))
			changed = false
		}
	}()
	var lockedRoom int64
	if err := tx.GetContext(ctx, &lockedRoom, `SELECT id FROM rooms WHERE id = $1 FOR UPDATE`, roomID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, apperror.ErrNotFound
		}
		return false, fmt.Errorf("chat repo SetMemberRoleWithAuthorization room: %w", err)
	}
	var members []*RoomMember
	if err := tx.SelectContext(ctx, &members, `SELECT rm.room_id, rm.user_id, u.username, rm.role, rm.joined_at
  FROM room_members rm JOIN users u ON u.id = rm.user_id
  WHERE rm.room_id = $1 AND rm.user_id IN ($2, $3)
  ORDER BY rm.user_id FOR UPDATE OF rm`, roomID, actorID, userID); err != nil {
		return false, fmt.Errorf("chat repo SetMemberRoleWithAuthorization members: %w", err)
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
		return false, err
	}
	changed = target.Role != role
	if changed {
		result, err := tx.ExecContext(ctx, `UPDATE room_members SET role = $3 WHERE room_id = $1 AND user_id = $2 AND role <> 'owner'`, roomID, userID, role)
		if err != nil {
			return false, fmt.Errorf("chat repo SetMemberRoleWithAuthorization update: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("chat repo SetMemberRoleWithAuthorization rows affected: %w", err)
		}
		if affected != 1 {
			return false, apperror.ErrConflict
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("chat repo SetMemberRoleWithAuthorization commit: %w", err)
	}
	return changed, nil
}
