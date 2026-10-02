package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"go.opentelemetry.io/otel"
)

// Repository defines data-access operations for the chat domain.
type Repository interface {
	// Room operations
	CreateRoom(ctx context.Context, room *Room) error
	CreateRoomWithMember(ctx context.Context, room *Room, userID int64) error
	GetRoomByID(ctx context.Context, id int64) (*Room, error)
	ListRooms(ctx context.Context) ([]*Room, error)
	GetRoomForUser(ctx context.Context, roomID, userID int64) (*Room, error)
	ListRoomsForUser(ctx context.Context, userID int64) ([]*Room, error)

	// Member operations
	AddMember(ctx context.Context, roomID, userID int64) error
	RemoveMember(ctx context.Context, roomID, userID int64) error
	// RemoveMemberWithAuthorization locks current memberships, calls the service
	// policy with nil for missing members, and deletes only if it authorizes.
	RemoveMemberWithAuthorization(ctx context.Context, roomID, actorID, userID int64, authorize func(actor, target *RoomMember) error) (*RoomMember, error)
	IsMember(ctx context.Context, roomID, userID int64) (bool, error)
	JoinPublicRoom(ctx context.Context, roomID, userID int64) (bool, error)
	GetMember(ctx context.Context, roomID, userID int64) (*RoomMember, error)
	ListMembers(ctx context.Context, roomID int64) ([]*RoomMember, error)
	SetMemberRole(ctx context.Context, roomID, userID int64, role RoomRole) error
	TransferOwnership(ctx context.Context, roomID, currentOwnerID, targetUserID int64) error
	CreateOrGetInvitation(ctx context.Context, roomID, inviterID, inviteeID int64) (*Invitation, bool, error)
	GetInvitation(ctx context.Context, invitationID, inviteeID int64) (*Invitation, error)
	ListInvitations(ctx context.Context, inviteeID int64, status InvitationStatus, limit int) ([]*Invitation, error)
	RespondInvitation(ctx context.Context, invitationID, inviteeID int64, status InvitationStatus) (*Invitation, bool, error)

	// Message operations
	CreateMessage(ctx context.Context, msg *Message) error
	ListMessages(ctx context.Context, roomID int64, limit int, beforeID int64) ([]*Message, error)
	// SearchMessages reads matches while holding room and membership locks.
	SearchMessages(ctx context.Context, roomID, userID int64, query string, limit int, beforeID int64, authorize ReadStatePolicy) ([]*Message, error)
	EditMessage(ctx context.Context, roomID, actorID, messageID int64, content string, policy MessageMutationPolicy) (*Message, bool, error)
	DeleteMessage(ctx context.Context, roomID, actorID, messageID int64, policy MessageMutationPolicy) (*Message, bool, error)

	// Read-state operations authorize against locked room and membership rows.
	GetReadState(ctx context.Context, roomID, userID int64, authorize ReadStatePolicy) (*ReadState, error)
	AdvanceReadState(ctx context.Context, roomID, userID, messageID int64, authorize ReadStatePolicy) (*ReadState, bool, error)
}

type postgresRepository struct {
	db *sqlx.DB
}

// NewRepository creates a new PostgreSQL-backed chat Repository.
func NewRepository(db *sqlx.DB) Repository {
	return &explicitRepository{Repository: &postgresRepository{db: db}, db: db}
}

// ── Room ──────────────────────────────────────────────────────────────────────

