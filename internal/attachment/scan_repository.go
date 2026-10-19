package attachment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type scanRepository struct{ db *sqlx.DB }

// NewScanRepository creates the schema-12 fenced scan repository.
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

// ProcessedMetadata describes the immutable final artifact, separate from quota input.
type ProcessedMetadata struct {
	SHA256      string
	SizeBytes   int64
	ContentType string
	Engine      string
	Policy      string
}

func (p *ProcessedMetadata) valid() bool {
	return p != nil && p.SizeBytes > 0 && p.SizeBytes <= 10<<20 && regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(p.SHA256) &&
		(p.ContentType == "image/png" || p.ContentType == "image/jpeg" || p.ContentType == "text/plain") &&
		((p.Engine == "sanitize" && p.Policy == "sanitize-v1") || (p.Engine == "clamav" && p.Policy == "clamav-v1"))
}
func (r *scanRepository) FinishScan(ctx context.Context, job *outbox.Job, outcome string, artifact *ProcessedMetadata) (err error) {
	if (outcome == "clean" && !artifact.valid()) || (outcome != "clean" && artifact != nil) {
		return fmt.Errorf("attachment scan invalid artifact metadata")
	}

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
		var hash, media, engine, policy any
		var size any
		if artifact != nil {
			if artifact.ContentType != row.ContentType || (artifact.Engine == "clamav" && (artifact.SHA256 != row.SHA256 || artifact.SizeBytes != row.SizeBytes)) {
				return fmt.Errorf("attachment scan artifact identity mismatch")
			}
			hash, size, media, engine, policy = artifact.SHA256, artifact.SizeBytes, artifact.ContentType, artifact.Engine, artifact.Policy
		}
		result, err := tx.ExecContext(ctx, `UPDATE attachments SET
 state=CASE WHEN $2='clean' THEN 'ready' ELSE 'rejected' END,
 processed_sha256=$3,processed_size_bytes=$4,processed_content_type=$5,processing_engine=$6,processing_policy=$7,
 scan_completed_at=clock_timestamp(),scan_error=CASE WHEN $2='clean' THEN NULL ELSE $2 END,
 cleanup_after=CASE WHEN $2='clean' THEN GREATEST(cleanup_after,clock_timestamp()+interval '24 hours') ELSE cleanup_after END
 WHERE id=$1 AND scan_started_at IS NOT NULL`, row.ID, outcome, hash, size, media, engine, policy)
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
