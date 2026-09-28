package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// explicitRepository overrides the read paths that need stable projections
// and nullable message authors while retaining the existing write operations.
type explicitRepository struct {
	Repository
	db *sqlx.DB
}

func (r *explicitRepository) GetRoomByID(ctx context.Context, id int64) (*Room, error) {
	var room Room
	err := r.db.GetContext(ctx, &room,
		`SELECT id, name, created_by, created_at, visibility FROM rooms WHERE id = $1`, id,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("chat repo GetRoomByID: %w", err)
	}
	return &room, nil
}

func (r *explicitRepository) ListRooms(ctx context.Context) ([]*Room, error) {
	var rooms []*Room
	if err := r.db.SelectContext(ctx, &rooms,
		`SELECT id, name, created_by, created_at, visibility FROM rooms ORDER BY created_at DESC`,
	); err != nil {
		return nil, fmt.Errorf("chat repo ListRooms: %w", err)
	}
	return rooms, nil
}

func (r *explicitRepository) ListMessages(ctx context.Context, roomID int64, limit int, beforeID int64) ([]*Message, error) {
	if limit <= 0 {
		limit = 50
	}

	const projection = `
		SELECT m.id, m.room_id, m.user_id,
		       COALESCE(u.username, '[deleted user]') AS username,
		       m.content, m.created_at
		FROM messages m
		LEFT JOIN users u ON u.id = m.user_id
		WHERE m.room_id = $1`

	var (
		msgs []*Message
		err  error
	)
	if beforeID > 0 {
		err = r.db.SelectContext(ctx, &msgs, projection+`
			AND m.id < $2
			ORDER BY m.id DESC
			LIMIT $3`, roomID, beforeID, limit)
	} else {
		err = r.db.SelectContext(ctx, &msgs, projection+`
			ORDER BY m.id DESC
			LIMIT $2`, roomID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("chat repo ListMessages: %w", err)
	}
	return msgs, nil
}
