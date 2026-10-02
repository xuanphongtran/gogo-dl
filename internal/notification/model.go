// Package notification implements the private, durable in-app mention inbox.
package notification

import "time"

// Notification contains references only; referenced content requires room authorization.
type Notification struct {
	ID           int64      `db:"id" json:"id"`
	Kind         string     `db:"kind" json:"kind"`
	RoomID       int64      `db:"room_id" json:"room_id"`
	MessageID    int64      `db:"message_id" json:"message_id"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at"`
	ReadAt       *time.Time `db:"read_at" json:"read_at" extensions:"x-nullable"`
	Availability string     `db:"availability" json:"availability" enums:"available,deleted"`
}

// Feed is an ID-descending cursor page of the authenticated user's notifications.
type Feed struct {
	Notifications []*Notification `json:"notifications"`
	NextBefore    *int64          `json:"next_before" extensions:"x-nullable"`
}

// Query selects an owned feed, without changing the room read cursor.
type Query struct {
	Before     int64 `form:"before" binding:"omitempty,min=1"`
	Limit      int   `form:"limit" binding:"omitempty,min=1,max=100"`
	UnreadOnly bool  `form:"unread_only"`
}

// GlobalPreferences defaults to mention notifications enabled.
type GlobalPreferences struct {
	MentionsEnabled bool `json:"mentions_enabled"`
}

// RoomPreferences belongs to the user's current membership generation.
type RoomPreferences struct {
	Muted bool `json:"muted"`
}

// GlobalRequest uses a pointer to accept false while rejecting a missing boolean.
type GlobalRequest struct {
	MentionsEnabled *bool `json:"mentions_enabled" binding:"required"`
}

// RoomRequest uses a pointer to accept false while rejecting a missing boolean.
type RoomRequest struct {
	Muted *bool `json:"muted" binding:"required"`
}
