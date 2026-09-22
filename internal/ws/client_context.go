package ws

import "context"

// Context returns the lifetime context for this WebSocket connection.
func (c *Client) Context() context.Context {
	return c.ctx
}

func (c *Client) enqueueInbound(msg inboundMessage) {
	select {
	case c.hub.inbound <- msg:
	case <-c.ctx.Done():
	case <-c.hub.done:
	}
}
