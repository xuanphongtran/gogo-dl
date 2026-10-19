package attachment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type scanRepository struct{ db *sqlx.DB }

// NewScanRepository creates the schema-11 fenced scan repository.
func NewScanRepository(db *sqlx.DB) ScanRepository { return &scanRepository{db} }
func scanLease(ctx context.Context, tx *sqlx.Tx, job *outbox.Job) error {
	if job == nil || job.AttachmentID <= 0 {
		return fmt.Errorf("attachment scan invalid job")
	}
	var id string
	err := tx.GetContext(ctx, &id, `SELECT d.event_id FROM domain_outbox_deliveries d JOIN domain_outbox o ON o.event_id=d.event_id
 WHERE d.event_id=$1 AND d.purpose='attachment_scan' AND d.state='leased' AND d.lease_token=$2
 AND d.lease_until>clock_timestamp() AND o.kind='attachment.scan_requested' AND o.attachment_id=$3
 FOR UPDATE OF d`, job.EventID, job.Token, job.AttachmentID)
	if err != nil {
		return fmt.Errorf("attachment scan lease: %w", err)
	}
	return nil
}
func ackScan(ctx context.Context, tx *sqlx.Tx, job *outbox.Job) error {
	result, err := tx.ExecContext(ctx, `UPDATE domain_outbox_deliveries SET state='done',lease_token=NULL,lease_until=NULL,completed_at=clock_timestamp(),last_error=NULL
 WHERE event_id=$1 AND purpose='attachment_scan' AND state='leased' AND lease_token=$2 AND lease_until>clock_timestamp()`, job.EventID, job.Token)
	if err != nil {
		return fmt.Errorf("attachment scan acknowledge: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("attachment scan acknowledge count: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("attachment scan lease lost")
	}
	return nil
}
func scanRow(ctx context.Context, tx *sqlx.Tx, job *outbox.Job, prepare bool) (*Reservation, error) {
	var hint struct {
		RoomID *int64 `db:"room_id"`
		UserID *int64 `db:"uploader_id"`
	}
	if err := tx.GetContext(ctx, &hint, `SELECT room_id,uploader_id FROM attachments WHERE id=$1`, job.AttachmentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("attachment scan references: %w", err)
	}
	if hint.RoomID == nil || hint.UserID == nil {
		return nil, nil
	}
	if err := authorize(ctx, tx, *hint.RoomID, *hint.UserID, requireMember); err != nil {
		if errors.Is(err, apperror.ErrNotFound) || errors.Is(err, apperror.ErrForbidden) {
			return nil, nil
		}
		return nil, err
	}
	var row Reservation
	if err := tx.GetContext(ctx, &row, `SELECT `+projection+` FROM attachments WHERE id=$1 FOR UPDATE`, job.AttachmentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("attachment scan row: %w", err)
	}
	if row.RoomID == nil || row.UploaderID == nil || *row.RoomID != *hint.RoomID || *row.UploaderID != *hint.UserID || row.State != "scanning" {
		return nil, nil
	}
	var eligible bool
	seconds := 0
	if prepare {
		seconds = 60
	}
	if err := tx.GetContext(ctx, &eligible, `SELECT cleanup_after>clock_timestamp()+$2*interval '1 second' FROM attachments WHERE id=$1`, row.ID, seconds); err != nil {
		return nil, fmt.Errorf("attachment scan expiry: %w", err)
	}
	if !eligible {
		return nil, nil
	}
	return &row, nil
}
func (r *scanRepository) PrepareScan(ctx context.Context, job *outbox.Job) (row *Reservation, err error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("attachment scan prepare begin: %w", err)
	}
	defer rollback(tx, &err)
	if err := scanLease(ctx, tx, job); err != nil {
		return nil, err
	}
	row, err = scanRow(ctx, tx, job, true)
	if err != nil {
		return nil, err
	}
	if row == nil {
		if err := ackScan(ctx, tx, job); err != nil {
			return nil, err
		}
	} else {
		result, err := tx.ExecContext(ctx, `UPDATE attachments SET scan_started_at=COALESCE(scan_started_at,clock_timestamp()) WHERE id=$1`, row.ID)
		if err != nil {
			return nil, fmt.Errorf("attachment scan start: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("attachment scan start count: %w", err)
		}
		if n != 1 {
			return nil, fmt.Errorf("attachment scan start row missing")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("attachment scan prepare commit: %w", err)
	}
	return row, nil
}
func (r *scanRepository) FinishScan(ctx context.Context, job *outbox.Job, outcome string) (err error) {
	if outcome != "clean" && outcome != "infected" && outcome != "invalid_content" && outcome != "scanner_rejected" {
		return fmt.Errorf("attachment scan invalid outcome")
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("attachment scan finish begin: %w", err)
	}
	defer rollback(tx, &err)
	if err := scanLease(ctx, tx, job); err != nil {
		return err
	}
	row, err := scanRow(ctx, tx, job, false)
	if err != nil {
		return err
	}
	if err := ackScan(ctx, tx, job); err != nil {
		return err
	}
	if row != nil {
		result, err := tx.ExecContext(ctx, `UPDATE attachments SET
 state=CASE WHEN $2='clean' THEN 'ready' ELSE 'rejected' END,
 scan_completed_at=clock_timestamp(),scan_error=CASE WHEN $2='clean' THEN NULL ELSE $2 END,
 cleanup_after=CASE WHEN $2='clean' THEN GREATEST(cleanup_after,clock_timestamp()+interval '24 hours') ELSE cleanup_after END
 WHERE id=$1 AND scan_started_at IS NOT NULL`, row.ID, outcome)
		if err != nil {
			return fmt.Errorf("attachment scan persist: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("attachment scan persist count: %w", err)
		}
		if n != 1 {
			return fmt.Errorf("attachment scan not prepared")
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("attachment scan finish commit: %w", err)
	}
	return nil
}
