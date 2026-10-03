package notification

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
	"net/http"
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
func (h *Handler) globalGet(c *gin.Context) {
	v, err := h.svc.GlobalPreference(c.Request.Context(), middleware.MustGetUserID(c), nil)
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, v)
}
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
	var id int64
	_, err := fmt.Sscan(v, &id)
	if err != nil || id <= 0 {
		return 0, apperror.ErrInvalidRequest
	}
	return id, nil
}
