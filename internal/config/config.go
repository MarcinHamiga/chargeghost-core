package config

// ConnectorConfig holds per-connector startup parameters.
type ConnectorConfig struct {
	Voltage float64 `json:"voltage"`
	Current float64 `json:"current"`
	Phase   int     `json:"phase"`
}

// Config is the full application configuration. Stored in-memory;
// persistence to ~/.chargeghost/config.json is added in Plan 6.
type Config struct {
	ConnectionURL      string            `json:"connection_url"`
	OCPPID             string            `json:"ocpp_id"`
	OCPPPassword       *string           `json:"ocpp_password,omitempty"`
	ChargePointModel   string            `json:"charge_point_model"`
	ChargePointVendor  string            `json:"charge_point_vendor"`
	Connectors         []ConnectorConfig `json:"connectors"`
	SkipTLSVerify      bool              `json:"skip_tls_verify"`
	LogMode            string            `json:"log_mode"`
	MultiEVSEMode      bool              `json:"multi_evse_mode"`
	EVBatteryCapacity  float64           `json:"ev_battery_capacity"` // kWh (user-facing)
	OCPPVersion        string            `json:"ocpp_version"`
	PersistMessageQueue bool             `json:"persist_message_queue"`
	RFIDTag            *string           `json:"rfid_tag"`
	IgnoredVersion     *string           `json:"ignored_version"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		ConnectionURL:     "wss://localhost:3000/CP_1",
		OCPPID:            "CP_1",
		ChargePointModel:  "ChargeGhostV1",
		ChargePointVendor: "ChargeGhost",
		Connectors: []ConnectorConfig{
			{Voltage: 230.0, Current: 32.0, Phase: 1},
		},
		LogMode:           "shallow",
		OCPPVersion:       "1.6",
		EVBatteryCapacity: 55.0,
	}
}
