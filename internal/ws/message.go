// Package ws provides a WebSocket hub that manages rooms and broadcasts messages.
package ws

import "time"

// EventType identifies the kind of event being sent over the wire.
type EventType string

const (
	EventMessage           EventType = "message" // new chat message
	EventMessageUpdated    EventType = "message_updated"
	EventMessageDeleted    EventType = "message_deleted"
	EventJoin              EventType = "join"      // user joined a room
	EventLeave             EventType = "leave"     // user left a room
	EventUserList          EventType = "user_list" // current room members snapshot
	EventError             EventType = "error"     // server-side error notification
	EventMembershipChanged EventType = "membership_changed"
	EventInvitation        EventType = "invitation"
	EventPresence          EventType = "presence"
	EventPresenceSnapshot  EventType = "presence_snapshot"
	EventTypingStarted     EventType = "typing_started"
	EventTypingStopped     EventType = "typing_stopped"
	EventReadState         EventType = "read_state"
	EventNotification      EventType = "notification"
)

// PresencePayload reports an aggregated user's room subscription status.
type PresencePayload struct {
	UserID int64 `json:"user_id"`
	Online bool  `json:"online"`
}

// PresenceSnapshot is a process-local room snapshot visible to current members.
type PresenceSnapshot struct {
	RoomID        string  `json:"room_id"`
	OnlineUserIDs []int64 `json:"online_user_ids"`
	TypingUserIDs []int64 `json:"typing_user_ids"`
}

// TypingPayload identifies the authenticated user and the aggregated deadline.
// Stopped events have a nil deadline.
type TypingPayload struct {
	UserID    int64      `json:"user_id"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// Message is the canonical envelope for all WebSocket events.
// Both the hub → client "push" and client → hub "receive" paths use this struct.
//
// JSON layout:
//
//	{
//	  "type":    "message",
//	  "room_id": "abc-123",
//	  "payload": { ... event-specific data ... }
//	}
type Message struct {
	Type    EventType   `json:"type"`
	RoomID  string      `json:"room_id"`
	Payload interface{} `json:"payload"`
}

// inboundMessage is what the hub reads from a client after JSON decode.
// The Payload field carries the raw JSON so each handler can further unmarshal it.
type inboundMessage struct {
	ClientID  string
	UserID    int64
	Message   Message
	ErrorCode string
	handled   chan struct{}
	closed    chan struct{}
}

type outboundMessage struct {
	data       []byte
	closeAfter bool
	closed     chan struct{}
}
