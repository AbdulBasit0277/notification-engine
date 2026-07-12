package channel

import (
	"context"
	"fmt"

	"notification-service/internal/domains"
	"notification-service/internal/hub"
)

// WSSender delivers notifications through the WebSocket hub.
type WSSender struct {
	hub *hub.Hub
}

// NewWSSender creates a WSSender that broadcasts via the given hub.
func NewWSSender(h *hub.Hub) *WSSender {
	return &WSSender{hub: h}
}

// Send broadcasts n to all active WebSocket connections for n.UserID.
// Returns an error if the user has no active connections at dispatch time.
func (ws *WSSender) Send(_ context.Context, n domains.Notification) error {
	delivered := ws.hub.Broadcast(n.UserID, n)
	if !delivered {
		return fmt.Errorf("no active WebSocket connections for user %s", n.UserID)
	}
	return nil
}
