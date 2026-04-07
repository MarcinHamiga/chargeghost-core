package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // allow any origin
}

// Hub manages WebSocket client lifecycle via a single goroutine.
// All client map mutations happen in Run(), never in handler goroutines.
type Hub struct {
	clients    map[*Client]bool
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte
}

// Client represents a single WebSocket connection.
type Client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

// NewHub creates an idle Hub. Call Run(ctx) in a goroutine before use.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan []byte, 256),
	}
}

// Run is the single goroutine that owns the client map. Blocks until ctx is done.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
		case message := <-h.broadcast:
			dead := make([]*Client, 0)
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// Client's send buffer full — mark for removal.
					dead = append(dead, client)
				}
			}
			for _, client := range dead {
				close(client.send)
				delete(h.clients, client)
			}
		}
	}
}

// BroadcastAsync enqueues pre-marshaled bytes for broadcast.
// Non-blocking — safe to call from engine callbacks (which hold the engine mutex).
func (h *Hub) BroadcastAsync(msg []byte) {
	select {
	case h.broadcast <- msg:
	default:
		// Broadcast channel full — drop rather than block.
	}
}

// BroadcastMessage marshals a Message and enqueues it for broadcast.
func (h *Hub) BroadcastMessage(msg Message) {
	msg.Timestamp = time.Now()
	b, err := json.Marshal(msg)
	if err != nil {
		slog.Error("ws: marshal failed", "type", msg.Type, "error", err)
		return
	}
	h.BroadcastAsync(b)
}

// ServeWS upgrades the HTTP connection to WebSocket, sends the snapshot,
// and registers the client with the Hub.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, snapshot Message) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("ws: upgrade failed", "error", err)
		return
	}

	client := &Client{
		hub:  h,
		conn: conn,
		send: make(chan []byte, 256),
	}
	h.register <- client

	// Send state snapshot immediately.
	snapshot.Timestamp = time.Now()
	if b, err := json.Marshal(snapshot); err == nil {
		client.send <- b
	}

	go client.writePump()
	go client.readPump()
}

// writePump drains the client's send channel to the WebSocket connection.
func (c *Client) writePump() {
	ticker := time.NewTicker(54 * time.Second) // ping interval
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump reads from the WebSocket to detect disconnects.
// Clients don't send commands to the server (read-only stream).
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
	}
}
