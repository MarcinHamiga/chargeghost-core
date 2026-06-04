package v16

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	ocpp16 "github.com/lorenzodonini/ocpp-go/ocpp1.6"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

type stubChargePoint struct {
	ocpp16.ChargePoint
	sendRequest func(request ocpp.Request) (ocpp.Response, error)
}

func (s *stubChargePoint) SendRequest(request ocpp.Request) (ocpp.Response, error) {
	return s.sendRequest(request)
}

func TestSendStartTransaction_QueuesTypedPayloadWhenDisconnected(t *testing.T) {
	q := queue.NewInMemoryQueue(3)
	reservationID := 42
	timestamp := time.Unix(1714348800, 123456789).UTC()

	b := &Bridge16{queue: q}

	txID, err := b.SendStartTransaction(1, "RFID-001", 1234.5, timestamp, &reservationID)
	require.NoError(t, err)
	assert.Zero(t, txID)

	msg, ok := q.Peek()
	require.True(t, ok)
	assert.Equal(t, "StartTransaction", msg.Type)

	payload, ok := msg.Payload.(queuedStartTransaction16)
	require.True(t, ok)
	assert.Equal(t, queuedStartTransaction16{
		ConnectorID:   1,
		IDTag:         "RFID-001",
		MeterStart:    1234.5,
		Timestamp:     timestamp,
		ReservationID: &reservationID,
	}, payload)
}

func TestSendStopTransaction_QueuesTypedPayloadWhenDisconnected(t *testing.T) {
	q := queue.NewInMemoryQueue(3)
	timestamp := time.Unix(1714349800, 987654321).UTC()
	meterHistory := []engine.MeterRecord{
		{Timestamp: "2026-04-29T12:00:00Z", Value: 4300.1},
		{Timestamp: "2026-04-29T12:05:00Z", Value: 4567.89},
	}

	b := &Bridge16{queue: q}

	err := b.SendStopTransaction(4567.89, timestamp, 77, "EVDisconnected", meterHistory)
	require.NoError(t, err)

	msg, ok := q.Peek()
	require.True(t, ok)
	assert.Equal(t, "StopTransaction", msg.Type)

	payload, ok := msg.Payload.(queuedStopTransaction16)
	require.True(t, ok)
	assert.Equal(t, queuedStopTransaction16{
		TransactionID: 77,
		MeterStop:     4567.89,
		Timestamp:     timestamp,
		Reason:        "EVDisconnected",
		MeterHistory:  meterHistory,
	}, payload)
}

func TestSendMeterValues_QueuesTypedPayloadWhenDisconnected(t *testing.T) {
	q := queue.NewInMemoryQueue(3)

	b := &Bridge16{queue: q}

	before := time.Now()
	err := b.SendMeterValues(2, 6789.01, 88, "Transaction.End")
	after := time.Now()
	require.NoError(t, err)

	msg, ok := q.Peek()
	require.True(t, ok)
	assert.Equal(t, "MeterValues", msg.Type)

	payload, ok := msg.Payload.(queuedMeterValues16)
	require.True(t, ok)
	assert.Equal(t, 2, payload.ConnectorID)
	assert.Equal(t, 6789.01, payload.Value)
	assert.Equal(t, 88, payload.TransactionID)
	assert.Equal(t, "Transaction.End", payload.Context)
	assert.False(t, payload.Timestamp.Before(before))
	assert.False(t, payload.Timestamp.After(after))
}

func TestSendMeterValues_UsesSuppliedContext(t *testing.T) {
	b := &Bridge16{}
	b.connected.Store(true)

	var captured *core.MeterValuesRequest
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.MeterValuesRequest)
		require.True(t, ok)
		captured = req
		return core.NewMeterValuesConfirmation(), nil
	}}

	require.NoError(t, b.SendMeterValues(1, 123.45, 77, "Sample.Clock"))
	require.NotNil(t, captured)
	require.Len(t, captured.MeterValue, 1)
	require.Len(t, captured.MeterValue[0].SampledValue, 1)
	assert.Equal(t, types.ReadingContextSampleClock, captured.MeterValue[0].SampledValue[0].Context)
	assert.Equal(t, 77, *captured.TransactionId)
}

func TestSendMeterValues_NormalizesInvalidContextToOther(t *testing.T) {
	b := &Bridge16{}
	b.connected.Store(true)

	var captured *core.MeterValuesRequest
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.MeterValuesRequest)
		require.True(t, ok)
		captured = req
		return core.NewMeterValuesConfirmation(), nil
	}}

	require.NoError(t, b.SendMeterValues(1, 123.45, 0, "not-valid"))
	require.NotNil(t, captured)
	assert.Equal(t, types.ReadingContextOther, captured.MeterValue[0].SampledValue[0].Context)
	assert.Nil(t, captured.TransactionId)
}

