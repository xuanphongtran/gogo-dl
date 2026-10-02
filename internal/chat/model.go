// Package chat implements the chat domain: rooms, members, and messages.
package chat

import "time"

// Room represents a chat channel that users can join and exchange messages in.
type Room struct {
	ID        int64     `db:"id"          json:"id"`
	Name      string    `db:"name"        json:"name"`
	CreatedBy int64     `db:"created_by"  json:"created_by"`
	CreatedAt time.Time `db:"created_at"  json:"created_at"`
}

// Message is a single chat message sent within a room.
type Message struct {
	ID        int64     `db:"id"         json:"id"`
	RoomID    int64     `db:"room_id"    json:"room_id"`
	UserID    int64     `db:"user_id"    json:"user_id"`
	Username  string    `db:"username"   json:"username"` // joined from users table
	Content   string    `db:"content"    json:"content"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// RoomMember links a user to a room (for membership tracking).
type RoomMember struct {
	RoomID   int64     `db:"room_id"`
	UserID   int64     `db:"user_id"`
	JoinedAt time.Time `db:"joined_at"`
}

// ── Request / Response DTOs ───────────────────────────────────────────────────

// CreateRoomRequest is the body for POST /rooms.
type CreateRoomRequest struct {
	Name string `json:"name" binding:"required,min=1,max=100"`
}

// SendMessageRequest is the body for POST /rooms/:id/messages.
type SendMessageRequest struct {
	Content string `json:"content" binding:"required,min=1,max=4000"`
}

// ListMessagesQuery is the query string for GET /rooms/:id/messages.
type ListMessagesQuery struct {
	Limit  int   `form:"limit"   binding:"omitempty,min=1,max=100"`
	Before int64 `form:"before"  binding:"omitempty"` // cursor: message ID to paginate from
}
