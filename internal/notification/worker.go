package notification

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jmoiron/sqlx"
	"github.com/rs/zerolog/log"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"time"
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
	if _, err := w.db.ExecContext(ctx, `DELETE FROM notifications WHERE id IN (SELECT id FROM notifications WHERE created_at <= clock_timestamp()-interval '30 days' ORDER BY created_at,id LIMIT 100)`); err != nil {
		log.Warn().Err(err).Msg("notification retention failed")
	}
	if _, err := w.db.ExecContext(ctx, `DELETE FROM message_send_keys WHERE (user_id,request_key) IN (SELECT user_id,request_key FROM message_send_keys WHERE expires_at <= clock_timestamp() ORDER BY expires_at LIMIT 100)`); err != nil {
		log.Warn().Err(err).Msg("notification retry retention failed")
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	job, err := w.queue.Claim(cctx, outbox.Notification)
	if err != nil || job == nil {
		return
	}
	if err := w.deliver(cctx, job); err != nil {
		_ = w.queue.Retry(cctx, outbox.Notification, job, "storage_unavailable")
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
	var enabled bool
	if _, err = tx.ExecContext(ctx, `INSERT INTO notification_preferences(user_id) VALUES($1) ON CONFLICT DO NOTHING`, job.UserID); err != nil {
		return err
	}
	if err = tx.GetContext(ctx, &enabled, `SELECT mentions_enabled FROM notification_preferences WHERE user_id=$1 FOR UPDATE`, job.UserID); err != nil {
		return err
	}
	var generation int64
	if err = tx.GetContext(ctx, &generation, `SELECT membership_generation FROM room_members WHERE room_id=$1 AND user_id=$2 FOR SHARE`, job.RoomID, job.UserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if e := w.finish(ctx, tx, job); e != nil {
				return e
			}
			return tx.Commit()
		}
		return err
	}
	if generation != job.Generation || !enabled {
		if err = w.finish(ctx, tx, job); err != nil {
			return err
		}
		return tx.Commit()
	}
	var muted bool
	if _, err = tx.ExecContext(ctx, `INSERT INTO room_notification_preferences(room_id,user_id,membership_generation) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, job.RoomID, job.UserID, generation); err != nil {
		return err
	}
	if err = tx.GetContext(ctx, &muted, `SELECT muted FROM room_notification_preferences WHERE room_id=$1 AND user_id=$2 AND membership_generation=$3 FOR UPDATE`, job.RoomID, job.UserID, generation); err != nil {
		return err
	}
	if muted {
		if err = w.finish(ctx, tx, job); err != nil {
			return err
		}
		return tx.Commit()
	}
	var deleted bool
	if err = tx.GetContext(ctx, &deleted, `SELECT deleted_at IS NOT NULL FROM messages WHERE id=$1`, job.MessageID); err != nil {
		return err
	}
	if deleted {
		if err = w.finish(ctx, tx, job); err != nil {
			return err
		}
		return tx.Commit()
	}
	var id int64
	if err = tx.QueryRowxContext(ctx, `INSERT INTO notifications(event_id,user_id,room_id,membership_generation,message_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(event_id,user_id,channel) DO UPDATE SET event_id=EXCLUDED.event_id RETURNING id`, job.EventID, job.UserID, job.RoomID, generation, job.MessageID).Scan(&id); err != nil {
		return err
	}
	if err = w.finish(ctx, tx, job); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if w.hub != nil {
		if err := w.hub.BroadcastToUser(job.UserID, ws.Message{Type: ws.EventNotification, RoomID: fmt.Sprint(job.RoomID), Payload: map[string]any{"id": id, "kind": "mention", "room_id": job.RoomID, "message_id": job.MessageID}}); err != nil {
			log.Warn().Err(err).Int64("user_id", job.UserID).Msg("notification realtime delivery dropped")
		}
	}
	return nil
}
func (w *Worker) finish(ctx context.Context, tx *sqlx.Tx, job *outbox.Job) error {
	r, err := tx.ExecContext(ctx, `UPDATE domain_outbox_deliveries SET state='done',lease_token=NULL,lease_until=NULL,completed_at=clock_timestamp(),last_error=NULL WHERE event_id=$1 AND purpose='notification' AND state='leased' AND lease_token=$2 AND lease_until>clock_timestamp()`, job.EventID, job.Token)
	if err != nil {
		return err
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
