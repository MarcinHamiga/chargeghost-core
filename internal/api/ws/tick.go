package ws

import (
	"context"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
)

// StartTicker broadcasts a full status snapshot to all WebSocket clients every interval.
// Call in a dedicated goroutine. serverStart is used for uptime_seconds in tick payloads.
func StartTicker(ctx context.Context, hub *Hub, e *engine.Engine, ocppBridge interface{ IsConnected() bool }, serverStart time.Time, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ocppConnected := ocppBridge != nil && ocppBridge.IsConnected()
			uptime := time.Since(serverStart).Seconds()
			hub.BroadcastMessage(Message{
				Type: "tick",
				Data: BuildStatusSnapshot(e, ocppConnected, uptime),
			})
		}
	}
}
