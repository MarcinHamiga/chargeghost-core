package v201

import (
	"testing"
	"time"

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

	pm.SetProfile(1, profile)
	profiles := pm.GetAllProfiles()
	require.Len(t, profiles, 1)
	assert.Equal(t, 1, profiles[0].ID)
}

func TestChargingProfileManager201_Clear(t *testing.T) {
	pm := NewChargingProfileManager201()
	p1 := types.ChargingProfile{ID: 1, StackLevel: 0, ChargingProfilePurpose: types.ChargingProfilePurposeTxDefaultProfile}
	p2 := types.ChargingProfile{ID: 2, StackLevel: 1, ChargingProfilePurpose: types.ChargingProfilePurposeTxDefaultProfile}

	pm.SetProfile(1, p1)
	pm.SetProfile(2, p2)

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

func TestChargingProfileManager201_Filter(t *testing.T) {
	pm := NewChargingProfileManager201()
	p1 := types.ChargingProfile{ID: 1, StackLevel: 0, ChargingProfilePurpose: types.ChargingProfilePurposeTxDefaultProfile}
	p2 := types.ChargingProfile{ID: 2, StackLevel: 1, ChargingProfilePurpose: types.ChargingProfilePurposeTxDefaultProfile}

	pm.SetProfile(1, p1)
	pm.SetProfile(2, p2)

	// Filter by evseID
	evseID := 1
	profiles := pm.GetFilteredProfiles(&evseID, nil, nil, nil)
	assert.Len(t, profiles, 1)
	assert.Equal(t, 1, profiles[0].ID)

	// Filter by purpose
	purpose := types.ChargingProfilePurposeTxDefaultProfile
	profiles = pm.GetFilteredProfiles(nil, nil, &purpose, nil)
	assert.Len(t, profiles, 2)
}

func TestChargingProfileManager201_GetCompositeSchedule(t *testing.T) {
	pm := NewChargingProfileManager201()
	now := time.Now().UTC().Truncate(time.Second)
	pm.SetProfile(1, types.ChargingProfile{
		ID:                     7,
		StackLevel:             0,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxDefaultProfile,
		ChargingProfileKind:    types.ChargingProfileKindAbsolute,
		ChargingSchedule: []types.ChargingSchedule{{
			ID:               7,
			StartSchedule:    types.NewDateTime(now),
			ChargingRateUnit: types.ChargingRateUnitAmperes,
			ChargingSchedulePeriod: []types.ChargingSchedulePeriod{{
				StartPeriod: 0,
				Limit:       16,
			}},
		}},
	})

	periods, err := pm.GetCompositeSchedule(1, 0, now, 3600, 230, nil, 1)
	require.NoError(t, err)
	require.Len(t, periods, 1)
	assert.Equal(t, 0, periods[0].StartPeriod)
	assert.Equal(t, 16.0, periods[0].Limit)
}
