package outbox

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Reference is a typed, content-free mention or private notification intent.
type Reference struct {
	Kind           string
	AggregateType  string
	AggregateID    int64
	RoomID         int64
	UserID         int64
	MessageID      int64
	Generation     int64
	NotificationID *int64
}

// AppendReference shares the aggregate counter and independent delivery table.
func AppendReference(ctx context.Context, tx *sqlx.Tx, ref Reference, purpose string) error {
	id, err := uuid.NewRandom()
	if err != nil {
		return fmt.Errorf("outbox reference ID: %w", err)
	}
	var seq int64
	if err := tx.GetContext(ctx, &seq, `INSERT INTO domain_aggregate_counters(aggregate_type,aggregate_id,last_seq)
        VALUES($1,$2,1) ON CONFLICT(aggregate_type,aggregate_id) DO UPDATE
        SET last_seq=domain_aggregate_counters.last_seq+1 RETURNING last_seq`, ref.AggregateType, ref.AggregateID); err != nil {
		return fmt.Errorf("outbox reference sequence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_outbox(event_id,schema_version,kind,aggregate_type,aggregate_id,aggregate_seq,
        aggregate_version,room_id,user_id,message_id,membership_generation,notification_id)
        VALUES($1,1,$2,$3,$4,$5,1,$6,$7,$8,$9,$10)`, id.String(), ref.Kind, ref.AggregateType, ref.AggregateID, seq,
		ref.RoomID, ref.UserID, ref.MessageID, ref.Generation, ref.NotificationID); err != nil {
		return fmt.Errorf("outbox reference append: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_outbox_deliveries(event_id,purpose) VALUES($1,$2)`, id.String(), purpose); err != nil {
		return fmt.Errorf("outbox reference purpose: %w", err)
	}
	return nil
}
