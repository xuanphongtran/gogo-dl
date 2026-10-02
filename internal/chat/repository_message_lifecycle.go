package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"go.opentelemetry.io/otel"
)

// MessageMutationPolicy evaluates locked state without I/O. Missing membership
// or message is nil. False permits an idempotent response without a write.
type MessageMutationPolicy func(RoomVisibility, *RoomMember, *Message) (bool, error)

// EditMessage commits a policy-approved edit under membership and message locks.
func (r *postgresRepository) EditMessage(ctx context.Context, roomID, actorID, messageID int64, content string, policy MessageMutationPolicy) (*Message, bool, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.EditMessage")
	defer span.End()
	return r.mutateMessage(ctx, roomID, actorID, messageID, &content, policy)
}

// DeleteMessage commits a policy-approved tombstone with its deletion audit.
func (r *postgresRepository) DeleteMessage(ctx context.Context, roomID, actorID, messageID int64, policy MessageMutationPolicy) (*Message, bool, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.DeleteMessage")
	defer span.End()
	return r.mutateMessage(ctx, roomID, actorID, messageID, nil, policy)
}

// Nil content selects deletion. The service owns permissions and retry rules;
// this helper owns locks, database timestamps, audit metadata and the commit.
func (r *postgresRepository) mutateMessage(ctx context.Context, roomID, actorID, messageID int64, content *string, policy MessageMutationPolicy) (*Message, bool, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.mutateMessage")
	defer span.End()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("chat repo mutateMessage begin: %w", err)
	}
	rollback := func(opErr error) (*Message, bool, error) {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			opErr = errors.Join(opErr, fmt.Errorf("chat repo mutateMessage rollback: %w", rollbackErr))
		}
		return nil, false, opErr
	}

	// Room first matches ownership-transfer locking. Shared membership locks
	// allow other readers but prevent revocation/demotion until this commits.
	var visibility RoomVisibility
	if err := tx.GetContext(ctx, &visibility, `SELECT visibility FROM rooms WHERE id = $1 FOR SHARE`, roomID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rollback(apperror.ErrNotFound)
		}
		return rollback(fmt.Errorf("chat repo mutateMessage room: %w", err))
	}
	var member *RoomMember
	var memberRow RoomMember
	err = tx.GetContext(ctx, &memberRow, `
        SELECT room_id, user_id, role, joined_at FROM room_members
        WHERE room_id = $1 AND user_id = $2 FOR SHARE`, roomID, actorID)
	if err == nil {
		member = &memberRow
	} else if !errors.Is(err, sql.ErrNoRows) {
		return rollback(fmt.Errorf("chat repo mutateMessage membership: %w", err))
	}
	var msg *Message
	var messageRow Message
	err = tx.GetContext(ctx, &messageRow, `
        SELECT m.id, m.room_id, m.user_id, COALESCE(u.username, '[deleted user]') AS username,
               m.content, m.created_at, m.revision, m.edited_at, m.deleted_at
        FROM messages m LEFT JOIN users u ON u.id = m.user_id
        WHERE m.room_id = $1 AND m.id = $2 FOR UPDATE OF m`, roomID, messageID)
	if err == nil {
		msg = &messageRow
	} else if !errors.Is(err, sql.ErrNoRows) {
		return rollback(fmt.Errorf("chat repo mutateMessage message: %w", err))
	}
	changed, err := policy(visibility, member, msg)
	if err != nil {
		return rollback(err)
	}
	if changed {
		if content != nil {
			err = tx.QueryRowxContext(ctx, `
                UPDATE messages SET content = $3, edited_at = clock_timestamp(), revision = revision + 1
                WHERE room_id = $1 AND id = $2 RETURNING edited_at, revision`, roomID, messageID, *content).
				Scan(&msg.EditedAt, &msg.Revision)
			msg.Content = *content
		} else {
			err = tx.QueryRowxContext(ctx, `
                UPDATE messages SET content = '', deleted_at = clock_timestamp(), deleted_by = $3, revision = revision + 1
                WHERE room_id = $1 AND id = $2 RETURNING deleted_at, revision`, roomID, messageID, actorID).
				Scan(&msg.DeletedAt, &msg.Revision)
			msg.Content = ""
		}
		if err != nil {
			return rollback(fmt.Errorf("chat repo mutateMessage write: %w", err))
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("chat repo mutateMessage commit: %w", err)
	}
	return msg, changed, nil
}
