package chat

import (
	"context"
	"fmt"
	"strconv"

	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
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
	room := &Room{
		Name:      req.Name,
		CreatedBy: creatorID,
	}
	if err := s.repo.CreateRoom(ctx, room); err != nil {
		return nil, err
	}

	// The creator is automatically a member.
	_ = s.repo.AddMember(ctx, room.ID, creatorID)

	return room, nil
}

// GetRoom fetches a room by ID.
func (s *Service) GetRoom(ctx context.Context, roomID int64) (*Room, error) {
	return s.repo.GetRoomByID(ctx, roomID)
}

// ListRooms returns all available rooms.
func (s *Service) ListRooms(ctx context.Context) ([]*Room, error) {
	return s.repo.ListRooms(ctx)
}

// JoinRoom adds a user to a room.
func (s *Service) JoinRoom(ctx context.Context, roomID, userID int64) error {
	// Validate the room exists.
	if _, err := s.repo.GetRoomByID(ctx, roomID); err != nil {
		return err
	}
	return s.repo.AddMember(ctx, roomID, userID)
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
func (s *Service) SendMessage(ctx context.Context, userID int64, roomID int64, req *SendMessageRequest, username string) (*Message, error) {
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
		RoomID:   roomID,
		UserID:   userID,
		Username: username,
		Content:  req.Content,
	}
	if err := s.repo.CreateMessage(ctx, msg); err != nil {
		return nil, err
	}

	// ── Realtime broadcast ────────────────────────────────────────────────
	// Convert numeric roomID to string for the ws layer.
	wsRoomID := strconv.FormatInt(roomID, 10)

	wsMsg := ws.Message{
		Type:   ws.EventMessage,
		RoomID: wsRoomID,
		Payload: map[string]interface{}{
			"id":         msg.ID,
			"user_id":    msg.UserID,
			"username":   msg.Username,
			"content":    msg.Content,
			"created_at": msg.CreatedAt,
		},
	}

	// Broadcast is non-blocking. Log the error but don't fail the HTTP request —
	// the message is already persisted.
	if err := s.hub.Broadcast(wsRoomID, wsMsg); err != nil {
		// In production you might emit a metric here.
		fmt.Printf("[chat] broadcast warning: %v\n", err)
	}

	return msg, nil
}

// ListMessages returns paginated message history for a room.
func (s *Service) ListMessages(ctx context.Context, roomID int64, q *ListMessagesQuery) ([]*Message, error) {
	if _, err := s.repo.GetRoomByID(ctx, roomID); err != nil {
		return nil, err
	}
	return s.repo.ListMessages(ctx, roomID, q.Limit, q.Before)
}
