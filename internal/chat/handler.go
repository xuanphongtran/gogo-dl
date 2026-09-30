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
		rooms.PATCH("/:id/messages/:message_id", h.EditMessage)
		rooms.DELETE("/:id/messages/:message_id", h.DeleteMessage)
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
// @Summary      List rooms visible to the current user
// @Tags         rooms
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object} RoomsResponse
// @Failure      401  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms [get]
func (h *Handler) ListRooms(c *gin.Context) {
	rooms, err := h.svc.ListRoomsForUser(c.Request.Context(), middleware.MustGetUserID(c))
	if err != nil {
		apperror.Respond(c, err)
		return
	}
	c.JSON(http.StatusOK, RoomsResponse{Rooms: rooms})
}

// CreateRoom creates a new room.
// @Summary      Create a room
// @Tags         rooms
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  CreateRoomRequest  true  "Room payload"
// @Success      201   {object} Room
// @Failure      400   {object} apperror.AppError
// @Failure      401   {object} apperror.AppError
// @Failure      500   {object} apperror.AppError
// @Router       /api/v1/rooms [post]
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
// @Summary      Get a room visible to the current user
// @Tags         rooms
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int64  true  "Room ID"
// @Success      200  {object} Room
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id} [get]
func (h *Handler) GetRoom(c *gin.Context) {
	roomID, err := parseRoomID(c)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	room, err := h.svc.GetRoomForUser(c.Request.Context(), middleware.MustGetUserID(c), roomID)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusOK, room)
}

// JoinRoom adds the authenticated user to a room.
// @Summary      Join a public room
// @Tags         rooms
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int64  true  "Room ID"
// @Success      200  {object} JoinResponse
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      403  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/join [post]
func (h *Handler) JoinRoom(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	roomID, err := parseRoomID(c)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	if err := h.svc.JoinPublicRoom(c.Request.Context(), roomID, userID); err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusOK, JoinResponse{Message: "joined"})
}

// ListMessages returns paginated message history for a room.
// @Summary      List room messages
// @Tags         messages
// @Produce      json
// @Security     BearerAuth
// @Param        id      path   int64  true   "Room ID"
// @Param        limit   query  int    false  "Page size"  minimum(1) maximum(100) default(50)
// @Param        before  query  int64  false  "Return messages with IDs below this cursor" minimum(1)
// @Success      200  {object} MessagesResponse
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      403  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/messages [get]
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

	msgs, err := h.svc.ListMessagesForUser(c.Request.Context(), middleware.MustGetUserID(c), roomID, &q)
	if err != nil {
		apperror.Respond(c, err)
		return
	}

	c.JSON(http.StatusOK, MessagesResponse{Messages: msgs})
}

// SendMessage persists a message and triggers a realtime broadcast via the hub.
// @Summary      Send a message to a room
// @Tags         messages
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path  int64              true  "Room ID"
// @Param        body  body  SendMessageRequest true  "Message payload"
// @Success      201  {object} Message
// @Failure      400  {object} apperror.AppError
// @Failure      401  {object} apperror.AppError
// @Failure      403  {object} apperror.AppError
// @Failure      404  {object} apperror.AppError
// @Failure      413  {object} apperror.AppError
// @Failure      500  {object} apperror.AppError
// @Router       /api/v1/rooms/{id}/messages [post]
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

	msg, err := h.svc.SendMessage(c.Request.Context(), userID, roomID, &req, "")
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
