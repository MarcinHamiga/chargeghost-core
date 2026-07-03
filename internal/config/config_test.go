package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/chargeghost/engine/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Defaults(t *testing.T) {
	c := config.DefaultConfig()
	assert.Equal(t, "CP_1", c.OCPPID)
	assert.Equal(t, "ChargeGhostV1", c.ChargePointModel)
	assert.Equal(t, "1.6", c.OCPPVersion)
	assert.Equal(t, 55.0, c.EVBatteryCapacity) // kWh
	assert.Equal(t, 0, c.SecurityProfile)
	assert.False(t, c.MultiEVSEMode)
	assert.Len(t, c.Connectors, 1)
	assert.Equal(t, 230.0, c.Connectors[0].Voltage)
}

func TestConfig_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := config.DefaultConfig()
	cfg.OCPPID = "TestCP"
	cfg.SecurityProfile = 2
	cfg.TLSCAPath = "/tmp/ca.pem"
	cfg.TLSClientCertPath = "/tmp/client.crt"
	cfg.TLSClientKeyPath = "/tmp/client.key"
	secret := "secret"
	cfg.OCPPPassword = &secret
	require.NoError(t, cfg.Save(path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var stored map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &stored))
	_, hasPassword := stored["ocpp_password"]
	assert.False(t, hasPassword)

	loaded, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "TestCP", loaded.OCPPID)
	assert.Equal(t, 2, loaded.SecurityProfile)
	assert.Equal(t, "/tmp/ca.pem", loaded.TLSCAPath)
	assert.Equal(t, "/tmp/client.crt", loaded.TLSClientCertPath)
	assert.Equal(t, "/tmp/client.key", loaded.TLSClientKeyPath)
	assert.Nil(t, loaded.OCPPPassword)
}

func TestConfig_LoadNonExistent_ReturnsDefault(t *testing.T) {
	loaded, err := config.Load("/tmp/nonexistent-chargeghost-config.json")
	require.NoError(t, err)
	assert.Equal(t, "CP_1", loaded.OCPPID)
}

func TestConfig_EffectiveStationConfigs_LegacySingleStation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.OCPPID = "LegacyCP"
	cfg.ConnectionURL = "wss://example.com/CP"

	effective, err := cfg.EffectiveStationConfigs()
	require.NoError(t, err)
	require.Len(t, effective, 1)
	assert.Equal(t, "LegacyCP", effective[0].Config.OCPPID)
	assert.Equal(t, "wss://example.com/CP", effective[0].Config.ConnectionURL)
	assert.Equal(t, "1.6", effective[0].Config.OCPPVersion)
	assert.Nil(t, effective[0].Config.Stations)
}

// TestConfig_EffectiveStationConfigs_LegacyDetached is a regression test:
// in legacy single-station mode (no Stations entries), EffectiveStationConfigs
// used to hand back the SAME *Config pointer as the caller's live config,
// so mutating the "effective" config (or a runtime built from it) mutated the
// fleet's global config out from under any lock discipline. It must now be a
// detached clone.
func TestConfig_EffectiveStationConfigs_LegacyDetached(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.OCPPID = "LegacyCP"
	cfg.ConnectionURL = "wss://example.com/CP"

	effective, err := cfg.EffectiveStationConfigs()
	require.NoError(t, err)
	require.Len(t, effective, 1)

	effective[0].Config.ConnectionURL = "wss://mutated.example.com/CP"
	effective[0].Config.ChargePointVendor = "Mutated"
	assert.Equal(t, "wss://example.com/CP", cfg.ConnectionURL, "mutating the effective config must not affect the original")
	assert.NotEqual(t, "Mutated", cfg.ChargePointVendor)
	assert.NotSame(t, cfg, effective[0].Config)
}

