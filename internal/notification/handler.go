package notification

import (
	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"net/http"
	"strconv"
)

// Handler exposes the private notification feed and preference resources.
type Handler struct{ svc *Service }

// NewHandler constructs a notification handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterRoutes registers authenticated notification endpoints.
func (h *Handler) RegisterRoutes(private *gin.RouterGroup) {
	users := private.Group("/users/me")
	users.GET("/notifications", h.list)
	users.PUT("/notifications/:id/read", h.read)
	users.GET("/notification-preferences", h.globalGet)
	users.PUT("/notification-preferences", h.globalPut)
	rooms := private.Group("/rooms")
	rooms.GET("/:id/notification-preferences", h.roomGet)
	rooms.PUT("/:id/notification-preferences", h.roomPut)
}

// list handles the authenticated notification resource.
// @Summary List my notification inbox
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Param before query int64 false "Return IDs below this cursor" minimum(1)
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param unread_only query bool false "Return unread notifications only"
// @Success 200 {object} notification.Feed
// @Failure 400 {object} apperror.AppError
// @Failure 401 {object} apperror.AppError
// @Failure 500 {object} apperror.AppError
// @Router /api/v1/users/me/notifications [get]
func (h *Handler) list(c *gin.Context) {
	var q Query
	if err := c.ShouldBindQuery(&q); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}
	v, err := h.svc.List(c.Request.Context(), middleware.MustGetUserID(c), q)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// read handles the authenticated notification resource.
// @Summary Mark my notification read
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "Notification ID" minimum(1)
// @Failure 404 {object} apperror.AppError
// @Success 200 {object} notification.Notification
// @Failure 400 {object} apperror.AppError
// @Failure 401 {object} apperror.AppError
// @Failure 500 {object} apperror.AppError
// @Router /api/v1/users/me/notifications/{id}/read [put]
func (h *Handler) read(c *gin.Context) {
	id, err := parseID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	v, err := h.svc.Read(c.Request.Context(), middleware.MustGetUserID(c), id)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// globalGet handles the authenticated notification resource.
// @Summary Get my global notification preference
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} notification.GlobalPreferences
// @Failure 400 {object} apperror.AppError
// @Failure 401 {object} apperror.AppError
// @Failure 500 {object} apperror.AppError
// @Router /api/v1/users/me/notification-preferences [get]
func (h *Handler) globalGet(c *gin.Context) {
	v, err := h.svc.GlobalPreference(c.Request.Context(), middleware.MustGetUserID(c), nil)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// globalPut handles the authenticated notification resource.
// @Summary Set my global notification preference
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Accept json
// @Param body body notification.GlobalRequest true "Required mentions_enabled boolean; false is accepted"
// @Success 200 {object} notification.GlobalPreferences
// @Failure 400 {object} apperror.AppError
// @Failure 401 {object} apperror.AppError
// @Failure 500 {object} apperror.AppError
// @Router /api/v1/users/me/notification-preferences [put]
func (h *Handler) globalPut(c *gin.Context) {
	var req GlobalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}
	v, err := h.svc.GlobalPreference(c.Request.Context(), middleware.MustGetUserID(c), req.MentionsEnabled)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// roomGet handles the authenticated notification resource.
// @Summary Get my room notification preference
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Param id path int64 true "Room ID" minimum(1)
// @Failure 403 {object} apperror.AppError
// @Failure 404 {object} apperror.AppError
// @Success 200 {object} notification.RoomPreferences
// @Failure 400 {object} apperror.AppError
// @Failure 401 {object} apperror.AppError
// @Failure 500 {object} apperror.AppError
// @Router /api/v1/rooms/{id}/notification-preferences [get]
func (h *Handler) roomGet(c *gin.Context) {
	id, err := parseID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	v, err := h.svc.RoomPreference(c.Request.Context(), middleware.MustGetUserID(c), id, nil)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}

// roomPut handles the authenticated notification resource.
// @Summary Set my room notification preference
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Accept json
// @Param id path int64 true "Room ID" minimum(1)
// @Param body body notification.RoomRequest true "Required muted boolean; false is accepted"
// @Failure 403 {object} apperror.AppError
// @Failure 404 {object} apperror.AppError
// @Success 200 {object} notification.RoomPreferences
// @Failure 400 {object} apperror.AppError
// @Failure 401 {object} apperror.AppError
// @Failure 500 {object} apperror.AppError
// @Router /api/v1/rooms/{id}/notification-preferences [put]
func (h *Handler) roomPut(c *gin.Context) {
	id, err := parseID(c, "id")
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	var req RoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}
	v, err := h.svc.RoomPreference(c.Request.Context(), middleware.MustGetUserID(c), id, req.Muted)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}
func parseID(c *gin.Context, key string) (int64, error) {
	v, ok := c.Params.Get(key)
	if !ok {
		return 0, apperror.ErrInvalidRequest
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return 0, apperror.ErrInvalidRequest
	}
	return id, nil
}
