package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = NewUpgrader(nil)

// NewUpgrader creates a websocket.Upgrader with optional origin enforcement.
// If allowedOrigins is empty, any origin is accepted (development default).
func NewUpgrader(allowedOrigins []string) *websocket.Upgrader {
	originSet := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		originSet[o] = true
	}
	enforce := len(allowedOrigins) > 0
	return &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			if !enforce {
				return true
			}
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			return originSet[origin]
		},
	}
}

// AuthorizeAdmin checks the request for a valid admin bearer token via the
// Authorization header or the `access_token` query parameter.
func AuthorizeAdmin(r *http.Request, expectedToken string) bool {
	if expectedToken == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(auth) >= len(prefix) && auth[:len(prefix)] == prefix {
		return auth[len(prefix):] == expectedToken
	}
	if r.URL.Query().Get("access_token") == expectedToken {
		return true
	}
	return false
}

// ClientScope controls which station-scoped messages a WebSocket client receives.
type ClientScope int

const (
	// ScopeDefault receives messages for the default station only.
	ScopeDefault ClientScope = iota
	// ScopeStation receives messages for a single explicit station.
	ScopeStation
	// ScopeAll receives all station-scoped messages plus fleet aggregates.
	ScopeAll
)

// Hub manages WebSocket client lifecycle via a single goroutine.
// All client map mutations happen in Run(), never in handler goroutines.
// It supports station-scoped subscriptions so a single process can stream
// events for multiple stations without cross-leakage.
type Hub struct {
	clients          map[*Client]bool
	register         chan *Client
	unregister       chan *Client
	broadcast        chan Message
	defaultStationID string
}

// Client represents a single WebSocket connection.
type Client struct {
	hub       *Hub
	conn      *websocket.Conn
	send      chan []byte
	scope     ClientScope
	stationID string
}

// NewHub creates an idle Hub. Call Run(ctx) in a goroutine before use.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan Message, 256),
	}
}

// SetDefaultStationID sets the station ID used for default-scope subscriptions.
// Call before clients connect.
func (h *Hub) SetDefaultStationID(id string) {
	h.defaultStationID = id
}

// messageVisibleTo returns true if a client should receive the given message
// based on its subscription scope.
func (h *Hub) messageVisibleTo(msg Message, c *Client) bool {
	if msg.Type == "fleet_tick" {
		return c.scope == ScopeAll
	}
	if msg.StationID == "" {
		return true
	}
	switch c.scope {
	case ScopeAll:
		return true
	case ScopeDefault:
		return msg.StationID == h.defaultStationID
	case ScopeStation:
		return msg.StationID == c.stationID
	}
	return false
}

// Run is the single goroutine that owns the client map. Blocks until ctx is done.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			for client := range h.clients {
				close(client.send)
				delete(h.clients, client)
			}
			return
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
		case msg := <-h.broadcast:
			b, err := json.Marshal(msg)
			if err != nil {
				slog.Error("ws: marshal failed", "type", msg.Type, "error", err)
				continue
			}
			dead := make([]*Client, 0)
			for client := range h.clients {
				if !h.messageVisibleTo(msg, client) {
					continue
				}
				select {
				case client.send <- b:
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

// BroadcastMessage marshals a Message and enqueues it for broadcast.
func (h *Hub) BroadcastMessage(msg Message) {
	msg.Timestamp = time.Now()
	select {
	case h.broadcast <- msg:
	default:
		slog.Warn("ws: broadcast channel full, message dropped")
	}
}

// ServeWS upgrades the HTTP connection to WebSocket using the default upgrader,
// sends the snapshot, and registers the client with the Hub.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, snapshot Message, scope ClientScope, stationID string) {
	h.ServeWSWithUpgrader(w, r, upgrader, snapshot, scope, stationID)
}

// ServeWSWithUpgrader is like ServeWS but uses the provided upgrader.
func (h *Hub) ServeWSWithUpgrader(w http.ResponseWriter, r *http.Request, upgrader *websocket.Upgrader, snapshot Message, scope ClientScope, stationID string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("ws: upgrade failed", "error", err)
		return
	}

	client := &Client{
		hub:       h,
		conn:      conn,
		send:      make(chan []byte, 256),
		scope:     scope,
		stationID: stationID,
	}
	h.register <- client

	// Send state snapshot immediately.
	snapshot.Timestamp = time.Now()
	b, err := json.Marshal(snapshot)
	if err != nil {
		slog.Error("ws: snapshot marshal failed", "error", err)
		conn.Close()
		return
	}
	client.send <- b

	go client.writePump()
	go client.readPump()
}

// ScopeFromRequest parses a WebSocket request's query parameters into a
// subscription scope and optional station ID. The default station ID is used
// when no explicit scope is requested.
func ScopeFromRequest(r *http.Request, defaultStationID string) (string, ClientScope) {
	stationID := r.URL.Query().Get("station_id")
	if stationID != "" {
		return stationID, ScopeStation
	}
	if r.URL.Query().Get("scope") == "all" {
		return "", ScopeAll
	}
	return defaultStationID, ScopeDefault
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
