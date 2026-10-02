package ws

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type presenceRequest struct {
	roomID   string
	response chan *PresenceSnapshot
}

// RoomPresence obtains a consistent snapshot through the Hub event loop.
// The caller must authorize current durable room membership before calling it.
func (h *Hub) RoomPresence(ctx context.Context, roomID string) (*PresenceSnapshot, error) {
	_, canonical, err := parseRoomID(roomID)
	if err != nil {
		return nil, fmt.Errorf("ws room presence: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response := make(chan *PresenceSnapshot, 1)
	select {
	case h.presence <- presenceRequest{roomID: canonical, response: response}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.done:
		return nil, errHubStopped
	}
	select {
	case snapshot := <-response:
		return snapshot, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.done:
		return nil, errHubStopped
	}
}

func (h *Hub) roomPresence(roomID string, now time.Time) *PresenceSnapshot {
	snapshot := &PresenceSnapshot{RoomID: roomID, OnlineUserIDs: []int64{}, TypingUserIDs: []int64{}}
	online, typing := make(map[int64]struct{}), make(map[int64]struct{})
	for clientID, client := range h.rooms[roomID] {
		online[client.UserID] = struct{}{}
		if expiresAt := h.typing[roomID][clientID]; expiresAt.After(now) {
			typing[client.UserID] = struct{}{}
		}
	}
	for userID := range online {
		snapshot.OnlineUserIDs = append(snapshot.OnlineUserIDs, userID)
	}
	for userID := range typing {
		snapshot.TypingUserIDs = append(snapshot.TypingUserIDs, userID)
	}
	sort.Slice(snapshot.OnlineUserIDs, func(i, j int) bool { return snapshot.OnlineUserIDs[i] < snapshot.OnlineUserIDs[j] })
	sort.Slice(snapshot.TypingUserIDs, func(i, j int) bool { return snapshot.TypingUserIDs[i] < snapshot.TypingUserIDs[j] })
	return snapshot
}

func (h *Hub) sendPresenceSnapshot(client *Client, roomID string) {
	client.sendJSON(Message{Type: EventPresenceSnapshot, RoomID: roomID, Payload: h.roomPresence(roomID, time.Now())})
}

func (h *Hub) userOnline(roomID string, userID int64) bool {
	for _, client := range h.rooms[roomID] {
		if client.UserID == userID {
			return true
		}
	}
	return false
}

func (h *Hub) publishPresence(roomID string, userID int64, online bool) {
	h.fanOut(roomID, Message{Type: EventPresence, RoomID: roomID, Payload: PresencePayload{UserID: userID, Online: online}}, "")
}

func (h *Hub) leaveRoom(roomID string, client *Client) {
	if _, joined := client.rooms[roomID]; !joined {
		return
	}
	h.removeFromRoom(roomID, client)
	delete(client.rooms, roomID)
	h.clearTyping(roomID, client, time.Now())
	if !h.userOnline(roomID, client.UserID) {
		h.publishPresence(roomID, client.UserID, false)
	}
}
