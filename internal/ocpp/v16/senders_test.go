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

	"github.com/chargeghost/engine/internal/config"
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
	b.registered.Store(true)

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

// TestSendStartTransaction_RejectsWhenNotRegistered verifies that a station
// which has not completed BootNotification (or whose last BootNotification
// was Pending/Rejected) refuses to send StartTransaction — and does not
// queue it for later replay either — per OCPP 1.6 §4.2.1. This guards
// local/REST-triggered starts and the offline-queue drain path, not just
// the RemoteStartTransaction handler (which rejects earlier).
func TestSendStartTransaction_RejectsWhenNotRegistered(t *testing.T) {
	q := queue.NewInMemoryQueue(3)
	b := &Bridge16{queue: q}

	txID, err := b.SendStartTransaction(1, "RFID-001", 100.0, time.Now(), nil)
	require.Error(t, err)
	assert.Zero(t, txID)
	assert.Equal(t, 0, q.Len(), "must not be queued for later replay while unregistered")
}

// TestSendBootNotification_AcceptedSetsRegistered verifies that an Accepted
// BootNotification response flips the bridge into the registered state,
// permitting subsequent transaction traffic.
func TestSendBootNotification_AcceptedSetsRegistered(t *testing.T) {
	b := &Bridge16{
		cfg:        config.DefaultConfig(),
		configKeys: NewConfigKeyManager(),
		engine:     engine.NewEngine(false, 55000),
		dispatcher: ocpppkg.NewCommandDispatcher(),
	}
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		return core.NewBootNotificationConfirmation(types.NewDateTime(time.Now()), 300, core.RegistrationStatusAccepted), nil
	}}

	require.NoError(t, b.SendBootNotification())
	assert.True(t, b.registered.Load())
}

// TestSendBootNotification_PendingLeavesRegisteredFalse verifies that a
// Pending BootNotification response does not flip the bridge into the
// registered state, so transaction traffic stays blocked until a later
// Accepted response arrives.
func TestSendBootNotification_PendingLeavesRegisteredFalse(t *testing.T) {
	b := &Bridge16{
		cfg:        config.DefaultConfig(),
		configKeys: NewConfigKeyManager(),
		engine:     engine.NewEngine(false, 55000),
		dispatcher: ocpppkg.NewCommandDispatcher(),
	}
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		return core.NewBootNotificationConfirmation(types.NewDateTime(time.Now()), 30, core.RegistrationStatusPending), nil
	}}

	require.NoError(t, b.SendBootNotification())
	assert.False(t, b.registered.Load())
}

func TestSendStopTransaction_QueuesTypedPayloadWhenDisconnected(t *testing.T) {
	q := queue.NewInMemoryQueue(3)
	timestamp := time.Unix(1714349800, 987654321).UTC()
	meterHistory := []engine.MeterRecord{
		{Timestamp: "2026-04-29T12:00:00Z", Value: 4300.1},
		{Timestamp: "2026-04-29T12:05:00Z", Value: 4567.89},
	}

	b := &Bridge16{queue: q}

	err := b.SendStopTransaction(4567.89, timestamp, 77, "EVDisconnected", nil, meterHistory)
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
	b := &Bridge16{engine: engine.NewEngine(false, 55000), configKeys: NewConfigKeyManager()}
	b.engine.AddConnector(230, 32, 1)
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
	b := &Bridge16{engine: engine.NewEngine(false, 55000), configKeys: NewConfigKeyManager()}
	b.engine.AddConnector(230, 32, 1)
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

func TestSendMeterValues_UsesConfiguredSampledMeasurands(t *testing.T) {
	b := &Bridge16{engine: engine.NewEngine(false, 55000), configKeys: NewConfigKeyManager()}
	b.engine.AddConnector(230, 32, 1)
	b.connected.Store(true)
	require.Equal(t, "Accepted", b.configKeys.SetConfigValue("MeterValuesSampledData", "Energy.Active.Import.Register,Voltage,Current.Offered,Power.Offered"))

	var captured *core.MeterValuesRequest
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.MeterValuesRequest)
		require.True(t, ok)
		captured = req
		return core.NewMeterValuesConfirmation(), nil
	}}

	require.NoError(t, b.SendMeterValues(1, 123.45, 0, "Sample.Periodic"))
	require.NotNil(t, captured)
	require.Len(t, captured.MeterValue, 1)
	require.Len(t, captured.MeterValue[0].SampledValue, 4)
	assert.Equal(t, types.MeasurandEnergyActiveImportRegister, captured.MeterValue[0].SampledValue[0].Measurand)
	assert.Equal(t, types.MeasurandVoltage, captured.MeterValue[0].SampledValue[1].Measurand)
	assert.Equal(t, "230.0", captured.MeterValue[0].SampledValue[1].Value)
	assert.Equal(t, types.MeasurandCurrentOffered, captured.MeterValue[0].SampledValue[2].Measurand)
	assert.Equal(t, "32.00", captured.MeterValue[0].SampledValue[2].Value)
	assert.Equal(t, types.MeasurandPowerOffered, captured.MeterValue[0].SampledValue[3].Measurand)
	assert.Equal(t, "7360.00", captured.MeterValue[0].SampledValue[3].Value)
}

