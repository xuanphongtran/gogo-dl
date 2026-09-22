package user

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// Handler holds the HTTP handlers for the user domain.
type Handler struct {
	svc *Service
}

// NewHandler creates a new user Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes attaches all user routes to the provided router group.
//
// Public  : POST /auth/register, POST /auth/login, POST /auth/refresh
// Private : GET/PATCH/DELETE /users/me  (requires Auth middleware)
func (h *Handler) RegisterRoutes(public, private *gin.RouterGroup) {
	// -- Auth endpoints (no JWT required) ------------------------------------
	auth := public.Group("/auth")
	{
		auth.POST("/register", h.Register)
		auth.POST("/login", h.Login)
		auth.POST("/refresh", h.RefreshTokens)
	}

	// -- Profile endpoints (JWT required) ------------------------------------
	me := private.Group("/users/me")
	{
		me.GET("", h.GetMe)
		me.PATCH("", h.UpdateMe)
		me.DELETE("", h.DeleteMe)
	}
}

// Register godoc
// @Summary  Register a new user
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body body RegisterRequest true "Registration payload"
// @Success  201 {object} middleware.TokenPair
// @Router   /auth/register [post]
func (h *Handler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}

	pair, err := h.svc.Register(c.Request.Context(), &req)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusCreated, pair)
}

// Login godoc
// @Summary  Login and get JWT token pair
// @Tags     auth
func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}

	pair, err := h.svc.Login(c.Request.Context(), &req)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusOK, pair)
}

// RefreshTokens godoc
// @Summary  Exchange a refresh token for a new token pair
// @Tags     auth
func (h *Handler) RefreshTokens(c *gin.Context) {
	var body struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}

	pair, err := h.svc.RefreshTokens(c.Request.Context(), body.RefreshToken)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusOK, pair)
}

// GetMe returns the authenticated user's profile.
func (h *Handler) GetMe(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	profile, err := h.svc.GetProfile(c.Request.Context(), userID)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusOK, profile)
}

// UpdateMe applies profile changes for the authenticated user.
func (h *Handler) UpdateMe(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}

	profile, err := h.svc.UpdateProfile(c.Request.Context(), userID, &req)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusOK, profile)
}

// DeleteMe removes the authenticated user's account.
func (h *Handler) DeleteMe(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	if err := h.svc.DeleteAccount(c.Request.Context(), userID, userID); err != nil {
		apperror.Respond(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
