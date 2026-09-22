package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

// Hub maintains the set of active clients and their room memberships.
// It is the single source of truth for all WebSocket state.
//
// Architecture overview:
//
//	┌──────────────────────────────────────────────────────┐
//	│                         Hub                          │
//	│                                                      │
//	│  rooms: map[roomID] → set of *Client                 │
//	│  clients: map[clientID] → *Client                    │
//	│                                                      │
//	│  ← register    (new client connects)                 │
//	│  ← unregister  (client disconnects)                  │
//	│  ← inbound     (client sends a message)              │
//	│  ← broadcast   (domain service pushes an event)      │
//	└──────────────────────────────────────────────────────┘
//
// All mutations to internal maps happen inside the Run() goroutine — no mutex needed.
type Options struct {
	AllowedOrigins        []string
	AllowMissingOrigin    bool
	MaxMessageBytes       int64
	MaxConnections        int
	MaxConnectionsPerUser int
}

type admissionRequest struct {
	userID   int64
	response chan error
}

type releaseRequest struct {
	userID   int64
	response chan struct{}
}

var errHubStopped = errors.New("ws: hub is stopped")

type Hub struct {
	// clients maps clientID → *Client for O(1) lookup.
	clients map[string]*Client

	// rooms maps roomID → set of *Client.
	// Using a map of maps avoids a per-room mutex while keeping room fan-out O(n members).
	rooms map[string]map[string]*Client

	// Inbound registration requests from new connections.
	register   chan *Client
	registered chan *Client

	// Inbound unregistration requests — client closed or errored.
	unregister chan *Client

	// inbound carries messages received from any client.
	inbound chan inboundMessage

	// broadcast carries events pushed by domain services (e.g. chat service).
	broadcast chan BroadcastRequest

	// authorized carries results of room authorization work performed outside Run.
	authorized chan authorizationResult
	// revoke carries durable membership revocations into the event loop.
	revoke     chan revocationRequest
	admit      chan admissionRequest
	release    chan releaseRequest
	authorizer RoomAuthorizer

	// done is closed to signal Run() to exit (graceful shutdown).
	done         chan struct{}
	stopped      chan struct{}
	shutdownOnce sync.Once

	// mu protects upgrader (gorilla upgrader is safe, but we wrap for future use).
	mu                    sync.Mutex
	upgrader              websocket.Upgrader
	upgraderOrigins       []string
	missingOriginAllowed  bool
	maxMessageBytes       int64
	maxConnections        int
	maxConnectionsPerUser int
	connectionCount       int
	connectionsByUser     map[int64]int
}

// BroadcastRequest is the payload that domain services send to Hub.Broadcast().
type BroadcastRequest struct {
	RoomID  string
	Message Message
}

// New creates and returns a new Hub. Call hub.Run() in a separate goroutine.
func New(options ...Options) *Hub {
	opts := Options{
		AllowedOrigins:        []string{"http://localhost:3000", "http://localhost:5173"},
		AllowMissingOrigin:    true,
		MaxMessageBytes:       maxMessageSize,
		MaxConnections:        1000,
		MaxConnectionsPerUser: 5,
	}
	if len(options) > 0 {
		if options[0].AllowedOrigins != nil {
			opts.AllowedOrigins = options[0].AllowedOrigins
		}
		opts.AllowMissingOrigin = options[0].AllowMissingOrigin
		if options[0].MaxMessageBytes > 0 {
			opts.MaxMessageBytes = options[0].MaxMessageBytes
		}
		if options[0].MaxConnections > 0 {
			opts.MaxConnections = options[0].MaxConnections
		}
		if options[0].MaxConnectionsPerUser > 0 {
			opts.MaxConnectionsPerUser = options[0].MaxConnectionsPerUser
		}
	}
	return &Hub{
		clients:               make(map[string]*Client),
		rooms:                 make(map[string]map[string]*Client),
		register:              make(chan *Client, 64),
		registered:            make(chan *Client, 64),
		unregister:            make(chan *Client, 64),
		inbound:               make(chan inboundMessage, 256),
		broadcast:             make(chan BroadcastRequest, 256),
		authorized:            make(chan authorizationResult, 64),
		revoke:                make(chan revocationRequest, 64),
		admit:                 make(chan admissionRequest, 64),
		release:               make(chan releaseRequest, 64),
		done:                  make(chan struct{}),
		stopped:               make(chan struct{}),
		maxMessageBytes:       opts.MaxMessageBytes,
		maxConnections:        opts.MaxConnections,
		maxConnectionsPerUser: opts.MaxConnectionsPerUser,
		connectionsByUser:     make(map[int64]int),
		upgraderOrigins:       opts.AllowedOrigins,
		missingOriginAllowed:  opts.AllowMissingOrigin,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(r *http.Request) bool { return originAllowed(r, opts.AllowedOrigins, opts.AllowMissingOrigin) },
		},
	}
}

