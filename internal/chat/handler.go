package chat

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xuanphongtran/gogo-dl/internal/middleware"
	"github.com/xuanphongtran/gogo-dl/internal/ws"
	"github.com/xuanphongtran/gogo-dl/pkg/apperror"
)

// Handler holds the HTTP (and WebSocket upgrade) handlers for the chat domain.
type Handler struct {
	svc *Service
	hub *ws.Hub
}

// NewHandler creates a new chat Handler.
func NewHandler(svc *Service, hub *ws.Hub) *Handler {
	return &Handler{svc: svc, hub: hub}
}

// RegisterRoutes attaches all chat routes to the provided router groups.
// All routes in private require a valid JWT (Auth middleware applied by httpserver).
func (h *Handler) RegisterRoutes(private *gin.RouterGroup, wsMiddleware ...gin.HandlerFunc) {
	rooms := private.Group("/rooms")
	{
		rooms.GET("", h.ListRooms)
		rooms.POST("", h.CreateRoom)
		rooms.GET("/:id", h.GetRoom)
		rooms.POST("/:id/join", h.JoinRoom)
		rooms.GET("/:id/messages", h.ListMessages)
		rooms.POST("/:id/messages", h.SendMessage)
	}

	// Keep the optional middleware argument for callers that register the
	// WebSocket route on the authenticated group. The composition root uses
	// RegisterWebSocketRoute so pre-auth admission can run before Auth.
	if len(wsMiddleware) > 0 {
		h.RegisterWebSocketRoute(private, wsMiddleware...)
	}
}

// RegisterWebSocketRoute attaches the authenticated WebSocket upgrade route to
// the provided group. Callers can place pre-auth middleware on the group and
// post-auth middleware in wsMiddleware.
func (h *Handler) RegisterWebSocketRoute(group *gin.RouterGroup, wsMiddleware ...gin.HandlerFunc) {
	wsRoutes := group.Group("", wsMiddleware...)
	wsRoutes.GET("/ws", h.ServeWS)
}

// ListRooms returns all chat rooms.
func (h *Handler) ListRooms(c *gin.Context) {
	rooms, err := h.svc.ListRooms(c.Request.Context())
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"rooms": rooms})
}

// CreateRoom creates a new room.
func (h *Handler) CreateRoom(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req CreateRoomRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}

	room, err := h.svc.CreateRoom(c.Request.Context(), userID, &req)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusCreated, room)
}

// GetRoom fetches a single room by ID.
func (h *Handler) GetRoom(c *gin.Context) {
	roomID, err := parseRoomID(c)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	room, err := h.svc.GetRoom(c.Request.Context(), roomID)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusOK, room)
}

// JoinRoom adds the authenticated user to a room.
func (h *Handler) JoinRoom(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	roomID, err := parseRoomID(c)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	if err := h.svc.JoinRoom(c.Request.Context(), roomID, userID); err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "joined"})
}

// ListMessages returns paginated message history for a room.
func (h *Handler) ListMessages(c *gin.Context) {
	roomID, err := parseRoomID(c)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	var q ListMessagesQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}

	msgs, err := h.svc.ListMessages(c.Request.Context(), roomID, &q)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"messages": msgs})
}

// SendMessage persists a message and triggers a realtime broadcast via the hub.
func (h *Handler) SendMessage(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	roomID, err := parseRoomID(c)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apperror.Respond(c, middleware.BindingError(err))
		return
	}

	// Pull username from token claims (avoids an extra DB round-trip for the username).
	username := fmt.Sprintf("user_%d", userID)
	if rawClaims, exists := c.Get(middleware.ContextKeyClaims); exists {
		if mc, ok := rawClaims.(*middleware.Claims); ok {
			username = fmt.Sprintf("user_%d", mc.UserID)
		}
	}

	msg, err := h.svc.SendMessage(c.Request.Context(), userID, roomID, &req, username)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusCreated, msg)
}

// ServeWS upgrades the HTTP connection to WebSocket.
// Auth middleware must have already validated the token before this handler runs.
//
// Client connection flow:
//  1. Client connects: GET /api/ws?token=<access_token>
//  2. Auth middleware validates the token and sets userID in gin.Context.
//  3. Hub.Upgrade() creates the Client and starts ReadPump/WritePump goroutines.
//  4. Client sends {"type":"join","room_id":"<id>"} to subscribe to a room.
//  5. Client receives broadcast events whenever someone calls hub.Broadcast(roomID, msg).
func (h *Handler) ServeWS(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	// Unique client ID: userID + nanosecond timestamp is sufficient for single-node.
	// For multi-node deployments, use a UUID library instead.
	clientID := fmt.Sprintf("%d-%d", userID, time.Now().UnixNano())

	if err := h.hub.Upgrade(c.Writer, c.Request, clientID, userID); err != nil {
		// Upgrade writes protocol-specific HTTP errors before the handshake fails.
		if !c.Writer.Written() {
			apperror.Respond(c, apperror.New(http.StatusInternalServerError, "ws upgrade failed"))
		}
		return
	}
	// After Upgrade() the connection is owned by the hub's goroutines.
	// Do NOT write to c.Writer after this point.
}

// ── helpers ───────────────────────────────────────────────────────────────────

func parseRoomID(c *gin.Context) (int64, error) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return 0, apperror.ErrInvalidRequest
	}
	return id, nil
}