func TestDrainQueue_PreservesQueuedMeterContext(t *testing.T) {
	timestamp := time.Unix(1714351800, 444555666).UTC()
	q := queue.NewInMemoryQueue(3)
	_, err := q.Enqueue(queue.QueuedMessage{
		Type: "MeterValues",
		Payload: map[string]interface{}{
			"connectorID":   1,
			"value":         50.0,
			"transactionID": 12,
			"context":       "Trigger",
			"timestamp":     timestamp.Format(time.RFC3339Nano),
		},
	})
	require.NoError(t, err)

	b := &Bridge16{queue: q, engine: engine.NewEngine(false, 55000), configKeys: NewConfigKeyManager()}
	b.connected.Store(true)

	var captured *core.MeterValuesRequest
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.MeterValuesRequest)
		require.True(t, ok)
		captured = req
		return core.NewMeterValuesConfirmation(), nil
	}}

	b.drainQueue()

	require.NotNil(t, captured)
	assert.Equal(t, types.ReadingContextTrigger, captured.MeterValue[0].SampledValue[0].Context)
	assert.True(t, timestamp.Equal(captured.MeterValue[0].Timestamp.Time))
	assert.Equal(t, 0, q.Len())
}

func TestDrainQueue_ReplaysLegacyStartTransactionPayloadPreservingFields(t *testing.T) {
	timestamp := time.Unix(1714348800, 123456789).UTC()
	reservationID := 42
	q := queue.NewInMemoryQueue(3)
	_, err := q.Enqueue(queue.QueuedMessage{
		Type: "StartTransaction",
		Payload: map[string]interface{}{
			"connectorID":   1,
			"idTag":         "RFID-LEGACY",
			"meterStart":    1234.5,
			"timestamp":     timestamp.Format(time.RFC3339Nano),
			"reservationID": reservationID,
		},
	})
	require.NoError(t, err)

	b := &Bridge16{queue: q, engine: engine.NewEngine(false, 55000), configKeys: NewConfigKeyManager()}
	b.connected.Store(true)

	var captured *core.StartTransactionRequest
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.StartTransactionRequest)
		require.True(t, ok)
		captured = req
		return core.NewStartTransactionConfirmation(types.NewIdTagInfo(types.AuthorizationStatusAccepted), 77), nil
	}}

	b.drainQueue()

	require.NotNil(t, captured)
	assert.Equal(t, 1, captured.ConnectorId)
	assert.Equal(t, "RFID-LEGACY", captured.IdTag)
	assert.Equal(t, 1235, captured.MeterStart)
	assert.True(t, timestamp.Equal(captured.Timestamp.Time))
	require.NotNil(t, captured.ReservationId)
	assert.Equal(t, reservationID, *captured.ReservationId)
	assert.Equal(t, 0, q.Len())
}

func TestDrainQueue_ReplaysTypedStopTransactionPreservingMeterHistory(t *testing.T) {
	timestamp := time.Unix(1714349800, 987654321).UTC()
	meterHistory := []engine.MeterRecord{
		{Timestamp: "2026-04-29T12:00:00Z", Value: 4300.1},
		{Timestamp: "2026-04-29T12:05:00Z", Value: 4567.89},
	}
	q := queue.NewInMemoryQueue(3)
	_, err := q.Enqueue(queue.QueuedMessage{
		Type: "StopTransaction",
		Payload: queuedStopTransaction16{
			TransactionID: 77,
			MeterStop:     4567.89,
			Timestamp:     timestamp,
			Reason:        "EVDisconnected",
			MeterHistory:  meterHistory,
		},
	})
	require.NoError(t, err)

	b := &Bridge16{queue: q, engine: engine.NewEngine(false, 55000), configKeys: NewConfigKeyManager()}
	b.connected.Store(true)

	var captured *core.StopTransactionRequest
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.StopTransactionRequest)
		require.True(t, ok)
		captured = req
		return core.NewStopTransactionConfirmation(), nil
	}}

	b.drainQueue()

	require.NotNil(t, captured)
	assert.Equal(t, 77, captured.TransactionId)
	assert.Equal(t, 4568, captured.MeterStop)
	assert.True(t, timestamp.Equal(captured.Timestamp.Time))
	assert.Equal(t, core.Reason("EVDisconnected"), captured.Reason)
	require.Len(t, captured.TransactionData, 1)
	require.Len(t, captured.TransactionData[0].SampledValue, 2)
	assert.Equal(t, "4300.10", captured.TransactionData[0].SampledValue[0].Value)
	assert.Equal(t, "4567.89", captured.TransactionData[0].SampledValue[1].Value)
	assert.Equal(t, meterHistory[1].Timestamp, captured.TransactionData[0].Timestamp.Time.Format(time.RFC3339))
	assert.Equal(t, 0, q.Len())
}

