package chat

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// EditMessage edits the authenticated author's message.
// @Summary      Edit a message authored by the current member
// @Tags         messages
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id          path  int64              true "Room ID"
// @Param        message_id  path  int64              true "Message ID"
// @Param        body        body  EditMessageRequest true "Content and expected revision"
// @Success      200 {object} Message
// @Failure      400 {object} apperror.AppError
// @Failure      401 {object} apperror.AppError
// @Failure      403 {object} apperror.AppError
// @Failure      404 {object} apperror.AppError
// @Failure      409 {object} apperror.AppError
// @Failure      413 {object} apperror.AppError
// @Failure      500 {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/messages/{message_id} [patch]
func (h *Handler) EditMessage(c *gin.Context) {
	roomID, messageID, err := parseMessagePath(c)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	var req EditMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}
	msg, err := h.svc.EditMessage(c.Request.Context(), middleware.MustGetUserID(c), roomID, messageID, &req)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, msg)
}

// DeleteMessage returns an idempotent tombstone after authorized deletion.
// @Summary      Delete a message as author, owner or moderator
// @Tags         messages
// @Produce      json
// @Security     BearerAuth
// @Param        id          path int64 true "Room ID"
// @Param        message_id  path int64 true "Message ID"
// @Success      200 {object} Message
// @Failure      400 {object} apperror.AppError
// @Failure      401 {object} apperror.AppError
// @Failure      403 {object} apperror.AppError
// @Failure      404 {object} apperror.AppError
// @Failure      500 {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/messages/{message_id} [delete]
func (h *Handler) DeleteMessage(c *gin.Context) {
	roomID, messageID, err := parseMessagePath(c)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	msg, err := h.svc.DeleteMessage(c.Request.Context(), middleware.MustGetUserID(c), roomID, messageID)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, msg)
}

func parseMessagePath(c *gin.Context) (int64, int64, error) {
	roomID, err := parsePathID(c, "id")
	if err != nil {
		return 0, 0, err
	}
	messageID, err := parsePathID(c, "message_id")
	return roomID, messageID, err
}
