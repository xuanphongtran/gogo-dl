// Package chat implements the chat domain: rooms, members, and messages.
package chat

import "time"

// RoomVisibility controls who can discover and join a room.
type RoomVisibility string

const (
	RoomVisibilityPublic  RoomVisibility = "public"
	RoomVisibilityPrivate RoomVisibility = "private"
)

// RoomRole identifies a user's authority within a room.
type RoomRole string

const (
	RoomRoleOwner     RoomRole = "owner"
	RoomRoleModerator RoomRole = "moderator"
	RoomRoleMember    RoomRole = "member"
)

// InvitationStatus identifies the durable state of a room invitation.
type InvitationStatus string

const (
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationDeclined InvitationStatus = "declined"
)

// Room represents a chat channel that users can join and exchange messages in.
type Room struct {
	ID         int64          `db:"id"          json:"id"`
	Name       string         `db:"name"        json:"name"`
	CreatedBy  int64          `db:"created_by"  json:"created_by"`
	CreatedAt  time.Time      `db:"created_at"  json:"created_at"`
	Visibility RoomVisibility `db:"visibility" json:"visibility"`
	Role       *RoomRole      `db:"role"        json:"role" extensions:"x-nullable"`
}

// Message is a single chat message sent within a room.
type Message struct {
	ID        int64      `db:"id"         json:"id"`
	RoomID    int64      `db:"room_id"    json:"room_id"`
	UserID    *int64     `db:"user_id"    json:"user_id" extensions:"x-nullable"`
	Username  string     `db:"username"   json:"username"` // joined from users table
	Content   string     `db:"content"    json:"content"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
	Revision  int64      `db:"revision"   json:"revision" minimum:"1"`
	EditedAt  *time.Time `db:"edited_at"  json:"edited_at" extensions:"x-nullable" format:"date-time"`
	DeletedAt *time.Time `db:"deleted_at" json:"deleted_at" extensions:"x-nullable" format:"date-time"`
}

// RoomMember links a user to a room (for membership tracking).
type RoomMember struct {
	RoomID   int64     `db:"room_id"   json:"room_id"`
	UserID   int64     `db:"user_id"   json:"user_id"`
	Username string    `db:"username"  json:"username"`
	Role     RoomRole  `db:"role"      json:"role"`
	JoinedAt time.Time `db:"joined_at" json:"joined_at"`
}

// Invitation is the public representation of a room invitation.
type Invitation struct {
	ID          int64            `db:"id"           json:"id"`
	RoomID      int64            `db:"room_id"      json:"room_id"`
	RoomName    string           `db:"room_name"    json:"room_name"`
	InviteeID   int64            `db:"invitee_id"   json:"invitee_id"`
	InvitedBy   *int64           `db:"invited_by"   json:"invited_by"`
	Status      InvitationStatus `db:"status"       json:"status"`
	CreatedAt   time.Time        `db:"created_at"   json:"created_at"`
	UpdatedAt   time.Time        `db:"updated_at"   json:"updated_at"`
	RespondedAt *time.Time       `db:"responded_at" json:"responded_at"`
}

// ── Request / Response DTOs ───────────────────────────────────────────────────

// CreateRoomRequest is the body for POST /rooms.
type CreateRoomRequest struct {
	Name       string         `json:"name"       binding:"required,min=1,max=100"`
	Visibility RoomVisibility `json:"visibility" binding:"omitempty,oneof=public private"`
}

// InviteUserRequest is the body for creating a room invitation.
type InviteUserRequest struct {
	UserID int64 `json:"user_id" binding:"required,min=1"`
}

// ChangeMemberRoleRequest is the body for changing a member's role.
type ChangeMemberRoleRequest struct {
	Role RoomRole `json:"role" binding:"required,oneof=member moderator"`
}

// TransferOwnershipRequest is the body for transferring room ownership.
type TransferOwnershipRequest struct {
	UserID int64 `json:"user_id" binding:"required,min=1"`
}

// ListInvitationsQuery controls the authenticated user's invitation list.
type ListInvitationsQuery struct {
	Status InvitationStatus `form:"status" binding:"omitempty,oneof=pending accepted declined all"`
	Limit  int              `form:"limit" binding:"omitempty,min=1,max=100"`
}

// SendMessageRequest is the body for POST /rooms/:id/messages.
type SendMessageRequest struct {
	// AttachmentIDs are reserved for scanner-backed attachment binding.
	AttachmentIDs []int64 `json:"attachment_ids,omitempty"`
	// Content must be nonblank and at most 4000 UTF-8 bytes after trimming whitespace.
	Content string `json:"content" binding:"required,min=1,max=4000"`
}

// EditMessageRequest contains new content and the message revision being edited.
type EditMessageRequest struct {
	// Content must be nonblank and at most 4000 UTF-8 bytes after trimming whitespace.
	Content  string `json:"content" binding:"required,min=1,max=4000"`
	Revision int64  `json:"revision" binding:"required,min=1"`
}

// ListMessagesQuery is the query string for GET /rooms/:id/messages.
type ListMessagesQuery struct {
	Limit  int   `form:"limit"   binding:"omitempty,min=1,max=100"`
	Before int64 `form:"before"  binding:"omitempty"` // cursor: message ID to paginate from
}

// RoomsResponse is the response envelope for room listings.
type RoomsResponse struct {
	Rooms []*Room `json:"rooms"`
}

// MessagesResponse is the response envelope for message history.
type MessagesResponse struct {
	Messages []*Message `json:"messages"`
}

// MembersResponse is the response envelope for room members.
type MembersResponse struct {
	Members []*RoomMember `json:"members"`
}

// InvitationsResponse is the response envelope for invitation listings.
type InvitationsResponse struct {
	Invitations []*Invitation `json:"invitations"`
}

// JoinResponse is returned after joining a public room.
type JoinResponse struct {
	Message string `json:"message"`
}

// ReadState is a member's personal room cursor and current unread count.
type ReadState struct {
	RoomID            int64 `db:"room_id"              json:"room_id"`
	LastReadMessageID int64 `db:"last_read_message_id" json:"last_read_message_id" minimum:"0"`
	UnreadCount       int64 `db:"unread_count"         json:"unread_count" minimum:"0"`
}

// AdvanceReadStateRequest acknowledges messages through a valid room message.
type AdvanceReadStateRequest struct {
	LastReadMessageID int64 `json:"last_read_message_id" binding:"required,min=1" minimum:"1"`
}