func TestDrainQueue_ReplaysPersistedJSONQueueAfterRestart(t *testing.T) {
	startTimestamp := time.Unix(1714348800, 123456789).UTC()
	meterTimestamp := time.Unix(1714351800, 444555666).UTC()
	stopTimestamp := time.Unix(1714349800, 987654321).UTC()
	reservationID := 42
	meterHistory := []engine.MeterRecord{
		{Timestamp: "2026-04-29T12:00:00Z", Value: 4300.1},
		{Timestamp: "2026-04-29T12:05:00Z", Value: 4567.89},
	}

	queuePath := filepath.Join(t.TempDir(), "message_queue.json")
	persistedQueue, err := queue.NewJsonFileQueue(queuePath, 3)
	require.NoError(t, err)

	_, err = persistedQueue.Enqueue(queue.QueuedMessage{
		Type: "StartTransaction",
		Payload: queuedStartTransaction16{
			ConnectorID:   1,
			IDTag:         "RFID-REPLAY",
			MeterStart:    1234.5,
			Timestamp:     startTimestamp,
			ReservationID: &reservationID,
		},
	})
	require.NoError(t, err)
	_, err = persistedQueue.Enqueue(queue.QueuedMessage{
		Type: "MeterValues",
		Payload: queuedMeterValues16{
			ConnectorID:   1,
			Value:         1500.25,
			TransactionID: 77,
			Context:       "Sample.Clock",
			Timestamp:     meterTimestamp,
		},
	})
	require.NoError(t, err)
	_, err = persistedQueue.Enqueue(queue.QueuedMessage{
		Type: "StopTransaction",
		Payload: queuedStopTransaction16{
			TransactionID: 77,
			MeterStop:     4567.89,
			Timestamp:     stopTimestamp,
			Reason:        "EVDisconnected",
			MeterHistory:  meterHistory,
		},
	})
	require.NoError(t, err)

	reloadedQueue, err := queue.NewJsonFileQueue(queuePath, 3)
	require.NoError(t, err)
	require.Equal(t, 3, reloadedQueue.Len())

	b := &Bridge16{queue: reloadedQueue, engine: engine.NewEngine(false, 55000), configKeys: NewConfigKeyManager()}
	b.connected.Store(true)

	requestTypes := make([]string, 0, 3)
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		switch req := request.(type) {
		case *core.StartTransactionRequest:
			requestTypes = append(requestTypes, "StartTransaction")
			assert.Equal(t, 1, req.ConnectorId)
			assert.Equal(t, "RFID-REPLAY", req.IdTag)
			assert.Equal(t, 1235, req.MeterStart)
			assert.True(t, startTimestamp.Equal(req.Timestamp.Time))
			require.NotNil(t, req.ReservationId)
			assert.Equal(t, reservationID, *req.ReservationId)
			return core.NewStartTransactionConfirmation(types.NewIdTagInfo(types.AuthorizationStatusAccepted), 77), nil
		case *core.MeterValuesRequest:
			requestTypes = append(requestTypes, "MeterValues")
			assert.Equal(t, 1, req.ConnectorId)
			require.Len(t, req.MeterValue, 1)
			assert.True(t, meterTimestamp.Equal(req.MeterValue[0].Timestamp.Time))
			require.Len(t, req.MeterValue[0].SampledValue, 1)
			assert.Equal(t, types.ReadingContextSampleClock, req.MeterValue[0].SampledValue[0].Context)
			require.NotNil(t, req.TransactionId)
			assert.Equal(t, 77, *req.TransactionId)
			return core.NewMeterValuesConfirmation(), nil
		case *core.StopTransactionRequest:
			requestTypes = append(requestTypes, "StopTransaction")
			assert.Equal(t, 77, req.TransactionId)
			assert.Equal(t, 4568, req.MeterStop)
			assert.True(t, stopTimestamp.Equal(req.Timestamp.Time))
			assert.Equal(t, core.Reason("EVDisconnected"), req.Reason)
			require.Len(t, req.TransactionData, 1)
			require.Len(t, req.TransactionData[0].SampledValue, 2)
			assert.Equal(t, "4300.10", req.TransactionData[0].SampledValue[0].Value)
			assert.Equal(t, "4567.89", req.TransactionData[0].SampledValue[1].Value)
			assert.Equal(t, meterHistory[1].Timestamp, req.TransactionData[0].Timestamp.Time.Format(time.RFC3339))
			return core.NewStopTransactionConfirmation(), nil
		default:
			return nil, fmt.Errorf("unexpected request type: %T", request)
		}
	}}

	b.drainQueue()

	assert.Equal(t, []string{"StartTransaction", "MeterValues", "StopTransaction"}, requestTypes)
	assert.Equal(t, 0, reloadedQueue.Len())
}

