package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

type roomViewRow struct {
	ID         int64          `db:"id"`
	Name       string         `db:"name"`
	CreatedBy  int64          `db:"created_by"`
	CreatedAt  sql.NullTime   `db:"created_at"`
	Visibility RoomVisibility `db:"visibility"`
	Role       sql.NullString `db:"role"`
}

func (r roomViewRow) room() *Room {
	room := &Room{
		ID:         r.ID,
		Name:       r.Name,
		CreatedBy:  r.CreatedBy,
		Visibility: r.Visibility,
	}
	if r.CreatedAt.Valid {
		room.CreatedAt = r.CreatedAt.Time
	}
	if r.Role.Valid {
		role := RoomRole(r.Role.String)
		room.Role = &role
	}
	return room
}

func (r *postgresRepository) GetRoomForUser(ctx context.Context, roomID, userID int64) (*Room, error) {
	var row roomViewRow
	err := r.db.GetContext(ctx, &row, `
		SELECT r.id, r.name, r.created_by, r.created_at, r.visibility, rm.role
		FROM rooms r
		LEFT JOIN room_members rm
		  ON rm.room_id = r.id AND rm.user_id = $2
		WHERE r.id = $1
		  AND (r.visibility = 'public' OR rm.user_id IS NOT NULL)`, roomID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("chat repo GetRoomForUser: %w", err)
	}
	return row.room(), nil
}

func (r *postgresRepository) ListRoomsForUser(ctx context.Context, userID int64) ([]*Room, error) {
	var rows []roomViewRow
	if err := r.db.SelectContext(ctx, &rows, `
		SELECT r.id, r.name, r.created_by, r.created_at, r.visibility, rm.role
		FROM rooms r
		LEFT JOIN room_members rm
		  ON rm.room_id = r.id AND rm.user_id = $1
		WHERE r.visibility = 'public' OR rm.user_id IS NOT NULL
		ORDER BY r.created_at DESC`, userID); err != nil {
		return nil, fmt.Errorf("chat repo ListRoomsForUser: %w", err)
	}
	rooms := make([]*Room, 0, len(rows))
	for _, row := range rows {
		rooms = append(rooms, row.room())
	}
	return rooms, nil
}

func (r *postgresRepository) JoinPublicRoom(ctx context.Context, roomID, userID int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO room_members (room_id, user_id, role)
		SELECT $1, $2, 'member'
		WHERE EXISTS (SELECT 1 FROM rooms WHERE id = $1 AND visibility = 'public')
		ON CONFLICT (room_id, user_id) DO NOTHING`, roomID, userID)
	if err != nil {
		if isPostgresConstraintCode(err, "23503") {
			return false, apperror.ErrNotFound
		}
		return false, fmt.Errorf("chat repo JoinPublicRoom: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("chat repo JoinPublicRoom rows affected: %w", err)
	}
	return affected > 0, nil
}

func (r *postgresRepository) GetMember(ctx context.Context, roomID, userID int64) (*RoomMember, error) {
	var member RoomMember
	err := r.db.GetContext(ctx, &member, `
		SELECT rm.room_id, rm.user_id, u.username, rm.role, rm.joined_at
		FROM room_members rm
		JOIN users u ON u.id = rm.user_id
		WHERE rm.room_id = $1 AND rm.user_id = $2`, roomID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("chat repo GetMember: %w", err)
	}
	return &member, nil
}

func (r *postgresRepository) ListMembers(ctx context.Context, roomID int64) ([]*RoomMember, error) {
	var members []*RoomMember
	if err := r.db.SelectContext(ctx, &members, `
		SELECT rm.room_id, rm.user_id, u.username, rm.role, rm.joined_at
		FROM room_members rm
		JOIN users u ON u.id = rm.user_id
		WHERE rm.room_id = $1
		ORDER BY rm.joined_at ASC, rm.user_id ASC`, roomID); err != nil {
		return nil, fmt.Errorf("chat repo ListMembers: %w", err)
	}
	return members, nil
}

func (r *postgresRepository) SetMemberRole(ctx context.Context, roomID, userID int64, role RoomRole) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE room_members
		SET role = $3
		WHERE room_id = $1 AND user_id = $2 AND role <> 'owner'`, roomID, userID, role)
	if err != nil {
		return fmt.Errorf("chat repo SetMemberRole: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("chat repo SetMemberRole rows affected: %w", err)
	}
	if affected == 0 {
		member, memberErr := r.GetMember(ctx, roomID, userID)
		if memberErr != nil {
			return memberErr
		}
		if member.Role == RoomRoleOwner {
			return apperror.ErrConflict
		}
		return nil
	}
	return nil
}