// Run is the event loop of the Hub. It MUST be called in its own goroutine.
//
// All state mutations (maps) are performed here — no locking required.
func (h *Hub) Run() {
	defer close(h.stopped)
	log.Info().Msg("ws: hub started")
	for {
		select {

		case req := <-h.admit:
			req.response <- h.admitUser(req.userID)

		case req := <-h.release:
			h.releaseUser(req.userID)
			if req.response != nil {
				req.response <- struct{}{}
			}

		// ── New client connected ──────────────────────────────────────────────
		case client := <-h.register:
			h.clients[client.ID] = client
			log.Info().
				Str("client_id", client.ID).
				Int64("user_id", client.UserID).
				Msg("ws: client registered")
			if h.registered != nil {
				select {
				case h.registered <- client:
				default:
				}
			}

		// ── Client disconnected ───────────────────────────────────────────────
		case client := <-h.unregister:
			if _, ok := h.clients[client.ID]; ok {
				delete(h.clients, client.ID)
				if client.admitted {
					h.releaseUser(client.UserID)
				}
				client.cancel()
				close(client.send)

				// Remove the client from every room it was in.
				for roomID := range client.rooms {
					h.removeFromRoom(roomID, client)
					// Notify remaining members that this user left.
					h.fanOut(roomID, Message{
						Type:   EventLeave,
						RoomID: roomID,
						Payload: map[string]interface{}{
							"user_id":   client.UserID,
							"client_id": client.ID,
						},
					}, "")
				}

				log.Info().
					Str("client_id", client.ID).
					Int64("user_id", client.UserID).
					Msg("ws: client unregistered")
			}

		// ── Message received from a client ────────────────────────────────────
		case ibm := <-h.inbound:
			h.handleInbound(ibm)
			if ibm.handled != nil {
				close(ibm.handled)
			}

		case result := <-h.authorized:
			h.handleAuthorization(result)

		case req := <-h.revoke:
			h.handleRevocation(req)

		// ── Event pushed by a domain service ─────────────────────────────────
		case req := <-h.broadcast:
			h.fanOut(req.RoomID, req.Message, "")

		// ── Graceful shutdown ─────────────────────────────────────────────────
		case <-h.done:
			// Close all client send channels so WritePump goroutines exit.
			for _, c := range h.clients {
				c.cancel()
				close(c.send)
			}
			h.connectionCount = 0
			h.connectionsByUser = make(map[int64]int)
			log.Info().Msg("ws: hub stopped")
			return
		}
	}
}

// Shutdown signals the hub event loop to stop. Safe to call from any goroutine.
func (h *Hub) Shutdown() {
	h.shutdownOnce.Do(func() { close(h.done) })
}

