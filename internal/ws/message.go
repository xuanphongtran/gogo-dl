// Package ws provides a WebSocket hub that manages rooms and broadcasts messages.
package ws

// EventType identifies the kind of event being sent over the wire.
type EventType string

const (
	EventMessage  EventType = "message"   // new chat message
	EventJoin     EventType = "join"      // user joined a room
	EventLeave    EventType = "leave"     // user left a room
	EventUserList EventType = "user_list" // current room members snapshot
	EventError    EventType = "error"     // server-side error notification
)

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
