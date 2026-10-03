package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"go.opentelemetry.io/otel"
)

const searchMessagesSQL = `
    SELECT m.id, m.room_id, m.user_id, COALESCE(u.username, '[deleted user]') AS username,
           m.content, m.created_at, m.revision, m.edited_at, m.deleted_at
    FROM messages m LEFT JOIN users u ON u.id = m.user_id
    WHERE m.room_id = $1 AND m.deleted_at IS NULL
      AND m.search_vector @@ plainto_tsquery('simple'::regconfig, $2)`

func (r *postgresRepository) SearchMessages(ctx context.Context, roomID, userID int64, query string, limit int, beforeID int64, authorize ReadStatePolicy) (messages []*Message, err error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.SearchMessages")
	defer span.End()
	if roomID <= 0 || userID <= 0 || limit < 1 || limit > 101 || beforeID < 0 || authorize == nil {
		return nil, apperror.ErrInvalidRequest
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("chat repo SearchMessages begin: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("chat repo SearchMessages rollback: %w", rollbackErr))
			messages = nil
		}
	}()
	// Match mutation lock order and keep membership stable until search commits.
	var visibility RoomVisibility
	if err := tx.GetContext(ctx, &visibility, `SELECT visibility FROM rooms WHERE id = $1 FOR SHARE`, roomID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperror.ErrNotFound
		}
		return nil, fmt.Errorf("chat repo SearchMessages room: %w", err)
	}
	var member RoomMember
	var currentMember *RoomMember
	err = tx.GetContext(ctx, &member, `SELECT room_id, user_id, role, joined_at FROM room_members
        WHERE room_id = $1 AND user_id = $2 FOR SHARE`, roomID, userID)
	if err == nil {
		currentMember = &member
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("chat repo SearchMessages membership: %w", err)
	}
	if err := authorize(visibility, currentMember); err != nil {
		return nil, err
	}
	// PostgreSQL's parser decides searchability, including punctuation-only input.
	var searchable bool
	if err := tx.GetContext(ctx, &searchable, `SELECT numnode(plainto_tsquery('simple'::regconfig, $1)) > 0`, query); err != nil {
		return nil, fmt.Errorf("chat repo SearchMessages query: %w", err)
	}
	if !searchable {
		return nil, apperror.ErrInvalidRequest
	}
	projection := searchMessagesSQL
	if r.mentions {
		projection = strings.Replace(projection, "m.deleted_at\n", "m.deleted_at"+mentionProjection+"\n", 1)
	}
	if beforeID > 0 {
		err = tx.SelectContext(ctx, &messages, projection+` AND m.id < $3 ORDER BY m.id DESC LIMIT $4`, roomID, query, beforeID, limit)
	} else {
		err = tx.SelectContext(ctx, &messages, projection+` ORDER BY m.id DESC LIMIT $3`, roomID, query, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("chat repo SearchMessages select: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("chat repo SearchMessages commit: %w", err)
	}
	return messages, nil
}
