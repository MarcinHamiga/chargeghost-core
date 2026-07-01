package config

import (
	"errors"
	"fmt"
	"path/filepath"
)

// EffectiveStation is a runtime view of a single configured station.
// It carries the stable station ID, the enabled flag, and the effective
// runtime Config (which is the global config merged with station overrides).
type EffectiveStation struct {
	ID      string
	Enabled bool
	Config  *Config
}

// StationID returns the stable station ID for this station config.
// If no explicit ID is set, it derives one from the OCPP ID for backwards
// compatibility with legacy configs.
func (sc *StationConfig) StationID() string {
	if sc.ID != nil && *sc.ID != "" {
		return *sc.ID
	}
	if sc.OCPPID != nil && *sc.OCPPID != "" {
		return *sc.OCPPID
	}
	return ""
}

// IsEnabled reports whether the station is enabled. A nil Enabled field
// defaults to true for backwards compatibility.
func (sc *StationConfig) IsEnabled() bool {
	if sc.Enabled == nil {
		return true
	}
	return *sc.Enabled
}

// Clone returns a deep copy of the configuration. Slices are copied so that
// mutations on the clone do not affect the original.
func (c *Config) Clone() *Config {
	copy := *c
	copy.OCPPPassword = nil
	copy.Connectors = append([]ConnectorConfig(nil), c.Connectors...)
	copy.RFIDTag = nil
	if c.RFIDTag != nil {
		tag := *c.RFIDTag
		copy.RFIDTag = &tag
	}
	copy.IgnoredVersion = nil
	if c.IgnoredVersion != nil {
		v := *c.IgnoredVersion
		copy.IgnoredVersion = &v
	}
	copy.Stations = make([]StationConfig, len(c.Stations))
	for i, sc := range c.Stations {
		copy.Stations[i] = cloneStationConfig(sc)
	}
	return &copy
}

func cloneStationConfig(sc StationConfig) StationConfig {
	copy := sc
	copy.Connectors = append([]ConnectorConfig(nil), sc.Connectors...)
	return copy
}

// FindStation locates a station by its stable station ID. It returns the
// station config, its index in the Stations slice, and whether it was found.
// Matching first checks the explicit ID field, then falls back to the OCPP
// ID for legacy configs.
func (c *Config) FindStation(id string) (*StationConfig, int, bool) {
	for i := range c.Stations {
		sc := &c.Stations[i]
		if sc.StationID() == id {
			return sc, i, true
		}
	}
	return nil, -1, false
}

// UpsertStation adds a new station or updates an existing one by stable ID.
// It returns an error if the station lacks an OCPP ID or if the resulting
// station list would exceed the maximum of 8.
func (c *Config) UpsertStation(st StationConfig) error {
	if st.OCPPID == nil || *st.OCPPID == "" {
		return errors.New("station ocpp_id is required")
	}
	id := st.StationID()
	if id == "" {
		return errors.New("station id or ocpp_id is required")
	}
	if _, _, found := c.FindStation(id); found {
		return c.updateStation(id, st)
	}
	if len(c.Stations) >= 8 {
		return errors.New("too many stations: maximum is 8")
	}
	c.Stations = append(c.Stations, cloneStationConfig(st))
	return nil
}

func (c *Config) updateStation(id string, st StationConfig) error {
	_, idx, found := c.FindStation(id)
	if !found {
		return fmt.Errorf("station %s not found", id)
	}
	c.Stations[idx] = cloneStationConfig(st)
	return nil
}

// RemoveStation removes the station with the given stable ID from the
// Stations slice. It returns an error if the station is not found.
func (c *Config) RemoveStation(id string) error {
	_, idx, found := c.FindStation(id)
	if !found {
		return fmt.Errorf("station %s not found", id)
	}
	c.Stations = append(c.Stations[:idx], c.Stations[idx+1:]...)
	return nil
}

// ValidateStations checks that all configured stations are valid and unique.
func (c *Config) ValidateStations() error {
	if len(c.Stations) > 8 {
		return errors.New("too many stations: maximum is 8")
	}
	seen := make(map[string]bool)
	for i, sc := range c.Stations {
		if sc.OCPPID == nil || *sc.OCPPID == "" {
			return fmt.Errorf("station %d missing ocpp_id", i)
		}
		id := sc.StationID()
		if seen[id] {
			return fmt.Errorf("duplicate station id: %s", id)
		}
		seen[id] = true
		if seen[*sc.OCPPID] && id != *sc.OCPPID {
			return fmt.Errorf("duplicate ocpp_id: %s", *sc.OCPPID)
		}
		if len(sc.Connectors) == 0 && len(c.Connectors) == 0 {
			return fmt.Errorf("station %s has no connectors after defaults applied", id)
		}
	}
	return nil
}

// EffectiveStationConfig returns the effective runtime Config for the station
// with the given stable ID. The returned Config has its OCPP ID set, the
// Stations slice stripped, and connection URL template expanded.
func (c *Config) EffectiveStationConfig(id string) (*Config, error) {
	sc, _, found := c.FindStation(id)
	if !found {
		return nil, fmt.Errorf("station %s not found", id)
	}
	cfg := c.mergeStation(*sc)
	cfg.OCPPPassword = nil
	cfg.Stations = nil
	cfg.ConnectionURL = expandConnectionURLTemplate(cfg.ConnectionURL, cfg.OCPPID)
	return cfg, nil
}

// StationIDs returns all configured station IDs, including disabled stations.
// When Stations is empty, it returns the top-level OCPP ID as the single
// station ID for legacy single-station mode.
func (c *Config) StationIDs() []string {
	if len(c.Stations) == 0 {
		if c.OCPPID != "" {
			return []string{c.OCPPID}
		}
		return nil
	}
	ids := make([]string, 0, len(c.Stations))
	for i := range c.Stations {
		ids = append(ids, c.Stations[i].StationID())
	}
	return ids
}

// StationPersistDirByID returns the recommended station-scoped persistence
// directory using the stable station ID. When the station has no explicit ID
// the directory is derived from the OCPP ID for legacy compatibility.
func StationPersistDirByID(baseDir, stationID string) string {
	return filepath.Join(baseDir, "stations", stationSafeKey(stationID))
}
