package ws

import (
	"fmt"
	"net/http"
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
	authorizer RoomAuthorizer

	// done is closed to signal Run() to exit (graceful shutdown).
	done         chan struct{}
	stopped      chan struct{}
	shutdownOnce sync.Once

	// mu protects upgrader (gorilla upgrader is safe, but we wrap for future use).
	mu       sync.Mutex
	upgrader websocket.Upgrader
}

// BroadcastRequest is the payload that domain services send to Hub.Broadcast().
type BroadcastRequest struct {
	RoomID  string
	Message Message
}

// New creates and returns a new Hub. Call hub.Run() in a separate goroutine.
func New() *Hub {
	return &Hub{
		clients:    make(map[string]*Client),
		rooms:      make(map[string]map[string]*Client),
		register:   make(chan *Client, 64),
		registered: make(chan *Client, 64),
		unregister: make(chan *Client, 64),
		inbound:    make(chan inboundMessage, 256),
		broadcast:  make(chan BroadcastRequest, 256),
		authorized: make(chan authorizationResult, 64),
		revoke:     make(chan revocationRequest, 64),
		done:       make(chan struct{}),
		stopped:    make(chan struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			// CheckOrigin should be tightened in production; rely on CORS middleware instead.
			CheckOrigin: func(r *http.Request) bool { return true },
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
		return fmt.Errorf("ws: hub is stopped")
	case h.broadcast <- req:
		return nil
	default:
		return fmt.Errorf("ws: broadcast channel full, message dropped for room %s", roomID)
	}
}

// Upgrade upgrades an HTTP connection to WebSocket, creates a Client, and
// starts its read/write pumps. Auth (JWT) must be verified BEFORE calling this.
func (h *Hub) Upgrade(w http.ResponseWriter, r *http.Request, clientID string, userID int64) error {
	h.mu.Lock()
	conn, err := h.upgrader.Upgrade(w, r, nil)
	h.mu.Unlock()
	if err != nil {
		return fmt.Errorf("ws: upgrade: %w", err)
	}

	client := newClient(clientID, userID, conn, h, r.Context())
	select {
	case h.register <- client:
	case <-h.done:
		client.cancel()
		conn.Close()
		return fmt.Errorf("ws: hub is stopped")
	}

	// Each client needs exactly 2 goroutines: one reader, one writer.
	go client.WritePump()
	go client.ReadPump()

	return nil
}

// ── private helpers ───────────────────────────────────────────────────────────

// handleInbound dispatches a message received from a client.
func (h *Hub) handleInbound(ibm inboundMessage) {
	client, ok := h.clients[ibm.ClientID]
	if !ok {
		return
	}

	if ibm.ErrorCode != "" {
		h.sendProtocolError(client, ibm.Message.RoomID, ibm.ErrorCode, "invalid WebSocket command")
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
