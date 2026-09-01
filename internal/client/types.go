package client

import (
	"encoding/json"
	"time"
)

// Event is the envelope of one WebSocket message. Data stays raw so the
// TUI decodes it per type (mirrors ws.Message on the server).
type Event struct {
	Type        string
	StationID   string
	OperationID string
	Ts          time.Time
	Raw         json.RawMessage
}

// Synthetic event types delivered by the subscriber on connection changes.
const (
	EventDisconnected = "__disconnected"
	EventReconnected  = "__reconnected"
)

// Tick mirrors the server's per-station status snapshot
// (ws.BuildStatusSnapshot). Unknown fields are ignored.
type Tick struct {
	OCPPConnected       bool                     `json:"ocpp_connected"`
	Connectors          []TickConnector          `json:"connectors"`
	ActiveSessions      []TickSession            `json:"active_sessions"`
	EnergyMeters        map[string]TickMeter     `json:"energy_meters"`
	Reservations        []TickReservation        `json:"reservations"`
	PendingRemoteStarts []TickPendingRemoteStart `json:"pending_remote_starts"`
	UptimeSeconds       float64                  `json:"uptime_seconds"`
}

type TickConnector struct {
	ID          int     `json:"id"`
	Status      string  `json:"status"`
	Voltage     float64 `json:"voltage"`
	Current     float64 `json:"current"`
	Phase       int     `json:"phase"`
	IsPluggedIn bool    `json:"is_plugged_in"`
	IDTag       string  `json:"id_tag"`
}

type TickSession struct {
	TransactionID   int       `json:"transaction_id"`
	ConnectorID     int       `json:"connector_id"`
	EnergyChargedWh float64   `json:"energy_charged_wh"`
	StateOfCharge   int       `json:"state_of_charge"`
	StartTime       time.Time `json:"start_time"`
	IDTag           string    `json:"id_tag"`
	IsCharging      bool      `json:"is_charging"`
}

type TickMeter struct {
	ReadingWh  float64 `json:"reading_wh"`
	IsCharging bool    `json:"is_charging"`
}

type TickReservation struct {
	ReservationID string `json:"reservation_id"`
	ConnectorID   int    `json:"connector_id"`
	IDTag         string `json:"id_tag"`
	ExpiryDate    string `json:"expiry_date"`
	ParentIDTag   string `json:"parent_id_tag"`
}

type TickPendingRemoteStart struct {
	ConnectorID   int    `json:"connector_id"`
	TransactionID int    `json:"transaction_id"`
	IDTag         string `json:"id_tag"`
	Expiry        string `json:"expiry"`
}

// wsEnvelope is the on-the-wire shape of ws.Message.
type wsEnvelope struct {
	Type        string          `json:"type"`
	StationID   string          `json:"station_id"`
	OperationID string          `json:"operation_id"`
	Timestamp   time.Time       `json:"timestamp"`
	Data        json.RawMessage `json:"data"`
}

// DecodeTick decodes a `tick` data payload.
func DecodeTick(raw json.RawMessage) (Tick, error) {
	var t Tick
	err := json.Unmarshal(raw, &t)
	return t, err
}

// DecodeFleetTick decodes a `fleet_tick` data payload into per-station ticks.
func DecodeFleetTick(raw json.RawMessage) (map[string]Tick, error) {
	var payload struct {
		Stations map[string]Tick `json:"stations"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload.Stations, nil
}
