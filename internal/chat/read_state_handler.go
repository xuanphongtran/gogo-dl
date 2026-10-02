package chat

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// RegisterPresenceReadRoutes attaches authenticated presence and read-state routes.
func (h *Handler) RegisterPresenceReadRoutes(private *gin.RouterGroup) {
	rooms := private.Group("/rooms")
	rooms.GET("/:id/presence", h.GetPresence)
	rooms.GET("/:id/read-state", h.GetReadState)
	rooms.PUT("/:id/read-state", h.AdvanceReadState)
}

// GetReadState returns a personal read cursor and unread count.
// @Summary      Get personal room read state
// @Tags         read-state
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int64  true  "Room ID" minimum(1)
// @Success      200  {object} ReadState
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      403  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/read-state [get]
func (h *Handler) GetReadState(c *gin.Context) {
	roomID, err := parsePathID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	state, err := h.svc.GetReadState(c.Request.Context(), middleware.MustGetUserID(c), roomID)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, state)
}

// AdvanceReadState monotonically advances the authenticated member's cursor.
// @Summary      Advance personal room read state
// @Tags         read-state
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path  int64                    true  "Room ID" minimum(1)
// @Param        body  body  AdvanceReadStateRequest  true  "Read acknowledgement"
// @Success      200  {object} ReadState
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      403  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      413  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/read-state [put]
func (h *Handler) AdvanceReadState(c *gin.Context) {
	roomID, err := parsePathID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	var req AdvanceReadStateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}
	state, err := h.svc.AdvanceReadState(c.Request.Context(), middleware.MustGetUserID(c), roomID, &req)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, state)
}
