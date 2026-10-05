package notification

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// Worker materializes mention intents after the send transaction commits.
type Worker struct {
	db    *sqlx.DB
	queue *outbox.Store
	hub   *ws.Hub
}

// NewWorker constructs the bounded notification consumer.
func NewWorker(db *sqlx.DB, queue *outbox.Store, hub *ws.Hub) *Worker {
	return &Worker{db: db, queue: queue, hub: hub}
}

// Run claims one job per tick and exits on cancellation.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.process(ctx)
		}
	}
}
func (w *Worker) process(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := NewRepository(w.db).Sweep(cctx); err != nil {
		log.Warn().Err(err).Msg("notification retention failed")
	}
	job, err := w.queue.Claim(cctx, outbox.Notification)
	if err != nil {
		log.Warn().Err(err).Msg("notification claim failed")
		return
	}
	if job == nil {
		return
	}
	if err := w.deliver(cctx, job); err != nil {
		log.Warn().Err(err).Msg("notification delivery failed")
		if err := w.queue.Retry(cctx, outbox.Notification, job, "storage_unavailable"); err != nil {
			log.Warn().Err(err).Msg("notification retry failed")
		}
	}
}
func (w *Worker) deliver(ctx context.Context, job *outbox.Job) (err error) {
	tx, err := w.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("notification delivery begin: %w", err)
	}
	defer func() {
		if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("notification delivery rollback: %w", e))
		}
	}()
	// Match preference/account mutation lock order: user, room, membership,
	// global preference, room preference. Never hold preferences while waiting
	// for membership locks, which removal cascades also need.
	if err = lockUser(ctx, tx, job.UserID); err != nil {
		if errors.Is(err, apperror.ErrNotFound) {
			if e := w.finish(ctx, tx, job); e != nil {
				return e
			}
			return commitDelivery(tx)
		}
		return err
	}
	generation, err := lockMember(ctx, tx, job.UserID, job.RoomID, requireMember)
	if err != nil {
		if errors.Is(err, apperror.ErrNotFound) || errors.Is(err, apperror.ErrForbidden) {
			if e := w.finish(ctx, tx, job); e != nil {
				return e
			}
			return commitDelivery(tx)
		}
		return err
	}
	enabled, err := globalPreference(ctx, tx, job.UserID)
	if err != nil {
		return err
	}
	if generation != job.Generation || !enabled {
		if err = w.finish(ctx, tx, job); err != nil {
			return err
		}
		return commitDelivery(tx)
	}
	muted, err := roomPreference(ctx, tx, job.UserID, job.RoomID, generation)
	if err != nil {
		return err
	}
	if muted {
		if err = w.finish(ctx, tx, job); err != nil {
			return err
		}
		return commitDelivery(tx)
	}
	var deleted bool
	if err = tx.GetContext(ctx, &deleted, `SELECT deleted_at IS NOT NULL FROM messages WHERE id=$1 FOR SHARE`, job.MessageID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if e := w.finish(ctx, tx, job); e != nil {
				return e
			}
			return commitDelivery(tx)
		}
		return fmt.Errorf("notification source lookup: %w", err)
	}
	if deleted {
		if err = w.finish(ctx, tx, job); err != nil {
			return err
		}
		return commitDelivery(tx)
	}
	var id int64
	err = tx.QueryRowxContext(ctx, `INSERT INTO notifications(event_id,user_id,room_id,membership_generation,message_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(event_id,user_id,channel) DO NOTHING RETURNING id`, job.EventID, job.UserID, job.RoomID, generation, job.MessageID).Scan(&id)
	created := !errors.Is(err, sql.ErrNoRows)
	if err != nil && created {
		return fmt.Errorf("notification insert: %w", err)
	}
	if created {
		if err = outbox.AppendReference(ctx, tx, outbox.Reference{Kind: "notification.created", AggregateType: "user", AggregateID: job.UserID, RoomID: job.RoomID, UserID: job.UserID, MessageID: job.MessageID, Generation: generation, NotificationID: &id}, "broker"); err != nil {
			return err
		}
	}
	if err = w.finish(ctx, tx, job); err != nil {
		return err
	}
	if err = commitDelivery(tx); err != nil {
		return err
	}
	if created && w.hub != nil {
		if err := w.hub.BroadcastToUser(job.UserID, ws.Message{Type: ws.EventNotification, RoomID: fmt.Sprint(job.RoomID), Payload: map[string]any{"id": id, "kind": "mention", "room_id": job.RoomID, "message_id": job.MessageID}}); err != nil {
			log.Warn().Err(err).Int64("user_id", job.UserID).Msg("notification realtime delivery dropped")
		}
	}
	return nil
}
func (w *Worker) finish(ctx context.Context, tx *sqlx.Tx, job *outbox.Job) error {
	r, err := tx.ExecContext(ctx, `UPDATE domain_outbox_deliveries SET state='done',lease_token=NULL,lease_until=NULL,completed_at=clock_timestamp(),last_error=NULL WHERE event_id=$1 AND purpose='notification' AND state='leased' AND lease_token=$2 AND lease_until>clock_timestamp()`, job.EventID, job.Token)
	if err != nil {
		return fmt.Errorf("notification finish lease: %w", err)
	}
	n, err := r.RowsAffected()
	if err != nil {
		return fmt.Errorf("notification lease affected: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("notification lease lost")
	}
	return nil
}

func commitDelivery(tx *sqlx.Tx) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("notification delivery commit: %w", err)
	}
	return nil
}
