package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	send chan outboundMessage

	// Reference to the parent hub so the client can unregister itself.
	hub *Hub

	ctx             context.Context
	cancel          context.CancelFunc
	admitted        bool
	maxMessageBytes int64
}

// newClient creates a Client and registers it with the hub.
func newClient(id string, userID int64, conn *websocket.Conn, hub *Hub, parentCtx context.Context, maxMessageBytes ...int64) *Client {
	readLimit := int64(maxMessageSize)
	if len(maxMessageBytes) > 0 {
		if maxMessageBytes[0] > 0 {
			readLimit = maxMessageBytes[0]
		}
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parentCtx))
	return &Client{
		ID:              id,
		UserID:          userID,
		rooms:           make(map[string]struct{}),
		conn:            conn,
		send:            make(chan outboundMessage, 256),
		hub:             hub,
		ctx:             ctx,
		cancel:          cancel,
		maxMessageBytes: readLimit,
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
		select {
		case c.hub.unregister <- c:
		case <-c.hub.done:
		}
		c.conn.Close()
		if c.cancel != nil {
			c.cancel()
		}
	}()

	// Gorilla sends close code 1009 immediately when its read limit is exceeded.
	// Keep its internal limit disabled and enforce the application limit with a
	// bounded read so the Hub can deliver payload_too_large first.
	c.conn.SetReadLimit(0)
	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		return
	}
	// Reset the deadline every time a pong is received.
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, reader, err := c.conn.NextReader()
		if err != nil {
			if errors.Is(err, websocket.ErrReadLimit) {
				c.reportPayloadTooLarge()
			}
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
			) {
				log.Error().Err(err).Str("client_id", c.ID).Msg("ws: unexpected close")
			}
			break
		}

		readLimit := c.maxMessageBytes + 1
		if readLimit <= c.maxMessageBytes {
			readLimit = c.maxMessageBytes
		}
		rawBytes, err := io.ReadAll(io.LimitReader(reader, readLimit))
		if err != nil {
			break
		}
		if int64(len(rawBytes)) > c.maxMessageBytes {
			c.reportPayloadTooLarge()
			break
		}

		var msg Message
		decoder := json.NewDecoder(bytes.NewReader(rawBytes))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&msg); err != nil {
			log.Warn().Err(err).Str("client_id", c.ID).Msg("ws: invalid message format")
			c.enqueueInbound(inboundMessage{ClientID: c.ID, UserID: c.UserID, ErrorCode: "invalid_command"})
			continue
		}
		var extra interface{}
		if err := decoder.Decode(&extra); err != io.EOF {
			log.Warn().Str("client_id", c.ID).Msg("ws: multiple JSON messages in frame")
			c.enqueueInbound(inboundMessage{ClientID: c.ID, UserID: c.UserID, ErrorCode: "invalid_command"})
			continue
		}

		// Forward to hub for dispatch.
		c.enqueueInbound(inboundMessage{
			ClientID: c.ID,
			UserID:   c.UserID,
			Message:  msg,
		})
	}
}

func (c *Client) reportPayloadTooLarge() {
	handled := make(chan struct{})
	closed := make(chan struct{})
	if c.enqueueInbound(inboundMessage{
		ClientID:  c.ID,
		UserID:    c.UserID,
		ErrorCode: "payload_too_large",
		handled:   handled,
		closed:    closed,
	}) {
		select {
		case <-handled:
		case <-c.hub.done:
			return
		}
		select {
		case <-closed:
		case <-c.hub.done:
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
	var pendingClose chan struct{}
	defer func() {
		ticker.Stop()
		if pendingClose != nil {
			close(pendingClose)
		}
		c.conn.Close()
	}()

	for {
		select {
		case outbound, ok := <-c.send:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if !ok {
				// Hub closed the channel — send a close message.
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if outbound.closeAfter {
				pendingClose = outbound.closed
				if err := c.conn.WriteMessage(websocket.TextMessage, outbound.data); err != nil {
					return
				}
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				close(pendingClose)
				pendingClose = nil
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			if _, err := w.Write(outbound.data); err != nil {
				_ = w.Close()
				return
			}

			// Drain any queued messages into the same WebSocket frame batch.
			n := len(c.send)
			for i := 0; i < n; i++ {
				next, ok := <-c.send
				if !ok {
					_ = w.Close()
					return
				}
				if next.closeAfter {
					pendingClose = next.closed
				}
				if _, err := w.Write([]byte("\n")); err != nil {
					_ = w.Close()
					return
				}
				if _, err := w.Write(next.data); err != nil {
					_ = w.Close()
					return
				}
				if next.closeAfter {
					break
				}
			}

			if err := w.Close(); err != nil {
				return
			}
			if pendingClose != nil {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				close(pendingClose)
				pendingClose = nil
				return
			}

		case <-ticker.C:
			// Send a ping to keep the connection alive.
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// send a JSON-encoded Message to this client (non-blocking on hub side).
func (c *Client) sendJSON(msg Message) bool {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Msg("ws: marshal error")
		return false
	}
	select {
	case c.send <- outboundMessage{data: data}:
		return true
	default:
		// Client is too slow or disconnected — drop the message.
		log.Warn().Str("client_id", c.ID).Msg("ws: send buffer full, dropping message")
		return false
	}
}

func (c *Client) sendJSONAndClose(msg Message, closed chan struct{}) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Error().Err(err).Msg("ws: marshal error")
		close(closed)
		return
	}
	select {
	case c.send <- outboundMessage{data: data, closeAfter: true, closed: closed}:
	default:
		log.Warn().Str("client_id", c.ID).Msg("ws: send buffer full, dropping protocol error")
		close(closed)
	}
}
