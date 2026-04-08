package v201

import (
	"testing"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChargingProfileManager201_SetAndGet(t *testing.T) {
	pm := NewChargingProfileManager201()
	profile := types.ChargingProfile{
		ID:                     1,
		StackLevel:             0,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxDefaultProfile,
		ChargingProfileKind:    types.ChargingProfileKindAbsolute,
		ChargingSchedule: []types.ChargingSchedule{
			{
				ID:               1,
				ChargingRateUnit: types.ChargingRateUnitWatts,
				ChargingSchedulePeriod: []types.ChargingSchedulePeriod{
					{StartPeriod: 0, Limit: 11000},
				},
			},
		},
	}

	pm.SetProfile(profile)
	profiles := pm.GetAllProfiles()
	require.Len(t, profiles, 1)
	assert.Equal(t, 1, profiles[0].ID)
}

func TestChargingProfileManager201_Clear(t *testing.T) {
	pm := NewChargingProfileManager201()
	p1 := types.ChargingProfile{ID: 1, StackLevel: 0, ChargingProfilePurpose: types.ChargingProfilePurposeTxDefaultProfile}
	p2 := types.ChargingProfile{ID: 2, StackLevel: 1, ChargingProfilePurpose: types.ChargingProfilePurposeTxDefaultProfile}
	
	pm.SetProfile(p1)
	pm.SetProfile(p2)
	
	// Clear by ID
	id := 1
	cleared := pm.ClearProfile(&id, nil, nil, nil)
	assert.Equal(t, 1, cleared)
	assert.Len(t, pm.GetAllProfiles(), 1)
	
	// Clear by StackLevel
	stack := 1
	cleared = pm.ClearProfile(nil, nil, nil, &stack)
	assert.Equal(t, 1, cleared)
	assert.Len(t, pm.GetAllProfiles(), 0)
}
