package notification

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// Postgres stores private inbox rows and preferences, using explicit SQL.
type Postgres struct{ db *sqlx.DB }

// NewRepository constructs a PostgreSQL in-app adapter.
func NewRepository(db *sqlx.DB) *Postgres { return &Postgres{db: db} }

const notificationProjection = `n.id,n.kind,n.room_id,n.message_id,n.created_at,n.read_at,
    CASE WHEN m.deleted_at IS NULL THEN 'available' ELSE 'deleted' END AS availability`
const feedJoin = ` FROM notifications n JOIN messages m ON m.id=n.message_id
    JOIN room_members rm ON rm.room_id=n.room_id AND rm.user_id=n.user_id AND rm.membership_generation=n.membership_generation `

func (p *Postgres) List(ctx context.Context, user int64, q Query) ([]*Notification, error) {
	rows := []*Notification{}
	if err := p.db.SelectContext(ctx, &rows, `SELECT `+notificationProjection+feedJoin+`
        WHERE n.user_id=$1 AND ($2::bigint=0 OR n.id<$2) AND (NOT $3 OR n.read_at IS NULL)
        AND n.created_at>clock_timestamp()-interval '30 days' ORDER BY n.id DESC LIMIT $4`, user, q.Before, q.UnreadOnly, q.Limit); err != nil {
		return nil, fmt.Errorf("notification list: %w", err)
	}
	return rows, nil
}

func rollback(tx *sqlx.Tx, err *error) {
	if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
		*err = errors.Join(*err, fmt.Errorf("notification rollback: %w", e))
	}
}
func lockUser(ctx context.Context, tx *sqlx.Tx, user int64) error {
	var id int64
	if err := tx.GetContext(ctx, &id, `SELECT id FROM users WHERE id=$1 FOR KEY SHARE`, user); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return apperror.ErrNotFound
		}
		return fmt.Errorf("notification lock user: %w", err)
	}
	return nil
}
func lockMember(ctx context.Context, tx *sqlx.Tx, user, room int64, policy RoomPolicy) (int64, error) {
	var private bool
	if err := tx.GetContext(ctx, &private, `SELECT visibility='private' FROM rooms WHERE id=$1 FOR SHARE`, room); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, apperror.ErrNotFound
		}
		return 0, fmt.Errorf("notification lock room: %w", err)
	}
	var generation int64
	err := tx.GetContext(ctx, &generation, `SELECT membership_generation FROM room_members WHERE room_id=$1 AND user_id=$2 FOR SHARE`, room, user)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("notification lock membership: %w", err)
	}
	if err := policy(private, generation > 0); err != nil {
		return 0, err
	}
	return generation, nil
}
func globalPreference(ctx context.Context, tx *sqlx.Tx, user int64) (bool, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_preferences(user_id) VALUES($1) ON CONFLICT DO NOTHING`, user); err != nil {
		return false, fmt.Errorf("notification global default: %w", err)
	}
	var enabled bool
	if err := tx.GetContext(ctx, &enabled, `SELECT mentions_enabled FROM notification_preferences WHERE user_id=$1 FOR UPDATE`, user); err != nil {
		return false, fmt.Errorf("notification global lock: %w", err)
	}
	return enabled, nil
}
func roomPreference(ctx context.Context, tx *sqlx.Tx, user, room, generation int64) (bool, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO room_notification_preferences(room_id,user_id,membership_generation) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, room, user, generation); err != nil {
		return false, fmt.Errorf("notification room default: %w", err)
	}
	var muted bool
	if err := tx.GetContext(ctx, &muted, `SELECT muted FROM room_notification_preferences WHERE room_id=$1 AND user_id=$2 AND membership_generation=$3 FOR UPDATE`, room, user, generation); err != nil {
		return false, fmt.Errorf("notification room preference lock: %w", err)
	}
	return muted, nil
}

func (p *Postgres) Preference(ctx context.Context, user, room int64, value *bool, policy RoomPolicy) (saved bool, err error) {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("notification preference begin: %w", err)
	}
	defer rollback(tx, &err)
	if err := lockUser(ctx, tx, user); err != nil {
		return false, err
	}
	var generation int64
	if room > 0 {
		generation, err = lockMember(ctx, tx, user, room, policy)
		if err != nil {
			return false, err
		}
	}
	saved, err = globalPreference(ctx, tx, user)
	if err != nil {
		return false, err
	}
	if room > 0 {
		saved, err = roomPreference(ctx, tx, user, room, generation)
		if err != nil {
			return false, err
		}
	}
	if value != nil {
		saved = *value
		if room == 0 {
			_, err = tx.ExecContext(ctx, `UPDATE notification_preferences SET mentions_enabled=$2 WHERE user_id=$1`, user, saved)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE room_notification_preferences SET muted=$4 WHERE room_id=$1 AND user_id=$2 AND membership_generation=$3`, room, user, generation, saved)
		}
		if err != nil {
			return false, fmt.Errorf("notification preference write: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("notification preference commit: %w", err)
	}
	return saved, nil
}

func (p *Postgres) Read(ctx context.Context, user, id int64) (row *Notification, err error) {
	tx, err := p.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("notification read begin: %w", err)
	}
	defer rollback(tx, &err)
	if err := lockUser(ctx, tx, user); err != nil {
		return nil, err
	}
	var room int64
	if err := tx.GetContext(ctx, &room, `SELECT room_id FROM notifications WHERE id=$1 AND user_id=$2 AND created_at>clock_timestamp()-interval '30 days'`, id, user); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperror.ErrNotFound
		}
		return nil, fmt.Errorf("notification read lookup: %w", err)
	}
	if _, err := lockMember(ctx, tx, user, room, func(_, member bool) error {
		if !member {
			return apperror.ErrNotFound
		}
		return nil
	}); err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE notifications SET read_at=COALESCE(read_at,clock_timestamp()) WHERE id=$1 AND user_id=$2`, id, user)
	if err != nil {
		return nil, fmt.Errorf("notification read write: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("notification read affected: %w", err)
	}
	if n == 0 {
		return nil, apperror.ErrNotFound
	}
	row = &Notification{}
	if err := tx.GetContext(ctx, row, `SELECT `+notificationProjection+feedJoin+`WHERE n.id=$1 AND n.user_id=$2`, id, user); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperror.ErrNotFound
		}
		return nil, fmt.Errorf("notification read projection: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("notification read commit: %w", err)
	}
	return row, nil
}

// Sweep removes expired inbox rows and retry keys in bounded batches.
func (p *Postgres) Sweep(ctx context.Context) error {
	if _, err := p.db.ExecContext(ctx, `DELETE FROM notifications WHERE id IN (SELECT id FROM notifications WHERE created_at<=clock_timestamp()-interval '30 days' ORDER BY created_at,id LIMIT 100)`); err != nil {
		return fmt.Errorf("notification retention: %w", err)
	}
	if _, err := p.db.ExecContext(ctx, `DELETE FROM message_send_keys WHERE (user_id,request_key) IN (SELECT user_id,request_key FROM message_send_keys WHERE expires_at<=clock_timestamp() ORDER BY expires_at LIMIT 100)`); err != nil {
		return fmt.Errorf("notification retry retention: %w", err)
	}
	return nil
}
