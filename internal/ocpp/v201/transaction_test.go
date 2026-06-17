package v201

import (
	"testing"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Ensure types is referenced (used by NewDateTime in optional-fields test).
var _ = ocpp201types.NewDateTime

func TestTransactionEventBuilder_Started(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	require.NotEmpty(t, b.TransactionID())

	now := time.Now()
	idToken := ocpp201types.IdToken{IdToken: "RFID123", Type: ocpp201types.IdTokenTypeISO14443}
	meter := makeMeterValue(1000.0, now, string(ocpp201types.ReadingContextTransactionBegin))

	req := b.Started(idToken, &meter, now)
	assert.Equal(t, transactions.TransactionEventStarted, req.EventType)
	assert.Equal(t, transactions.TriggerReasonAuthorized, req.TriggerReason)
	assert.Equal(t, 0, req.SequenceNo)
	assert.Equal(t, b.TransactionID(), req.TransactionInfo.TransactionID)
	assert.NotNil(t, req.IDToken)
	assert.Len(t, req.MeterValue, 1)
	assert.Equal(t, ocpp201types.ReadingContextTransactionBegin, req.MeterValue[0].SampledValue[0].Context)
}

func TestTransactionEventBuilder_Updated(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	now := time.Now()
	meter := makeMeterValue(2000.0, now, string(ocpp201types.ReadingContextSampleClock))

	// First call increments seqNo
	_ = b.Started(ocpp201types.IdToken{IdToken: "X", Type: ocpp201types.IdTokenTypeISO14443}, nil, now)
	req := b.Updated(transactions.TriggerReasonMeterValuePeriodic, &meter, now)

	assert.Equal(t, transactions.TransactionEventUpdated, req.EventType)
	assert.Equal(t, 1, req.SequenceNo)
	assert.Equal(t, transactions.TriggerReasonMeterValuePeriodic, req.TriggerReason)
	assert.Equal(t, ocpp201types.ReadingContextSampleClock, req.MeterValue[0].SampledValue[0].Context)
}

func TestTransactionEventBuilder_Ended(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	now := time.Now()
	meter := makeMeterValue(5000.0, now, string(ocpp201types.ReadingContextTransactionEnd))

	_ = b.Started(ocpp201types.IdToken{IdToken: "X", Type: ocpp201types.IdTokenTypeISO14443}, nil, now)
	_ = b.Updated(transactions.TriggerReasonMeterValuePeriodic, nil, now)
	req := b.Ended(transactions.ReasonRemote, &meter, now, nil)

	assert.Equal(t, transactions.TransactionEventEnded, req.EventType)
	assert.Equal(t, 2, req.SequenceNo)
	assert.Equal(t, transactions.ReasonRemote, req.TransactionInfo.StoppedReason)
	assert.Equal(t, ocpp201types.ReadingContextTransactionEnd, req.MeterValue[0].SampledValue[0].Context)
	assert.Nil(t, req.IDToken)
}

func TestTransactionEventBuilder_Ended_WithIDToken(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	now := time.Now()
	meter := makeMeterValue(5000.0, now, string(ocpp201types.ReadingContextTransactionEnd))
	token := ocpp201types.IdToken{IdToken: "RFID-001", Type: ocpp201types.IdTokenTypeISO14443}

	_ = b.Started(token, nil, now)
	req := b.Ended(transactions.ReasonLocal, &meter, now, &token)

	require.NotNil(t, req.IDToken)
	assert.Equal(t, "RFID-001", req.IDToken.IdToken)
	assert.Equal(t, ocpp201types.IdTokenTypeISO14443, req.IDToken.Type)
}

func TestTransactionEventBuilder_SeqNoIncrements(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	now := time.Now()
	token := ocpp201types.IdToken{IdToken: "X", Type: ocpp201types.IdTokenTypeISO14443}

	r1 := b.Started(token, nil, now)
	r2 := b.Updated(transactions.TriggerReasonMeterValuePeriodic, nil, now)
	r3 := b.Updated(transactions.TriggerReasonMeterValuePeriodic, nil, now)
	r4 := b.Ended(transactions.ReasonLocal, nil, now, nil)

	assert.Equal(t, 0, r1.SequenceNo)
	assert.Equal(t, 1, r2.SequenceNo)
	assert.Equal(t, 2, r3.SequenceNo)
	assert.Equal(t, 3, r4.SequenceNo)
}

func TestTriggerReasonForMeterContext(t *testing.T) {
	assert.Equal(t, transactions.TriggerReasonMeterValueClock, triggerReasonForMeterContext("Sample.Clock"))
	assert.Equal(t, transactions.TriggerReasonMeterValuePeriodic, triggerReasonForMeterContext("Sample.Periodic"))
	assert.Equal(t, transactions.TriggerReasonTrigger, triggerReasonForMeterContext("Trigger"))
	assert.Equal(t, transactions.TriggerReasonMeterValuePeriodic, triggerReasonForMeterContext("Transaction.Begin"))
}

func TestNormalizeMeterContext(t *testing.T) {
	assert.Equal(t, ocpp201types.ReadingContextSampleClock, normalizeMeterContext("Sample.Clock"))
	assert.Equal(t, ocpp201types.ReadingContextTrigger, normalizeMeterContext("Trigger"))
	assert.Equal(t, ocpp201types.ReadingContextOther, normalizeMeterContext("not-valid"))
}

func TestTransactionEventBuilder_Updated_ChargingState(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	now := time.Now()
	meter := makeMeterValue(2000.0, now, string(ocpp201types.ReadingContextSampleClock))
	token := ocpp201types.IdToken{IdToken: "X", Type: ocpp201types.IdTokenTypeISO14443}

	// Prime seqNo with a Started event.
	_ = b.Started(token, nil, now)

	req := b.Updated(transactions.TriggerReasonChargingStateChanged, &meter, now, transactions.ChargingStateSuspendedEV)
	assert.Equal(t, transactions.TransactionEventUpdated, req.EventType)
	assert.Equal(t, transactions.TriggerReasonChargingStateChanged, req.TriggerReason)
	assert.Equal(t, transactions.ChargingStateSuspendedEV, req.TransactionInfo.ChargingState)
	assert.Equal(t, 1, req.SequenceNo)
}

func TestTransactionEventBuilder_Updated_DefaultChargingState(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	now := time.Now()
	token := ocpp201types.IdToken{IdToken: "X", Type: ocpp201types.IdTokenTypeISO14443}

	_ = b.Started(token, nil, now)

	// No chargingState argument — defaults to Charging for backward compat.
	req := b.Updated(transactions.TriggerReasonMeterValuePeriodic, nil, now)
	assert.Equal(t, transactions.ChargingStateCharging, req.TransactionInfo.ChargingState)
}

func TestTransactionEventBuilder_Updated_EmptyChargingStateDefaults(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	now := time.Now()
	token := ocpp201types.IdToken{IdToken: "X", Type: ocpp201types.IdTokenTypeISO14443}

	_ = b.Started(token, nil, now)

	// Empty string falls back to default Charging state.
	req := b.Updated(transactions.TriggerReasonMeterValuePeriodic, nil, now, "")
	assert.Equal(t, transactions.ChargingStateCharging, req.TransactionInfo.ChargingState)
}

func TestEngineStateToChargingState(t *testing.T) {
	cases := []struct {
		engineState string
		wantState   transactions.ChargingState
		wantOK      bool
	}{
		{"Charging", transactions.ChargingStateCharging, true},
		{"SuspendedEV", transactions.ChargingStateSuspendedEV, true},
		{"SuspendedEVSE", transactions.ChargingStateSuspendedEVSE, true},
		{"Preparing", transactions.ChargingStateEVConnected, true},
		{"Available", "", false},
		{"Reserved", "", false},
		{"Unavailable", "", false},
		{"Faulted", "", false},
		{"Finishing", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := engineStateToChargingState(tc.engineState)
		assert.Equal(t, tc.wantOK, ok, "engineStateToChargingState(%q).ok", tc.engineState)
		assert.Equal(t, tc.wantState, got, "engineStateToChargingState(%q) state", tc.engineState)
	}
}

func TestTransactionEventBuilder_SetLastCost(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	require.Nil(t, b.Cost(), "no cost set initially")

	b.SetLastCost(12.34)
	require.NotNil(t, b.Cost())
	assert.InDelta(t, 12.34, *b.Cost(), 0.001)

	b.SetLastCost(15.99)
	assert.InDelta(t, 15.99, *b.Cost(), 0.001, "second SetLastCost overwrites the first")
}

func TestConvertChargingProfile201_NilProfile(t *testing.T) {
	assert.Nil(t, convertChargingProfile201(nil, 1))
}

func TestConvertChargingProfile201_EmptySchedule(t *testing.T) {
	p := &ocpp201types.ChargingProfile{
		ID:                     5,
		StackLevel:             1,
		ChargingProfilePurpose: ocpp201types.ChargingProfilePurposeTxProfile,
		ChargingProfileKind:    ocpp201types.ChargingProfileKindAbsolute,
	}
	assert.Nil(t, convertChargingProfile201(p, 1))
}

func TestConvertChargingProfile201_BasicFields(t *testing.T) {
	period1 := 2
	p := &ocpp201types.ChargingProfile{
		ID:                     42,
		StackLevel:             3,
		ChargingProfilePurpose: ocpp201types.ChargingProfilePurposeTxDefaultProfile,
		ChargingProfileKind:    ocpp201types.ChargingProfileKindAbsolute,
		TransactionID:          "tx-123",
		ChargingSchedule: []ocpp201types.ChargingSchedule{
			{
				ID:               1,
				ChargingRateUnit: ocpp201types.ChargingRateUnitAmperes,
				ChargingSchedulePeriod: []ocpp201types.ChargingSchedulePeriod{
					{StartPeriod: 0, Limit: 16.0, NumberPhases: &period1},
					{StartPeriod: 600, Limit: 32.0},
				},
			},
		},
	}

	got := convertChargingProfile201(p, 7)
	require.NotNil(t, got)
	assert.Equal(t, 42, got.ProfileID)
	assert.Equal(t, 7, got.ConnectorID)
	assert.Equal(t, 3, got.StackLevel)
	assert.Equal(t, "TxDefaultProfile", got.Purpose)
	assert.Equal(t, "Absolute", got.Kind)
	assert.Equal(t, "tx-123", got.TransactionID)
	assert.Equal(t, "A", got.Schedule.ChargingRateUnit)
	assert.Len(t, got.Schedule.Periods, 2)
	assert.Equal(t, 0, got.Schedule.Periods[0].StartPeriod)
	assert.Equal(t, 16.0, got.Schedule.Periods[0].Limit)
	require.NotNil(t, got.Schedule.Periods[0].NumberPhases)
	assert.Equal(t, 2, *got.Schedule.Periods[0].NumberPhases)
	assert.Equal(t, 600, got.Schedule.Periods[1].StartPeriod)
	assert.Equal(t, 32.0, got.Schedule.Periods[1].Limit)
	assert.Nil(t, got.Schedule.Periods[1].NumberPhases)
}

func TestConvertChargingProfile201_MultipleSchedulesFlattensFirst(t *testing.T) {
	// OCPP 2.0.1 allows up to 3 ChargingSchedules per profile.  The engine
	// representation is a single schedule, so we flatten the first one and
	// ignore subsequent schedules.
	p := &ocpp201types.ChargingProfile{
		ID:                     1,
		StackLevel:             0,
		ChargingProfilePurpose: ocpp201types.ChargingProfilePurposeChargingStationMaxProfile,
		ChargingProfileKind:    ocpp201types.ChargingProfileKindAbsolute,
		ChargingSchedule: []ocpp201types.ChargingSchedule{
			{
				ID:               1,
				ChargingRateUnit: ocpp201types.ChargingRateUnitWatts,
				ChargingSchedulePeriod: []ocpp201types.ChargingSchedulePeriod{
					{StartPeriod: 0, Limit: 11000.0},
				},
			},
			{
				ID:               2,
				ChargingRateUnit: ocpp201types.ChargingRateUnitWatts,
				ChargingSchedulePeriod: []ocpp201types.ChargingSchedulePeriod{
					{StartPeriod: 0, Limit: 22000.0},
				},
			},
		},
	}
	got := convertChargingProfile201(p, 2)
	require.NotNil(t, got)
	assert.Equal(t, "W", got.Schedule.ChargingRateUnit)
	assert.Equal(t, 11000.0, got.Schedule.Periods[0].Limit)
}

func TestConvertChargingProfile201_OptionalFields(t *testing.T) {
	from := ocpp201types.NewDateTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	to := ocpp201types.NewDateTime(time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC))
	start := ocpp201types.NewDateTime(time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC))
	dur := 3600
	p := &ocpp201types.ChargingProfile{
		ID:                     7,
		StackLevel:             0,
		ChargingProfilePurpose: ocpp201types.ChargingProfilePurposeTxProfile,
		ChargingProfileKind:    ocpp201types.ChargingProfileKindRecurring,
		RecurrencyKind:         ocpp201types.RecurrencyKindDaily,
		ValidFrom:              from,
		ValidTo:                to,
		ChargingSchedule: []ocpp201types.ChargingSchedule{
			{
				ID:               1,
				StartSchedule:    start,
				Duration:         &dur,
				ChargingRateUnit: ocpp201types.ChargingRateUnitAmperes,
				ChargingSchedulePeriod: []ocpp201types.ChargingSchedulePeriod{
					{StartPeriod: 0, Limit: 10.0},
				},
			},
		},
	}
	got := convertChargingProfile201(p, 3)
	require.NotNil(t, got)
	assert.Equal(t, "Daily", got.RecurrencyKind)
	require.NotNil(t, got.ValidFrom)
	assert.Equal(t, 2026, got.ValidFrom.Year())
	require.NotNil(t, got.ValidTo)
	assert.Equal(t, 2026, got.ValidTo.Year())
	require.NotNil(t, got.Schedule.StartSchedule)
	assert.Equal(t, 2026, got.Schedule.StartSchedule.Year())
	assert.Equal(t, 3600, got.Schedule.Duration)
}
