package ws

import (
	"fmt"
	"sort"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
)

// BuildStatusSnapshot assembles the full status payload (aligned with GET /api/v1/status).
// uptimeSeconds should be time.Since(serverStart).Seconds() when known; pass 0 to omit.
func BuildStatusSnapshot(e *engine.Engine, ocppConnected bool, uptimeSeconds float64) map[string]interface{} {
	ids := e.GetConnectorIDs()
	sort.Ints(ids)

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

	reservations := e.ListReservations()
	sort.Slice(reservations, func(i, j int) bool {
		return reservations[i].ReservationID < reservations[j].ReservationID
	})
	reservationList := make([]map[string]interface{}, 0, len(reservations))
	for _, res := range reservations {
		reservationList = append(reservationList, map[string]interface{}{
			"reservation_id": res.ReservationID,
			"connector_id":   res.ConnectorID,
			"id_tag":         res.IDTag,
			"expiry_date":    res.ExpiryDate.UTC().Format(time.RFC3339),
			"parent_id_tag":  res.ParentIDTag,
		})
	}

	pending := e.ListPendingRemoteStarts()
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].ConnectorID < pending[j].ConnectorID
	})
	pendingList := make([]map[string]interface{}, 0, len(pending))
	for _, p := range pending {
		pendingList = append(pendingList, map[string]interface{}{
			"connector_id":   p.ConnectorID,
			"transaction_id": p.TransactionID,
			"id_tag":         p.IDTag,
			"expiry":         p.Expiry.UTC().Format(time.RFC3339),
		})
	}

	out := map[string]interface{}{
		"ocpp_connected":        ocppConnected,
		"connectors":            connectors,
		"active_sessions":       sessionList,
		"energy_meters":         meters,
		"reservations":          reservationList,
		"pending_remote_starts": pendingList,
	}
	if uptimeSeconds > 0 {
		out["uptime_seconds"] = uptimeSeconds
	}
	return out
}

// BuildStationStatusSnapshot wraps BuildStatusSnapshot as a station-scoped tick message.
func BuildStationStatusSnapshot(stationID string, e *engine.Engine, ocppConnected bool, uptimeSeconds float64) Message {
	return Message{
		Type:      "tick",
		StationID: stationID,
		Data:      BuildStatusSnapshot(e, ocppConnected, uptimeSeconds),
	}
}

// EngineSnapshotSource describes a single station for aggregate fleet snapshots.
type EngineSnapshotSource struct {
	Engine    *engine.Engine
	Bridge    interface{ IsConnected() bool }
	StartTime time.Time
}

// BuildFleetStatusSnapshot assembles an aggregate status payload for all stations.
func BuildFleetStatusSnapshot(stations map[string]*EngineSnapshotSource) Message {
	stationSnapshots := make(map[string]map[string]interface{}, len(stations))
	for id, src := range stations {
		ocppConnected := src.Bridge != nil && src.Bridge.IsConnected()
		stationSnapshots[id] = BuildStatusSnapshot(src.Engine, ocppConnected, time.Since(src.StartTime).Seconds())
	}
	return Message{
		Type: "fleet_tick",
		Data: map[string]interface{}{
			"stations": stationSnapshots,
		},
	}
}
