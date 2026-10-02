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
	register chan *Client

	// Inbound unregistration requests — client closed or errored.
	unregister chan *Client

	// inbound carries messages received from any client.
	inbound chan inboundMessage

	// broadcast carries events pushed by domain services (e.g. chat service).
	broadcast chan BroadcastRequest

	// done is closed to signal Run() to exit (graceful shutdown).
	done chan struct{}

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
		unregister: make(chan *Client, 64),
		inbound:    make(chan inboundMessage, 256),
		broadcast:  make(chan BroadcastRequest, 256),
		done:       make(chan struct{}),
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

		// ── Client disconnected ───────────────────────────────────────────────
		case client := <-h.unregister:
			if _, ok := h.clients[client.ID]; ok {
				delete(h.clients, client.ID)
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

		// ── Event pushed by a domain service ─────────────────────────────────
		case req := <-h.broadcast:
			h.fanOut(req.RoomID, req.Message, "")

		// ── Graceful shutdown ─────────────────────────────────────────────────
		case <-h.done:
			// Close all client send channels so WritePump goroutines exit.
			for _, c := range h.clients {
				close(c.send)
			}
			log.Info().Msg("ws: hub stopped")
			return
		}
	}
}

// Shutdown signals the hub event loop to stop. Safe to call from any goroutine.
func (h *Hub) Shutdown() {
	close(h.done)
}

// Broadcast pushes a Message to all clients currently in roomID.
// This is the primary API for domain services (e.g. chat service) to push realtime events.
// It is non-blocking: if the broadcast channel is full the message is dropped and an error is returned.
func (h *Hub) Broadcast(roomID string, msg Message) error {
	req := BroadcastRequest{RoomID: roomID, Message: msg}
	select {
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

	client := newClient(clientID, userID, conn, h)
	h.register <- client

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

	switch ibm.Message.Type {
	case EventJoin:
		// The client wants to subscribe to a room.
		roomID := ibm.Message.RoomID
		if roomID == "" {
			return
		}
		h.addToRoom(roomID, client)
		client.rooms[roomID] = struct{}{}

		// Notify everyone in the room (including the new joiner).
		h.fanOut(roomID, Message{
			Type:   EventJoin,
			RoomID: roomID,
			Payload: map[string]interface{}{
				"user_id":   client.UserID,
				"client_id": client.ID,
			},
		}, "")

		log.Debug().
			Str("room_id", roomID).
			Str("client_id", client.ID).
			Msg("ws: client joined room")

	case EventLeave:
		roomID := ibm.Message.RoomID
		h.removeFromRoom(roomID, client)
		delete(client.rooms, roomID)

	default:
		// For all other event types (e.g. "message"), fan out to the room.
		// In a real app you might validate/persist here via a callback.
		if ibm.Message.RoomID != "" {
			// Exclude the sender to avoid echo (comment out if echo is desired).
			h.fanOut(ibm.Message.RoomID, ibm.Message, ibm.ClientID)
		}
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
