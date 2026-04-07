package v201

import (
	"testing"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransactionEventBuilder_Started(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	require.NotEmpty(t, b.TransactionID())

	now := time.Now()
	idToken := ocpp201types.IdToken{IdToken: "RFID123", Type: ocpp201types.IdTokenTypeISO14443}
	meter := makeMeterValue(1000.0, now)

	req := b.Started(idToken, &meter, now)
	assert.Equal(t, transactions.TransactionEventStarted, req.EventType)
	assert.Equal(t, transactions.TriggerReasonAuthorized, req.TriggerReason)
	assert.Equal(t, 0, req.SequenceNo)
	assert.Equal(t, b.TransactionID(), req.TransactionInfo.TransactionID)
	assert.NotNil(t, req.IDToken)
	assert.Len(t, req.MeterValue, 1)
}

func TestTransactionEventBuilder_Updated(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	now := time.Now()
	meter := makeMeterValue(2000.0, now)

	// First call increments seqNo
	_ = b.Started(ocpp201types.IdToken{IdToken: "X", Type: ocpp201types.IdTokenTypeISO14443}, nil, now)
	req := b.Updated(transactions.TriggerReasonMeterValuePeriodic, &meter, now)

	assert.Equal(t, transactions.TransactionEventUpdated, req.EventType)
	assert.Equal(t, 1, req.SequenceNo)
	assert.Equal(t, transactions.TriggerReasonMeterValuePeriodic, req.TriggerReason)
}

func TestTransactionEventBuilder_Ended(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	now := time.Now()
	meter := makeMeterValue(5000.0, now)

	_ = b.Started(ocpp201types.IdToken{IdToken: "X", Type: ocpp201types.IdTokenTypeISO14443}, nil, now)
	_ = b.Updated(transactions.TriggerReasonMeterValuePeriodic, nil, now)
	req := b.Ended(transactions.ReasonRemote, &meter, now)

	assert.Equal(t, transactions.TransactionEventEnded, req.EventType)
	assert.Equal(t, 2, req.SequenceNo)
	assert.Equal(t, transactions.ReasonRemote, req.TransactionInfo.StoppedReason)
}

func TestTransactionEventBuilder_SeqNoIncrements(t *testing.T) {
	b := NewTransactionEventBuilder(1, 1)
	now := time.Now()
	token := ocpp201types.IdToken{IdToken: "X", Type: ocpp201types.IdTokenTypeISO14443}

	r1 := b.Started(token, nil, now)
	r2 := b.Updated(transactions.TriggerReasonMeterValuePeriodic, nil, now)
	r3 := b.Updated(transactions.TriggerReasonMeterValuePeriodic, nil, now)
	r4 := b.Ended(transactions.ReasonLocal, nil, now)

	assert.Equal(t, 0, r1.SequenceNo)
	assert.Equal(t, 1, r2.SequenceNo)
	assert.Equal(t, 2, r3.SequenceNo)
	assert.Equal(t, 3, r4.SequenceNo)
}
