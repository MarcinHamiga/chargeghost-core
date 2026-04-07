package ws

import "time"

// Message is the JSON envelope sent to all WebSocket clients.
// Callers construct a Message and call hub.BroadcastMessage — the hub marshals it internally.
type Message struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}
