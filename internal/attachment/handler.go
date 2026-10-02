package attachment

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// Handler exposes authenticated attachment reservation lifecycle routes.
type Handler struct{ svc *Service }

// NewHandler wraps attachment business rules for HTTP.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterRoutes requires the caller's group to already enforce authentication.
func (h *Handler) RegisterRoutes(private *gin.RouterGroup) {
	private.POST("/rooms/:id/attachments/uploads", h.Initiate)
	private.GET("/rooms/:id/attachments/:attachment_id", h.Get)
	private.POST("/rooms/:id/attachments/:attachment_id/complete", h.Complete)
	private.DELETE("/rooms/:id/attachments/:attachment_id", h.Cancel)
}

func ids(c *gin.Context, withAttachment bool) (int64, int64, error) {
	room, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || room <= 0 {
		return 0, 0, apperror.ErrInvalidRequest
	}
	var id int64
	if withAttachment {
		id, err = strconv.ParseInt(c.Param("attachment_id"), 10, 64)
		if err != nil || id <= 0 {
			return 0, 0, apperror.ErrInvalidRequest
		}
	}
	return room, id, nil
}

// Initiate godoc
// @Summary Reserve attachment upload (unavailable while scanner is pending)
// @Tags attachments
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param id path int true "Room ID"
// @Param Idempotency-Key header string true "16-128 printable ASCII bytes"
// @Param body body UploadRequest true "Unverified metadata"
// @Success 201 {object} UploadTicket
// @Failure 400,401,403,404,409,503 {object} apperror.AppError
// @Router /api/v1/rooms/{id}/attachments/uploads [post]
func (h *Handler) Initiate(c *gin.Context) {
	room, _, err := ids(c, false)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	var req UploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}
	ticket, err := h.svc.Initiate(c.Request.Context(), middleware.MustGetUserID(c), room, c.GetHeader("Idempotency-Key"), &req)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusCreated, ticket)
}

// Get godoc
// @Summary Get declared attachment metadata owned by the current room member
// @Tags attachments
// @Security BearerAuth
// @Produce json
// @Param id path int true "Room ID"
// @Param attachment_id path int true "Attachment ID"
// @Success 200 {object} Metadata
// @Failure 400,401,403,404,500 {object} apperror.AppError
// @Router /api/v1/rooms/{id}/attachments/{attachment_id} [get]
func (h *Handler) Get(c *gin.Context) {
	room, id, err := ids(c, true)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	result, err := h.svc.Get(c.Request.Context(), middleware.MustGetUserID(c), room, id)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Complete godoc
// @Summary Queue scan request; does not verify or publish attachment
// @Tags attachments
// @Security BearerAuth
// @Produce json
// @Param id path int true "Room ID"
// @Param attachment_id path int true "Attachment ID"
// @Success 202 {object} Metadata
// @Failure 400,401,403,404,409,500 {object} apperror.AppError
// @Router /api/v1/rooms/{id}/attachments/{attachment_id}/complete [post]
func (h *Handler) Complete(c *gin.Context) {
	room, id, err := ids(c, true)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	result, err := h.svc.Complete(c.Request.Context(), middleware.MustGetUserID(c), room, id)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}

// Cancel godoc
// @Summary Cancel an owned upload and schedule delayed cleanup
// @Tags attachments
// @Security BearerAuth
// @Param id path int true "Room ID"
// @Param attachment_id path int true "Attachment ID"
// @Success 204
// @Failure 400,401,403,404,500 {object} apperror.AppError
// @Router /api/v1/rooms/{id}/attachments/{attachment_id} [delete]
func (h *Handler) Cancel(c *gin.Context) {
	room, id, err := ids(c, true)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	if err := h.svc.Cancel(c.Request.Context(), middleware.MustGetUserID(c), room, id); err != nil {
		apperror.Respond(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