func (r *postgresRepository) TransferOwnership(ctx context.Context, roomID, currentOwnerID, targetUserID int64) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("chat repo TransferOwnership begin: %w", err)
	}
	rollback := func(opErr error) error {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return fmt.Errorf("chat repo TransferOwnership rollback: %v: %w", rollbackErr, opErr)
		}
		return opErr
	}

	var ownerID int64
	if err := tx.GetContext(ctx, &ownerID, `SELECT created_by FROM rooms WHERE id = $1 FOR UPDATE`, roomID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rollback(apperror.ErrNotFound)
		}
		return rollback(fmt.Errorf("chat repo TransferOwnership room: %w", err))
	}
	if ownerID != currentOwnerID {
		return rollback(apperror.ErrForbidden)
	}
	if targetUserID == currentOwnerID {
		return rollback(apperror.ErrConflict)
	}

	var targetRole RoomRole
	if err := tx.GetContext(ctx, &targetRole,
		`SELECT role FROM room_members WHERE room_id = $1 AND user_id = $2 FOR UPDATE`, roomID, targetUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rollback(apperror.ErrNotFound)
		}
		return rollback(fmt.Errorf("chat repo TransferOwnership target: %w", err))
	}
	if targetRole == RoomRoleOwner {
		return rollback(apperror.ErrConflict)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE room_members SET role = 'moderator' WHERE room_id = $1 AND user_id = $2`, roomID, currentOwnerID); err != nil {
		return rollback(fmt.Errorf("chat repo TransferOwnership previous owner: %w", err))
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE room_members SET role = 'owner' WHERE room_id = $1 AND user_id = $2`, roomID, targetUserID); err != nil {
		return rollback(fmt.Errorf("chat repo TransferOwnership target owner: %w", err))
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE rooms SET created_by = $2 WHERE id = $1`, roomID, targetUserID); err != nil {
		return rollback(fmt.Errorf("chat repo TransferOwnership room owner: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("chat repo TransferOwnership commit: %w", err)
	}
	return nil
}

func (r *postgresRepository) CreateOrGetInvitation(ctx context.Context, roomID, inviterID, inviteeID int64) (*Invitation, bool, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("chat repo CreateOrGetInvitation begin: %w", err)
	}
	rollback := func(opErr error) (*Invitation, bool, error) {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return nil, false, fmt.Errorf("chat repo CreateOrGetInvitation rollback: %v: %w", rollbackErr, opErr)
		}
		return nil, false, opErr
	}

	var memberExists bool
	if err := tx.GetContext(ctx, &memberExists,
		`SELECT EXISTS (SELECT 1 FROM room_members WHERE room_id = $1 AND user_id = $2)`, roomID, inviteeID); err != nil {
		return rollback(fmt.Errorf("chat repo CreateOrGetInvitation membership: %w", err))
	}

	var invitation Invitation
	var query string
	if memberExists {
		query = `
			SELECT i.id, i.room_id, r.name AS room_name, i.invitee_id, i.invited_by,
			       i.status, i.created_at, i.updated_at, i.responded_at
			FROM room_invitations i
			JOIN rooms r ON r.id = i.room_id
			WHERE i.room_id = $1 AND i.invitee_id = $2
			ORDER BY i.id DESC
			LIMIT 1
			FOR UPDATE`
		if err := tx.GetContext(ctx, &invitation, query, roomID, inviteeID); err == nil {
			if invitation.Status != InvitationAccepted {
				var respondedAt sql.NullTime
				if err := tx.QueryRowxContext(ctx, `
					UPDATE room_invitations
					SET status = 'accepted', updated_at = NOW(), responded_at = COALESCE(responded_at, NOW())
					WHERE id = $1
					RETURNING updated_at, responded_at`, invitation.ID).Scan(&invitation.UpdatedAt, &respondedAt); err != nil {
					return rollback(fmt.Errorf("chat repo CreateOrGetInvitation accepted update: %w", err))
				}
				invitation.Status = InvitationAccepted
				if respondedAt.Valid {
					invitation.RespondedAt = &respondedAt.Time
				}
			}
			if err := tx.Commit(); err != nil {
				return nil, false, fmt.Errorf("chat repo CreateOrGetInvitation commit: %w", err)
			}
			return &invitation, false, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return rollback(fmt.Errorf("chat repo CreateOrGetInvitation existing: %w", err))
		}
	}

	if query == "" {
		query = `
			SELECT i.id, i.room_id, r.name AS room_name, i.invitee_id, i.invited_by,
			       i.status, i.created_at, i.updated_at, i.responded_at
			FROM room_invitations i
			JOIN rooms r ON r.id = i.room_id
			WHERE i.room_id = $1 AND i.invitee_id = $2
			ORDER BY i.id DESC
			LIMIT 1
			FOR UPDATE`
	}
	if err := tx.GetContext(ctx, &invitation, query, roomID, inviteeID); err == nil {
		if invitation.Status == InvitationPending || invitation.Status == InvitationAccepted {
			if err := tx.Commit(); err != nil {
				return nil, false, fmt.Errorf("chat repo CreateOrGetInvitation commit: %w", err)
			}
			return &invitation, false, nil
		}
		var updatedAt time.Time
		if err := tx.GetContext(ctx, &updatedAt, `
			UPDATE room_invitations
			SET invited_by = $2, status = 'pending', updated_at = NOW(), responded_at = NULL
			WHERE id = $1
			RETURNING updated_at`, invitation.ID, inviterID); err != nil {
			return rollback(fmt.Errorf("chat repo CreateOrGetInvitation retry: %w", err))
		}
		invitation.InvitedBy = &inviterID
		invitation.Status = InvitationPending
		invitation.UpdatedAt = updatedAt
		invitation.RespondedAt = nil
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("chat repo CreateOrGetInvitation commit: %w", err)
		}
		return &invitation, !memberExists, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return rollback(fmt.Errorf("chat repo CreateOrGetInvitation lookup: %w", err))
	}

	insertStatus := InvitationPending
	if memberExists {
		insertStatus = InvitationAccepted
	}
	if err := tx.GetContext(ctx, &invitation, `
		INSERT INTO room_invitations (room_id, invitee_id, invited_by, status, responded_at)
		VALUES ($1, $2, $3, $4, CASE WHEN $4 = 'accepted' THEN NOW() ELSE NULL END)
		RETURNING id, room_id, invitee_id, invited_by, status, created_at, updated_at, responded_at`, roomID, inviteeID, inviterID, insertStatus); err != nil {
		if isPostgresConstraintCode(err, "23503") {
			return rollback(apperror.ErrNotFound)
		}
		return rollback(fmt.Errorf("chat repo CreateOrGetInvitation insert: %w", err))
	}
	if err := tx.GetContext(ctx, &invitation.RoomName, `SELECT name FROM rooms WHERE id = $1`, roomID); err != nil {
		return rollback(fmt.Errorf("chat repo CreateOrGetInvitation room: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("chat repo CreateOrGetInvitation commit: %w", err)
	}
	return &invitation, !memberExists, nil
}

func (r *postgresRepository) ListInvitations(ctx context.Context, inviteeID int64, status InvitationStatus, limit int) ([]*Invitation, error) {
	if limit <= 0 {
		limit = 50
	}
	var invitations []*Invitation
	query := `
		SELECT i.id, i.room_id, r.name AS room_name, i.invitee_id, i.invited_by,
		       i.status, i.created_at, i.updated_at, i.responded_at
		FROM room_invitations i
		JOIN rooms r ON r.id = i.room_id
		WHERE i.invitee_id = $1
		  AND ($2 = 'all' OR i.status = $2)
		ORDER BY i.id DESC
		LIMIT $3`
	if err := r.db.SelectContext(ctx, &invitations, query, inviteeID, status, limit); err != nil {
		return nil, fmt.Errorf("chat repo ListInvitations: %w", err)
	}
	return invitations, nil
}

func (r *postgresRepository) RespondInvitation(ctx context.Context, invitationID, inviteeID int64, status InvitationStatus) (*Invitation, bool, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("chat repo RespondInvitation begin: %w", err)
	}
	rollback := func(opErr error) (*Invitation, bool, error) {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return nil, false, fmt.Errorf("chat repo RespondInvitation rollback: %v: %w", rollbackErr, opErr)
		}
		return nil, false, opErr
	}

	var invitation Invitation
	if err := tx.GetContext(ctx, &invitation, `
		SELECT i.id, i.room_id, r.name AS room_name, i.invitee_id, i.invited_by,
		       i.status, i.created_at, i.updated_at, i.responded_at
		FROM room_invitations i
		JOIN rooms r ON r.id = i.room_id
		WHERE i.id = $1 AND i.invitee_id = $2
		FOR UPDATE`, invitationID, inviteeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rollback(apperror.ErrNotFound)
		}
		return rollback(fmt.Errorf("chat repo RespondInvitation lookup: %w", err))
	}

	membershipCreated := false
	if invitation.Status == status {
		if status == InvitationAccepted {
			result, err := tx.ExecContext(ctx,
				`INSERT INTO room_members (room_id, user_id, role) VALUES ($1, $2, 'member') ON CONFLICT DO NOTHING`, invitation.RoomID, inviteeID)
			if err != nil {
				return rollback(fmt.Errorf("chat repo RespondInvitation membership: %w", err))
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return rollback(fmt.Errorf("chat repo RespondInvitation membership rows affected: %w", err))
			}
			membershipCreated = affected > 0
		}
		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("chat repo RespondInvitation commit: %w", err)
		}
		return &invitation, membershipCreated, nil
	}
	if invitation.Status != InvitationPending {
		return rollback(apperror.ErrInvitationState)
	}

	if status == InvitationAccepted {
		result, err := tx.ExecContext(ctx,
			`INSERT INTO room_members (room_id, user_id, role) VALUES ($1, $2, 'member') ON CONFLICT DO NOTHING`, invitation.RoomID, inviteeID)
		if err != nil {
			return rollback(fmt.Errorf("chat repo RespondInvitation membership: %w", err))
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return rollback(fmt.Errorf("chat repo RespondInvitation membership rows affected: %w", err))
		}
		membershipCreated = affected > 0
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE room_invitations
		SET status = $2, updated_at = NOW(), responded_at = NOW()
		WHERE id = $1`, invitationID, status); err != nil {
		return rollback(fmt.Errorf("chat repo RespondInvitation update: %w", err))
	}
	invitation.Status = status
	if err := tx.GetContext(ctx, &invitation.UpdatedAt, `SELECT updated_at FROM room_invitations WHERE id = $1`, invitationID); err != nil {
		return rollback(fmt.Errorf("chat repo RespondInvitation updated timestamp: %w", err))
	}
	respondedAt := invitation.UpdatedAt
	invitation.RespondedAt = &respondedAt
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("chat repo RespondInvitation commit: %w", err)
	}
	return &invitation, membershipCreated, nil
}
