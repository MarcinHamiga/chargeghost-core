package api

import "time"

// Response is the standard envelope for all mutation endpoints.
type Response struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

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

// CreateConnectorRequest is the body for POST /api/v1/connectors.
type CreateConnectorRequest struct {
	Voltage float64 `json:"voltage"`
	Current float64 `json:"current"`
	Phase   int     `json:"phase"`
}

// UpdateConnectorRequest is the body for PUT /api/v1/connectors/{id}.
type UpdateConnectorRequest struct {
	Voltage *float64 `json:"voltage"`
	Current *float64 `json:"current"`
	Phase   *int     `json:"phase"`
}

// StartSessionRequest is the body for POST /api/v1/sessions/start.
type StartSessionRequest struct {
	ConnectorID    int     `json:"connector_id"`
	IDTag          *string `json:"id_tag"`
	TimeoutSeconds int     `json:"timeout_seconds"`
}

// UpdateAvailabilityRequest is the body for PUT /api/v1/connectors/{id}/availability.
type UpdateAvailabilityRequest struct {
	Type string `json:"type"`
}

// StoppedSessionDTO is returned by GET /api/v1/sessions/last-stopped.
type StoppedSessionDTO struct {
	TransactionID int     `json:"transaction_id"`
	ConnectorID   int     `json:"connector_id"`
	EnergyWh      float64 `json:"energy_charged_wh"`
	MeterStop     float64 `json:"meter_stop"`
	Reason        string  `json:"reason"`
	IDTag         *string `json:"id_tag"`
}

// PatchConfigRequest is the body for PATCH /api/v1/config.
type PatchConfigRequest struct {
	ConnectionURL       *string  `json:"connection_url"`
	OCPPID              *string  `json:"ocpp_id"`
	OCPPPassword        *string  `json:"ocpp_password"`
	ChargePointModel    *string  `json:"charge_point_model"`
	ChargePointVendor   *string  `json:"charge_point_vendor"`
	SecurityProfile     *int     `json:"security_profile"`
	SkipTLSVerify       *bool    `json:"skip_tls_verify"`
	TLSCAPath           *string  `json:"tls_ca_path"`
	TLSClientCertPath   *string  `json:"tls_client_cert_path"`
	TLSClientKeyPath    *string  `json:"tls_client_key_path"`
	LogMode             *string  `json:"log_mode"`
	MultiEVSEMode       *bool    `json:"multi_evse_mode"`
	EVBatteryCapacity   *float64 `json:"ev_battery_capacity"`
	OCPPVersion         *string  `json:"ocpp_version"`
	PersistMessageQueue *bool    `json:"persist_message_queue"`
	RFIDTag             *string  `json:"rfid_tag"`
	ConnectorType       *string  `json:"connector_type"`
}

// PatchConfigResponse is returned by PATCH /api/v1/config.
type PatchConfigResponse struct {
	Success       bool     `json:"success"`
	Action        string   `json:"action"` // "no-op" | "applied" | "restart_required"
	ChangedFields []string `json:"changed_fields"`
	Message       string   `json:"message"`
}

// ReservationDTO is the JSON representation of a reservation.
type ReservationDTO struct {
	ReservationID int     `json:"reservation_id"`
	ConnectorID   int     `json:"connector_id"`
	IDTag         string  `json:"id_tag"`
	ExpiryDate    string  `json:"expiry_date"`
	ParentIDTag   *string `json:"parent_id_tag"`
}

// CreateReservationRequest is the body for POST /api/v1/reservations.
type CreateReservationRequest struct {
	ConnectorID   int     `json:"connector_id"`
	ReservationID int     `json:"reservation_id"`
	IDTag         string  `json:"id_tag"`
	ExpiryDate    string  `json:"expiry_date"` // RFC3339
	ParentIDTag   *string `json:"parent_id_tag"`
}
