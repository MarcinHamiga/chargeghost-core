package v16

import (
	"testing"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigKeyManager_SaveLoadState(t *testing.T) {
	dir := t.TempDir()

	m := NewConfigKeyManager()
	m.SetConfigValue("HeartbeatInterval", "120")
	m.SetConfigValue("MeterValueSampleInterval", "15")

	require.NoError(t, m.SaveState(dir))

	m2 := NewConfigKeyManager()
	require.NoError(t, m2.LoadState(dir))

	assert.Equal(t, "120", m2.GetConfigValue("HeartbeatInterval"))
	assert.Equal(t, "15", m2.GetConfigValue("MeterValueSampleInterval"))
	// Read-only keys should retain defaults.
	assert.Equal(t, "1", m2.GetConfigValue("NumberOfConnectors"))
}

func TestConfigKeyManager_LoadState_MissingFile(t *testing.T) {
	m := NewConfigKeyManager()
	err := m.LoadState(t.TempDir())
	assert.NoError(t, err)
	// Defaults should remain.
	assert.Equal(t, "300", m.GetConfigValue("HeartbeatInterval"))
}

func TestChargingProfileManager_SaveLoadState(t *testing.T) {
	dir := t.TempDir()

	m := NewChargingProfileManager()
	now := time.Now()
	require.NoError(t, m.SetChargingProfile(1, engine.ChargingProfile{
		ProfileID: 1, ConnectorID: 1, StackLevel: 0,
		Purpose: "TxDefaultProfile", Kind: "Absolute",
		StartSchedule: &now,
		Schedule: engine.ChargingSchedule{
			Duration: 3600, ChargingRateUnit: "A",
			Periods: []engine.ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 32.0},
			},
		},
	}))

	require.NoError(t, m.SaveState(dir))

	m2 := NewChargingProfileManager()
	require.NoError(t, m2.LoadState(dir))

	profiles := m2.GetChargingProfiles()
	require.Len(t, profiles, 1)
	assert.Equal(t, 1, profiles[0].ProfileID)
	assert.Equal(t, "TxDefaultProfile", profiles[0].Purpose)
	assert.Equal(t, 32.0, profiles[0].Schedule.Periods[0].Limit)
}

func TestChargingProfileManager_LoadState_MissingFile(t *testing.T) {
	m := NewChargingProfileManager()
	err := m.LoadState(t.TempDir())
	assert.NoError(t, err)
	assert.Empty(t, m.GetChargingProfiles())
}
