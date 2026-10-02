package ws

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

const roomAuthorizationTimeout = 5 * time.Second

// RoomAuthorizer is implemented by the chat service. Database access happens
// outside the Hub event loop through this interface.
type RoomAuthorizer interface {
	AuthorizeRoom(ctx context.Context, userID, roomID int64) error
}

type authorizationResult struct {
	client     *Client
	roomID     string
	command    EventType
	generation uint64
	err        error
}

type pendingAuthorization struct {
	roomID     string
	generation uint64
	cancel     context.CancelFunc
}

type revocationRequest struct {
	roomID string
	userID int64
	done   chan error
}

// SetRoomAuthorizer configures the service used to authorize future join
// commands. It must be called before the Hub starts accepting connections.
func (h *Hub) SetRoomAuthorizer(authorizer RoomAuthorizer) {
	h.authorizer = authorizer
}

func (h *Hub) requestJoin(client *Client, roomID string) {
	if client == nil {
		return
	}
	roomNumber, canonicalRoomID, err := parseRoomID(roomID)
	if err != nil {
		h.sendProtocolError(client, roomID, "invalid_room_id", "invalid room id")
		return
	}
	h.requestAuthorization(client, roomNumber, canonicalRoomID, EventJoin)
}

func (h *Hub) requestAuthorization(client *Client, roomNumber int64, roomID string, command EventType) {
	if h.authorizer == nil {
		h.sendProtocolError(client, roomID, "authorization_unavailable", "room authorization is unavailable")
		return
	}
	if client.authorization != nil {
		h.sendProtocolError(client, roomID, "authorization_pending", "a room command is awaiting authorization")
		return
	}

	client.authorizationGeneration++
	generation := client.authorizationGeneration
	ctx, cancel := context.WithTimeout(client.Context(), roomAuthorizationTimeout)
	client.authorization = &pendingAuthorization{roomID: roomID, generation: generation, cancel: cancel}

	go func() {
		defer cancel()

		err := h.authorizer.AuthorizeRoom(ctx, client.UserID, roomNumber)
		if err == nil {
			err = ctx.Err()
		}
		result := authorizationResult{
			client:     client,
			roomID:     roomID,
			command:    command,
			generation: generation,
			err:        err,
		}
		select {
		case h.authorized <- result:
		case <-h.done:
		}
	}()
}

// Invalidated work keeps its slot until completion, bounding concurrent workers
// even when a client repeatedly alternates join and leave.
func (h *Hub) invalidateAuthorization(client *Client, roomID string) {
	if pending := client.authorization; pending != nil && pending.roomID == roomID {
		client.authorizationGeneration++
		pending.cancel()
	}
}

func (h *Hub) handleAuthorization(result authorizationResult) {
	client, ok := h.clients[result.client.ID]
	if !ok || client != result.client || client.authorization == nil || client.authorization.generation != result.generation {
		return
	}
	client.authorization = nil
	if client.authorizationGeneration != result.generation || client.Context().Err() != nil {
		return
	}
	if result.err != nil {
		if isAccessDenied(result.err) {
			h.revokeSubscriptions(result.roomID, client.UserID, false)
			h.sendProtocolError(client, result.roomID, "forbidden", "you are not allowed to access this room")
			return
		}
		h.sendProtocolError(client, result.roomID, "authorization_failed", "room authorization failed")
		return
	}

	if result.command != EventJoin {
		if _, joined := client.rooms[result.roomID]; joined {
			h.applyTyping(client, result.roomID, result.command, time.Now())
		}
		return
	}
	if _, joined := client.rooms[result.roomID]; joined {
		h.sendPresenceSnapshot(client, result.roomID)
		return
	}
	wasOnline := h.userOnline(result.roomID, client.UserID)
	h.addToRoom(result.roomID, client)
	client.rooms[result.roomID] = struct{}{}
	h.fanOut(result.roomID, Message{
		Type:   EventJoin,
		RoomID: result.roomID,
		Payload: map[string]interface{}{
			"user_id":   client.UserID,
			"client_id": client.ID,
		},
	}, "")
	if !wasOnline {
		h.publishPresence(result.roomID, client.UserID, true)
	}
	h.sendPresenceSnapshot(client, result.roomID)
}

func (h *Hub) sendProtocolError(client *Client, roomID, code, message string) {
	if client == nil {
		return
	}
	client.sendJSON(Message{
		Type:   EventError,
		RoomID: roomID,
		Payload: map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func (h *Hub) sendProtocolErrorAndClose(client *Client, roomID, code, message string, closed chan struct{}) {
	if client == nil {
		close(closed)
		return
	}
	client.sendJSONAndClose(Message{
		Type:   EventError,
		RoomID: roomID,
		Payload: map[string]string{
			"code":    code,
			"message": message,
		},
	}, closed)
}

func parseRoomID(raw string) (int64, string, error) {
	roomID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || roomID <= 0 {
		return 0, "", fmt.Errorf("invalid room id")
	}
	return roomID, strconv.FormatInt(roomID, 10), nil
}

func isAccessDenied(err error) bool {
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) {
		return false
	}
	return appErr.Code == 403 || appErr.Code == 404
}

// RevokeUserFromRoom removes all active subscriptions for a user after a
// durable membership removal. The map mutation is performed by Hub.Run.
func (h *Hub) RevokeUserFromRoom(ctx context.Context, roomID string, userID int64) error {
	_, canonicalRoomID, err := parseRoomID(roomID)
	if err != nil {
		return fmt.Errorf("ws revoke membership: %w", err)
	}

	done := make(chan error, 1)
	req := revocationRequest{roomID: canonicalRoomID, userID: userID, done: done}
	select {
	case h.revoke <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-h.done:
		return fmt.Errorf("ws revoke membership: hub is stopped")
	}

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-h.done:
		return fmt.Errorf("ws revoke membership: hub is stopped")
	}
}

func (h *Hub) handleRevocation(req revocationRequest) {
	h.revokeSubscriptions(req.roomID, req.userID, true)
	req.done <- nil
}

func (h *Hub) revokeSubscriptions(roomID string, userID int64, notify bool) {
	// Pending joins are not in rooms yet, so scan every active connection.
	for _, client := range h.clients {
		if client.UserID != userID {
			continue
		}
		h.invalidateAuthorization(client, roomID)
		if _, joined := client.rooms[roomID]; !joined {
			continue
		}
		if notify {
			h.sendProtocolError(client, roomID, "membership_revoked", "your room membership was revoked")
		}
		h.leaveRoom(roomID, client)
	}
}