// TestBuildSampledValues_ImportReflectsEffectiveCurrentNotRatedCurrent
// verifies Current.Import/Power.Active.Import report the profile-limited
// (effective) current while Current.Offered/Power.Offered keep reporting
// the connector's rated current — the two must differ whenever a charging
// profile is throttling the session below the connector's rated current.
func TestBuildSampledValues_ImportReflectsEffectiveCurrentNotRatedCurrent(t *testing.T) {
	conn := engine.NewConnector(1, 230, 32, 1)
	conn.Status = engine.StateCharging

	effectiveCurrent := 16.0
	sampled := buildSampledValues(conn, 1000.0, effectiveCurrent, 42, types.ReadingContextSamplePeriodic, []types.Measurand{
		types.MeasurandCurrentImport,
		types.MeasurandCurrentOffered,
		types.MeasurandPowerActiveImport,
		types.MeasurandPowerOffered,
	})

	require.Len(t, sampled, 4)
	assert.Equal(t, types.MeasurandCurrentImport, sampled[0].Measurand)
	assert.Equal(t, "16.00", sampled[0].Value, "Current.Import must reflect the profile-limited current, not the rated current")
	assert.Equal(t, types.MeasurandCurrentOffered, sampled[1].Measurand)
	assert.Equal(t, "32.00", sampled[1].Value, "Current.Offered must reflect the connector's rated current regardless of any active limit")
	assert.NotEqual(t, sampled[0].Value, sampled[1].Value)
	assert.Equal(t, types.MeasurandPowerActiveImport, sampled[2].Measurand)
	assert.Equal(t, fmt.Sprintf("%.2f", 230.0*effectiveCurrent), sampled[2].Value)
	assert.Equal(t, types.MeasurandPowerOffered, sampled[3].Measurand)
	assert.Equal(t, fmt.Sprintf("%.2f", 230.0*32.0), sampled[3].Value)
}

// TestBuildSampledValues_NotCharging_ImportIsZero verifies Current.Import
// stays 0 when the connector isn't actively charging, even if a stale
// effectiveCurrent were passed in — actualCurrent is gated on
// conn.Status == StateCharging.
func TestBuildSampledValues_NotCharging_ImportIsZero(t *testing.T) {
	conn := engine.NewConnector(1, 230, 32, 1)
	conn.Status = engine.StateSuspendedEV

	sampled := buildSampledValues(conn, 1000.0, 16.0, 42, types.ReadingContextSamplePeriodic, []types.Measurand{
		types.MeasurandCurrentImport,
	})

	require.Len(t, sampled, 1)
	assert.Equal(t, "0.00", sampled[0].Value)
}

func TestMapErrorCode16_ValidCodesPassThrough(t *testing.T) {
	for _, code := range []core.ChargePointErrorCode{
		core.ConnectorLockFailure, core.EVCommunicationError, core.GroundFailure,
		core.HighTemperature, core.InternalError, core.LocalListConflict, core.NoError,
		core.OtherError, core.OverCurrentFailure, core.OverVoltage, core.PowerMeterFailure,
		core.PowerSwitchFailure, core.ReaderFailure, core.ResetFailure, core.UnderVoltage,
		core.WeakSignal,
	} {
		assert.Equal(t, code, mapErrorCode16(string(code)))
	}
}

func TestMapErrorCode16_UnknownFallsBackToOtherError(t *testing.T) {
	assert.Equal(t, core.OtherError, mapErrorCode16("SomethingMadeUp"))
	assert.Equal(t, core.OtherError, mapErrorCode16(""))
}

func TestSendStatusNotification_SendsNormalizedErrorCode(t *testing.T) {
	b := &Bridge16{}
	var captured *core.StatusNotificationRequest
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.StatusNotificationRequest)
		require.True(t, ok)
		captured = req
		return core.NewStatusNotificationConfirmation(), nil
	}}

	require.NoError(t, b.SendStatusNotification(1, "HighTemperature", "Faulted"))
	require.NotNil(t, captured)
	assert.Equal(t, core.HighTemperature, captured.ErrorCode)

	require.NoError(t, b.SendStatusNotification(1, "not-a-real-fault-code", "Faulted"))
	require.NotNil(t, captured)
	assert.Equal(t, core.OtherError, captured.ErrorCode, "unrecognized fault codes must fall back to OtherError instead of failing client-side validation")
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

