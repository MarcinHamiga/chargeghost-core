package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
)

// ConnectorDTO is the JSON representation of a connector.
type ConnectorDTO struct {
	ID          int     `json:"id"`
	Status      string  `json:"status"`
	Voltage     float64 `json:"voltage"`
	Current     float64 `json:"current"`
	Phase       int     `json:"phase"`
	IsPluggedIn bool    `json:"is_plugged_in"`
	IDTag       *string `json:"id_tag"`
}

// SessionDTO is the JSON representation of an active session.
type SessionDTO struct {
	TransactionID int       `json:"transaction_id"`
	ConnectorID   int       `json:"connector_id"`
	EnergyWh      float64   `json:"energy_charged_wh"`
	StateOfCharge float64   `json:"state_of_charge"`
	StartTime     time.Time `json:"start_time"`
	IDTag         *string   `json:"id_tag"`
	IsCharging    bool      `json:"is_charging"`
}

// EnergyMeterDTO is the JSON representation of an energy meter.
type EnergyMeterDTO struct {
	ReadingWh  float64 `json:"reading_wh"`
	IsCharging bool    `json:"is_charging"`
}

// StatusResponseDTO is returned by GET /api/v1/status.
type StatusResponseDTO struct {
	OCPPConnected  bool                      `json:"ocpp_connected"`
	UptimeSeconds  float64                   `json:"uptime_seconds"`
	Connectors     []ConnectorDTO            `json:"connectors"`
	ActiveSessions []SessionDTO              `json:"active_sessions"`
	EnergyMeters   map[string]EnergyMeterDTO `json:"energy_meters"`
}

// GetStatus handles GET /api/v1/status.
func GetStatus(e *engine.Engine, startTime time.Time, ocppBridge OCPPSendAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		connectorIDs := e.GetConnectorIDs()
		connectors := make([]ConnectorDTO, 0, len(connectorIDs))
		for _, id := range connectorIDs {
			c := e.GetConnector(id)
			if c == nil {
				continue
			}
			connectors = append(connectors, ConnectorDTO{
				ID:          c.ID,
				Status:      string(c.Status),
				Voltage:     c.Voltage,
				Current:     c.Current,
				Phase:       c.Phase,
				IsPluggedIn: c.IsPluggedIn,
				IDTag:       c.IDTag,
			})
		}

		sessions := e.GetSessionInfo()
		sessionDTOs := make([]SessionDTO, 0, len(sessions))
		for _, s := range sessions {
			sessionDTOs = append(sessionDTOs, SessionDTO{
				TransactionID: s.TransactionID,
				ConnectorID:   s.ConnectorID,
				EnergyWh:      s.EnergyCharged,
				StateOfCharge: s.StateOfCharge,
				StartTime:     s.StartTime,
				IDTag:         s.IDTag,
				IsCharging:    s.IsCharging,
			})
		}

		meters := make(map[string]EnergyMeterDTO)
		for _, id := range connectorIDs {
			m := e.GetEnergyMeter(id)
			if m != nil {
				meters[fmt.Sprintf("%d", id)] = EnergyMeterDTO{
					ReadingWh:  m.Value,
					IsCharging: m.IsCharging,
				}
			}
		}

		ocppConnected := false
		if ocppBridge != nil {
			ocppConnected = ocppBridge.IsConnected()
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(StatusResponseDTO{
			OCPPConnected:  ocppConnected,
			UptimeSeconds:  time.Since(startTime).Seconds(),
			Connectors:     connectors,
			ActiveSessions: sessionDTOs,
			EnergyMeters:   meters,
		})
	}
}
