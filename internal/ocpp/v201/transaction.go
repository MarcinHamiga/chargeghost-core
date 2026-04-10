package v201

import (
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

// Started constructs a TransactionEvent with EventType=Started, seqNo=0.
// seqNo is incremented after each call.
func (b *TransactionEventBuilder) Started(idToken ocpp201types.IdToken, meterValue *ocpp201types.MeterValue, timestamp time.Time) *transactions.TransactionEventRequest {
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
func (b *TransactionEventBuilder) Updated(trigger transactions.TriggerReason, meterValue *ocpp201types.MeterValue, timestamp time.Time) *transactions.TransactionEventRequest {
	seq := b.seqNo
	b.seqNo++

	connID := b.connectorID
	req := transactions.NewTransactionEventRequest(
		transactions.TransactionEventUpdated,
		ocpp201types.NewDateTime(timestamp),
		trigger,
		seq,
		transactions.Transaction{
			TransactionID: b.transactionID,
			ChargingState: transactions.ChargingStateCharging,
		},
	)
	req.Evse = &ocpp201types.EVSE{ID: b.evseID, ConnectorID: &connID}
	if meterValue != nil {
		req.MeterValue = []ocpp201types.MeterValue{*meterValue}
	}
	return req
}

// Ended constructs a TransactionEvent with EventType=Ended, incrementing seqNo.
func (b *TransactionEventBuilder) Ended(reason transactions.Reason, meterValue *ocpp201types.MeterValue, timestamp time.Time) *transactions.TransactionEventRequest {
	seq := b.seqNo
	b.seqNo++

	connID := b.connectorID
	req := transactions.NewTransactionEventRequest(
		transactions.TransactionEventEnded,
		ocpp201types.NewDateTime(timestamp),
		transactions.TriggerReasonStopAuthorized,
		seq,
		transactions.Transaction{
			TransactionID: b.transactionID,
			ChargingState: transactions.ChargingStateIdle,
			StoppedReason: reason,
		},
	)
	req.Evse = &ocpp201types.EVSE{ID: b.evseID, ConnectorID: &connID}
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
func makeMeterValue(energyWh float64, timestamp time.Time, meterContext string) ocpp201types.MeterValue {
	return ocpp201types.MeterValue{
		Timestamp: *ocpp201types.NewDateTime(timestamp),
		SampledValue: []ocpp201types.SampledValue{
			{
				Value:     energyWh,
				Context:   normalizeMeterContext(meterContext),
				Measurand: ocpp201types.MeasurandEnergyActiveImportRegister,
				Location:  ocpp201types.LocationOutlet,
			},
		},
	}
}
