package attachment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"

	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
)

// AccountCleanup retains room subscriptions and affected messages before account cascades.
type AccountCleanup struct {
	Rooms    []int64
	Messages []MessageChange
}

// MessageChange is a content-free post-commit refresh reference.
type MessageChange struct {
	RoomID    int64 `db:"room_id"`
	MessageID int64 `db:"id"`
	Revision  int64 `db:"revision"`
}

func scheduleCleanupRows(ctx context.Context, tx *sqlx.Tx, rows []*Reservation) error {
	for _, row := range rows {
		if row.State == "deleted" || row.State == "deleting" {
			continue
		}
		if err := tx.GetContext(ctx, &row.CleanupAfter, `UPDATE attachments SET state='cancelled',cleanup_after=GREATEST(upload_expires_at+interval '10 minutes',clock_timestamp()+interval '60 seconds') WHERE id=$1 RETURNING cleanup_after`, row.ID); err != nil {
			return fmt.Errorf("attachment lifecycle cancel: %w", err)
		}
		if err := appendIntent(ctx, tx, row, outbox.Cleanup); err != nil {
			return err
		}
	}
	return nil
}

// ScheduleMessageCleanup runs after locking the message, before committing its tombstone.
func ScheduleMessageCleanup(ctx context.Context, tx *sqlx.Tx, message int64) error {
	var rows []*Reservation
	if err := tx.SelectContext(ctx, &rows, `SELECT `+projection+`,message_id FROM attachments WHERE message_id=$1 ORDER BY id FOR UPDATE`, message); err != nil {
		return fmt.Errorf("attachment message cleanup lock: %w", err)
	}
	return scheduleCleanupRows(ctx, tx, rows)
}

// ScheduleAccountCleanup pins user, rooms, memberships, messages and attachments in order.
// The caller deletes the account in this same transaction, after retaining cleanup intent.
func ScheduleAccountCleanup(ctx context.Context, tx *sqlx.Tx, user int64) (*AccountCleanup, error) {
	var pinned int64
	if err := tx.GetContext(ctx, &pinned, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, user); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperror.ErrNotFound
		}
		return nil, fmt.Errorf("attachment account user lock: %w", err)
	}
	var rooms []int64
	if err := tx.SelectContext(ctx, &rooms, `SELECT id FROM rooms WHERE id IN (SELECT room_id FROM attachments WHERE uploader_id=$1 UNION SELECT room_id FROM room_members WHERE user_id=$1) ORDER BY id FOR SHARE`, user); err != nil {
		return nil, fmt.Errorf("attachment account rooms lock: %w", err)
	}
	var memberships []int64
	if err := tx.SelectContext(ctx, &memberships, `SELECT room_id FROM room_members WHERE user_id=$1 ORDER BY room_id FOR UPDATE`, user); err != nil {
		return nil, fmt.Errorf("attachment account memberships lock: %w", err)
	}
	var changes []MessageChange
	if err := tx.SelectContext(ctx, &changes, `SELECT id,room_id,revision FROM messages WHERE deleted_at IS NULL AND id IN (SELECT message_id FROM attachments WHERE uploader_id=$1 AND state='attached') ORDER BY room_id,id FOR UPDATE`, user); err != nil {
		return nil, fmt.Errorf("attachment account messages lock: %w", err)
	}
	var rows []*Reservation
	if err := tx.SelectContext(ctx, &rows, `SELECT `+projection+`,message_id FROM attachments WHERE uploader_id=$1 AND state<>'deleted' ORDER BY id FOR UPDATE`, user); err != nil {
		return nil, fmt.Errorf("attachment account files lock: %w", err)
	}
	if err := scheduleCleanupRows(ctx, tx, rows); err != nil {
		return nil, err
	}
	for i := range changes {
		change := &changes[i]
		if err := tx.GetContext(ctx, &change.Revision, `UPDATE messages SET revision=revision+1,edited_at=clock_timestamp() WHERE id=$1 RETURNING revision`, change.MessageID); err != nil {
			return nil, fmt.Errorf("attachment account message revision: %w", err)
		}
		if err := outbox.AppendReference(ctx, tx, outbox.Reference{Kind: "message.updated", AggregateType: "room", AggregateID: change.RoomID, RoomID: change.RoomID, MessageID: change.MessageID, Version: change.Revision}, "broker"); err != nil {
			return nil, err
		}
	}
	return &AccountCleanup{Rooms: rooms, Messages: changes}, nil
}
