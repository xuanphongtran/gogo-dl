package chat

import (
	"context"
	"strconv"

	"github.com/rs/zerolog/log"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// GetRoomForUser returns a room only when it is public or the caller is a
// member. The repository deliberately hides private-room existence from
// non-members.
func (s *Service) GetRoomForUser(ctx context.Context, userID, roomID int64) (*Room, error) {
	return s.repo.GetRoomForUser(ctx, roomID, userID)
}

// ListRoomsForUser returns discoverable public rooms and private rooms the
// caller belongs to.
func (s *Service) ListRoomsForUser(ctx context.Context, userID int64) ([]*Room, error) {
	return s.repo.ListRoomsForUser(ctx, userID)
}

// ListMessagesForUser enforces membership before returning room history.
func (s *Service) ListMessagesForUser(ctx context.Context, userID, roomID int64, q *ListMessagesQuery) ([]*Message, error) {
	if _, err := s.repo.GetRoomForUser(ctx, roomID, userID); err != nil {
		return nil, err
	}
	if err := s.requireMember(ctx, roomID, userID); err != nil {
		return nil, err
	}
	return s.repo.ListMessages(ctx, roomID, q.Limit, q.Before)
}

// JoinPublicRoom adds a user to a public room. The operation is idempotent.
func (s *Service) JoinPublicRoom(ctx context.Context, roomID, userID int64) error {
	room, err := s.repo.GetRoomByID(ctx, roomID)
	if err != nil {
		return err
	}
	if room.Visibility == RoomVisibilityPrivate {
		return apperror.ErrForbidden
	}
	added, err := s.repo.JoinPublicRoom(ctx, roomID, userID)
	if err != nil {
		return err
	}
	if added {
		s.broadcastMembership(roomID, "joined", userID, RoomRoleMember, userID)
	}
	return nil
}

// ListMembers returns a room's member projection after checking membership.
func (s *Service) ListMembers(ctx context.Context, actorID, roomID int64) ([]*RoomMember, error) {
	if err := s.requireMember(ctx, roomID, actorID); err != nil {
		return nil, err
	}
	return s.repo.ListMembers(ctx, roomID)
}

// LeaveRoom removes the caller's membership. Owners must transfer ownership
// first so every room retains exactly one owner.
func (s *Service) LeaveRoom(ctx context.Context, userID, roomID int64) error {
	member, err := s.repo.GetMember(ctx, roomID, userID)
	if err != nil {
		return err
	}
	if member.Role == RoomRoleOwner {
		return apperror.ErrOwnerTransfer
	}
	if err := s.repo.RemoveMember(ctx, roomID, userID); err != nil {
		return err
	}
	s.revokeMembership(ctx, roomID, userID)
	s.broadcastMembership(roomID, "left", userID, member.Role, userID)
	return nil
}

// Invite creates or retries an invitation after checking owner/moderator
// permissions. Invitation persistence is idempotent in the repository.
func (s *Service) Invite(ctx context.Context, actorID, roomID, inviteeID int64) (*Invitation, bool, error) {
	if actorID == inviteeID {
		return nil, false, apperror.ErrInvalidRequest
	}
	if err := s.requireManager(ctx, roomID, actorID); err != nil {
		return nil, false, err
	}
	invitation, created, err := s.repo.CreateOrGetInvitation(ctx, roomID, actorID, inviteeID)
	if err != nil {
		return nil, false, err
	}
	if created && invitation.Status == InvitationPending {
		if err := s.hub.BroadcastToUser(inviteeID, ws.Message{
			Type:   ws.EventInvitation,
			RoomID: strconv.FormatInt(roomID, 10),
			Payload: map[string]interface{}{
				"invitation_id": invitation.ID,
				"action":        "created",
				"status":        invitation.Status,
			},
		}); err != nil {
			log.Warn().Err(err).Int64("room_id", roomID).Int64("invitee_id", inviteeID).Msg("chat: invitation event dropped")
		}
	}
	return invitation, created, nil
}

// ListInvitations returns only invitations addressed to the caller.
func (s *Service) ListInvitations(ctx context.Context, userID int64, status InvitationStatus, limit int) ([]*Invitation, error) {
	if status == "" {
		status = InvitationPending
	}
	if status != InvitationPending && status != InvitationAccepted && status != InvitationDeclined && status != "all" {
		return nil, apperror.ErrInvalidRequest
	}
	if limit <= 0 {
		limit = 50
	}
	return s.repo.ListInvitations(ctx, userID, status, limit)
}

// RespondInvitation accepts or declines an invitation owned by the caller.
func (s *Service) RespondInvitation(ctx context.Context, userID, invitationID int64, status InvitationStatus) (*Invitation, error) {
	if status != InvitationAccepted && status != InvitationDeclined {
		return nil, apperror.ErrInvalidRequest
	}
	invitation, err := s.repo.GetInvitation(ctx, invitationID, userID)
	if err != nil {
		return nil, err
	}
	wasMember := false
	if status == InvitationAccepted {
		_, memberErr := s.repo.GetMember(ctx, invitation.RoomID, userID)
		wasMember = memberErr == nil
	}
	invitation, err = s.repo.RespondInvitation(ctx, invitationID, userID, status)
	if err != nil {
		return nil, err
	}
	if status == InvitationAccepted && !wasMember {
		s.broadcastMembership(invitation.RoomID, "joined", userID, RoomRoleMember, userID)
	}
	return invitation, nil
}

// RemoveMember removes a target after checking the actor's role. It is not a
// permanent ban; public users may join again explicitly after removal.
func (s *Service) RemoveMemberAs(ctx context.Context, actorID, roomID, targetID int64) error {
	actor, err := s.repo.GetMember(ctx, roomID, actorID)
	if err != nil {
		return apperror.ErrForbidden
	}
	target, err := s.repo.GetMember(ctx, roomID, targetID)
	if err != nil {
		return err
	}
	if target.Role == RoomRoleOwner {
		return apperror.ErrForbidden
	}
	if actor.Role == RoomRoleModerator && target.Role != RoomRoleMember {
		return apperror.ErrForbidden
	}
	if actor.Role != RoomRoleOwner && actor.Role != RoomRoleModerator {
		return apperror.ErrForbidden
	}
	if err := s.repo.RemoveMember(ctx, roomID, targetID); err != nil {
		return err
	}
	s.revokeMembership(ctx, roomID, targetID)
	s.broadcastMembership(roomID, "removed", targetID, target.Role, actorID)
	return nil
}

// ChangeMemberRole promotes or demotes a non-owner member. Only the owner may
// change roles.
func (s *Service) ChangeMemberRole(ctx context.Context, actorID, roomID, targetID int64, role RoomRole) error {
	actor, err := s.repo.GetMember(ctx, roomID, actorID)
	if err != nil || actor.Role != RoomRoleOwner {
		return apperror.ErrForbidden
	}
	target, err := s.repo.GetMember(ctx, roomID, targetID)
	if err != nil {
		return err
	}
	if target.Role == RoomRoleOwner || (role != RoomRoleMember && role != RoomRoleModerator) {
		return apperror.ErrInvalidRequest
	}
	if target.Role == role {
		return nil
	}
	if err := s.repo.SetMemberRole(ctx, roomID, targetID, role); err != nil {
		return err
	}
	s.broadcastMembership(roomID, "role_changed", targetID, role, actorID)
	return nil
}

// TransferOwnership atomically changes the room owner and preserves the
// previous owner's membership as a moderator.
func (s *Service) TransferOwnership(ctx context.Context, actorID, roomID, targetID int64) error {
	actor, err := s.repo.GetMember(ctx, roomID, actorID)
	if err != nil || actor.Role != RoomRoleOwner {
		return apperror.ErrForbidden
	}
	target, err := s.repo.GetMember(ctx, roomID, targetID)
	if err != nil {
		return err
	}
	if target.Role == RoomRoleOwner {
		return apperror.ErrConflict
	}
	if err := s.repo.TransferOwnership(ctx, roomID, actorID, targetID); err != nil {
		return err
	}
	s.broadcastMembership(roomID, "ownership_transferred", targetID, RoomRoleOwner, actorID)
	return nil
}

func (s *Service) requireMember(ctx context.Context, roomID, userID int64) error {
	if _, err := s.repo.GetRoomByID(ctx, roomID); err != nil {
		return err
	}
	if _, err := s.repo.GetMember(ctx, roomID, userID); err != nil {
		if err == apperror.ErrNotFound {
			return apperror.ErrForbidden
		}
		return err
	}
	return nil
}

func (s *Service) requireManager(ctx context.Context, roomID, userID int64) error {
	member, err := s.repo.GetMember(ctx, roomID, userID)
	if err != nil {
		if err == apperror.ErrNotFound {
			if _, roomErr := s.repo.GetRoomByID(ctx, roomID); roomErr != nil {
				return roomErr
			}
			return apperror.ErrForbidden
		}
		return err
	}
	if member.Role != RoomRoleOwner && member.Role != RoomRoleModerator {
		return apperror.ErrForbidden
	}
	return nil
}

func (s *Service) revokeMembership(ctx context.Context, roomID, userID int64) {
	if err := s.hub.RevokeUserFromRoom(ctx, strconv.FormatInt(roomID, 10), userID); err != nil {
		log.Error().Err(err).Int64("room_id", roomID).Int64("user_id", userID).Msg("chat: durable membership changed but websocket revocation failed")
	}
}

func (s *Service) broadcastMembership(roomID int64, action string, userID int64, role RoomRole, actorID int64) {
	payload := map[string]interface{}{
		"action":        action,
		"user_id":       userID,
		"actor_user_id": actorID,
	}
	if role != "" {
		payload["role"] = role
	}
	if err := s.hub.Broadcast(strconv.FormatInt(roomID, 10), ws.Message{
		Type:    ws.EventMembershipChanged,
		RoomID:  strconv.FormatInt(roomID, 10),
		Payload: payload,
	}); err != nil {
		log.Warn().Err(err).Int64("room_id", roomID).Msg("chat: membership event dropped")
	}
}
