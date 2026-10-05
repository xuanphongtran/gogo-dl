// Package outbox persists immutable intents with independent fenced deliveries.
package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const (
	// Scan identifies the independent scan delivery purpose.
	Scan = "attachment_scan"
	// Cleanup identifies the independent object cleanup delivery purpose.
	Cleanup = "attachment_cleanup"
	// Notification owns mention materialization independently of cleanup and relay.
	Notification = "notification"
)

// Intent contains server-owned references, never message contents or signed URLs.
type Intent struct {
	Kind          string
	AggregateType string
	AggregateID   int64
	RoomID        *int64
	UserID        *int64
	AttachmentID  int64
}

// Append writes intent and purpose in the caller's mutation transaction.
// Callers serialize the aggregate before allocating its sequence.
func Append(ctx context.Context, tx *sqlx.Tx, intent Intent, purpose string, availableAt time.Time, objectKey string) error {
	var existing bool
	if err := tx.GetContext(ctx, &existing, `SELECT EXISTS (SELECT 1 FROM domain_outbox WHERE kind=$1 AND attachment_id=$2)`, intent.Kind, intent.AttachmentID); err != nil {
		return fmt.Errorf("outbox check intent: %w", err)
	}
	if existing {
		return nil
	}
	var seq int64
	if err := tx.GetContext(ctx, &seq, `INSERT INTO domain_aggregate_counters(aggregate_type,aggregate_id,last_seq)
        VALUES($1,$2,1) ON CONFLICT(aggregate_type,aggregate_id) DO UPDATE
        SET last_seq=domain_aggregate_counters.last_seq+1 RETURNING last_seq`, intent.AggregateType, intent.AggregateID); err != nil {
		return fmt.Errorf("outbox allocate sequence: %w", err)
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("outbox event ID: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_outbox(event_id,schema_version,kind,aggregate_type,aggregate_id,aggregate_seq,room_id,user_id,attachment_id)
        VALUES($1,1,$2,$3,$4,$5,$6,$7,$8)`, id.String(), intent.Kind, intent.AggregateType, intent.AggregateID, seq, intent.RoomID, intent.UserID, intent.AttachmentID); err != nil {
		return fmt.Errorf("outbox append: %w", err)
	}
	if purpose == Cleanup {
		if _, err := tx.ExecContext(ctx, `INSERT INTO attachment_cleanup_objects(event_id,object_key) VALUES($1,$2)`, id.String(), objectKey); err != nil {
			return fmt.Errorf("outbox cleanup reference: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_outbox_deliveries(event_id,purpose,available_at) VALUES($1,$2,$3)`, id.String(), purpose, availableAt); err != nil {
		return fmt.Errorf("outbox purpose: %w", err)
	}
	return nil
}

// Job is one purpose's leased work; Token fences expired workers.
type Job struct {
	EventID      string `db:"event_id"`
	Token        string `db:"lease_token"`
	AttachmentID int64  `db:"attachment_id"`
	ObjectKey    string `db:"object_key"`
	RoomID       int64  `db:"room_id"`
	UserID       int64  `db:"user_id"`
	MessageID    int64  `db:"message_id"`
	Generation   int64  `db:"membership_generation"`
}

// Store owns delivery claims and acknowledgements, not immutable event updates.
type Store struct{ db *sqlx.DB }

// NewStore creates PostgreSQL delivery storage.
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// Claim commits a short claim before any provider I/O. An empty queue returns nil.
func (s *Store) Claim(ctx context.Context, purpose string) (*Job, error) {
	if purpose != Scan && purpose != Cleanup && purpose != "broker" && purpose != Notification {
		return nil, fmt.Errorf("outbox invalid purpose")
	}
	if _, err := s.db.ExecContext(ctx, `WITH exhausted AS (
 SELECT event_id,purpose FROM domain_outbox_deliveries
 WHERE purpose=$1 AND attempts>=8 AND (state='pending' OR (state='leased' AND lease_until<=clock_timestamp()))
 ORDER BY available_at,event_id FOR UPDATE SKIP LOCKED LIMIT 20
 ) UPDATE domain_outbox_deliveries d SET state='dead',lease_token=NULL,lease_until=NULL,last_error='storage_unavailable'
 FROM exhausted e WHERE d.event_id=e.event_id AND d.purpose=e.purpose`, purpose); err != nil {
		return nil, fmt.Errorf("outbox exhausted leases: %w", err)
	}
	token, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("outbox lease ID: %w", err)
	}
	var job Job
	projection := `c.event_id,c.lease_token,COALESCE(o.attachment_id,0) AS attachment_id,COALESCE(k.object_key,'') AS object_key`
	if purpose == Notification {
		projection = `c.event_id,c.lease_token,COALESCE(o.attachment_id,0) AS attachment_id,COALESCE(k.object_key,'') AS object_key,
        o.room_id,o.user_id,o.message_id,o.membership_generation`
	}
	err = s.db.GetContext(ctx, &job, `WITH candidate AS (
        SELECT event_id,purpose FROM domain_outbox_deliveries
        WHERE purpose=$1 AND attempts<8 AND available_at<=clock_timestamp()
          AND ($1<>'attachment_cleanup' OR EXISTS (SELECT 1 FROM domain_outbox o JOIN attachments a ON a.id=o.attachment_id WHERE o.event_id=domain_outbox_deliveries.event_id AND a.state='deleting'))
          AND (state='pending' OR (state='leased' AND lease_until<=clock_timestamp()))
        ORDER BY available_at,event_id FOR UPDATE SKIP LOCKED LIMIT 1
    ), claimed AS (
        UPDATE domain_outbox_deliveries d SET state='leased',lease_token=$2,lease_until=clock_timestamp()+interval '30 seconds',attempts=attempts+1
        FROM candidate c WHERE d.event_id=c.event_id AND d.purpose=c.purpose
        RETURNING d.event_id,d.lease_token
    ) SELECT `+projection+`
      FROM claimed c JOIN domain_outbox o ON o.event_id=c.event_id
      LEFT JOIN attachment_cleanup_objects k ON k.event_id=c.event_id`, purpose, token.String())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("outbox claim: %w", err)
	}
	return &job, nil
}

