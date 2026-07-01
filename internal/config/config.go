package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zalando/go-keyring"
)

// ConnectorConfig holds per-connector startup parameters.
type ConnectorConfig struct {
	Voltage float64 `json:"voltage"`
	Current float64 `json:"current"`
	Phase   int     `json:"phase"`
}

// StationConfig holds per-station overrides. Nil pointer fields inherit
// from the top-level Config. Slices that are empty or nil inherit the
// top-level connector list.
type StationConfig struct {
	ConnectionURL       *string           `json:"connection_url,omitempty"`
	OCPPID              *string           `json:"ocpp_id,omitempty"`
	OCPPPassword        *string           `json:"ocpp_password,omitempty"`
	ChargePointModel    *string           `json:"charge_point_model,omitempty"`
	ChargePointVendor   *string           `json:"charge_point_vendor,omitempty"`
	ChargePointSerial   *string           `json:"charge_point_serial,omitempty"`
	FirmwareVersion     *string           `json:"firmware_version,omitempty"`
	ModemICCID          *string           `json:"modem_iccid,omitempty"`
	ModemIMSI           *string           `json:"modem_imsi,omitempty"`
	Connectors          []ConnectorConfig `json:"connectors,omitempty"`
	SecurityProfile     *int              `json:"security_profile,omitempty"`
	SkipTLSVerify       *bool             `json:"skip_tls_verify,omitempty"`
	TLSCAPath           *string           `json:"tls_ca_path,omitempty"`
	TLSClientCertPath   *string           `json:"tls_client_cert_path,omitempty"`
	TLSClientKeyPath    *string           `json:"tls_client_key_path,omitempty"`
	MultiEVSEMode       *bool             `json:"multi_evse_mode,omitempty"`
	EVBatteryCapacity   *float64          `json:"ev_battery_capacity,omitempty"`
	OCPPVersion         *string           `json:"ocpp_version,omitempty"`
	PersistMessageQueue *bool             `json:"persist_message_queue,omitempty"`
	RFIDTag             *string           `json:"rfid_tag,omitempty"`
	ConnectorType       *string           `json:"connector_type,omitempty"`
}

// Config is the full application configuration.
// Persisted to ~/.chargeghost/config.json via Load/Save.
type Config struct {
	ConnectionURL       string            `json:"connection_url"`
	OCPPID              string            `json:"ocpp_id"`
	OCPPPassword        *string           `json:"ocpp_password,omitempty"`
	ChargePointModel    string            `json:"charge_point_model"`
	ChargePointVendor   string            `json:"charge_point_vendor"`
	ChargePointSerial   string            `json:"charge_point_serial,omitempty"` // OCPP 2.0.1 ChargingStation.SerialNumber
	FirmwareVersion     string            `json:"firmware_version,omitempty"`    // OCPP 2.0.1 ChargingStation.FirmwareVersion
	ModemICCID          string            `json:"modem_iccid,omitempty"`         // OCPP 2.0.1 ChargingStation.Modem.Iccid
	ModemIMSI           string            `json:"modem_imsi,omitempty"`          // OCPP 2.0.1 ChargingStation.Modem.Imsi
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
	Stations            []StationConfig   `json:"stations,omitempty"`
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
	for i := range cfg.Stations {
		sc := &cfg.Stations[i]
		ocppID := ""
		if sc.OCPPID != nil {
			ocppID = *sc.OCPPID
		}
		if sc.OCPPPassword != nil && ocppID != "" {
			if err := SetPassword(ocppID, *sc.OCPPPassword); err != nil {
				slog.Warn("failed to migrate station ocpp_password to keyring", "ocpp_id", ocppID, "err", err)
			}
			sc.OCPPPassword = nil
		}
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
	copy.Stations = make([]StationConfig, len(c.Stations))
	for i, sc := range c.Stations {
		copy.Stations[i] = sc
		copy.Stations[i].OCPPPassword = nil
		copy.Stations[i].Connectors = append([]ConnectorConfig(nil), sc.Connectors...)
	}
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

// stationSafeKey returns a stable, filesystem-safe directory name for a station.
// It uses URL path escaping plus a short hash to avoid collisions.
func stationSafeKey(ocppID string) string {
	escaped := strings.ReplaceAll(urlPathEscapeRe.ReplaceAllString(ocppID, "_"), "_", "-")
	hash := fnvHash(ocppID)
	return fmt.Sprintf("%s-%s", escaped, hash)
}

var urlPathEscapeRe = regexp.MustCompile(`[^a-zA-Z0-9\-_.~]`)

func fnvHash(s string) string {
	const fnvPrime = 0x100000001b3
	var hash uint64 = 0xcbf29ce484222325
	for i := 0; i < len(s); i++ {
		hash ^= uint64(s[i])
		hash *= fnvPrime
	}
	return fmt.Sprintf("%x", hash)[:8]
}

// EffectiveStationConfigs expands the global config into a per-station runtime
// Config. When Stations is empty, the top-level config becomes the single
// station. Station-scoped connection URLs may contain the {ocpp_id} template,
// which is expanded to the station's OCPP ID. Passwords are not copied into the
// effective configs; callers must look them up via GetPassword(ocppID).
func (c *Config) EffectiveStationConfigs() ([]*Config, error) {
	if len(c.Stations) == 0 {
		return []*Config{c}, nil
	}
	if len(c.Stations) > 8 {
		return nil, errors.New("too many stations: maximum is 8")
	}
	used := make(map[string]bool)
	result := make([]*Config, 0, len(c.Stations))
	for i, sc := range c.Stations {
		if sc.OCPPID == nil || *sc.OCPPID == "" {
			return nil, fmt.Errorf("station %d missing ocpp_id", i)
		}
		ocppID := *sc.OCPPID
		if used[ocppID] {
			return nil, fmt.Errorf("duplicate ocpp_id: %s", ocppID)
		}
		used[ocppID] = true
		cfg := c.mergeStation(sc)
		cfg.OCPPPassword = nil
		cfg.Stations = nil
		cfg.ConnectionURL = expandConnectionURLTemplate(cfg.ConnectionURL, ocppID)
		if len(cfg.Connectors) == 0 {
			return nil, fmt.Errorf("station %s has no connectors after defaults applied", ocppID)
		}
		result = append(result, cfg)
	}
	return result, nil
}

// StationSafeKey returns a stable, filesystem-safe directory name for the station
// with the given OCPP ID.
func StationSafeKey(ocppID string) string {
	return stationSafeKey(ocppID)
}

// StationPersistDir returns the recommended station-scoped persistence directory.
func StationPersistDir(baseDir, ocppID string) string {
	return filepath.Join(baseDir, "stations", stationSafeKey(ocppID))
}

// LegacyPersistDir returns the current single-station persistence directory.
func LegacyPersistDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".chargeghost", "engine")
}

