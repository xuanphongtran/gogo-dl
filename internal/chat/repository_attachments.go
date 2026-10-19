package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/xuanphongtran/gogo-dl/internal/attachment"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

const attachmentProjection = `, COALESCE((SELECT jsonb_agg(jsonb_build_object(
 'id',a.id,'filename',a.filename,'content_type',a.processed_content_type,
 'size_bytes',a.processed_size_bytes,'sha256',a.processed_sha256,'verified',true) ORDER BY a.id)
 FROM attachments a WHERE a.message_id=m.id AND a.room_id=m.room_id AND a.uploader_id IS NOT NULL
 AND a.state='attached' AND m.deleted_at IS NULL),'[]'::jsonb) AS attachments`

// NewAttachmentRepository enables schema13 bindings plus schema10 atomic mentions.
func NewAttachmentRepository(db *sqlx.DB) Repository {
	return &explicitRepository{Repository: &postgresRepository{db: db, mentions: true, attachments: true}, db: db, mentions: true, attachments: true}
}
func (r *explicitRepository) AttachmentsEnabled() bool { return r.attachments }

func claimAttachments(ctx context.Context, tx *sqlx.Tx, user, room, message int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	type claim struct {
		ID       int64  `db:"id"`
		User     *int64 `db:"uploader_id"`
		Room     *int64 `db:"room_id"`
		State    string `db:"state"`
		Eligible bool   `db:"eligible"`
	}
	var rows []claim
	if err := tx.SelectContext(ctx, &rows, `SELECT id,uploader_id,room_id,state,(cleanup_after>clock_timestamp() AND processed_sha256 IS NOT NULL AND message_id IS NULL) AS eligible FROM attachments WHERE id=ANY($1) ORDER BY id FOR UPDATE`, pq.Array(ids)); err != nil {
		return fmt.Errorf("chat attachment claim lookup: %w", err)
	}
	if len(rows) != len(ids) {
		return apperror.ErrNotFound
	}
	for _, row := range rows {
		if row.User == nil || *row.User != user || row.Room == nil || *row.Room != room {
			return apperror.ErrNotFound
		}
		if row.State != "ready" || !row.Eligible {
			return apperror.ErrConflict
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE attachments SET state='attached',message_id=$2 WHERE id=ANY($1) AND state='ready' AND cleanup_after>clock_timestamp()`, pq.Array(ids), message)
	if err != nil {
		return fmt.Errorf("chat attachment bind: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("chat attachment bind count: %w", err)
	}
	if n != int64(len(ids)) {
		return apperror.ErrConflict
	}
	return nil
}

func loadMessageAttachments(ctx context.Context, tx *sqlx.Tx, msg *Message) error {
	if err := tx.GetContext(ctx, &msg.Attachments, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id,'filename',filename,'content_type',processed_content_type,'size_bytes',processed_size_bytes,'sha256',processed_sha256,'verified',true) ORDER BY id),'[]'::jsonb) FROM attachments WHERE message_id=$1 AND room_id=$2 AND uploader_id IS NOT NULL AND state='attached'`, msg.ID, msg.RoomID); err != nil {
		return fmt.Errorf("chat attachment metadata: %w", err)
	}
	if msg.DeletedAt != nil {
		msg.Attachments = attachment.List{}
	}
	return nil
}

// MessageForAttachmentEvent reads an authoritative post-commit account-cleanup snapshot.
// IDs originate only from the account deletion transaction, never a public request.
func (r *explicitRepository) MessageForAttachmentEvent(ctx context.Context, room, message int64) (*Message, error) {
	if !r.attachments {
		return nil, apperror.ErrAttachmentsUnavailable
	}
	var msg Message
	err := r.db.GetContext(ctx, &msg, `SELECT m.id,m.room_id,m.user_id,COALESCE(u.username,'[deleted user]') AS username,m.content,m.created_at,m.revision,m.edited_at,m.deleted_at`+mentionProjection+attachmentProjection+` FROM messages m LEFT JOIN users u ON u.id=m.user_id WHERE m.id=$1 AND m.room_id=$2`, message, room)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("chat attachment refresh: %w", err)
	}
	return &msg, nil
}

func appendMessageIntent(ctx context.Context, tx *sqlx.Tx, msg *Message, kind string) error {
	user := int64(0)
	if msg.UserID != nil {
		user = *msg.UserID
	}
	return outbox.AppendReference(ctx, tx, outbox.Reference{Kind: kind, AggregateType: "room", AggregateID: msg.RoomID, RoomID: msg.RoomID, UserID: user, MessageID: msg.ID, Version: msg.Revision}, "broker")
}
