package ws

import "time"

const (
	typingTTL           = 5 * time.Second
	typingStartInterval = time.Second
	typingSweepInterval = 250 * time.Millisecond
)

func (h *Hub) requestTyping(client *Client, rawRoomID string, command EventType, now time.Time) {
	roomNumber, roomID, err := parseRoomID(rawRoomID)
	if err != nil {
		h.sendProtocolError(client, rawRoomID, "invalid_room_id", "invalid room id")
		return
	}
	if _, joined := client.rooms[roomID]; !joined {
		h.sendProtocolError(client, roomID, "not_joined", "join the room before sending typing commands")
		return
	}
	if command == EventTypingStopped && client.authorization == nil {
		if _, active := h.typing[roomID][client.ID]; !active {
			return
		}
	}
	if command == EventTypingStarted && now.Sub(client.lastTypingStart) < typingStartInterval {
		h.sendProtocolError(client, roomID, "rate_limited", "typing starts are limited to once per second")
		return
	}
	h.requestAuthorization(client, roomNumber, roomID, command)
}

func (h *Hub) applyTyping(client *Client, roomID string, command EventType, now time.Time) {
	if command == EventTypingStopped {
		h.clearTyping(roomID, client, now)
		return
	}
	client.lastTypingStart = now
	if h.typing[roomID] == nil {
		h.typing[roomID] = make(map[string]time.Time)
	}
	h.typing[roomID][client.ID] = now.Add(typingTTL)
	h.publishTyping(roomID, client.UserID, now)
}

func (h *Hub) typingDeadline(roomID string, userID int64, now time.Time) *time.Time {
	var latest time.Time
	for clientID, expiresAt := range h.typing[roomID] {
		client, joined := h.rooms[roomID][clientID]
		if joined && client.UserID == userID && expiresAt.After(now) && expiresAt.After(latest) {
			latest = expiresAt
		}
	}
	if latest.IsZero() {
		return nil
	}
	return &latest
}

func (h *Hub) publishTyping(roomID string, userID int64, now time.Time) {
	deadline := h.typingDeadline(roomID, userID, now)
	event := EventTypingStopped
	if deadline != nil {
		event = EventTypingStarted
	}
	h.fanOut(roomID, Message{Type: event, RoomID: roomID, Payload: TypingPayload{UserID: userID, ExpiresAt: deadline}}, "")
}

func (h *Hub) clearTyping(roomID string, client *Client, now time.Time) {
	if _, typing := h.typing[roomID][client.ID]; !typing {
		return
	}
	delete(h.typing[roomID], client.ID)
	if len(h.typing[roomID]) == 0 {
		delete(h.typing, roomID)
	}
	h.publishTyping(roomID, client.UserID, now)
}

func (h *Hub) expireTyping(now time.Time) {
	for roomID, clients := range h.typing {
		affectedUsers := make(map[int64]struct{})
		for clientID, expiresAt := range clients {
			if expiresAt.After(now) {
				continue
			}
			if client, joined := h.rooms[roomID][clientID]; joined {
				affectedUsers[client.UserID] = struct{}{}
			}
			delete(clients, clientID)
		}
		if len(clients) == 0 {
			delete(h.typing, roomID)
		}
		for userID := range affectedUsers {
			if h.typingDeadline(roomID, userID, now) == nil {
				h.publishTyping(roomID, userID, now)
			}
		}
	}
}
