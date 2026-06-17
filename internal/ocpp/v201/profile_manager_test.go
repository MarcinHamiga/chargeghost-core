package v201

import (
	"testing"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	engine "github.com/chargeghost/engine/internal/engine"
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

func TestChargingProfileManager201_ClearTxProfilesForTransaction(t *testing.T) {
	pm := NewChargingProfileManager201()

	// TxProfile scoped to tx-A
	pm.SetProfile(1, types.ChargingProfile{
		ID: 1, StackLevel: 0,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxProfile,
		TransactionID:          "tx-A",
		ChargingProfileKind:    types.ChargingProfileKindAbsolute,
		ChargingSchedule:       []types.ChargingSchedule{{ID: 1, ChargingRateUnit: types.ChargingRateUnitAmperes, ChargingSchedulePeriod: []types.ChargingSchedulePeriod{{StartPeriod: 0, Limit: 32}}}},
	})
	// TxProfile scoped to tx-B (must NOT be cleared when tx-A ends)
	pm.SetProfile(1, types.ChargingProfile{
		ID: 2, StackLevel: 0,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxProfile,
		TransactionID:          "tx-B",
		ChargingProfileKind:    types.ChargingProfileKindAbsolute,
		ChargingSchedule:       []types.ChargingSchedule{{ID: 2, ChargingRateUnit: types.ChargingRateUnitAmperes, ChargingSchedulePeriod: []types.ChargingSchedulePeriod{{StartPeriod: 0, Limit: 16}}}},
	})
	// TxDefaultProfile with empty TransactionID (must NOT be cleared — applies to any tx)
	pm.SetProfile(1, types.ChargingProfile{
		ID: 3, StackLevel: 0,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxDefaultProfile,
		TransactionID:          "",
		ChargingProfileKind:    types.ChargingProfileKindAbsolute,
		ChargingSchedule:       []types.ChargingSchedule{{ID: 3, ChargingRateUnit: types.ChargingRateUnitAmperes, ChargingSchedulePeriod: []types.ChargingSchedulePeriod{{StartPeriod: 0, Limit: 10}}}},
	})

	assert.Len(t, pm.GetAllProfiles(), 3)

	cleared := pm.ClearTxProfilesForTransaction("tx-A")
	assert.Equal(t, 1, cleared)

	remaining := pm.GetAllProfiles()
	assert.Len(t, remaining, 2)
	ids := []int{}
	for _, p := range remaining {
		ids = append(ids, p.ID)
	}
	assert.Contains(t, ids, 2)
	assert.Contains(t, ids, 3)

	// Empty transaction ID is a no-op
	cleared = pm.ClearTxProfilesForTransaction("")
	assert.Equal(t, 0, cleared)
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

func TestChargingProfileManager201_TxProfileScoping(t *testing.T) {
	pm := NewChargingProfileManager201()
	now := time.Now().UTC().Truncate(time.Second)

	// TxProfile scoped to transaction A
	txA := "tx-A"
	pm.SetProfile(1, types.ChargingProfile{
		ID:                     10,
		StackLevel:             0,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxProfile,
		TransactionID:          txA,
		ChargingProfileKind:    types.ChargingProfileKindAbsolute,
		ChargingSchedule: []types.ChargingSchedule{{
			ID:               10,
			StartSchedule:    types.NewDateTime(now.Add(-time.Hour)),
			ChargingRateUnit: types.ChargingRateUnitAmperes,
			ChargingSchedulePeriod: []types.ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 32},
			},
		}},
	})

	// TxProfile scoped to transaction B
	txB := "tx-B"
	pm.SetProfile(1, types.ChargingProfile{
		ID:                     11,
		StackLevel:             0,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxProfile,
		TransactionID:          txB,
		ChargingProfileKind:    types.ChargingProfileKindAbsolute,
		ChargingSchedule: []types.ChargingSchedule{{
			ID:               11,
			StartSchedule:    types.NewDateTime(now.Add(-time.Hour)),
			ChargingRateUnit: types.ChargingRateUnitAmperes,
			ChargingSchedulePeriod: []types.ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 16},
			},
		}},
	})

	// When asking for tx-A, resolveLimit must only consider A
	limit := pm.resolveLimit(types.ChargingProfilePurposeTxProfile, 1, now, 230.0, nil, 1, txA, true)
	require.NotNil(t, limit)
	assert.InDelta(t, 32.0, *limit, 0.001)

	// When asking for tx-B, resolveLimit must only consider B
	limit = pm.resolveLimit(types.ChargingProfilePurposeTxProfile, 1, now, 230.0, nil, 1, txB, true)
	require.NotNil(t, limit)
	assert.InDelta(t, 16.0, *limit, 0.001)

	// When asking for an unknown transaction, TxProfile must NOT be applied
	limit = pm.resolveLimit(types.ChargingProfilePurposeTxProfile, 1, now, 230.0, nil, 1, "tx-UNKNOWN", true)
	assert.Nil(t, limit)

	// Clearing all profiles
	cleared := pm.ClearProfile(nil, nil, nil, nil)
	_ = cleared
	profiles := pm.GetAllProfiles()
	assert.Empty(t, profiles)
}

