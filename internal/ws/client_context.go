package ws

import "context"

// Context returns the lifetime context for this WebSocket connection.
func (c *Client) Context() context.Context {
	return c.ctx
}

func (c *Client) enqueueInbound(msg inboundMessage) bool {
	select {
	case c.hub.inbound <- msg:
		return true
	case <-c.ctx.Done():
	case <-c.hub.done:
	}
	return false
}