// TestDrainOfflineQueue_ReplaysViaRealSend is a regression test for the
// FleetManager-level bug where POST /queue/drain dequeued every offline
// message without ever sending it. DrainOfflineQueue (the method
// FleetManager now calls) must delegate to the bridge's real replay, not
// silently discard the queue.
func TestDrainOfflineQueue_ReplaysViaRealSend(t *testing.T) {
	q := queue.NewInMemoryQueue(3)
	_, err := q.Enqueue(queue.QueuedMessage{
		Type: "MeterValues",
		Payload: map[string]interface{}{
			"connectorID":   1,
			"value":         50.0,
			"transactionID": 12,
			"context":       "Trigger",
			"timestamp":     time.Now().UTC().Format(time.RFC3339Nano),
		},
	})
	require.NoError(t, err)

	b := &Bridge16{queue: q, engine: engine.NewEngine(false, 55000), configKeys: NewConfigKeyManager()}
	b.connected.Store(true)

	sendCalled := false
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		sendCalled = true
		return core.NewMeterValuesConfirmation(), nil
	}}

	b.DrainOfflineQueue()

	assert.True(t, sendCalled, "DrainOfflineQueue must actually replay queued messages via the bridge, not discard them")
	assert.Equal(t, 0, q.Len())
}

// TestDrainQueue_SingleFlightSkipsOverlappingCall is a regression test: the
// reconnect handler, the periodic drain loop, and an explicit
// DrainOfflineQueue call (e.g. from a REST request) can all trigger
// drainQueue concurrently. Without single-flighting, two overlapping passes
// could both Peek and send the same message. Simulate an in-progress drain
// and confirm a second call is a no-op.
func TestDrainQueue_SingleFlightSkipsOverlappingCall(t *testing.T) {
	q := queue.NewInMemoryQueue(3)
	_, err := q.Enqueue(queue.QueuedMessage{
		Type: "MeterValues",
		Payload: map[string]interface{}{
			"connectorID":   1,
			"value":         50.0,
			"transactionID": 12,
			"context":       "Trigger",
			"timestamp":     time.Now().UTC().Format(time.RFC3339Nano),
		},
	})
	require.NoError(t, err)

	b := &Bridge16{queue: q, engine: engine.NewEngine(false, 55000), configKeys: NewConfigKeyManager()}
	b.connected.Store(true)
	b.draining.Store(true) // simulate an already-in-progress drain

	sendCalled := false
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		sendCalled = true
		return core.NewMeterValuesConfirmation(), nil
	}}

	b.drainQueue()

	assert.False(t, sendCalled, "an overlapping drainQueue call must be a no-op")
	assert.Equal(t, 1, q.Len(), "the queued message must be untouched by the skipped call")
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
	b.registered.Store(true)

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
	require.Len(t, captured.TransactionData, 2)
	assert.Equal(t, "4300.10", captured.TransactionData[0].SampledValue[0].Value)
	assert.Equal(t, meterHistory[0].Timestamp, captured.TransactionData[0].Timestamp.Time.Format(time.RFC3339))
	assert.Equal(t, "4567.89", captured.TransactionData[1].SampledValue[0].Value)
	assert.Equal(t, meterHistory[1].Timestamp, captured.TransactionData[1].Timestamp.Time.Format(time.RFC3339))
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
	b.registered.Store(true)

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
			require.Len(t, req.TransactionData, 2)
			assert.Equal(t, "4300.10", req.TransactionData[0].SampledValue[0].Value)
			assert.Equal(t, meterHistory[0].Timestamp, req.TransactionData[0].Timestamp.Time.Format(time.RFC3339))
			assert.Equal(t, "4567.89", req.TransactionData[1].SampledValue[0].Value)
			assert.Equal(t, meterHistory[1].Timestamp, req.TransactionData[1].Timestamp.Time.Format(time.RFC3339))
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
	b.registered.Store(true)

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

