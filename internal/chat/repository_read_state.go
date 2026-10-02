package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func (r *postgresRepository) GetReadState(ctx context.Context, roomID, userID int64, authorize ReadStatePolicy) (*ReadState, error) {
	state, _, err := r.readState(ctx, roomID, userID, 0, authorize)
	return state, err
}

func (r *postgresRepository) AdvanceReadState(ctx context.Context, roomID, userID, messageID int64, authorize ReadStatePolicy) (*ReadState, bool, error) {
	if messageID <= 0 {
		return nil, false, apperror.ErrInvalidRequest
	}
	return r.readState(ctx, roomID, userID, messageID, authorize)
}

// readState keeps membership stable through the cursor operation. Room-first
// locking follows ownership changes and membership removal, avoiding lock cycles.
func (r *postgresRepository) readState(ctx context.Context, roomID, userID, messageID int64, authorize ReadStatePolicy) (state *ReadState, changed bool, err error) {
	if roomID <= 0 || userID <= 0 || authorize == nil {
		return nil, false, apperror.ErrInvalidRequest
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("chat repo readState begin: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("chat repo readState rollback: %w", rollbackErr))
			state, changed = nil, false
		}
	}()

	var visibility RoomVisibility
	if err := tx.GetContext(ctx, &visibility, `SELECT visibility FROM rooms WHERE id = $1 FOR SHARE`, roomID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, apperror.ErrNotFound
		}
		return nil, false, fmt.Errorf("chat repo readState room: %w", err)
	}
	var member RoomMember
	memberErr := tx.GetContext(ctx, &member, `
		SELECT room_id, user_id, role, joined_at
		FROM room_members
		WHERE room_id = $1 AND user_id = $2
		FOR SHARE`, roomID, userID)
	var currentMember *RoomMember
	if memberErr == nil {
		currentMember = &member
	} else if !errors.Is(memberErr, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("chat repo readState membership: %w", memberErr)
	}
	if err := authorize(visibility, currentMember); err != nil {
		return nil, false, err
	}

	if messageID > 0 {
		var exists bool
		if err := tx.GetContext(ctx, &exists,
			`SELECT EXISTS (SELECT 1 FROM messages WHERE room_id = $1 AND id = $2)`, roomID, messageID); err != nil {
			return nil, false, fmt.Errorf("chat repo readState message: %w", err)
		}
		if !exists {
			return nil, false, apperror.ErrNotFound
		}
		var cursor int64
		upsertErr := tx.GetContext(ctx, &cursor, `
			INSERT INTO room_read_states (room_id, user_id, last_read_message_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (room_id, user_id) DO UPDATE
			SET last_read_message_id = GREATEST(room_read_states.last_read_message_id, EXCLUDED.last_read_message_id)
			WHERE room_read_states.last_read_message_id < EXCLUDED.last_read_message_id
			RETURNING last_read_message_id`, roomID, userID, messageID)
		if upsertErr != nil && !errors.Is(upsertErr, sql.ErrNoRows) {
			return nil, false, fmt.Errorf("chat repo readState upsert: %w", upsertErr)
		}
		changed = upsertErr == nil
	}

	state = &ReadState{}
	// A single statement sees the cursor and count in one PostgreSQL snapshot.
	if err := tx.GetContext(ctx, state, `
		SELECT $1::bigint AS room_id,
		       COALESCE(rs.last_read_message_id, 0) AS last_read_message_id,
		       (SELECT COUNT(*) FROM messages m
		        WHERE m.room_id = $1
		          AND m.id > COALESCE(rs.last_read_message_id, 0)
		          AND m.user_id IS DISTINCT FROM $2::bigint) AS unread_count
		FROM (SELECT 1) AS singleton
		LEFT JOIN room_read_states rs ON rs.room_id = $1 AND rs.user_id = $2`, roomID, userID); err != nil {
		return nil, false, fmt.Errorf("chat repo readState snapshot: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("chat repo readState commit: %w", err)
	}
	return state, changed, nil
}
