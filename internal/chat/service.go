package chat

import (
	"context"
	"fmt"
	"strconv"

	"github.com/rs/zerolog/log"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"go.opentelemetry.io/otel"
)

// Service encapsulates business logic for the chat domain.
// It receives a *ws.Hub so it can push realtime events after persisting data.
type Service struct {
	repo Repository
	hub  *ws.Hub
}

// NewService creates a new chat Service.
func NewService(repo Repository, hub *ws.Hub) *Service {
	return &Service{repo: repo, hub: hub}
}

// ── Rooms ─────────────────────────────────────────────────────────────────────

// CreateRoom creates a new room and automatically adds the creator as a member.
func (s *Service) CreateRoom(ctx context.Context, creatorID int64, req *CreateRoomRequest) (*Room, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.service.CreateRoom")
	defer span.End()
	if req == nil {
		return nil, apperror.ErrInvalidRequest
	}
	name, ok := normalizeRoomName(req.Name)
	if !ok {
		return nil, apperror.ErrInvalidRequest
	}
	visibility := req.Visibility
	if visibility == "" {
		visibility = RoomVisibilityPublic
	}
	switch visibility {
	case RoomVisibilityPublic, RoomVisibilityPrivate:
	default:
		return nil, apperror.ErrInvalidRequest
	}
	room := &Room{
		Name:       name,
		CreatedBy:  creatorID,
		Visibility: visibility,
	}
	if err := s.repo.CreateRoomWithMember(ctx, room, creatorID); err != nil {
		return nil, err
	}

	owner := RoomRoleOwner
	room.Role = new(RoomRole)
	*room.Role = owner
	return room, nil
}

// GetRoom fetches a room by ID.
func (s *Service) GetRoom(ctx context.Context, roomID int64) (*Room, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.service.GetRoom")
	defer span.End()
	return s.repo.GetRoomByID(ctx, roomID)
}

// ListRooms returns all available rooms.
func (s *Service) ListRooms(ctx context.Context) ([]*Room, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.service.ListRooms")
	defer span.End()
	return s.repo.ListRooms(ctx)
}

// JoinRoom adds a user to a room.
func (s *Service) JoinRoom(ctx context.Context, roomID, userID int64) error {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.service.JoinRoom")
	defer span.End()
	return s.JoinPublicRoom(ctx, roomID, userID)
}

// ── Messages ─────────────────────────────────────────────────────────────────

// SendMessage persists a new message and broadcasts it to all WebSocket clients
// in the room in real-time.
//
// Broadcast flow:
//  1. Validate room exists + caller is a member.
//  2. Insert message into DB (source of truth).
//  3. Call hub.Broadcast(roomID, wsMessage) — non-blocking channel send.
//     The Hub's Run() goroutine fans the message out to all connected clients.
func (s *Service) SendMessage(ctx context.Context, userID int64, roomID int64, req *SendMessageRequest, _ string) (*Message, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.service.SendMessage")
	defer span.End()
	if req == nil {
		return nil, apperror.ErrInvalidRequest
	}
	if len(req.AttachmentIDs) > 0 {
		return nil, apperror.ErrAttachmentsUnavailable
	}
	content, ok := normalizeMessageContent(req.Content)
	if !ok {
		return nil, apperror.ErrInvalidRequest
	}

	if rich, ok := s.repo.(mentionSender); ok && (rich.AtomicSends() || len(req.MentionUserIDs) > 0 || req.IdempotencyKey != "") {
		normalized, hash, err := normalizeMentionSend(roomID, content, req)
		if err != nil {
			return nil, err
		}
		msg, created, err := rich.SendMessageAtomic(ctx, userID, roomID, normalized, hash, requireReadStateMember)
		if err != nil {
			return nil, err
		}
		if created && s.hub != nil {
			room := strconv.FormatInt(roomID, 10)
			if err := s.hub.Broadcast(room, ws.Message{Type: ws.EventMessage, RoomID: room, Payload: msg}); err != nil {
				log.Warn().Msg("chat: realtime broadcast dropped")
			}
		}
		return msg, nil
	}
	if len(req.MentionUserIDs) > 0 || req.IdempotencyKey != "" {
		return nil, apperror.ErrInvalidRequest
	}

	// Check room exists.
	if _, err := s.repo.GetRoomByID(ctx, roomID); err != nil {
		return nil, err
	}

	// Membership check — only members can post.
	ok, err := s.repo.IsMember(ctx, roomID, userID)
	if err != nil {
		return nil, fmt.Errorf("chat service SendMessage: %w", err)
	}
	if !ok {
		return nil, apperror.ErrForbidden
	}

	msg := &Message{
		RoomID:  roomID,
		UserID:  &userID,
		Content: content,
	}
	if err := s.repo.CreateMessage(ctx, msg); err != nil {
		return nil, err
	}

	// ── Realtime broadcast ────────────────────────────────────────────────
	// Convert numeric roomID to string for the ws layer.
	wsRoomID := strconv.FormatInt(roomID, 10)

	wsMsg := ws.Message{
		Type:    ws.EventMessage,
		RoomID:  wsRoomID,
		Payload: msg,
	}

	// Broadcast is non-blocking. Log the error but don't fail the HTTP request —
	// the message is already persisted.
	if err := s.hub.Broadcast(wsRoomID, wsMsg); err != nil {
		// In production you might emit a metric here.
		log.Warn().Err(err).Str("room_id", wsRoomID).Msg("chat: realtime broadcast dropped")
	}

	return msg, nil
}

// ListMessages returns paginated message history for a room.
func (s *Service) ListMessages(ctx context.Context, roomID int64, q *ListMessagesQuery) ([]*Message, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.service.ListMessages")
	defer span.End()
	if _, err := s.repo.GetRoomByID(ctx, roomID); err != nil {
		return nil, err
	}
	return s.repo.ListMessages(ctx, roomID, q.Limit, q.Before)
}
