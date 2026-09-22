package chat

import (
	"context"
	"strconv"

	"github.com/rs/zerolog/log"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// RemoveMember removes durable membership first and then revokes any active
// WebSocket subscription. PostgreSQL remains the source of truth if the
// best-effort in-memory control path is unavailable.
func (s *Service) RemoveMember(ctx context.Context, requesterID, roomID, userID int64) error {
	if requesterID != userID {
		return apperror.ErrForbidden
	}
	if _, err := s.repo.GetRoomByID(ctx, roomID); err != nil {
		return err
	}
	if err := s.repo.RemoveMember(ctx, roomID, userID); err != nil {
		return err
	}

	if err := s.hub.RevokeUserFromRoom(ctx, strconv.FormatInt(roomID, 10), userID); err != nil {
		log.Error().
			Err(err).
			Int64("room_id", roomID).
			Int64("user_id", userID).
			Msg("chat: durable membership removed but websocket revocation failed")
	}
	return nil
}
