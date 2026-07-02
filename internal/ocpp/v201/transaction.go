package v201

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
)

// TransactionEventBuilder constructs TransactionEvent requests for a single
// charging session. One builder per active EVSE.
type TransactionEventBuilder struct {
	transactionID string
	seqNo         int
	evseID        int
	connectorID   int
	LastCost      *float64
}

// NewTransactionEventBuilder creates a new builder for the given EVSE and connector.
func NewTransactionEventBuilder(evseID, connectorID int) *TransactionEventBuilder {
	return &TransactionEventBuilder{
		transactionID: uuid.New().String(),
		evseID:        evseID,
		connectorID:   connectorID,
	}
}

// TransactionID returns the unique transaction ID for this session.
func (b *TransactionEventBuilder) TransactionID() string {
	return b.transactionID
}

// SetLastCost caches the running total cost for this transaction. The cost
// is provided by the CSMS via CostUpdated; we surface it through the
// TariffCostCtrlr device-model variable and also make it available to
// callers (e.g. UI / API) via LastCost() for the duration of the
// transaction.
func (b *TransactionEventBuilder) SetLastCost(cost float64) {
	b.LastCost = &cost
}

// Cost returns the latest known cost for the transaction, or nil if the
// CSMS has not yet sent a CostUpdated.
func (b *TransactionEventBuilder) Cost() *float64 {
	return b.LastCost
}

// Started constructs a TransactionEvent with EventType=Started, seqNo=0.
// seqNo is incremented after each call. remoteStartID, when non-nil, echoes
// back the RequestStartTransaction.remoteStartId that initiated this
// transaction so the CSMS can correlate the two (OCPP 2.0.1 §F02).
func (b *TransactionEventBuilder) Started(idToken ocpp201types.IdToken, meterValue *ocpp201types.MeterValue, timestamp time.Time, remoteStartID *int) *transactions.TransactionEventRequest {
	seq := b.seqNo
	b.seqNo++

	connID := b.connectorID
	req := transactions.NewTransactionEventRequest(
		transactions.TransactionEventStarted,
		ocpp201types.NewDateTime(timestamp),
		transactions.TriggerReasonAuthorized,
		seq,
		transactions.Transaction{
			TransactionID: b.transactionID,
			ChargingState: transactions.ChargingStateEVConnected,
			RemoteStartID: remoteStartID,
		},
	)
	req.IDToken = &idToken
	req.Evse = &ocpp201types.EVSE{ID: b.evseID, ConnectorID: &connID}
	if meterValue != nil {
		req.MeterValue = []ocpp201types.MeterValue{*meterValue}
	}
	return req
}

// Updated constructs a TransactionEvent with EventType=Updated, incrementing seqNo.
// The chargingState argument allows the caller to report the new charging state
// (Charging, SuspendedEV, SuspendedEVSE, etc.) so the CSMS can observe
// transitions in real time rather than only at the start and end of a session.
func (b *TransactionEventBuilder) Updated(trigger transactions.TriggerReason, meterValue *ocpp201types.MeterValue, timestamp time.Time, chargingState ...transactions.ChargingState) *transactions.TransactionEventRequest {
	seq := b.seqNo
	b.seqNo++

	connID := b.connectorID
	state := transactions.ChargingStateCharging
	if len(chargingState) > 0 && chargingState[0] != "" {
		state = chargingState[0]
	}
	req := transactions.NewTransactionEventRequest(
		transactions.TransactionEventUpdated,
		ocpp201types.NewDateTime(timestamp),
		trigger,
		seq,
		transactions.Transaction{
			TransactionID: b.transactionID,
			ChargingState: state,
		},
	)
	req.Evse = &ocpp201types.EVSE{ID: b.evseID, ConnectorID: &connID}
	if meterValue != nil {
		req.MeterValue = []ocpp201types.MeterValue{*meterValue}
	}
	return req
}

