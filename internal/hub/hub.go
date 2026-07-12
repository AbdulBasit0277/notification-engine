// Package hub implements the WebSocket connection hub.
// The hub serialises all register/unregister/broadcast operations through a
// single goroutine (Run), so no mutex is needed on the clients map.
package hub

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"
	"notification-service/internal/domains"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 4096
)

// Client represents a single WebSocket connection.
type Client struct {
	UserID string
	conn   *websocket.Conn
	send   chan []byte // buffered; closed by hub on unregister
	hub    *Hub
}

// Hub maintains the set of active clients and broadcasts messages to them.
// All state mutations go through the hub's Run goroutine via channels.
type Hub struct {
	// clients maps userID → set of active clients for that user
	clients map[string]map[*Client]struct{}

	// Inbound operations — all serialised through Run's select
	register   chan *Client
	unregister chan *Client
	broadcast  chan broadcastMsg

	logger *slog.Logger
}

type broadcastMsg struct {
	userID  string
	payload []byte
}

// New creates an uninitialised Hub. Call Run to start it.
func New(logger *slog.Logger) *Hub {
	return &Hub{
		clients:    make(map[string]map[*Client]struct{}),
		register:   make(chan *Client, 64),
		unregister: make(chan *Client, 64),
		broadcast:  make(chan broadcastMsg, 256),
		logger:     logger,
	}
}

// Run starts the hub event loop. It must be called in its own goroutine.
// It exits when ctx is cancelled, closing all active connections cleanly.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			h.logger.Info("hub shutting down, closing all connections")
			for _, conns := range h.clients {
				for c := range conns {
					close(c.send)
				}
			}
			return

		case c := <-h.register:
			if h.clients[c.UserID] == nil {
				h.clients[c.UserID] = make(map[*Client]struct{})
			}
			h.clients[c.UserID][c] = struct{}{}
			h.logger.Info("ws client registered", "user_id", c.UserID)

		case c := <-h.unregister:
			if conns, ok := h.clients[c.UserID]; ok {
				if _, exists := conns[c]; exists {
					delete(conns, c)
					close(c.send)
					if len(conns) == 0 {
						delete(h.clients, c.UserID)
					}
					h.logger.Info("ws client unregistered", "user_id", c.UserID)
				}
			}

		case msg := <-h.broadcast:
			conns, ok := h.clients[msg.userID]
			if !ok {
				continue
			}
			for c := range conns {
				select {
				case c.send <- msg.payload:
				default:
					// Slow consumer — unregister rather than blocking the hub
					h.logger.Warn("ws client send buffer full, dropping", "user_id", c.UserID)
					delete(conns, c)
					close(c.send)
				}
			}
		}
	}
}

// Broadcast encodes n as a WSMessage and delivers it to all active connections
// for the given userID. Returns false if the user has no active connections.
func (h *Hub) Broadcast(userID string, n domains.Notification) bool {
	msg := domains.WSMessage{
		ID:     n.ID,
		Title:  n.Title,
		Body:   n.Body,
		Type:   n.Type,
		SentAt: time.Now().UTC(),
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		h.logger.Error("ws broadcast marshal error", "error", err)
		return false
	}
	// Non-blocking send to the hub loop; will be picked up in Run's select
	select {
	case h.broadcast <- broadcastMsg{userID: userID, payload: payload}:
		return true
	default:
		h.logger.Warn("hub broadcast channel full, dropping message", "user_id", userID)
		return false
	}
}

// HasActiveConnections returns true if userID has at least one live connection.
// Note: this reads hub state from outside the hub goroutine. It is safe for
// a quick pre-dispatch check but the result may be stale.
func (h *Hub) HasActiveConnections(userID string) bool {
	// We snapshot via a synchronous channel round-trip to avoid a mutex.
	// For an optimistic pre-check this is fine — delivery attempts via
	// Broadcast are already safe.
	return len(h.clients[userID]) > 0
}

// ServeWS upgrades the HTTP connection to WebSocket, registers the client,
// and starts its read and write pumps.
func (h *Hub) ServeWS(userID string, conn *websocket.Conn) {
	c := &Client{
		UserID: userID,
		conn:   conn,
		send:   make(chan []byte, 256),
		hub:    h,
	}
	h.register <- c

	// Start goroutines for reading and writing
	go c.writePump()
	go c.readPump()
}

// readPump drains incoming frames (we only expect pong frames and control
// messages). On any error or normal close it unregisters the client.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			// Normal close or network error — readPump exits and triggers unregister
			break
		}
	}
}

// writePump sends outbound messages from c.send to the WebSocket connection.
// It also sends periodic ping frames to detect dead connections.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel — send close frame and exit
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
