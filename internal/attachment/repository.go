package attachment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

const projection = `id,room_id,uploader_id,object_key,filename,content_type,size_bytes,sha256,request_hash,state,created_at,upload_expires_at,cleanup_after`

type postgresRepository struct{ db *sqlx.DB }

// NewRepository creates PostgreSQL attachment storage.
func NewRepository(db *sqlx.DB) Repository { return &postgresRepository{db: db} }

func rollback(tx *sqlx.Tx, err *error) {
	if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
		*err = errors.Join(*err, fmt.Errorf("attachment rollback: %w", e))
	}
}

func authorize(ctx context.Context, tx *sqlx.Tx, roomID, userID int64, policy AccessPolicy) error {
	var private bool
	if err := tx.GetContext(ctx, &private, `SELECT visibility='private' FROM rooms WHERE id=$1 FOR SHARE`, roomID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperror.ErrNotFound
		}
		return fmt.Errorf("attachment room: %w", err)
	}
	var member int64
	err := tx.GetContext(ctx, &member, `SELECT user_id FROM room_members WHERE room_id=$1 AND user_id=$2 FOR SHARE`, roomID, userID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("attachment membership: %w", err)
	}
	return policy(private, err == nil)
}

func (r *postgresRepository) Reserve(ctx context.Context, roomID, userID int64, key, digest string, req *UploadRequest, policy AccessPolicy) (row *Reservation, err error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("attachment reserve begin: %w", err)
	}
	defer rollback(tx, &err)
	if err := authorize(ctx, tx, roomID, userID, policy); err != nil {
		return nil, err
	}
	// User then room quota locks serialize reservations across rooms/users.
	// SHARE on room is sufficient for authorization; advisory quota locks avoid
	// upgrading room locks after membership reads (which can deadlock).
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('attachment-user:' || $1::text,0))`, userID); err != nil {
		return nil, fmt.Errorf("attachment user quota lock: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('attachment-room:' || $1::text,0))`, roomID); err != nil {
		return nil, fmt.Errorf("attachment room quota lock: %w", err)
	}
	existing := &Reservation{}
	lookupErr := tx.GetContext(ctx, existing, `SELECT `+projection+` FROM attachments WHERE uploader_id=$1 AND request_key=$2`, userID, key)
	if lookupErr == nil {
		if existing.RoomID == nil || *existing.RoomID != roomID || existing.RequestHash != digest || existing.State != "pending_upload" || !time.Now().Before(existing.UploadExpiresAt) {
			return nil, apperror.ErrConflict
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("attachment retry commit: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(lookupErr, sql.ErrNoRows) {
		return nil, fmt.Errorf("attachment retry lookup: %w", lookupErr)
	}
	var userBytes, roomBytes, pending int64
	if err := tx.QueryRowxContext(ctx, `SELECT
        COALESCE((SELECT SUM(size_bytes) FROM attachments WHERE uploader_id=$1 AND state<>'deleted'),0),
        COALESCE((SELECT SUM(size_bytes) FROM attachments WHERE room_id=$2 AND state<>'deleted'),0),
        (SELECT COUNT(*) FROM attachments WHERE uploader_id=$1 AND state<>'deleted')`, userID, roomID).Scan(&userBytes, &roomBytes, &pending); err != nil {
		return nil, fmt.Errorf("attachment quota: %w", err)
	}
	// All admitted rows are pending/unattached in this scanner-pending slice.
	if userBytes+req.SizeBytes > 100<<20 || roomBytes+req.SizeBytes > 10<<30 || pending >= 10 {
		return nil, apperror.ErrConflict
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("attachment object ID: %w", err)
	}
	row = &Reservation{}
	if err := tx.GetContext(ctx, row, `INSERT INTO attachments(room_id,uploader_id,object_key,filename,content_type,size_bytes,sha256,request_key,request_hash,upload_expires_at,cleanup_after)
        VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,clock_timestamp()+interval '5 minutes',clock_timestamp()+interval '15 minutes')
        RETURNING `+projection, roomID, userID, "quarantine/"+id.String(), req.Filename, req.ContentType, req.SizeBytes, req.SHA256, key, digest); err != nil {
		return nil, fmt.Errorf("attachment insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("attachment reserve commit: %w", err)
	}
	return row, nil
}

func (r *postgresRepository) Get(ctx context.Context, roomID, userID, id int64, policy AccessPolicy) (row *Reservation, err error) {
	return r.access(ctx, roomID, userID, id, "get", policy)
}

func (r *postgresRepository) Transition(ctx context.Context, roomID, userID, id int64, action string, policy AccessPolicy) (row *Reservation, err error) {
	if action != "cancel" && action != "complete" {
		return nil, apperror.ErrInvalidRequest
	}
	return r.access(ctx, roomID, userID, id, action, policy)
}

func (r *postgresRepository) access(ctx context.Context, roomID, userID, id int64, action string, policy AccessPolicy) (row *Reservation, err error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("attachment access begin: %w", err)
	}
	defer rollback(tx, &err)
	if err := authorize(ctx, tx, roomID, userID, policy); err != nil {
		return nil, err
	}
	row = &Reservation{}
	if err := tx.GetContext(ctx, row, `SELECT `+projection+` FROM attachments WHERE id=$1 AND room_id=$2 AND uploader_id=$3 FOR UPDATE`, id, roomID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperror.ErrNotFound
		}
		return nil, fmt.Errorf("attachment lookup: %w", err)
	}
	if action == "complete" && row.State != "scanning" {
		if row.State != "pending_upload" || !time.Now().Before(row.UploadExpiresAt) {
			return nil, apperror.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE attachments SET state='scanning' WHERE id=$1`, id); err != nil {
			return nil, fmt.Errorf("attachment complete: %w", err)
		}
		if err := appendIntent(ctx, tx, row, outbox.Scan); err != nil {
			return nil, err
		}
		row.State = "scanning"
	}
	if action == "cancel" && row.State != "deleted" && row.State != "deleting" {
		if _, err := tx.ExecContext(ctx, `UPDATE attachments SET state='cancelled' WHERE id=$1`, id); err != nil {
			return nil, fmt.Errorf("attachment cancel: %w", err)
		}
		if err := appendIntent(ctx, tx, row, outbox.Cleanup); err != nil {
			return nil, err
		}
		row.State = "cancelled"
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("attachment access commit: %w", err)
	}
	return row, nil
}

func appendIntent(ctx context.Context, tx *sqlx.Tx, row *Reservation, purpose string) error {
	kind, when, key := "attachment.scan_requested", time.Now(), ""
	if purpose == outbox.Cleanup {
		kind, when, key = "attachment.cleanup_requested", row.CleanupAfter, row.ObjectKey
	}
	return outbox.Append(ctx, tx, outbox.Intent{Kind: kind, AggregateType: "attachment", AggregateID: row.ID, RoomID: row.RoomID, UserID: row.UploaderID, AttachmentID: row.ID}, purpose, when, key)
}

// Sweep preserves opaque tombstones through parent cascades and cleans abandoned
// reservations only after upload expiry plus grace. Each batch is bounded.
func (r *postgresRepository) Sweep(ctx context.Context) (err error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("attachment sweep begin: %w", err)
	}
	defer rollback(tx, &err)
	var rows []*Reservation
	if err := tx.SelectContext(ctx, &rows, `SELECT `+projection+` FROM attachments
        WHERE cleanup_after<=clock_timestamp() AND state NOT IN ('deleted','deleting')
        ORDER BY cleanup_after,id FOR UPDATE SKIP LOCKED LIMIT 20`); err != nil {
		return fmt.Errorf("attachment sweep read: %w", err)
	}
	for _, row := range rows {
		if err := appendIntent(ctx, tx, row, outbox.Cleanup); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE attachments SET state='deleting' WHERE id=$1`, row.ID); err != nil {
			return fmt.Errorf("attachment sweep mark: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("attachment sweep commit: %w", err)
	}
	return nil
}
