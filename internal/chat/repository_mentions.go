package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/lib/pq"
	"github.com/xuanphongtran/gogo-dl/internal/outbox"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

const mentionProjection = `, ARRAY(SELECT mm.user_id FROM message_mentions mm
    WHERE mm.message_id=m.id AND m.deleted_at IS NULL ORDER BY mm.user_id) AS mention_user_ids`

func (r *explicitRepository) AtomicSends() bool { return r.mentions }

// SendMessageAtomic authorizes retries afresh and commits recipients and intents together.
func (r *explicitRepository) SendMessageAtomic(ctx context.Context, userID, roomID int64, req *SendMessageRequest, hash string, policy ReadStatePolicy) (msg *Message, created bool, err error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("chat send begin: %w", err)
	}
	defer func() {
		if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("chat send rollback: %w", e))
			msg = nil
			created = false
		}
	}()
	// Account deletion locks users before cascades. Match that order before room/member locks.
	ids := append(slices.Clone(req.MentionUserIDs), userID)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var users []int64
	if err := tx.SelectContext(ctx, &users, `SELECT id FROM users WHERE id=ANY($1) ORDER BY id FOR KEY SHARE`, pq.Array(ids)); err != nil {
		return nil, false, fmt.Errorf("chat send users: %w", err)
	}
	if !slices.Contains(users, userID) {
		return nil, false, apperror.ErrNotFound
	}
	var visibility RoomVisibility
	if err := tx.GetContext(ctx, &visibility, `SELECT visibility FROM rooms WHERE id=$1 FOR SHARE`, roomID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, apperror.ErrNotFound
		}
		return nil, false, fmt.Errorf("chat send room: %w", err)
	}
	type memberRow struct {
		UserID     int64 `db:"user_id"`
		Generation int64 `db:"membership_generation"`
	}
	var members []memberRow
	if err := tx.SelectContext(ctx, &members, `SELECT user_id,membership_generation FROM room_members WHERE room_id=$1 AND user_id=ANY($2) ORDER BY user_id FOR SHARE`, roomID, pq.Array(ids)); err != nil {
		return nil, false, fmt.Errorf("chat send members: %w", err)
	}
	generations := make(map[int64]int64, len(members))
	for _, m := range members {
		generations[m.UserID] = m.Generation
	}
	var actor *RoomMember
	if generations[userID] > 0 {
		actor = &RoomMember{UserID: userID, RoomID: roomID}
	}
	if err := policy(visibility, actor); err != nil {
		return nil, false, err
	}
	if req.IdempotencyKey != "" {
		// Per-user advisory lock also serializes same key across different rooms.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
			return nil, false, fmt.Errorf("chat send retry lock: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM message_send_keys WHERE user_id=$1 AND request_key=$2 AND expires_at<=clock_timestamp()`, userID, req.IdempotencyKey); err != nil {
			return nil, false, fmt.Errorf("chat send expired retry: %w", err)
		}
		var saved struct {
			Hash      string `db:"request_hash"`
			MessageID int64  `db:"message_id"`
		}
		e := tx.GetContext(ctx, &saved, `SELECT request_hash,message_id FROM message_send_keys WHERE user_id=$1 AND request_key=$2`, userID, req.IdempotencyKey)
		if e == nil {
			if saved.Hash != hash {
				return nil, false, apperror.ErrConflict
			}
			msg = &Message{}
			if e := tx.GetContext(ctx, msg, `SELECT m.id,m.room_id,m.user_id,COALESCE(u.username,'[deleted user]') AS username,m.content,m.created_at,m.revision,m.edited_at,m.deleted_at`+mentionProjection+`
                FROM messages m LEFT JOIN users u ON u.id=m.user_id WHERE m.id=$1 AND m.room_id=$2`, saved.MessageID, roomID); e != nil {
				return nil, false, fmt.Errorf("chat send retry message: %w", e)
			}
			if e := tx.Commit(); e != nil {
				return nil, false, fmt.Errorf("chat send retry commit: %w", e)
			}
			return msg, false, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return nil, false, fmt.Errorf("chat send retry lookup: %w", e)
		}
	}
	for _, id := range req.MentionUserIDs {
		if generations[id] == 0 {
			return nil, false, apperror.ErrInvalidRequest
		}
	}
	msg = &Message{RoomID: roomID, UserID: &userID, Content: req.Content, MentionUserIDs: pq.Int64Array(req.MentionUserIDs)}
	if err := tx.QueryRowxContext(ctx, `INSERT INTO messages(room_id,user_id,content) VALUES($1,$2,$3)
        RETURNING id,created_at,revision,COALESCE((SELECT username FROM users WHERE id=messages.user_id),'[deleted user]')`, roomID, userID, req.Content).Scan(&msg.ID, &msg.CreatedAt, &msg.Revision, &msg.Username); err != nil {
		return nil, false, fmt.Errorf("chat send message: %w", err)
	}
	for _, id := range req.MentionUserIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO message_mentions(message_id,room_id,user_id,membership_generation) VALUES($1,$2,$3,$4)`, msg.ID, roomID, id, generations[id]); err != nil {
			return nil, false, fmt.Errorf("chat send mention: %w", err)
		}
		if id == userID {
			continue
		}
		if err := outbox.AppendReference(ctx, tx, outbox.Reference{Kind: "message.mentioned", AggregateType: "room", AggregateID: roomID, RoomID: roomID, UserID: id, MessageID: msg.ID, Generation: generations[id]}, outbox.Notification); err != nil {
			return nil, false, err
		}
	}
	if req.IdempotencyKey != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO message_send_keys(user_id,request_key,request_hash,message_id) VALUES($1,$2,$3,$4)`, userID, req.IdempotencyKey, hash, msg.ID); err != nil {
			return nil, false, fmt.Errorf("chat send retry insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("chat send commit: %w", err)
	}
	return msg, true, nil
}