// Broadcast pushes a Message to all clients currently in roomID.
// This is the primary API for domain services (e.g. chat service) to push realtime events.
// It is non-blocking: if the broadcast channel is full the message is dropped and an error is returned.
func (h *Hub) Broadcast(roomID string, msg Message) error {
	req := BroadcastRequest{RoomID: roomID, Message: msg}
	select {
	case <-h.done:
		return errHubStopped
	case h.broadcast <- req:
		return nil
	default:
		return fmt.Errorf("ws: broadcast channel full, message dropped for room %s", roomID)
	}
}

// Upgrade upgrades an HTTP connection to WebSocket, creates a Client, and
// starts its read/write pumps. Auth (JWT) must be verified BEFORE calling this.
func (h *Hub) Upgrade(w http.ResponseWriter, r *http.Request, clientID string, userID int64) error {
	if !originAllowed(r, h.allowedOrigins(), h.allowMissingOrigin()) {
		writeJSONError(w, http.StatusForbidden, "origin forbidden")
		return fmt.Errorf("ws: origin forbidden")
	}
	if err := h.admitConnection(r.Context(), userID); err != nil {
		status := http.StatusTooManyRequests
		message := "rate limit exceeded"
		if errors.Is(err, errConnectionLimit) || errors.Is(err, errHubStopped) {
			status = http.StatusServiceUnavailable
			message = "too many connections"
		}
		if errors.Is(err, errHubStopped) {
			message = "service unavailable"
		}
		writeJSONError(w, status, message)
		return err
	}

	h.mu.Lock()
	conn, err := h.upgrader.Upgrade(w, r, nil)
	h.mu.Unlock()
	if err != nil {
		h.releaseConnection(context.WithoutCancel(r.Context()), userID)
		return fmt.Errorf("ws: upgrade: %w", err)
	}

	client := newClient(clientID, userID, conn, h, r.Context(), h.maxMessageBytes)
	client.admitted = true
	select {
	case h.register <- client:
	case <-h.done:
		client.cancel()
		conn.Close()
		h.releaseConnection(context.WithoutCancel(r.Context()), userID)
		return errHubStopped
	}

	// Each client needs exactly 2 goroutines: one reader, one writer.
	go client.WritePump()
	go client.ReadPump()

	return nil
}

var errConnectionLimit = errors.New("ws: connection limit exceeded")

func (h *Hub) admitConnection(ctx context.Context, userID int64) error {
	if ctx == nil {
		ctx = context.TODO()
	}
	select {
	case <-h.done:
		return errHubStopped
	default:
	}
	response := make(chan error, 1)
	request := admissionRequest{userID: userID, response: response}
	select {
	case h.admit <- request:
	case <-h.done:
		return errHubStopped
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-response:
		if ctx.Err() != nil {
			if err == nil {
				h.releaseConnection(context.WithoutCancel(ctx), userID)
			}
			return ctx.Err()
		}
		return err
	case <-h.done:
		return errHubStopped
	case <-ctx.Done():
		go func() {
			select {
			case err := <-response:
				if err == nil {
					h.releaseConnection(context.WithoutCancel(ctx), userID)
				}
			case <-h.done:
			}
		}()
		return ctx.Err()
	}
}

func (h *Hub) releaseConnection(ctx context.Context, userID int64) {
	if ctx == nil {
		ctx = context.TODO()
	}
	response := make(chan struct{}, 1)
	select {
	case h.release <- releaseRequest{userID: userID, response: response}:
		select {
		case <-response:
		case <-h.done:
		case <-ctx.Done():
		}
	case <-h.done:
	case <-ctx.Done():
	}
}

func (h *Hub) admitUser(userID int64) error {
	if h.connectionCount >= h.maxConnections || h.connectionsByUser[userID] >= h.maxConnectionsPerUser {
		return errConnectionLimit
	}
	h.connectionCount++
	h.connectionsByUser[userID]++
	return nil
}

func (h *Hub) releaseUser(userID int64) {
	if h.connectionCount > 0 {
		h.connectionCount--
	}
	if count := h.connectionsByUser[userID]; count <= 1 {
		delete(h.connectionsByUser, userID)
	} else {
		h.connectionsByUser[userID] = count - 1
	}
}