// TestConfig_MergeStation_ConnectorsDetached is a regression test: mergeStation
// used to alias the global connector slice when a station had no connector
// override, so mutating one station's effective connectors mutated every
// other station (and the global config) sharing the same backing array.
func TestConfig_MergeStation_ConnectorsDetached(t *testing.T) {
	cfg := config.DefaultConfig()
	idA, idB := "CP_A", "CP_B"
	cfg.Stations = []config.StationConfig{{OCPPID: &idA}, {OCPPID: &idB}}

	effective, err := cfg.EffectiveStationConfigs()
	require.NoError(t, err)
	require.Len(t, effective, 2)

	effective[0].Config.Connectors[0].Voltage = 999
	assert.NotEqual(t, 999.0, effective[1].Config.Connectors[0].Voltage, "mutating station A's connectors must not affect station B")
	assert.NotEqual(t, 999.0, cfg.Connectors[0].Voltage, "mutating station A's connectors must not affect the global config")
}

func TestConfig_EffectiveStationConfigs_MultiStationDefaultsAndOverrides(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ChargePointVendor = "TopVendor"
	cfg.OCPPVersion = "1.6"

	ocppID1 := "CP_A"
	ocppID2 := "CP_B"
	url2 := "wss://example.com/{ocpp_id}"
	version201 := "2.0.1"
	connector2 := []config.ConnectorConfig{{Voltage: 400, Current: 32, Phase: 3}}
	cfg.Stations = []config.StationConfig{
		{OCPPID: &ocppID1},
		{OCPPID: &ocppID2, ConnectionURL: &url2, OCPPVersion: &version201, Connectors: connector2},
	}

	effective, err := cfg.EffectiveStationConfigs()
	require.NoError(t, err)
	require.Len(t, effective, 2)

	// Station A inherits top-level values.
	a := effective[0]
	assert.Equal(t, "CP_A", a.Config.OCPPID)
	assert.Equal(t, "TopVendor", a.Config.ChargePointVendor)
	assert.Equal(t, "1.6", a.Config.OCPPVersion)
	assert.Len(t, a.Config.Connectors, 1)
	assert.Equal(t, 230.0, a.Config.Connectors[0].Voltage)

	// Station B overrides connection URL (template expanded), version, and connectors.
	b := effective[1]
	assert.Equal(t, "CP_B", b.Config.OCPPID)
	assert.Equal(t, "TopVendor", b.Config.ChargePointVendor)
	assert.Equal(t, "wss://example.com/CP_B", b.Config.ConnectionURL)
	assert.Equal(t, "2.0.1", b.Config.OCPPVersion)
	require.Len(t, b.Config.Connectors, 1)
	assert.Equal(t, 400.0, b.Config.Connectors[0].Voltage)
	assert.Equal(t, 3, b.Config.Connectors[0].Phase)
}

func TestConfig_EffectiveStationConfigs_Validation(t *testing.T) {
	cfg := config.DefaultConfig()

	// Missing ocpp_id.
	cfg.Stations = []config.StationConfig{{}}
	_, err := cfg.EffectiveStationConfigs()
	require.Error(t, err)

	// Duplicate ocpp_id.
	id := "CP_1"
	cfg.Stations = []config.StationConfig{{OCPPID: &id}, {OCPPID: &id}}
	_, err = cfg.EffectiveStationConfigs()
	require.Error(t, err)

	// Too many stations.
	cfg.Stations = nil
	for i := 0; i < 9; i++ {
		id := fmt.Sprintf("CP_%d", i)
		cfg.Stations = append(cfg.Stations, config.StationConfig{OCPPID: &id})
	}
	_, err = cfg.EffectiveStationConfigs()
	require.Error(t, err)
}

func TestConfig_Sanitized_StripsStationPasswords(t *testing.T) {
	cfg := config.DefaultConfig()
	pass := "secret"
	id := "CP_X"
	cfg.Stations = []config.StationConfig{{OCPPID: &id, OCPPPassword: &pass}}

	sanitized := cfg.Sanitized()
	require.Len(t, sanitized.Stations, 1)
	assert.Nil(t, sanitized.OCPPPassword)
	assert.Nil(t, sanitized.Stations[0].OCPPPassword)
}
