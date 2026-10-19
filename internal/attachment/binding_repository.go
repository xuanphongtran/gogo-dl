package attachment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// NewBoundRepository enables schema13 metadata, retention and download authorization.
func NewBoundRepository(db *sqlx.DB) Repository { return &postgresRepository{db: db, bound: true} }

// WithDownload keeps membership, message and binding stable through local signing.
func (r *postgresRepository) WithDownload(ctx context.Context, room, user, message, id int64, policy AccessPolicy, sign func(*DownloadObject) error) (err error) {
	if !r.bound {
		return apperror.ErrAttachmentsUnavailable
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("attachment download begin: %w", err)
	}
	defer rollback(tx, &err)
	if err := authorize(ctx, tx, room, user, policy); err != nil {
		return err
	}
	var live int64
	if err := tx.GetContext(ctx, &live, `SELECT id FROM messages WHERE id=$1 AND room_id=$2 AND deleted_at IS NULL FOR SHARE`, message, room); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperror.ErrNotFound
		}
		return fmt.Errorf("attachment download message: %w", err)
	}
	var obj DownloadObject
	err = tx.GetContext(ctx, &obj, `SELECT object_key,filename,processed_content_type AS content_type,processed_size_bytes AS size_bytes,processed_sha256 AS sha256 FROM attachments WHERE id=$1 AND room_id=$2 AND message_id=$3 AND state='attached' AND uploader_id IS NOT NULL FOR SHARE`, id, room, message)
	if errors.Is(err, sql.ErrNoRows) {
		return apperror.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("attachment download binding: %w", err)
	}
	if err := sign(&obj); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("attachment download commit: %w", err)
	}
	return nil
}
