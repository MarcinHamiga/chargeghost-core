package ws

import (
	"context"
	"fmt"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
)

// StartTicker broadcasts a full status snapshot to all WebSocket clients every interval.
// Call in a dedicated goroutine.
func StartTicker(ctx context.Context, hub *Hub, e *engine.Engine, ocppBridge interface{ IsConnected() bool }, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ocppConnected := ocppBridge != nil && ocppBridge.IsConnected()
			hub.BroadcastMessage(Message{
				Type: "tick",
				Data: BuildStatusSnapshot(e, ocppConnected),
			})
		}
	}
}

// BuildStatusSnapshot assembles the full status payload (same as GET /api/v1/status).
func BuildStatusSnapshot(e *engine.Engine, ocppConnected bool) map[string]interface{} {
	ids := e.GetConnectorIDs()
	connectors := make([]map[string]interface{}, 0, len(ids))
	for _, id := range ids {
		c := e.GetConnector(id)
		if c == nil {
			continue
		}
		connectors = append(connectors, map[string]interface{}{
			"id":            c.ID,
			"status":        string(c.Status),
			"voltage":       c.Voltage,
			"current":       c.Current,
			"phase":         c.Phase,
			"is_plugged_in": c.IsPluggedIn,
			"id_tag":        c.IDTag,
		})
	}

	sessions := e.GetSessionInfo()
	sessionList := make([]map[string]interface{}, 0, len(sessions))
	for _, s := range sessions {
		sessionList = append(sessionList, map[string]interface{}{
			"transaction_id":    s.TransactionID,
			"connector_id":      s.ConnectorID,
			"energy_charged_wh": s.EnergyCharged,
			"state_of_charge":   s.StateOfCharge,
			"start_time":        s.StartTime,
			"id_tag":            s.IDTag,
			"is_charging":       s.IsCharging,
		})
	}

	meters := make(map[string]interface{})
	for _, id := range ids {
		m := e.GetEnergyMeter(id)
		if m != nil {
			meters[fmt.Sprintf("%d", id)] = map[string]interface{}{
				"reading_wh":  m.Value,
				"is_charging": m.IsCharging,
			}
		}
	}

	return map[string]interface{}{
		"ocpp_connected":  ocppConnected,
		"connectors":      connectors,
		"active_sessions": sessionList,
		"energy_meters":   meters,
	}
}