func (h *Hub) allowedOrigins() []string { return h.upgraderOrigins }
func (h *Hub) allowMissingOrigin() bool { return h.missingOriginAllowed }

func originAllowed(r *http.Request, configured []string, allowMissing bool) bool {
	if r == nil {
		return false
	}
	raw := r.Header.Get("Origin")
	if raw == "" {
		return allowMissing
	}
	normalized, ok := normalizeOrigin(raw)
	if !ok {
		return false
	}
	for _, candidate := range configured {
		if origin, valid := normalizeOrigin(candidate); valid && origin == normalized {
			return true
		}
	}
	return false
}

func normalizeOrigin(raw string) (string, bool) {
	if strings.TrimSpace(raw) != raw || raw == "" || strings.Contains(raw, ",") {
		return "", false
	}
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.User != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", false
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" {
		host += ":" + port
	}
	return strings.ToLower(u.Scheme) + "://" + host, true
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// ── private helpers ───────────────────────────────────────────────────────────

// handleInbound dispatches a message received from a client.
func (h *Hub) handleInbound(ibm inboundMessage) {
	client, ok := h.clients[ibm.ClientID]
	if !ok {
		return
	}

	if ibm.ErrorCode != "" {
		if ibm.ErrorCode == "payload_too_large" && ibm.closed != nil {
			h.sendProtocolErrorAndClose(client, ibm.Message.RoomID, ibm.ErrorCode, "invalid WebSocket command", ibm.closed)
		} else {
			h.sendProtocolError(client, ibm.Message.RoomID, ibm.ErrorCode, "invalid WebSocket command")
		}
		return
	}

	switch ibm.Message.Type {
	case EventJoin:
		if ibm.Message.Payload != nil {
			h.sendProtocolError(client, ibm.Message.RoomID, "invalid_command", "join does not accept a payload")
			return
		}
		h.requestJoin(client, ibm.Message.RoomID)

	case EventLeave:
		if ibm.Message.Payload != nil {
			h.sendProtocolError(client, ibm.Message.RoomID, "invalid_command", "leave does not accept a payload")
			return
		}
		_, roomID, err := parseRoomID(ibm.Message.RoomID)
		if err != nil {
			h.sendProtocolError(client, ibm.Message.RoomID, "invalid_room_id", "invalid room id")
			return
		}
		if _, joined := client.rooms[roomID]; !joined {
			return
		}
		h.removeFromRoom(roomID, client)
		delete(client.rooms, roomID)
		h.fanOut(roomID, Message{
			Type:   EventLeave,
			RoomID: roomID,
			Payload: map[string]interface{}{
				"user_id":   client.UserID,
				"client_id": client.ID,
			},
		}, "")

	case EventError:
		h.sendProtocolError(client, ibm.Message.RoomID, "unsupported_event", "client error events are not accepted")

	default:
		h.sendProtocolError(client, ibm.Message.RoomID, "unsupported_event", "unsupported WebSocket event")
	}
}

// addToRoom ensures the room map exists and inserts the client.
func (h *Hub) addToRoom(roomID string, c *Client) {
	if _, ok := h.rooms[roomID]; !ok {
		h.rooms[roomID] = make(map[string]*Client)
	}
	h.rooms[roomID][c.ID] = c
}

// removeFromRoom removes a client from a room and cleans up the room map if empty.
func (h *Hub) removeFromRoom(roomID string, c *Client) {
	if members, ok := h.rooms[roomID]; ok {
		delete(members, c.ID)
		if len(members) == 0 {
			delete(h.rooms, roomID)
		}
	}
}

// fanOut sends msg to every client in roomID, optionally excluding one client (excludeID).
func (h *Hub) fanOut(roomID string, msg Message, excludeID string) {
	members, ok := h.rooms[roomID]
	if !ok {
		return
	}
	for id, client := range members {
		if id == excludeID {
			continue
		}
		client.sendJSON(msg)
	}
}
