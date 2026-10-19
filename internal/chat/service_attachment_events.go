package chat

import (
	"context"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/xuanphongtran/gogo-dl/internal/attachment"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
)

// PublishAttachmentChanges refreshes room clients only after account cleanup commits.
func (s *Service) PublishAttachmentChanges(ctx context.Context, deletedUser int64, cleanup *attachment.AccountCleanup) {
	if s.hub == nil || cleanup == nil {
		return
	}
	// The deletion has committed: authorization cleanup must survive its HTTP
	// request. Keep this synchronous, bounded, and separate from request-scoped
	// database refreshes below; no detached goroutine owns this context.
	revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, room := range cleanup.Rooms {
		if err := s.hub.RevokeUserFromRoom(revokeCtx, strconv.FormatInt(room, 10), deletedUser); err != nil {
			log.Warn().Int64("room_id", room).Msg("account subscription revocation incomplete")
			return
		}
	}
	reader, ok := s.repo.(interface {
		MessageForAttachmentEvent(context.Context, int64, int64) (*Message, error)
	})
	if !ok {
		return
	}
	for _, change := range cleanup.Messages {
		msg, err := reader.MessageForAttachmentEvent(ctx, change.RoomID, change.MessageID)
		if err != nil {
			log.Warn().Int64("message_id", change.MessageID).Msg("attachment account refresh unavailable")
			continue
		}
		event := ws.EventMessageUpdated
		if msg.DeletedAt != nil {
			event = ws.EventMessageDeleted
		}
		s.broadcastMessageMutation(msg, event)
	}
}