func TestChargingProfileManager201_TxDefaultProfileScoping(t *testing.T) {
	pm := NewChargingProfileManager201()
	now := time.Now().UTC().Truncate(time.Second)

	// TxDefaultProfile applies to ANY transaction on the EVSE
	pm.SetProfile(1, types.ChargingProfile{
		ID:                     20,
		StackLevel:             0,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxDefaultProfile,
		TransactionID:          "", // empty means "any transaction on EVSE"
		ChargingProfileKind:    types.ChargingProfileKindAbsolute,
		ChargingSchedule: []types.ChargingSchedule{{
			ID:               20,
			StartSchedule:    types.NewDateTime(now.Add(-time.Hour)),
			ChargingRateUnit: types.ChargingRateUnitAmperes,
			ChargingSchedulePeriod: []types.ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 10},
			},
		}},
	})

	// TxDefaultProfile scoped to a specific transaction (higher stack level wins)
	pm.SetProfile(1, types.ChargingProfile{
		ID:                     21,
		StackLevel:             1,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxDefaultProfile,
		TransactionID:          "tx-X",
		ChargingProfileKind:    types.ChargingProfileKindAbsolute,
		ChargingSchedule: []types.ChargingSchedule{{
			ID:               21,
			StartSchedule:    types.NewDateTime(now.Add(-time.Hour)),
			ChargingRateUnit: types.ChargingRateUnitAmperes,
			ChargingSchedulePeriod: []types.ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 20},
			},
		}},
	})

	// For tx-X, the tx-X TxDefaultProfile (ID 21, stackLevel 1) wins.
	limit := pm.resolveLimit(types.ChargingProfilePurposeTxDefaultProfile, 1, now, 230.0, nil, 1, "tx-X", false)
	require.NotNil(t, limit)
	assert.InDelta(t, 20.0, *limit, 0.001)

	// For tx-Y (a different transaction), only the empty-TxDefaultProfile applies.
	limit = pm.resolveLimit(types.ChargingProfilePurposeTxDefaultProfile, 1, now, 230.0, nil, 1, "tx-Y", false)
	require.NotNil(t, limit)
	assert.InDelta(t, 10.0, *limit, 0.001)
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

func TestChargingProfileManager201_TxProfileRejectsEmptyTransactionID(t *testing.T) {
	pm := NewChargingProfileManager201()
	// TxProfile without a TransactionID is invalid per OCPP 2.0.1 §3.21
	// and must NOT be stored.
	pm.SetProfile(1, types.ChargingProfile{
		ID:                     1,
		StackLevel:             0,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxProfile,
		// TransactionID is intentionally empty
	})
	assert.Empty(t, pm.GetAllProfiles(), "TxProfile without TransactionID must be rejected")
}

func TestChargingProfileManager201_TxDefaultProfileAllowsEmptyTransactionID(t *testing.T) {
	pm := NewChargingProfileManager201()
	// TxDefaultProfile with empty TransactionID means "applies to any
	// transaction on the EVSE" — that is valid.
	pm.SetProfile(1, types.ChargingProfile{
		ID:                     2,
		StackLevel:             0,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxDefaultProfile,
		// TransactionID is intentionally empty
	})
	assert.Len(t, pm.GetAllProfiles(), 1, "TxDefaultProfile with empty txId must be stored")
}