func (r *postgresRepository) CreateRoom(ctx context.Context, room *Room) error {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.CreateRoom")
	defer span.End()
	query := `
		INSERT INTO rooms (name, created_by, visibility)
		VALUES (:name, :created_by, COALESCE(NULLIF(:visibility, ''), 'public'))
		RETURNING id, created_at`

	rows, err := r.db.NamedQueryContext(ctx, query, room)
	if err != nil {
		return fmt.Errorf("chat repo CreateRoom: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return fmt.Errorf("chat repo CreateRoom: %w", sql.ErrNoRows)
	}
	if err := rows.Scan(&room.ID, &room.CreatedAt); err != nil {
		return fmt.Errorf("chat repo CreateRoom scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("chat repo CreateRoom rows: %w", err)
	}
	return nil
}

func (r *postgresRepository) GetRoomByID(ctx context.Context, id int64) (*Room, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.GetRoomByID")
	defer span.End()
	var room Room
	err := r.db.GetContext(ctx, &room, `SELECT id, name, created_by, created_at, visibility FROM rooms WHERE id = $1`, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, apperror.ErrNotFound
		}
		return nil, fmt.Errorf("chat repo GetRoomByID: %w", err)
	}
	return &room, nil
}

func (r *postgresRepository) ListRooms(ctx context.Context) ([]*Room, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.ListRooms")
	defer span.End()
	var rooms []*Room
	err := r.db.SelectContext(ctx, &rooms, `SELECT id, name, created_by, created_at, visibility FROM rooms ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("chat repo ListRooms: %w", err)
	}
	return rooms, nil
}

// ── Members ───────────────────────────────────────────────────────────────────

func (r *postgresRepository) AddMember(ctx context.Context, roomID, userID int64) error {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.AddMember")
	defer span.End()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO room_members (room_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		roomID, userID,
	)
	if err != nil {
		if isPostgresConstraintCode(err, "23503") {
			return apperror.ErrNotFound
		}
		return fmt.Errorf("chat repo AddMember: %w", err)
	}
	return nil
}

func (r *postgresRepository) IsMember(ctx context.Context, roomID, userID int64) (bool, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.IsMember")
	defer span.End()
	var count int
	err := r.db.GetContext(ctx, &count,
		`SELECT COUNT(*) FROM room_members WHERE room_id = $1 AND user_id = $2`,
		roomID, userID,
	)
	if err != nil {
		return false, fmt.Errorf("chat repo IsMember: %w", err)
	}
	return count > 0, nil
}

// ── Messages ──────────────────────────────────────────────────────────────────

func (r *postgresRepository) CreateMessage(ctx context.Context, msg *Message) error {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.CreateMessage")
	defer span.End()
	query := `
		INSERT INTO messages (room_id, user_id, content)
		VALUES (:room_id, :user_id, :content)
		RETURNING id, created_at, revision,
            COALESCE((SELECT username FROM users WHERE users.id = messages.user_id), '[deleted user]') AS username`

	rows, err := r.db.NamedQueryContext(ctx, query, msg)
	if err != nil {
		return fmt.Errorf("chat repo CreateMessage: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return fmt.Errorf("chat repo CreateMessage: %w", sql.ErrNoRows)
	}
	if err := rows.Scan(&msg.ID, &msg.CreatedAt, &msg.Revision, &msg.Username); err != nil {
		return fmt.Errorf("chat repo CreateMessage scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("chat repo CreateMessage rows: %w", err)
	}
	return nil
}

// ListMessages returns up to `limit` messages in a room, with cursor-based pagination.
// If beforeID > 0, only messages with id < beforeID are returned (older messages).
func (r *postgresRepository) ListMessages(ctx context.Context, roomID int64, limit int, beforeID int64) ([]*Message, error) {
	ctx, span := otel.Tracer("gogo-dl/chat").Start(ctx, "chat.repository.ListMessages")
	defer span.End()
	if limit <= 0 {
		limit = 50 // sensible default
	}

	var (
		msgs []*Message
		err  error
	)

	// We JOIN users to get the username alongside each message.
	if beforeID > 0 {
		err = r.db.SelectContext(ctx, &msgs, `
			SELECT m.id, m.room_id, m.user_id, COALESCE(u.username, '[deleted user]') AS username,
                m.content, m.created_at, m.revision, m.edited_at, m.deleted_at
			FROM messages m
			LEFT JOIN users u ON u.id = m.user_id
			WHERE m.room_id = $1 AND m.id < $2
			ORDER BY m.id DESC
			LIMIT $3`,
			roomID, beforeID, limit,
		)
	} else {
		err = r.db.SelectContext(ctx, &msgs, `
			SELECT m.id, m.room_id, m.user_id, COALESCE(u.username, '[deleted user]') AS username,
                m.content, m.created_at, m.revision, m.edited_at, m.deleted_at
			FROM messages m
			LEFT JOIN users u ON u.id = m.user_id
			WHERE m.room_id = $1
			ORDER BY m.id DESC
			LIMIT $2`,
			roomID, limit,
		)
	}

	if err != nil {
		return nil, fmt.Errorf("chat repo ListMessages: %w", err)
	}
	return msgs, nil
}
