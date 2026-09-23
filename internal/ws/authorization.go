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
	clientID string
	userID   int64
	roomID   string
	err      error
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
	if h.authorizer == nil {
		h.sendProtocolError(client, roomID, "authorization_unavailable", "room authorization is unavailable")
		return
	}

	roomNumber, canonicalRoomID, err := parseRoomID(roomID)
	if err != nil {
		h.sendProtocolError(client, roomID, "invalid_room_id", "invalid room id")
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(client.Context(), roomAuthorizationTimeout)
		defer cancel()

		err := h.authorizer.AuthorizeRoom(ctx, client.UserID, roomNumber)
		result := authorizationResult{
			clientID: client.ID,
			userID:   client.UserID,
			roomID:   canonicalRoomID,
			err:      err,
		}
		select {
		case h.authorized <- result:
		case <-h.done:
		case <-client.Context().Done():
		}
	}()
}

func (h *Hub) handleAuthorization(result authorizationResult) {
	client, ok := h.clients[result.clientID]
	if !ok || client.UserID != result.userID {
		return
	}
	if result.err != nil {
		if isAccessDenied(result.err) {
			h.sendProtocolError(client, result.roomID, "forbidden", "you are not allowed to access this room")
			return
		}
		h.sendProtocolError(client, result.roomID, "authorization_failed", "room authorization failed")
		return
	}

	if _, joined := client.rooms[result.roomID]; joined {
		return
	}
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
	members, ok := h.rooms[req.roomID]
	if !ok {
		req.done <- nil
		return
	}

	for clientID, client := range members {
		if client.UserID != req.userID {
			continue
		}
		client.sendJSON(Message{
			Type:   EventError,
			RoomID: req.roomID,
			Payload: map[string]string{
				"code":    "membership_revoked",
				"message": "your room membership was revoked",
			},
		})
		delete(members, clientID)
		delete(client.rooms, req.roomID)
	}
	if len(members) == 0 {
		delete(h.rooms, req.roomID)
	}
	req.done <- nil
}
