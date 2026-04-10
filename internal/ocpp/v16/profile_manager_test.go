package v16_test

import (
	"testing"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
	v16 "github.com/chargeghost/engine/internal/ocpp/v16"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeAbsoluteProfile(profileID, connectorID, stackLevel int, limitA float64, purpose string) engine.ChargingProfile {
	return engine.ChargingProfile{
		ProfileID:     profileID,
		ConnectorID:   connectorID,
		StackLevel:    stackLevel,
		Purpose:       purpose,
		Kind:          "Absolute",
		StartSchedule: ptr(time.Now().Add(-1 * time.Hour)), // started 1 hour ago
		Schedule: engine.ChargingSchedule{
			ChargingRateUnit: "A",
			Periods: []engine.ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: limitA},
			},
		},
	}
}

func ptr[T any](v T) *T { return &v }

func TestProfileManager_NoProfiles_ReturnsNil(t *testing.T) {
	pm := v16.NewChargingProfileManager()
	limit := pm.GetCompositeLimit(1, 0, time.Now(), 230.0, nil, 1)
	assert.Nil(t, limit, "no profiles should return nil limit")
}

func TestProfileManager_TxDefaultProfile_LimitsCurrentBelowConnector(t *testing.T) {
	pm := v16.NewChargingProfileManager()
	profile := makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile")
	require.NoError(t, pm.SetChargingProfile(1, profile))

	limit := pm.GetCompositeLimit(1, 0, time.Now(), 230.0, nil, 1)
	require.NotNil(t, limit)
	assert.InDelta(t, 16.0, *limit, 0.01)
}

func TestProfileManager_ChargePointMaxProfile_TakesMinWithTxDefault(t *testing.T) {
	pm := v16.NewChargingProfileManager()
	pm.SetChargingProfile(1, makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile"))
	pm.SetChargingProfile(0, makeAbsoluteProfile(2, 0, 0, 8.0, "ChargePointMaxProfile")) // connector 0 = global

	limit := pm.GetCompositeLimit(1, 0, time.Now(), 230.0, nil, 1)
	require.NotNil(t, limit)
	assert.InDelta(t, 8.0, *limit, 0.01) // min(16, 8)
}

func TestProfileManager_HigherStackLevelWins(t *testing.T) {
	pm := v16.NewChargingProfileManager()
	pm.SetChargingProfile(1, makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile"))
	pm.SetChargingProfile(1, makeAbsoluteProfile(2, 1, 1, 24.0, "TxDefaultProfile")) // stackLevel 1 wins

	limit := pm.GetCompositeLimit(1, 0, time.Now(), 230.0, nil, 1)
	require.NotNil(t, limit)
	assert.InDelta(t, 24.0, *limit, 0.01)
}

func TestProfileManager_WattsConverted_ToAmps(t *testing.T) {
	pm := v16.NewChargingProfileManager()
	profile := engine.ChargingProfile{
		ProfileID:     1,
		ConnectorID:   1,
		Purpose:       "TxDefaultProfile",
		Kind:          "Absolute",
		StartSchedule: ptr(time.Now().Add(-1 * time.Hour)),
		Schedule: engine.ChargingSchedule{
			ChargingRateUnit: "W",
			Periods: []engine.ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 3680.0}, // 3680W / 230V / 1phase = 16A
			},
		},
	}
	pm.SetChargingProfile(1, profile)

	limit := pm.GetCompositeLimit(1, 0, time.Now(), 230.0, nil, 1)
	require.NotNil(t, limit)
	assert.InDelta(t, 16.0, *limit, 0.01)
}

func TestProfileManager_ClearByProfileID(t *testing.T) {
	pm := v16.NewChargingProfileManager()
	pm.SetChargingProfile(1, makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile"))

	profileID := 1
	require.NoError(t, pm.ClearChargingProfile(nil, &profileID, nil, nil))
	limit := pm.GetCompositeLimit(1, 0, time.Now(), 230.0, nil, 1)
	assert.Nil(t, limit)
}

func TestProfileManager_ClearByStackLevel(t *testing.T) {
	pm := v16.NewChargingProfileManager()
	pm.SetChargingProfile(1, makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile"))
	pm.SetChargingProfile(1, makeAbsoluteProfile(2, 1, 1, 24.0, "TxDefaultProfile"))

	stackLevel := "1"
	require.NoError(t, pm.ClearChargingProfile(nil, nil, nil, &stackLevel))

	_, ok := pm.GetChargingProfile(1, 1)
	assert.True(t, ok)
	_, ok = pm.GetChargingProfile(1, 2)
	assert.False(t, ok)
}

func TestProfileManager_GetCompositeSchedule(t *testing.T) {
	pm := v16.NewChargingProfileManager()
	pm.SetChargingProfile(1, makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile"))

	now := time.Now()
	periods, err := pm.GetCompositeSchedule(1, 0, now, 3600, 230.0, nil, 1)
	require.NoError(t, err)
	assert.NotEmpty(t, periods)
	assert.InDelta(t, 16.0, periods[0].Limit, 0.01)
}