func (c *Config) mergeStation(sc StationConfig) *Config {
	copy := *c
	if sc.ConnectionURL != nil {
		copy.ConnectionURL = *sc.ConnectionURL
	}
	if sc.OCPPID != nil {
		copy.OCPPID = *sc.OCPPID
	}
	if sc.ChargePointModel != nil {
		copy.ChargePointModel = *sc.ChargePointModel
	}
	if sc.ChargePointVendor != nil {
		copy.ChargePointVendor = *sc.ChargePointVendor
	}
	if sc.ChargePointSerial != nil {
		copy.ChargePointSerial = *sc.ChargePointSerial
	}
	if sc.FirmwareVersion != nil {
		copy.FirmwareVersion = *sc.FirmwareVersion
	}
	if sc.ModemICCID != nil {
		copy.ModemICCID = *sc.ModemICCID
	}
	if sc.ModemIMSI != nil {
		copy.ModemIMSI = *sc.ModemIMSI
	}
	if len(sc.Connectors) > 0 {
		copy.Connectors = append([]ConnectorConfig(nil), sc.Connectors...)
	}
	if sc.SecurityProfile != nil {
		copy.SecurityProfile = *sc.SecurityProfile
	}
	if sc.SkipTLSVerify != nil {
		copy.SkipTLSVerify = *sc.SkipTLSVerify
	}
	if sc.TLSCAPath != nil {
		copy.TLSCAPath = *sc.TLSCAPath
	}
	if sc.TLSClientCertPath != nil {
		copy.TLSClientCertPath = *sc.TLSClientCertPath
	}
	if sc.TLSClientKeyPath != nil {
		copy.TLSClientKeyPath = *sc.TLSClientKeyPath
	}
	if sc.MultiEVSEMode != nil {
		copy.MultiEVSEMode = *sc.MultiEVSEMode
	}
	if sc.EVBatteryCapacity != nil {
		copy.EVBatteryCapacity = *sc.EVBatteryCapacity
	}
	if sc.OCPPVersion != nil {
		copy.OCPPVersion = *sc.OCPPVersion
	}
	if sc.PersistMessageQueue != nil {
		copy.PersistMessageQueue = *sc.PersistMessageQueue
	}
	if sc.RFIDTag != nil {
		copy.RFIDTag = sc.RFIDTag
	}
	if sc.ConnectorType != nil {
		copy.ConnectorType = *sc.ConnectorType
	}
	return &copy
}

func expandConnectionURLTemplate(url, ocppID string) string {
	return strings.ReplaceAll(url, "{ocpp_id}", ocppID)
}
