package chat

import (
	"context"
	"strconv"

	"github.com/rs/zerolog/log"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"go.opentelemetry.io/otel"
)

// EditMessage changes an author's text using the expected revision and locked
// membership. A retry of the latest identical edit is a successful no-op.
func (s *Service) EditMessage(ctx context.Context, actorID, roomID, messageID int64, req *EditMessageRequest) (*Message, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.service.EditMessage")
	defer span.End()
	if actorID <= 0 || roomID <= 0 || messageID <= 0 || req == nil || req.Revision <= 0 {
		return nil, apperror.ErrInvalidRequest
	}
	content, valid := normalizeMessageContent(req.Content)
	if !valid {
		return nil, apperror.ErrInvalidRequest
	}
	msg, changed, err := s.repo.EditMessage(ctx, roomID, actorID, messageID, content,
		func(visibility RoomVisibility, member *RoomMember, msg *Message) (bool, error) {
			if err := authorizeMessageMutation(visibility, member, msg); err != nil {
				return false, err
			}
			if msg.UserID == nil || *msg.UserID != actorID {
				return false, apperror.ErrForbidden
			}
			if msg.DeletedAt != nil {
				return false, apperror.ErrMessageDeleted
			}
			if msg.Content == content && (req.Revision == msg.Revision || req.Revision == msg.Revision-1) {
				return false, nil
			}
			if req.Revision != msg.Revision {
				return false, apperror.ErrMessageRevision
			}
			return true, nil
		})
	if err != nil {
		return nil, err
	}
	if changed {
		s.broadcastMessageMutation(msg, ws.EventMessageUpdated)
	}
	return msg, nil
}

// DeleteMessage erases content while retaining a tombstone for stable history.
// Current authors, owners and moderators can delete, including on retries.
func (s *Service) DeleteMessage(ctx context.Context, actorID, roomID, messageID int64) (*Message, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.service.DeleteMessage")
	defer span.End()
	if actorID <= 0 || roomID <= 0 || messageID <= 0 {
		return nil, apperror.ErrInvalidRequest
	}
	msg, changed, err := s.repo.DeleteMessage(ctx, roomID, actorID, messageID,
		func(visibility RoomVisibility, member *RoomMember, msg *Message) (bool, error) {
			if err := authorizeMessageMutation(visibility, member, msg); err != nil {
				return false, err
			}
			isAuthor := msg.UserID != nil && *msg.UserID == actorID
			if !isAuthor && member.Role != RoomRoleOwner && member.Role != RoomRoleModerator {
				return false, apperror.ErrForbidden
			}
			return msg.DeletedAt == nil, nil
		})
	if err != nil {
		return nil, err
	}
	if changed {
		s.broadcastMessageMutation(msg, ws.EventMessageDeleted)
	}
	return msg, nil
}

func authorizeMessageMutation(visibility RoomVisibility, member *RoomMember, msg *Message) error {
	if member == nil {
		if visibility == RoomVisibilityPrivate {
			return apperror.ErrNotFound
		}
		return apperror.ErrForbidden
	}
	if msg == nil {
		return apperror.ErrNotFound
	}
	return nil
}

func (s *Service) broadcastMessageMutation(msg *Message, event ws.EventType) {
	if err := s.hub.Broadcast(strconv.FormatInt(msg.RoomID, 10), ws.Message{
		Type: event, RoomID: strconv.FormatInt(msg.RoomID, 10), Payload: msg,
	}); err != nil {
		log.Warn().Err(err).Int64("room_id", msg.RoomID).Int64("message_id", msg.ID).
			Str("event_type", string(event)).Msg("chat: message lifecycle event dropped")
	}
}