func TestDrainQueue_LeavesMalformedMessageVisibleWhenReplayFails(t *testing.T) {
	q := queue.NewInMemoryQueue(3)
	_, err := q.Enqueue(queue.QueuedMessage{
		Type: "StartTransaction",
		Payload: map[string]interface{}{
			"connectorID": 1,
		},
	})
	require.NoError(t, err)

	keys := NewConfigKeyManager()
	assert.Equal(t, "Accepted", keys.SetConfigValue("TransactionMessageAttempts", "1"))
	assert.Equal(t, "Accepted", keys.SetConfigValue("TransactionMessageRetryInterval", "0"))

	b := &Bridge16{queue: q, engine: engine.NewEngine(false, 55000), configKeys: keys}
	b.connected.Store(true)
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		return nil, fmt.Errorf("unexpected send: %T", request)
	}}

	b.drainQueue()

	require.Equal(t, 1, q.Len())
	msg, ok := q.Peek()
	require.True(t, ok)
	assert.Equal(t, 1, msg.RetryCount)
	assert.Equal(t, 1, msg.MaxRetries)
	assert.NotNil(t, msg.LastAttemptAt)
	assert.Contains(t, msg.LastError, "invalid StartTransaction payload")
}

func TestDrainQueue_StopsOnTransientSendErrorAndHonorsRetryPolicy(t *testing.T) {
	timestamp := time.Unix(1714348800, 123456789).UTC()
	q := queue.NewInMemoryQueue(3)
	_, err := q.Enqueue(queue.QueuedMessage{
		Type: "StartTransaction",
		Payload: map[string]interface{}{
			"connectorID": 1,
			"idTag":       "RFID-001",
			"meterStart":  100.0,
			"timestamp":   timestamp.Format(time.RFC3339Nano),
		},
	})
	require.NoError(t, err)
	_, err = q.Enqueue(queue.QueuedMessage{
		Type: "MeterValues",
		Payload: queuedMeterValues16{
			ConnectorID:   1,
			Value:         150.0,
			TransactionID: 77,
			Context:       "Sample.Clock",
			Timestamp:     timestamp.Add(time.Minute),
		},
	})
	require.NoError(t, err)

	keys := NewConfigKeyManager()
	assert.Equal(t, "Accepted", keys.SetConfigValue("TransactionMessageAttempts", "5"))
	assert.Equal(t, "Accepted", keys.SetConfigValue("TransactionMessageRetryInterval", "300"))

	b := &Bridge16{queue: q, engine: engine.NewEngine(false, 55000), configKeys: keys}
	b.connected.Store(true)

	attempts := 0
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		attempts++
		return nil, fmt.Errorf("temporary offline")
	}}

	b.drainQueue()
	b.drainQueue()

	assert.Equal(t, 1, attempts)
	require.Equal(t, 2, q.Len())
	msg, ok := q.Peek()
	require.True(t, ok)
	assert.Equal(t, 1, msg.RetryCount)
	assert.Equal(t, 5, msg.MaxRetries)
	assert.NotNil(t, msg.LastAttemptAt)
	assert.Equal(t, "temporary offline", msg.LastError)
}

func TestSendAuthorize_CachesAcceptedResponse(t *testing.T) {
	expiresAt := time.Now().Add(30 * time.Minute).UTC().Truncate(time.Second)
	b := &Bridge16{authCache: ocpppkg.NewAuthorizationCache()}
	b.connected.Store(true)

	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.AuthorizeRequest)
		require.True(t, ok)
		assert.Equal(t, "TAG-1", req.IdTag)

		idTagInfo := types.NewIdTagInfo(types.AuthorizationStatus("accepted"))
		idTagInfo.ExpiryDate = types.NewDateTime(expiresAt)
		return core.NewAuthorizationConfirmation(idTagInfo), nil
	}}

	require.NoError(t, b.SendAuthorize("TAG-1"))

	status, expiry, found := b.authCache.Get("TAG-1")
	require.True(t, found)
	assert.Equal(t, "Accepted", status)
	require.NotNil(t, expiry)
	assert.True(t, expiresAt.Equal(*expiry))
}

func TestSendAuthorize_CachesBlockedResponseAndReturnsError(t *testing.T) {
	b := &Bridge16{authCache: ocpppkg.NewAuthorizationCache()}
	b.connected.Store(true)

	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		_, ok := request.(*core.AuthorizeRequest)
		require.True(t, ok)
		return core.NewAuthorizationConfirmation(types.NewIdTagInfo(types.AuthorizationStatusBlocked)), nil
	}}

	err := b.SendAuthorize("TAG-BLOCKED")
	require.EqualError(t, err, "authorize rejected: status=Blocked")

	status, expiry, found := b.authCache.Get("TAG-BLOCKED")
	require.True(t, found)
	assert.Equal(t, "Blocked", status)
	assert.Nil(t, expiry)
}