// Ended constructs a TransactionEvent with EventType=Ended, incrementing seqNo.
// reason (StoppedReason) describes why the transaction ended; triggerReason
// describes what triggered this event message — they are related but
// distinct fields (e.g. a CSMS-initiated stop is StoppedReason=Remote,
// TriggerReason=RemoteStop). The optional idToken is included as IDToken
// when provided — OCPP 2.0.1 recommends the token in the ended event for
// audit purposes.
func (b *TransactionEventBuilder) Ended(reason transactions.Reason, triggerReason transactions.TriggerReason, meterValue *ocpp201types.MeterValue, timestamp time.Time, idToken *ocpp201types.IdToken) *transactions.TransactionEventRequest {
	seq := b.seqNo
	b.seqNo++

	connID := b.connectorID
	req := transactions.NewTransactionEventRequest(
		transactions.TransactionEventEnded,
		ocpp201types.NewDateTime(timestamp),
		triggerReason,
		seq,
		transactions.Transaction{
			TransactionID: b.transactionID,
			ChargingState: transactions.ChargingStateIdle,
			StoppedReason: reason,
		},
	)
	req.Evse = &ocpp201types.EVSE{ID: b.evseID, ConnectorID: &connID}
	if idToken != nil {
		req.IDToken = idToken
	}
	if meterValue != nil {
		req.MeterValue = []ocpp201types.MeterValue{*meterValue}
	}
	return req
}

func normalizeMeterContext(meterContext string) ocpp201types.ReadingContext {
	switch ocpp201types.ReadingContext(meterContext) {
	case ocpp201types.ReadingContextInterruptionBegin,
		ocpp201types.ReadingContextInterruptionEnd,
		ocpp201types.ReadingContextOther,
		ocpp201types.ReadingContextSampleClock,
		ocpp201types.ReadingContextSamplePeriodic,
		ocpp201types.ReadingContextTransactionBegin,
		ocpp201types.ReadingContextTransactionEnd,
		ocpp201types.ReadingContextTrigger:
		return ocpp201types.ReadingContext(meterContext)
	default:
		return ocpp201types.ReadingContextOther
	}
}

func triggerReasonForMeterContext(meterContext string) transactions.TriggerReason {
	switch ocpp201types.ReadingContext(meterContext) {
	case ocpp201types.ReadingContextSampleClock:
		return transactions.TriggerReasonMeterValueClock
	case ocpp201types.ReadingContextTrigger:
		return transactions.TriggerReasonTrigger
	default:
		return transactions.TriggerReasonMeterValuePeriodic
	}
}

// makeMeterValue creates a MeterValue with a single Energy.Active.Import.Register sample.
// Voltage/current/phases don't matter here since only the Energy measurand
// is requested — renderMeasurand ignores them for that case.
func makeMeterValue(energyWh float64, timestamp time.Time, meterContext string) ocpp201types.MeterValue {
	return makeMeterValueForMeasurands(energyWh, 0, 0, 0, 1, timestamp, meterContext, []ocpp201types.Measurand{ocpp201types.MeasurandEnergyActiveImportRegister})
}

// makeMeterValueForMeasurands builds a MeterValue whose SampledValue list
// honors the configured TxUpdatedMeasurands. Each measurand is rendered
// as a SampledValue with a reasonable synthetic value:
//
//   - Energy.Active.Import.Register: energyWh
//   - Voltage: voltage (constant per session)
//   - Current.Offered / Power.Offered: offeredCurrent — the connector's
//     rated current, i.e. what's available regardless of any active limit.
//   - Current.Import / Power.Active.Import: actualCurrent — what's actually
//     flowing, which is lower than offeredCurrent whenever a charging
//     profile is throttling the session.
//   - SoC: not reported (we don't know the EV's state of charge)
//   - Temperature: not reported
//   - anything else: omitted (CSMS will ignore unknown measurands)
//
// An empty measurand slice produces an empty SampledValue list, which is
// allowed by OCPP 2.0.1 but semantically a no-op.
func makeMeterValueForMeasurands(energyWh, voltage, offeredCurrent, actualCurrent float64, phases int, timestamp time.Time, meterContext string, measurands []ocpp201types.Measurand) ocpp201types.MeterValue {
	if len(measurands) == 0 {
		return ocpp201types.MeterValue{
			Timestamp:    *ocpp201types.NewDateTime(timestamp),
			SampledValue: []ocpp201types.SampledValue{},
		}
	}
	samples := make([]ocpp201types.SampledValue, 0, len(measurands))
	for _, m := range measurands {
		sv, ok := renderMeasurand(m, energyWh, voltage, offeredCurrent, actualCurrent, phases)
		if !ok {
			continue
		}
		sv.Context = normalizeMeterContext(meterContext)
		sv.Location = ocpp201types.LocationOutlet
		samples = append(samples, sv)
	}
	return ocpp201types.MeterValue{
		Timestamp:    *ocpp201types.NewDateTime(timestamp),
		SampledValue: samples,
	}
}