func TestChargingProfileManager201_TxEvseResolverRewritesEVSE(t *testing.T) {
	pm := NewChargingProfileManager201()
	// Install a resolver that maps a transaction id to EVSE 7.
	pm.SetTxEvseResolver(func(txID string) (int, bool) {
		if txID == "tx-42" {
			return 7, true
		}
		return 0, false
	})

	// Set a TxProfile with evseID=0; the manager should rewrite it to 7.
	pm.SetProfile(0, types.ChargingProfile{
		ID:                     1,
		StackLevel:             0,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxProfile,
		TransactionID:          "tx-42",
		ChargingProfileKind:    types.ChargingProfileKindAbsolute,
		ChargingSchedule: []types.ChargingSchedule{{
			ID:               1,
			ChargingRateUnit: types.ChargingRateUnitAmperes,
			ChargingSchedulePeriod: []types.ChargingSchedulePeriod{{
				StartPeriod: 0, Limit: 32,
			}},
		}},
	})

	// Confirm the profile is stored under EVSE 7 (the resolved evseId).
	// The manager's internal map is keyed by profile ID, so we verify the
	// profile with ID=1 has been re-scoped by looking it up via EVSE 7.
	evseID := 7
	profiles := pm.GetFilteredProfiles(&evseID, nil, nil, nil)
	require.Len(t, profiles, 1)
	assert.Equal(t, 1, profiles[0].ID)
	// And it must NOT be findable under EVSE 0 (where the caller originally
	// sent it).
	evse0 := 0
	profiles0 := pm.GetFilteredProfiles(&evse0, nil, nil, nil)
	// Note: GetFilteredProfiles filters by `mp.evseID != *evseID`, so an
	// evse0 query would not match a profile stored at evse 7.  Verify the
	// profile we just stored shows up exactly once.
	assert.NotEqual(t, 1, len(profiles0), "profile should not be visible under evse 0")
}

func TestConvertChargingProfile201_PreservesTransactionAndScheduleDetails(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	duration := 900
	minChargingRate := 7.5
	phases := 3

	profile := convertChargingProfile201(&types.ChargingProfile{
		ID:                     88,
		StackLevel:             2,
		ChargingProfilePurpose: types.ChargingProfilePurposeTxProfile,
		ChargingProfileKind:    types.ChargingProfileKindAbsolute,
		TransactionID:          "tx-convert-88",
		ChargingSchedule: []types.ChargingSchedule{{
			ID:               88,
			StartSchedule:    types.NewDateTime(now),
			Duration:         &duration,
			ChargingRateUnit: types.ChargingRateUnitAmperes,
			MinChargingRate:  &minChargingRate,
			ChargingSchedulePeriod: []types.ChargingSchedulePeriod{{
				StartPeriod:  0,
				Limit:        24,
				NumberPhases: &phases,
			}},
		}},
	}, 4)

	require.NotNil(t, profile)
	assert.Equal(t, 88, profile.ProfileID)
	assert.Equal(t, 4, profile.ConnectorID)
	assert.Equal(t, "tx-convert-88", profile.TransactionID)
	assert.Equal(t, duration, profile.Schedule.Duration)
	assert.Equal(t, minChargingRate, profile.Schedule.MinChargingRate)
	require.NotNil(t, profile.StartSchedule)
	assert.True(t, profile.StartSchedule.Equal(now))
	require.Len(t, profile.Schedule.Periods, 1)
	require.NotNil(t, profile.Schedule.Periods[0].NumberPhases)
	assert.Equal(t, phases, *profile.Schedule.Periods[0].NumberPhases)
}

func TestChargingProfileManager201_EngineProfileRoundTripPreservesV201Fields(t *testing.T) {
	pm := NewChargingProfileManager201()
	now := time.Now().UTC().Truncate(time.Second)
	minChargingRate := 6.0
	phases := 3

	profile := engine.ChargingProfile{
		ProfileID:      41,
		ConnectorID:    2,
		StackLevel:     3,
		Purpose:        string(types.ChargingProfilePurposeTxProfile),
		Kind:           string(types.ChargingProfileKindAbsolute),
		TransactionID:  "tx-engine-41",
		StartSchedule:  &now,
		RecurrencyKind: string(types.RecurrencyKindDaily),
		Schedule: engine.ChargingSchedule{
			Duration:         1800,
			ChargingRateUnit: string(types.ChargingRateUnitAmperes),
			MinChargingRate:  minChargingRate,
			Periods: []engine.ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 16, NumberPhases: &phases},
			},
		},
	}

	require.NoError(t, pm.SetChargingProfile(2, profile))

	stored := pm.GetAllProfiles()
	require.Len(t, stored, 1)
	assert.Equal(t, "tx-engine-41", stored[0].TransactionID)
	require.Len(t, stored[0].ChargingSchedule, 1)
	require.NotNil(t, stored[0].ChargingSchedule[0].MinChargingRate)
	assert.Equal(t, minChargingRate, *stored[0].ChargingSchedule[0].MinChargingRate)

	got, ok := pm.GetChargingProfile(2, 41)
	require.True(t, ok)
	assert.Equal(t, "tx-engine-41", got.TransactionID)
	assert.Equal(t, minChargingRate, got.Schedule.MinChargingRate)
	require.Len(t, got.Schedule.Periods, 1)
	require.NotNil(t, got.Schedule.Periods[0].NumberPhases)
	assert.Equal(t, phases, *got.Schedule.Periods[0].NumberPhases)
}
