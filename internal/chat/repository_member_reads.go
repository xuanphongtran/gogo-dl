package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"go.opentelemetry.io/otel"
)

// readMemberData holds room/membership locks through the protected read. Removal
// either precedes authorization or waits for this read's transaction to finish.
func readMemberData(ctx context.Context, db *sqlx.DB, roomID, userID int64, authorize ReadStatePolicy, read func(*sqlx.Tx) error) (err error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.memberRead")
	defer span.End()
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("chat repo member read begin: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("chat repo member read rollback: %w", rollbackErr))
		}
	}()
	var visibility RoomVisibility
	if err := tx.GetContext(ctx, &visibility, `SELECT visibility FROM rooms WHERE id = $1 FOR SHARE`, roomID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperror.ErrNotFound
		}
		return fmt.Errorf("chat repo member read room: %w", err)
	}
	var member RoomMember
	memberErr := tx.GetContext(ctx, &member, `SELECT room_id, user_id, role, joined_at FROM room_members WHERE room_id = $1 AND user_id = $2 FOR SHARE`, roomID, userID)
	var current *RoomMember
	if memberErr == nil {
		current = &member
	} else if !errors.Is(memberErr, sql.ErrNoRows) {
		return fmt.Errorf("chat repo member read membership: %w", memberErr)
	}
	if err := authorize(visibility, current); err != nil {
		return err
	}
	if err := read(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("chat repo member read commit: %w", err)
	}
	return nil
}

// ListMessagesForMember authorizes history in the same transaction as its read.
func (r *postgresRepository) ListMessagesForMember(ctx context.Context, roomID, userID int64, limit int, beforeID int64, authorize ReadStatePolicy) ([]*Message, error) {
	var messages []*Message
	err := readMemberData(ctx, r.db, roomID, userID, authorize, func(tx *sqlx.Tx) error {
		var err error
		messages, err = listMessageHistory(ctx, tx, r.mentions, roomID, limit, beforeID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return messages, nil
}

// ListMembersForUser keeps the caller authorized until the member list is read.
func (r *postgresRepository) ListMembersForUser(ctx context.Context, roomID, userID int64, authorize ReadStatePolicy) ([]*RoomMember, error) {
	var members []*RoomMember
	err := readMemberData(ctx, r.db, roomID, userID, authorize, func(tx *sqlx.Tx) error {
		if err := tx.SelectContext(ctx, &members, `SELECT rm.room_id, rm.user_id, u.username, rm.role, rm.joined_at
   FROM room_members rm JOIN users u ON u.id = rm.user_id
   WHERE rm.room_id = $1 ORDER BY rm.joined_at ASC, rm.user_id ASC`, roomID); err != nil {
			return fmt.Errorf("chat repo member read list: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return members, nil
}