// renderMeasurand produces a SampledValue for a single Measurand type.
// Returns false if the measurand cannot be rendered from the supplied
// values (e.g. SoC without battery info).
func renderMeasurand(m ocpp201types.Measurand, energyWh, voltage, offeredCurrent, actualCurrent float64, phases int) (ocpp201types.SampledValue, bool) {
	switch m {
	case ocpp201types.MeasurandEnergyActiveImportRegister:
		return ocpp201types.SampledValue{
			Value:         energyWh,
			Measurand:     ocpp201types.MeasurandEnergyActiveImportRegister,
			UnitOfMeasure: &ocpp201types.UnitOfMeasure{Unit: "Wh"},
		}, true
	case ocpp201types.MeasurandVoltage:
		return ocpp201types.SampledValue{
			Value:         voltage,
			Measurand:     ocpp201types.MeasurandVoltage,
			UnitOfMeasure: &ocpp201types.UnitOfMeasure{Unit: "V"},
		}, true
	case ocpp201types.MeasurandCurrentImport:
		return ocpp201types.SampledValue{
			Value:         actualCurrent,
			Measurand:     m,
			UnitOfMeasure: &ocpp201types.UnitOfMeasure{Unit: "A"},
		}, true
	case ocpp201types.MeasurandCurrentOffered:
		return ocpp201types.SampledValue{
			Value:         offeredCurrent,
			Measurand:     m,
			UnitOfMeasure: &ocpp201types.UnitOfMeasure{Unit: "A"},
		}, true
	case ocpp201types.MeasurandPowerActiveImport:
		power := voltage * actualCurrent * float64(phases)
		return ocpp201types.SampledValue{
			Value:         power,
			Measurand:     m,
			UnitOfMeasure: &ocpp201types.UnitOfMeasure{Unit: "W"},
		}, true
	case ocpp201types.MeasurandPowerOffered:
		power := voltage * offeredCurrent * float64(phases)
		return ocpp201types.SampledValue{
			Value:         power,
			Measurand:     m,
			UnitOfMeasure: &ocpp201types.UnitOfMeasure{Unit: "W"},
		}, true
	default:
		return ocpp201types.SampledValue{}, false
	}
}

// parseMeasurandList splits a comma-separated TxUpdatedMeasurands config
// value into a list of Measurand tokens, dropping empty entries and
// reporting unrecognized tokens via slog (CSMS will reject unknown
// measurand names per OCPP 2.0.1 §3.22).
func parseMeasurandList(raw string) []ocpp201types.Measurand {
	if raw == "" {
		return []ocpp201types.Measurand{ocpp201types.MeasurandEnergyActiveImportRegister}
	}
	parts := strings.Split(raw, ",")
	out := make([]ocpp201types.Measurand, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, ocpp201types.Measurand(p))
	}
	if len(out) == 0 {
		return []ocpp201types.Measurand{ocpp201types.MeasurandEnergyActiveImportRegister}
	}
	return out
}

// measurandListDebugString formats a measurand slice for slog.
func measurandListDebugString(measurands []ocpp201types.Measurand) string {
	parts := make([]string, len(measurands))
	for i, m := range measurands {
		parts[i] = string(m)
	}
	return fmt.Sprintf("[%s]", strings.Join(parts, ","))
}

// engineStateToChargingState maps an engine ConnectorState value to the
// OCPP 2.0.1 ChargingState enum used in TransactionEvent(Updated).
// Returns "" if the engine state does not correspond to a charging state
// (e.g. Available, Reserved, Unavailable, Faulted) — callers should skip
// emitting an Updated event in that case.
func engineStateToChargingState(state string) (transactions.ChargingState, bool) {
	switch state {
	case "Charging":
		return transactions.ChargingStateCharging, true
	case "SuspendedEV":
		return transactions.ChargingStateSuspendedEV, true
	case "SuspendedEVSE":
		return transactions.ChargingStateSuspendedEVSE, true
	case "EVConnected", "Preparing":
		// OCPP 2.0.1 distinguishes "Preparing" (no transaction yet) from
		// "EVConnected" (transaction started, awaiting energy). Real
		// chargers report EVConnected only when a transaction is in
		// progress. We return EVConnected as the closest match for an
		// active but non-charging state.
		return transactions.ChargingStateEVConnected, true
	default:
		return "", false
	}
}
