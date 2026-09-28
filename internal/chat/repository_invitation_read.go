package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

func (r *postgresRepository) GetInvitation(ctx context.Context, invitationID, inviteeID int64) (*Invitation, error) {
	var invitation Invitation
	err := r.db.GetContext(ctx, &invitation, `
		SELECT i.id, i.room_id, r.name AS room_name, i.invitee_id, i.invited_by,
		       i.status, i.created_at, i.updated_at, i.responded_at
		FROM room_invitations i
		JOIN rooms r ON r.id = i.room_id
		WHERE i.id = $1 AND i.invitee_id = $2`, invitationID, inviteeID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("chat repo GetInvitation: %w", err)
	}
	return &invitation, nil
}
