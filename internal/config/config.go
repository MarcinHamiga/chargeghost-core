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
	ID                  *string           `json:"id,omitempty"`
	Enabled             *bool             `json:"enabled,omitempty"`
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
	AdminAuthEnabled    bool              `json:"admin_auth_enabled,omitempty"`
	AllowedOrigins      []string          `json:"allowed_origins,omitempty"`
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

// Save writes the config to path atomically, creating parent directories as needed.
// It writes to a temporary file, fsyncs it, and renames it over the target path.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	sanitized := c.Sanitized()
	data, err := json.MarshalIndent(sanitized, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	if f, err := os.Open(tmpPath); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// Sanitized returns a copy safe to expose over the API or persist.
// Slices are cloned so callers cannot mutate the live config.
func (c *Config) Sanitized() *Config {
	copy := *c
	copy.OCPPPassword = nil
	copy.Connectors = append([]ConnectorConfig(nil), c.Connectors...)
	copy.Stations = make([]StationConfig, len(c.Stations))
	for i, sc := range c.Stations {
		copy.Stations[i] = cloneStationConfig(sc)
		copy.Stations[i].OCPPPassword = nil
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

// SetAdminToken stores the admin token in the OS keyring.
func SetAdminToken(token string) error {
	return keyring.Set(keyringService, "admin_token", token)
}

// GetAdminToken retrieves the admin token from the keyring or the
// CHARGEGHOST_ADMIN_TOKEN environment variable.
func GetAdminToken() string {
	if tok, err := keyring.Get(keyringService, "admin_token"); err == nil {
		return tok
	}
	return os.Getenv("CHARGEGHOST_ADMIN_TOKEN")
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
// view. When Stations is empty, the top-level config becomes the single station.
// Station-scoped connection URLs may contain the {ocpp_id} template, which is
// expanded to the station's OCPP ID. Passwords are not copied into the
// effective configs; callers must look them up via GetPassword(ocppID).
func (c *Config) EffectiveStationConfigs() ([]*EffectiveStation, error) {
	if len(c.Stations) == 0 {
		return []*EffectiveStation{{
			ID:      c.OCPPID,
			Enabled: true,
			Config:  c,
		}}, nil
	}
	if len(c.Stations) > 8 {
		return nil, errors.New("too many stations: maximum is 8")
	}
	used := make(map[string]bool)
	result := make([]*EffectiveStation, 0, len(c.Stations))
	for i, sc := range c.Stations {
		if sc.OCPPID == nil || *sc.OCPPID == "" {
			return nil, fmt.Errorf("station %d missing ocpp_id", i)
		}
		ocppID := *sc.OCPPID
		stationID := sc.StationID()
		if used[stationID] {
			return nil, fmt.Errorf("duplicate station id: %s", stationID)
		}
		used[stationID] = true
		if used[ocppID] {
			// Only an error when a different station ID reuses the same OCPP ID.
			if stationID != ocppID {
				return nil, fmt.Errorf("duplicate ocpp_id: %s", ocppID)
			}
		}
		cfg := c.mergeStation(sc)
		cfg.OCPPPassword = nil
		cfg.Stations = nil
		cfg.ConnectionURL = expandConnectionURLTemplate(cfg.ConnectionURL, ocppID)
		if len(cfg.Connectors) == 0 {
			return nil, fmt.Errorf("station %s has no connectors after defaults applied", stationID)
		}
		result = append(result, &EffectiveStation{
			ID:      stationID,
			Enabled: sc.IsEnabled(),
			Config:  cfg,
		})
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