// TestDrainQueue_PreservesFIFOOrderingForQueuedTransactions verifies
// that StartTransaction and StopTransaction messages queued while
// offline are replayed in their original FIFO order.
func TestDrainQueue_PreservesFIFOOrderingForQueuedTransactions(t *testing.T) {
	timestamp := time.Unix(1714350000, 0).UTC()
	q := queue.NewInMemoryQueue(10)

	// Queue a Start followed by a Stop on the same transaction.
	_, err := q.Enqueue(queue.QueuedMessage{
		Type: "StartTransaction",
		Payload: queuedStartTransaction16{
			ConnectorID: 1,
			IDTag:       "RFID-FIFO-1",
			MeterStart:  100.0,
			Timestamp:   timestamp,
		},
	})
	require.NoError(t, err)
	_, err = q.Enqueue(queue.QueuedMessage{
		Type: "StopTransaction",
		Payload: queuedStopTransaction16{
			TransactionID: 11,
			MeterStop:     250.0,
			Timestamp:     timestamp.Add(time.Minute),
			Reason:        "EVDisconnected",
		},
	})
	require.NoError(t, err)
	_, err = q.Enqueue(queue.QueuedMessage{
		Type: "StartTransaction",
		Payload: queuedStartTransaction16{
			ConnectorID: 2,
			IDTag:       "RFID-FIFO-2",
			MeterStart:  500.0,
			Timestamp:   timestamp.Add(2 * time.Minute),
		},
	})
	require.NoError(t, err)

	b := &Bridge16{queue: q, engine: engine.NewEngine(false, 55000), configKeys: NewConfigKeyManager()}
	b.connected.Store(true)
	b.registered.Store(true)

	received := make(chan string, 3)
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		switch req := request.(type) {
		case *core.StartTransactionRequest:
			received <- fmt.Sprintf("Start:%s", req.IdTag)
		case *core.StopTransactionRequest:
			received <- fmt.Sprintf("Stop:%d", req.TransactionId)
		default:
			t.Errorf("unexpected request type %T", request)
		}
		return core.NewStartTransactionConfirmation(types.NewIdTagInfo(types.AuthorizationStatusAccepted), 0), nil
	}}

	b.drainQueue()

	close(received)
	got := make([]string, 0, 3)
	for s := range received {
		got = append(got, s)
	}
	assert.Equal(t, []string{"Start:RFID-FIFO-1", "Stop:11", "Start:RFID-FIFO-2"}, got,
		"messages must be replayed in original FIFO order")
	require.Eventually(t, func() bool {
		return q.Len() == 0
	}, time.Second, 10*time.Millisecond)
}

// TestDrainQueue_PreservesIdempotencyKeyAcrossReplays verifies that the
// IdempotencyKey on a queued message survives across replay attempts so
// the CSMS can deduplicate identical retries.
func TestDrainQueue_PreservesIdempotencyKeyAcrossReplays(t *testing.T) {
	timestamp := time.Unix(1714350000, 0).UTC()
	q := queue.NewInMemoryQueue(3)

	_, err := q.Enqueue(queue.QueuedMessage{
		Type: "StartTransaction",
		Payload: queuedStartTransaction16{
			ConnectorID: 1,
			IDTag:       "RFID-IDEMP",
			MeterStart:  100.0,
			Timestamp:   timestamp,
		},
		IdempotencyKey: "v16-stable-key-001",
	})
	require.NoError(t, err)

	keys := NewConfigKeyManager()
	assert.Equal(t, "Accepted", keys.SetConfigValue("TransactionMessageAttempts", "5"))
	assert.Equal(t, "Accepted", keys.SetConfigValue("TransactionMessageRetryInterval", "300"))

	b := &Bridge16{queue: q, engine: engine.NewEngine(false, 55000), configKeys: keys}
	b.connected.Store(true)
	b.registered.Store(true)

	var observedKeys []string
	var attempts int
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		attempts++
		msg, _ := q.Peek()
		observedKeys = append(observedKeys, msg.IdempotencyKey)
		if attempts == 1 {
			return nil, fmt.Errorf("simulated transient CSMS error")
		}
		return core.NewStartTransactionConfirmation(types.NewIdTagInfo(types.AuthorizationStatusAccepted), 0), nil
	}}

	b.drainQueue() // first attempt fails
	require.Equal(t, 1, q.Len())
	msg, ok := q.Peek()
	require.True(t, ok)
	assert.Equal(t, "v16-stable-key-001", msg.IdempotencyKey, "idempotency key must survive failure")

	// Roll back LastAttemptAt so retry-interval does not gate the second drain.
	past := time.Now().UTC().Add(-1 * time.Hour)
	msg.LastAttemptAt = &past
	require.NoError(t, q.Update(msg))

	b.drainQueue() // second attempt succeeds
	assert.Equal(t, 0, q.Len())
	assert.Equal(t, []string{"v16-stable-key-001", "v16-stable-key-001"}, observedKeys,
		"idempotency key must be the same across replays")
}
