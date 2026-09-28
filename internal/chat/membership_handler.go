package chat

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// RegisterMembershipRoutes attaches Phase 05 membership and invitation routes.
func (h *Handler) RegisterMembershipRoutes(private *gin.RouterGroup) {
	rooms := private.Group("/rooms")
	rooms.GET("/:id/members", h.ListMembers)
	rooms.DELETE("/:id/membership", h.LeaveRoom)
	rooms.DELETE("/:id/members/:user_id", h.RemoveMember)
	rooms.PATCH("/:id/members/:user_id", h.ChangeMemberRole)
	rooms.POST("/:id/ownership", h.TransferOwnership)
	rooms.POST("/:id/invitations", h.Invite)

	private.GET("/users/me/invitations", h.ListInvitations)
	private.POST("/invitations/:id/accept", h.AcceptInvitation)
	private.POST("/invitations/:id/decline", h.DeclineInvitation)
}

// ListMembers returns the members of a room.
// @Summary      List room members
// @Tags         memberships
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int64  true  "Room ID"
// @Success      200  {object} MembersResponse
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      403  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/members [get]
func (h *Handler) ListMembers(c *gin.Context) {
	roomID, err := parsePathID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	members, err := h.svc.ListMembers(c.Request.Context(), middleware.MustGetUserID(c), roomID)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, MembersResponse{Members: members})
}

// LeaveRoom removes the current user's membership from a room.
// @Summary      Leave a room
// @Tags         memberships
// @Security     BearerAuth
// @Param        id  path  int64  true  "Room ID"
// @Success      204  "Membership removed"
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      409  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/membership [delete]
func (h *Handler) LeaveRoom(c *gin.Context) {
	roomID, err := parsePathID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	if err := h.svc.LeaveRoom(c.Request.Context(), middleware.MustGetUserID(c), roomID); err != nil {
		apperror.Respond(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// RemoveMember removes a regular member from a room.
// @Summary      Remove a room member
// @Tags         memberships
// @Security     BearerAuth
// @Param        id       path  int64  true  "Room ID"
// @Param        user_id  path  int64  true  "Target user ID"
// @Success      204  "Member removed"
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      403  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/members/{user_id} [delete]
func (h *Handler) RemoveMember(c *gin.Context) {
	roomID, err := parsePathID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	targetID, err := parsePathID(c, "user_id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	if err := h.svc.RemoveMemberAs(c.Request.Context(), middleware.MustGetUserID(c), roomID, targetID); err != nil {
		apperror.Respond(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ChangeMemberRole promotes or demotes a non-owner member.
// @Summary      Change a member role
// @Tags         memberships
// @Accept       json
// @Security     BearerAuth
// @Param        id       path  int64                   true  "Room ID"
// @Param        user_id  path  int64                   true  "Target user ID"
// @Param        body     body  ChangeMemberRoleRequest true  "Role payload"
// @Success      204  "Role changed"
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      403  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/members/{user_id} [patch]
func (h *Handler) ChangeMemberRole(c *gin.Context) {
	roomID, err := parsePathID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	targetID, err := parsePathID(c, "user_id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	var req ChangeMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}
	if err := h.svc.ChangeMemberRole(c.Request.Context(), middleware.MustGetUserID(c), roomID, targetID, req.Role); err != nil {
		apperror.Respond(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// TransferOwnership transfers ownership to an existing room member.
// @Summary      Transfer room ownership
// @Tags         memberships
// @Accept       json
// @Security     BearerAuth
// @Param        id    path  int64                    true  "Room ID"
// @Param        body  body  TransferOwnershipRequest true  "Ownership payload"
// @Success      204  "Ownership transferred"
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      403  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      409  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/ownership [post]
func (h *Handler) TransferOwnership(c *gin.Context) {
	roomID, err := parsePathID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	var req TransferOwnershipRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}
	if err := h.svc.TransferOwnership(c.Request.Context(), middleware.MustGetUserID(c), roomID, req.UserID); err != nil {
		apperror.Respond(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Invite creates or retries an invitation for a room user.
// @Summary      Invite a user to a room
// @Tags         invitations
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path  int64            true  "Room ID"
// @Param        body  body  InviteUserRequest true  "Invitation payload"
// @Success      200  {object} Invitation
// @Success      201  {object} Invitation
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      403  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/invitations [post]
func (h *Handler) Invite(c *gin.Context) {
	roomID, err := parsePathID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	var req InviteUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}
	invitation, created, err := h.svc.Invite(c.Request.Context(), middleware.MustGetUserID(c), roomID, req.UserID)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, invitation)
}

// ListInvitations returns invitations addressed to the current user.
// @Summary      List my invitations
// @Tags         invitations
// @Produce      json
// @Security     BearerAuth
// @Param        status  query  string  false  "Invitation status filter"  Enums(pending,accepted,declined,all) default(pending)
// @Param        limit   query  int     false  "Page size"                minimum(1) maximum(100) default(50)
// @Success      200  {object} InvitationsResponse
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/users/me/invitations [get]
func (h *Handler) ListInvitations(c *gin.Context) {
	var query ListInvitationsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}
	invitations, err := h.svc.ListInvitations(c.Request.Context(), middleware.MustGetUserID(c), query.Status, query.Limit)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, InvitationsResponse{Invitations: invitations})
}

// AcceptInvitation accepts an invitation addressed to the current user.
// @Summary      Accept an invitation
// @Tags         invitations
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int64  true  "Invitation ID"
// @Success      200  {object} Invitation
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      409  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/invitations/{id}/accept [post]
func (h *Handler) AcceptInvitation(c *gin.Context) {
	h.respondInvitation(c, InvitationAccepted)
}

// DeclineInvitation declines an invitation addressed to the current user.
// @Summary      Decline an invitation
// @Tags         invitations
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int64  true  "Invitation ID"
// @Success      200  {object} Invitation
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      409  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/invitations/{id}/decline [post]
func (h *Handler) DeclineInvitation(c *gin.Context) {
	h.respondInvitation(c, InvitationDeclined)
}

func (h *Handler) respondInvitation(c *gin.Context, status InvitationStatus) {
	invitationID, err := parsePathID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	invit, err := h.svc.RespondInvitation(c.Request.Context(), middleware.MustGetUserID(c), invitationID, status)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, invit)
}

func parsePathID(c *gin.Context, name string) (int64, error) {
	raw := c.Param(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, apperror.ErrInvalidRequest
	}
	return id, nil
}
