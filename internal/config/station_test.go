package config_test

import (
	"fmt"
	"testing"

	"github.com/chargeghost/engine/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStationConfig_StationID_FromID(t *testing.T) {
	id := "station-1"
	ocppID := "CP_1"
	sc := config.StationConfig{ID: &id, OCPPID: &ocppID}
	assert.Equal(t, "station-1", sc.StationID())
}

func TestStationConfig_StationID_FromOCPPID(t *testing.T) {
	ocppID := "CP_1"
	sc := config.StationConfig{OCPPID: &ocppID}
	assert.Equal(t, "CP_1", sc.StationID())
}

func TestStationConfig_IsEnabled_Default(t *testing.T) {
	sc := config.StationConfig{}
	assert.True(t, sc.IsEnabled())
}

func TestStationConfig_IsEnabled_Explicit(t *testing.T) {
	f := false
	sc := config.StationConfig{Enabled: &f}
	assert.False(t, sc.IsEnabled())
}

func TestConfig_FindStation(t *testing.T) {
	cfg := config.DefaultConfig()
	id := "station-1"
	ocppID := "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}

	sc, idx, found := cfg.FindStation("station-1")
	require.True(t, found)
	assert.Equal(t, 0, idx)
	assert.Equal(t, "CP_1", *sc.OCPPID)

	_, _, found = cfg.FindStation("missing")
	assert.False(t, found)
}

func TestConfig_UpsertStation_AddAndUpdate(t *testing.T) {
	cfg := config.DefaultConfig()
	id := "station-1"
	ocppID := "CP_1"
	require.NoError(t, cfg.UpsertStation(config.StationConfig{ID: &id, OCPPID: &ocppID}))
	assert.Len(t, cfg.Stations, 1)

	url := "wss://example.com/CP_1"
	require.NoError(t, cfg.UpsertStation(config.StationConfig{ID: &id, OCPPID: &ocppID, ConnectionURL: &url}))
	assert.Len(t, cfg.Stations, 1)
	assert.Equal(t, url, *cfg.Stations[0].ConnectionURL)
}

func TestConfig_UpsertStation_MaxStations(t *testing.T) {
	cfg := config.DefaultConfig()
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("s%d", i)
		ocppID := fmt.Sprintf("CP%d", i)
		require.NoError(t, cfg.UpsertStation(config.StationConfig{ID: &id, OCPPID: &ocppID}))
	}
	id := "extra"
	ocppID := "CP_X"
	err := cfg.UpsertStation(config.StationConfig{ID: &id, OCPPID: &ocppID})
	assert.Error(t, err)
}

func TestConfig_RemoveStation(t *testing.T) {
	cfg := config.DefaultConfig()
	id := "station-1"
	ocppID := "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}
	require.NoError(t, cfg.RemoveStation("station-1"))
	assert.Empty(t, cfg.Stations)

	err := cfg.RemoveStation("missing")
	assert.Error(t, err)
}

func TestConfig_ValidateStations(t *testing.T) {
	cfg := config.DefaultConfig()
	assert.NoError(t, cfg.ValidateStations())

	cfg.Stations = []config.StationConfig{{}}
	assert.Error(t, cfg.ValidateStations())

	id := "x"
	cfg.Stations = []config.StationConfig{{OCPPID: &id}, {OCPPID: &id}}
	assert.Error(t, cfg.ValidateStations())
}

// TestConfig_ValidateStations_DuplicateOCPPID is a regression test: two
// stations with distinct station IDs but the same OCPP ID must be rejected.
// The dedup check previously tracked only station IDs, so this passed
// validation and let two stations present the same CSMS identity while
// sharing one keyring password entry.
func TestConfig_ValidateStations_DuplicateOCPPID(t *testing.T) {
	cfg := config.DefaultConfig()
	stationA, stationB := "station-a", "station-b"
	sharedOCPPID := "CP_SHARED"
	cfg.Stations = []config.StationConfig{
		{ID: &stationA, OCPPID: &sharedOCPPID},
		{ID: &stationB, OCPPID: &sharedOCPPID},
	}
	err := cfg.ValidateStations()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate ocpp_id")

	_, err = cfg.EffectiveStationConfigs()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate ocpp_id")
}

