package ws

import (
	"encoding/json"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

const (
	// writeWait is the time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// pongWait is the time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// pingPeriod should be less than pongWait to keep the connection alive.
	pingPeriod = (pongWait * 9) / 10

	// maxMessageSize is the max bytes the client is allowed to send.
	maxMessageSize = 4096
)

// Client represents a single WebSocket connection managed by the Hub.
// It is the bridge between the raw *websocket.Conn and the Hub's channels.
type Client struct {
	// Unique ID for this connection (could be a UUID generated at upgrade time).
	ID string

	// Authenticated user who owns this connection.
	UserID int64

	// The set of rooms this client has joined.
	// Key: roomID. Value: struct{} (set semantics).
	rooms map[string]struct{}

	// The underlying WebSocket connection.
	conn *websocket.Conn

	// Hub sends outbound messages to this client via send.
	// Buffered so the Hub never blocks on a slow client.
	send chan []byte

	// Reference to the parent hub so the client can unregister itself.
	hub *Hub
}

// newClient creates a Client and registers it with the hub.
func newClient(id string, userID int64, conn *websocket.Conn, hub *Hub) *Client {
	return &Client{
		ID:     id,
		UserID: userID,
		rooms:  make(map[string]struct{}),
		conn:   conn,
		send:   make(chan []byte, 256),
		hub:    hub,
	}
}

// ReadPump pumps inbound messages from the WebSocket connection to the Hub.
//
// The application runs ReadPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) ReadPump() {
	defer func() {
		// When ReadPump exits (client disconnect / error), unregister & close.
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	// Reset the deadline every time a pong is received.
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, rawBytes, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
			) {
				log.Error().Err(err).Str("client_id", c.ID).Msg("ws: unexpected close")
			}
			break
		}

		var msg Message
		if err := json.Unmarshal(rawBytes, &msg); err != nil {
			log.Warn().Err(err).Str("client_id", c.ID).Msg("ws: invalid message format")
			continue
		}

		// Forward to hub for dispatch.
		c.hub.inbound <- inboundMessage{
			ClientID: c.ID,
			UserID:   c.UserID,
			Message:  msg,
		}
	}
}

// WritePump pumps outbound messages from the Hub's send channel to the WebSocket.
//
// A goroutine running WritePump is started for each connection. The application
// ensures that there is at most one writer per connection by executing all writes
// from this goroutine.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case data, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel — send a close message.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(data)

			// Drain any queued messages into the same WebSocket frame batch.
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write([]byte("\n"))
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			// Send a ping to keep the connection alive.
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// send a JSON-encoded Message to this client (non-blocking on hub side).
func (c *Client) sendJSON(msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Msg("ws: marshal error")
		return
	}
	select {
	case c.send <- data:
	default:
		// Client is too slow or disconnected — drop the message.
		log.Warn().Str("client_id", c.ID).Msg("ws: send buffer full, dropping message")
	}
}
