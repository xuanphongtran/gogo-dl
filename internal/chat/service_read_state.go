package chat

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/rs/zerolog/log"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// ReadStatePolicy checks room access using authoritative locked membership.
// Repositories pass nil for an absent membership; the callback performs no I/O.
type ReadStatePolicy func(visibility RoomVisibility, member *RoomMember) error

func requireReadStateMember(visibility RoomVisibility, member *RoomMember) error {
	if member != nil {
		return nil
	}
	if visibility == RoomVisibilityPrivate {
		return apperror.ErrNotFound
	}
	return apperror.ErrForbidden
}

// GetReadState returns the caller's durable cursor without advancing it.
func (s *Service) GetReadState(ctx context.Context, userID, roomID int64) (*ReadState, error) {
	if userID <= 0 || roomID <= 0 {
		return nil, apperror.ErrInvalidRequest
	}
	state, err := s.repo.GetReadState(ctx, roomID, userID, requireReadStateMember)
	if err != nil {
		return nil, fmt.Errorf("chat service GetReadState: %w", err)
	}
	return state, nil
}

// AdvanceReadState acknowledges a room message and notifies only this user
// after a changed cursor commits. Older valid acknowledgements are no-ops.
func (s *Service) AdvanceReadState(ctx context.Context, userID, roomID int64, req *AdvanceReadStateRequest) (*ReadState, error) {
	if userID <= 0 || roomID <= 0 || req == nil || req.LastReadMessageID <= 0 {
		return nil, apperror.ErrInvalidRequest
	}
	state, changed, err := s.repo.AdvanceReadState(ctx, roomID, userID, req.LastReadMessageID, requireReadStateMember)
	if err != nil {
		return nil, fmt.Errorf("chat service AdvanceReadState: %w", err)
	}
	if changed {
		if err := s.hub.BroadcastToUser(userID, ws.Message{
			Type: ws.EventReadState, RoomID: strconv.FormatInt(roomID, 10), Payload: state,
		}); err != nil {
			log.Warn().Err(err).Int64("room_id", roomID).Int64("user_id", userID).Msg("chat: read state event dropped")
		}
	}
	return state, nil
}

// GetPresence returns process-local room presence to current room members.
func (s *Service) GetPresence(ctx context.Context, userID, roomID int64) (*ws.PresenceSnapshot, error) {
	if userID <= 0 || roomID <= 0 {
		return nil, apperror.ErrInvalidRequest
	}
	room, err := s.repo.GetRoomByID(ctx, roomID)
	if err != nil {
		return nil, fmt.Errorf("chat service GetPresence room: %w", err)
	}
	member, err := s.repo.GetMember(ctx, roomID, userID)
	if err != nil && !errors.Is(err, apperror.ErrNotFound) {
		return nil, fmt.Errorf("chat service GetPresence membership: %w", err)
	}
	if err := requireReadStateMember(room.Visibility, member); err != nil {
		return nil, err
	}
	snapshot, err := s.hub.RoomPresence(ctx, strconv.FormatInt(roomID, 10))
	if err != nil {
		return nil, fmt.Errorf("chat service GetPresence snapshot: %w", err)
	}
	return snapshot, nil
}