// FinishCleanup atomically acknowledges only a live cleanup lease and its tombstone.
func (s *Store) FinishCleanup(ctx context.Context, job *Job) (err error) {
	if job == nil {
		return fmt.Errorf("outbox missing job")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("outbox finish begin: %w", err)
	}
	defer func() {
		if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("outbox finish rollback: %w", e))
		}
	}()
	res, err := tx.ExecContext(ctx, `UPDATE domain_outbox_deliveries SET state='done',lease_token=NULL,lease_until=NULL,completed_at=clock_timestamp(),last_error=NULL
        WHERE event_id=$1 AND purpose='attachment_cleanup' AND state='leased' AND lease_token=$2 AND lease_until>clock_timestamp()`, job.EventID, job.Token)
	if err != nil {
		return fmt.Errorf("outbox finish delivery: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("outbox finish affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("outbox cleanup lease lost")
	}
	res, err = tx.ExecContext(ctx, `UPDATE attachments SET state='deleted' WHERE id=$1 AND state='deleting'
 AND EXISTS (SELECT 1 FROM domain_outbox WHERE event_id=$2 AND attachment_id=$1)`, job.AttachmentID, job.EventID)
	if err != nil {
		return fmt.Errorf("outbox finish attachment: %w", err)
	}
	n, err = res.RowsAffected()
	if err != nil {
		return fmt.Errorf("outbox finish attachment affected: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("outbox cleanup tombstone missing")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("outbox finish commit: %w", err)
	}
	return nil
}

// Retry updates only the same live purpose/lease, with bounded retries and safe categories.
func (s *Store) Retry(ctx context.Context, purpose string, job *Job, category string) error {
	if job == nil {
		return fmt.Errorf("outbox missing job")
	}
	if category != "storage_unavailable" && category != "invalid_job" {
		return fmt.Errorf("outbox invalid failure category")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE domain_outbox_deliveries SET
        state=CASE WHEN attempts>=8 OR $4='invalid_job' THEN 'dead' ELSE 'pending' END,
        lease_token=NULL,lease_until=NULL,last_error=$4,
        available_at=clock_timestamp()+LEAST(1200,5*power(2,LEAST(attempts,8))) * interval '1 second'
        WHERE event_id=$1 AND purpose=$2 AND state='leased' AND lease_token=$3 AND lease_until>clock_timestamp()`, job.EventID, purpose, job.Token, category)
	if err != nil {
		return fmt.Errorf("outbox retry: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("outbox retry affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("outbox retry lease lost")
	}
	return nil
}
