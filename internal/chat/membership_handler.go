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
	c.JSON(http.StatusOK, gin.H{"members": members})
}

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
	c.JSON(http.StatusOK, gin.H{"invitations": invitations})
}

func (h *Handler) AcceptInvitation(c *gin.Context) {
	h.respondInvitation(c, InvitationAccepted)
}

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