func TestConfig_EffectiveStationConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ChargePointVendor = "TopVendor"
	id := "station-1"
	ocppID := "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}

	effective, err := cfg.EffectiveStationConfig("station-1")
	require.NoError(t, err)
	assert.Equal(t, "CP_1", effective.OCPPID)
	assert.Equal(t, "TopVendor", effective.ChargePointVendor)

	_, err = cfg.EffectiveStationConfig("missing")
	assert.Error(t, err)
}

// TestConfig_EffectiveStationConfig_LegacySingleStation is a regression
// test: in legacy single-station mode (no Stations entries), the implicit
// station's ID is the top-level OCPPID, but EffectiveStationConfig used to
// look it up only via FindStation, which searches the (empty) Stations
// slice — so it always returned "station not found" for a config that had
// never been migrated to the stations array, breaking every fleet-manager
// lifecycle path (Start, EnableStation, UpdateStation, ...) for that setup.
func TestConfig_EffectiveStationConfig_LegacySingleStation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.OCPPID = "CP_A"
	cfg.ConnectionURL = "wss://example.com/CP_A"

	effective, err := cfg.EffectiveStationConfig("CP_A")
	require.NoError(t, err)
	assert.Equal(t, "CP_A", effective.OCPPID)
	assert.Equal(t, "wss://example.com/CP_A", effective.ConnectionURL)
	assert.Nil(t, effective.Stations)

	_, err = cfg.EffectiveStationConfig("some-other-id")
	assert.Error(t, err)
}

func TestConfig_EffectiveStationConfigs_EnabledFilter(t *testing.T) {
	cfg := config.DefaultConfig()
	id1 := "station-1"
	ocppID1 := "CP_1"
	enabled := true
	id2 := "station-2"
	ocppID2 := "CP_2"
	disabled := false
	cfg.Stations = []config.StationConfig{
		{ID: &id1, OCPPID: &ocppID1, Enabled: &enabled},
		{ID: &id2, OCPPID: &ocppID2, Enabled: &disabled},
	}

	effective, err := cfg.EffectiveStationConfigs()
	require.NoError(t, err)
	require.Len(t, effective, 2)
	assert.True(t, effective[0].Enabled)
	assert.False(t, effective[1].Enabled)
}

func TestConfig_StationIDs(t *testing.T) {
	cfg := config.DefaultConfig()
	assert.Equal(t, []string{"CP_1"}, cfg.StationIDs())

	id := "station-1"
	ocppID := "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}
	assert.Equal(t, []string{"station-1"}, cfg.StationIDs())
}

func TestConfig_Clone(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.OCPPID = "CP_1"
	tag := "tag"
	cfg.RFIDTag = &tag
	id := "station-1"
	ocppID := "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID, Connectors: []config.ConnectorConfig{{Voltage: 230}}}}

	clone := cfg.Clone()
	clone.OCPPID = "CP_2"
	clone.RFIDTag = nil
	clone.Stations[0].Connectors[0].Voltage = 400

	assert.Equal(t, "CP_1", cfg.OCPPID)
	assert.NotNil(t, cfg.RFIDTag)
	assert.Equal(t, 230.0, cfg.Stations[0].Connectors[0].Voltage)
}

func TestStationPersistDirByID(t *testing.T) {
	dir := config.StationPersistDirByID("/base", "station-1")
	assert.Contains(t, dir, "station-1")
	assert.Contains(t, dir, "stations")
}

func TestConfig_Save_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.json"
	cfg := config.DefaultConfig()
	require.NoError(t, cfg.Save(path))

	loaded, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, cfg.OCPPID, loaded.OCPPID)
}
