package config

import (
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

// ConnectorConfig holds per-connector startup parameters.
type ConnectorConfig struct {
	Voltage float64 `json:"voltage"`
	Current float64 `json:"current"`
	Phase   int     `json:"phase"`
}

// Config is the full application configuration.
// Persisted to ~/.chargeghost/config.json via Load/Save.
type Config struct {
	ConnectionURL       string            `json:"connection_url"`
	OCPPID              string            `json:"ocpp_id"`
	OCPPPassword        *string           `json:"ocpp_password,omitempty"`
	ChargePointModel    string            `json:"charge_point_model"`
	ChargePointVendor   string            `json:"charge_point_vendor"`
	Connectors          []ConnectorConfig `json:"connectors"`
	SecurityProfile     int               `json:"security_profile"`
	SkipTLSVerify       bool              `json:"skip_tls_verify"`
	TLSCAPath           string            `json:"tls_ca_path,omitempty"`
	TLSClientCertPath   string            `json:"tls_client_cert_path,omitempty"`
	TLSClientKeyPath    string            `json:"tls_client_key_path,omitempty"`
	LogMode             string            `json:"log_mode"`
	MultiEVSEMode       bool              `json:"multi_evse_mode"`
	EVBatteryCapacity   float64           `json:"ev_battery_capacity"` // kWh (user-facing)
	OCPPVersion         string            `json:"ocpp_version"`
	PersistMessageQueue bool              `json:"persist_message_queue"`
	RFIDTag             *string           `json:"rfid_tag"`
	IgnoredVersion      *string           `json:"ignored_version"`
	ConnectorType       string            `json:"connector_type"` // e.g. "cType2", "cCCS2" — used by v201 device model
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
		SecurityProfile:   0,
		LogMode:           "shallow",
		OCPPVersion:       "1.6",
		EVBatteryCapacity: 55.0,
		ConnectorType:     "cType2",
	}
}

// Load reads the config from path. Returns DefaultConfig() if the file does not exist.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultConfig(), nil
		}
		return nil, err
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.OCPPPassword != nil && cfg.OCPPID != "" {
		if err := SetPassword(cfg.OCPPID, *cfg.OCPPPassword); err != nil {
			slog.Warn("failed to migrate ocpp_password to keyring — password will not be available at runtime", "err", err)
		}
		cfg.OCPPPassword = nil
	}
	return cfg, nil
}

// Save writes the config to path, creating parent directories as needed.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	sanitized := c.Sanitized()
	data, err := json.MarshalIndent(sanitized, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// Sanitized returns a copy safe to expose over the API or persist.
// Slices are cloned so callers cannot mutate the live config.
func (c *Config) Sanitized() *Config {
	copy := *c
	copy.OCPPPassword = nil
	copy.Connectors = append([]ConnectorConfig(nil), c.Connectors...)
	return &copy
}

// DefaultConfigPath returns the platform-standard config file path.
func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".chargeghost", "config.json")
}

const keyringService = "chargeghost"

// GetPassword retrieves the OCPP password from:
//  1. OS keyring (keyed by OCPP ID)
//  2. CHARGEGHOST_PASSWORD environment variable (fallback)
//  3. Returns "" if neither is set.
func GetPassword(ocppID string) string {
	if pw, err := keyring.Get(keyringService, ocppID); err == nil {
		return pw
	}
	return os.Getenv("CHARGEGHOST_PASSWORD")
}

// SetPassword stores the OCPP password in the OS keyring.
func SetPassword(ocppID, password string) error {
	return keyring.Set(keyringService, ocppID, password)
}
