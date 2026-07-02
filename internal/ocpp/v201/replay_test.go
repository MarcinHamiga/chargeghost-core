package v201

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	ocpp "github.com/lorenzodonini/ocpp-go/ocpp"
	ocpp2 "github.com/lorenzodonini/ocpp-go/ocpp2.0.1"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

func freeReplayPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

type replayMockCSMSHandler struct {
	bootReceived      chan *provisioning.BootNotificationRequest
	transactionEvents chan *transactions.TransactionEventRequest
}

func newReplayMockCSMSHandler() *replayMockCSMSHandler {
	return &replayMockCSMSHandler{
		bootReceived:      make(chan *provisioning.BootNotificationRequest, 10),
		transactionEvents: make(chan *transactions.TransactionEventRequest, 10),
	}
}

func (h *replayMockCSMSHandler) OnBootNotification(_ string, request *provisioning.BootNotificationRequest) (*provisioning.BootNotificationResponse, error) {
	h.bootReceived <- request
	return provisioning.NewBootNotificationResponse(ocpp201types.NewDateTime(time.Now()), 300, provisioning.RegistrationStatusAccepted), nil
}

func (h *replayMockCSMSHandler) OnNotifyReport(_ string, _ *provisioning.NotifyReportRequest) (*provisioning.NotifyReportResponse, error) {
	return provisioning.NewNotifyReportResponse(), nil
}

func (h *replayMockCSMSHandler) OnTransactionEvent(_ string, request *transactions.TransactionEventRequest) (*transactions.TransactionEventResponse, error) {
	h.transactionEvents <- request
	return transactions.NewTransactionEventResponse(), nil
}

func startReplayMockCSMS(t *testing.T, port int, handler *replayMockCSMSHandler) func() {
	t.Helper()
	csms := ocpp2.NewCSMS(nil, nil)
	csms.SetProvisioningHandler(handler)
	csms.SetTransactionsHandler(handler)

	go csms.Start(port, "/{ws}")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	return func() { csms.Stop() }
}

type stubChargingStation struct {
	ocpp2.ChargingStation
	bootNotification func(reason provisioning.BootReason, model string, chargePointVendor string, props ...func(request *provisioning.BootNotificationRequest)) (*provisioning.BootNotificationResponse, error)
	sendRequestAsync func(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error
}

func (s *stubChargingStation) BootNotification(reason provisioning.BootReason, model string, chargePointVendor string, props ...func(request *provisioning.BootNotificationRequest)) (*provisioning.BootNotificationResponse, error) {
	return s.bootNotification(reason, model, chargePointVendor, props...)
}

func (s *stubChargingStation) SendRequestAsync(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error {
	return s.sendRequestAsync(request, callback)
}

func TestQueuedTransactionEventRequest_RehydratesPersistedPayload(t *testing.T) {
	builder := NewTransactionEventBuilder(1, 1)
	req := builder.Started(
		ocpp201types.IdToken{IdToken: "RFID-001", Type: ocpp201types.IdTokenTypeISO14443},
		func() *ocpp201types.MeterValue {
			mv := makeMeterValue(1234.5, time.Unix(1710000000, 0).UTC(), string(ocpp201types.ReadingContextTransactionBegin))
			return &mv
		}(),
		time.Unix(1710000000, 0).UTC(),
		nil,
	)

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &payload))

	rehydrated, err := queuedTransactionEventRequest(payload)
	require.NoError(t, err)
	assert.Equal(t, req.EventType, rehydrated.EventType)
	assert.Equal(t, req.TriggerReason, rehydrated.TriggerReason)
	assert.Equal(t, req.TransactionInfo.TransactionID, rehydrated.TransactionInfo.TransactionID)
	require.NotNil(t, rehydrated.Evse)
	assert.Equal(t, req.Evse.ID, rehydrated.Evse.ID)
	require.Len(t, rehydrated.MeterValue, 1)
	require.Len(t, rehydrated.MeterValue[0].SampledValue, 1)
	assert.Equal(t, req.MeterValue[0].SampledValue[0].Value, rehydrated.MeterValue[0].SampledValue[0].Value)
	assert.Equal(t, req.MeterValue[0].SampledValue[0].Context, rehydrated.MeterValue[0].SampledValue[0].Context)
}

func TestSendBootNotification_DrainsQueuedMeterContextSemantics(t *testing.T) {
	b := newTestBridge(t)
	b.queue = queue.NewInMemoryQueue(3)

	b.mu.Lock()
	b.txBuilders[1] = NewTransactionEventBuilder(1, 1)
	b.txIntToEVSE[7] = 1
	b.mu.Unlock()

	b.connected.Store(false)
	require.NoError(t, b.SendMeterValues(1, 321.0, 7, "Sample.Clock"))
	require.Equal(t, 1, b.queue.Len())

	sent := make(chan *transactions.TransactionEventRequest, 1)
	b.cs = &stubChargingStation{
		bootNotification: func(reason provisioning.BootReason, model string, chargePointVendor string, props ...func(request *provisioning.BootNotificationRequest)) (*provisioning.BootNotificationResponse, error) {
			return provisioning.NewBootNotificationResponse(ocpp201types.NewDateTime(time.Now()), 300, provisioning.RegistrationStatusAccepted), nil
		},
		sendRequestAsync: func(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error {
			txReq, ok := request.(*transactions.TransactionEventRequest)
			require.True(t, ok)
			sent <- txReq
			if callback != nil {
				callback(transactions.NewTransactionEventResponse(), nil)
			}
			return nil
		},
	}

	b.connected.Store(true)
	require.NoError(t, b.SendBootNotification())

	select {
	case replayed := <-sent:
		assert.Equal(t, transactions.TriggerReasonMeterValueClock, replayed.TriggerReason)
		require.Len(t, replayed.MeterValue, 1)
		require.Len(t, replayed.MeterValue[0].SampledValue, 1)
		assert.Equal(t, ocpp201types.ReadingContextSampleClock, replayed.MeterValue[0].SampledValue[0].Context)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for queued meter update replay")
	}
}

func TestSendBootNotification_DrainsQueuedTransactionEvents(t *testing.T) {
	b := newTestBridge(t)
	b.connected.Store(true)
	b.queue = queue.NewInMemoryQueue(3)

	builder := NewTransactionEventBuilder(1, 1)
	req := builder.Started(
		ocpp201types.IdToken{IdToken: "RFID-BOOT", Type: ocpp201types.IdTokenTypeISO14443},
		nil,
		time.Now().UTC(),
		nil,
	)
	_, err := b.queue.Enqueue(queue.QueuedMessage{Type: "TransactionEvent", Payload: req})
	require.NoError(t, err)

	sent := make(chan *transactions.TransactionEventRequest, 1)
	b.cs = &stubChargingStation{
		bootNotification: func(reason provisioning.BootReason, model string, chargePointVendor string, props ...func(request *provisioning.BootNotificationRequest)) (*provisioning.BootNotificationResponse, error) {
			return provisioning.NewBootNotificationResponse(ocpp201types.NewDateTime(time.Now()), 300, provisioning.RegistrationStatusAccepted), nil
		},
		sendRequestAsync: func(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error {
			txReq, ok := request.(*transactions.TransactionEventRequest)
			require.True(t, ok)
			sent <- txReq
			if callback != nil {
				callback(transactions.NewTransactionEventResponse(), nil)
			}
			return nil
		},
	}

	require.NoError(t, b.SendBootNotification())

	select {
	case replayed := <-sent:
		assert.Equal(t, req.TransactionInfo.TransactionID, replayed.TransactionInfo.TransactionID)
		assert.Equal(t, req.EventType, replayed.EventType)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for queued TransactionEvent replay")
	}

	require.Eventually(t, func() bool {
		return b.queue.Len() == 0
	}, time.Second, 10*time.Millisecond)
}

func TestIntegration_TransactionEventReplayOnStartup(t *testing.T) {
	port := freeReplayPort(t)
	handler := newReplayMockCSMSHandler()
	stopCSMS := startReplayMockCSMS(t, port, handler)
	defer stopCSMS()

	queuePath := filepath.Join(t.TempDir(), "message_queue.json")
	persistedQueue, err := queue.NewJsonFileQueue(queuePath, 3)
	require.NoError(t, err)

	bridgeBeforeRestart := newTestBridge(t)
	bridgeBeforeRestart.queue = persistedQueue
	_, err = bridgeBeforeRestart.SendTransactionStart(1, "TEST-TAG-REPLAY", 12.5, time.Now().UTC(), nil)
	require.NoError(t, err)
	require.Equal(t, 1, persistedQueue.Len())

	reloadedQueue, err := queue.NewJsonFileQueue(queuePath, 3)
	require.NoError(t, err)
	require.Equal(t, 1, reloadedQueue.Len())

	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	cfg.OCPPID = "test-cp-replay-001"
	cfg.ConnectionURL = fmt.Sprintf("ws://127.0.0.1:%d", port)

	dispatcher := ocpppkg.NewCommandDispatcher()
	bridge := NewBridge(e, nil, cfg, dispatcher, reloadedQueue, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dispCtx, dispCancel := context.WithCancel(context.Background())
	defer dispCancel()
	go dispatcher.Run(dispCtx)

	go func() { _ = bridge.Start(ctx) }()

	select {
	case <-handler.bootReceived:
	case <-time.After(8 * time.Second):
		t.Fatal("timeout waiting for BootNotification")
	}

	select {
	case evt := <-handler.transactionEvents:
		assert.Equal(t, transactions.TransactionEventStarted, evt.EventType)
		assert.Equal(t, "TEST-TAG-REPLAY", evt.IDToken.IdToken)
	case <-time.After(8 * time.Second):
		t.Fatal("timeout waiting for replayed TransactionEvent")
	}

	require.Eventually(t, func() bool {
		return reloadedQueue.Len() == 0
	}, time.Second, 10*time.Millisecond)
}

// TestDrainQueue_PreservesFIFOOrderingAcrossReplays verifies that when
// multiple TransactionEvents are queued while the charger is offline,
// they are replayed in their original FIFO order to the CSMS.
func TestDrainQueue_PreservesFIFOOrderingAcrossReplays(t *testing.T) {
	b := newTestBridge(t)
	b.connected.Store(true)
	b.queue = queue.NewInMemoryQueue(10)

	now := time.Now().UTC()
	tags := []string{"TAG-A", "TAG-B", "TAG-C", "TAG-D"}

	// Enqueue 4 TransactionEvents in order. Each carries a unique idTag
	// we can use to verify the CSMS receives them in the same order.
	for i, tag := range tags {
		_, err := b.queue.Enqueue(queue.QueuedMessage{
			Type: "TransactionEvent",
			Payload: &transactions.TransactionEventRequest{
				EventType:     transactions.TransactionEventStarted,
				Timestamp:     ocpp201types.NewDateTime(now.Add(time.Duration(i) * time.Second)),
				SequenceNo:    i,
				TriggerReason: transactions.TriggerReasonAuthorized,
				TransactionInfo: transactions.Transaction{
					TransactionID: fmt.Sprintf("tx-%d", i),
				},
				IDToken: &ocpp201types.IdToken{IdToken: tag, Type: ocpp201types.IdTokenTypeISO14443},
			},
			IdempotencyKey: fmt.Sprintf("idemp-%d", i),
		})
		require.NoError(t, err)
	}
	require.Equal(t, len(tags), b.queue.Len())

	received := make(chan string, len(tags))
	b.cs = &stubChargingStation{
		bootNotification: func(reason provisioning.BootReason, model string, chargePointVendor string, props ...func(request *provisioning.BootNotificationRequest)) (*provisioning.BootNotificationResponse, error) {
			return provisioning.NewBootNotificationResponse(ocpp201types.NewDateTime(time.Now()), 300, provisioning.RegistrationStatusAccepted), nil
		},
		sendRequestAsync: func(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error {
			txReq, ok := request.(*transactions.TransactionEventRequest)
			require.True(t, ok)
			require.NotNil(t, txReq.IDToken)
			received <- txReq.IDToken.IdToken
			if callback != nil {
				callback(transactions.NewTransactionEventResponse(), nil)
			}
			return nil
		},
	}

	b.drainQueue()

	// All 4 messages must arrive in their original order.
	close(received)
	got := make([]string, 0, len(tags))
	for tag := range received {
		got = append(got, tag)
	}
	assert.Equal(t, tags, got, "messages must be replayed in FIFO order")
	require.Eventually(t, func() bool {
		return b.queue.Len() == 0
	}, time.Second, 10*time.Millisecond)
}

// TestDrainQueue_PreservesIdempotencyKeyAcrossFailedReplays verifies
// that when a TransactionEvent send fails and is replayed, the queued
// idempotency key remains stable across attempts so the CSMS can
// deduplicate.
func TestDrainQueue_PreservesIdempotencyKeyAcrossFailedReplays(t *testing.T) {
	b := newTestBridge(t)
	b.connected.Store(true)
	b.queue = queue.NewInMemoryQueue(3)
	b.deviceModel.SetVariable("OCPPCommCtrlr", "", 0, "RetryBackOffRepeatTimes", "5", MutabilityReadWrite)
	// Make the retry interval effectively zero so a second drain call
	// proceeds without waiting 60 seconds.
	b.deviceModel.SetVariable("OCPPCommCtrlr", "", 0, "TransactionMessageRetryInterval", "0", MutabilityReadWrite)

	now := time.Now().UTC()
	_, err := b.queue.Enqueue(queue.QueuedMessage{
		Type: "TransactionEvent",
		Payload: &transactions.TransactionEventRequest{
			EventType:     transactions.TransactionEventStarted,
			Timestamp:     ocpp201types.NewDateTime(now),
			SequenceNo:    0,
			TriggerReason: transactions.TriggerReasonAuthorized,
			TransactionInfo: transactions.Transaction{
				TransactionID: "tx-stable",
			},
			IDToken: &ocpp201types.IdToken{IdToken: "TAG-IDEMP", Type: ocpp201types.IdTokenTypeISO14443},
		},
		IdempotencyKey: "stable-key-abc",
	})
	require.NoError(t, err)

	// First attempt: fail. Second attempt: succeed.
	var attempts int
	var observedKeys []string
	b.cs = &stubChargingStation{
		bootNotification: func(reason provisioning.BootReason, model string, chargePointVendor string, props ...func(request *provisioning.BootNotificationRequest)) (*provisioning.BootNotificationResponse, error) {
			return provisioning.NewBootNotificationResponse(ocpp201types.NewDateTime(time.Now()), 300, provisioning.RegistrationStatusAccepted), nil
		},
		sendRequestAsync: func(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error {
			attempts++
			msg, _ := b.queue.Peek()
			observedKeys = append(observedKeys, msg.IdempotencyKey)
			if attempts == 1 {
				if callback != nil {
					callback(nil, fmt.Errorf("simulated transient CSMS error"))
				}
				return nil
			}
			if callback != nil {
				callback(transactions.NewTransactionEventResponse(), nil)
			}
			return nil
		},
	}

	b.drainQueue() // first attempt fails
	require.Equal(t, 1, b.queue.Len())
	msg, ok := b.queue.Peek()
	require.True(t, ok)
	assert.Equal(t, 1, msg.RetryCount)
	assert.Equal(t, "stable-key-abc", msg.IdempotencyKey, "idempotency key must survive failure")

	// Roll the LastAttemptAt back so the retry-interval check doesn't
	// gate the second drain call.
	require.NotNil(t, msg.LastAttemptAt)
	past := time.Now().UTC().Add(-1 * time.Hour)
	msg.LastAttemptAt = &past
	require.NoError(t, b.queue.Update(msg))

	b.drainQueue() // second attempt succeeds
	assert.Equal(t, 0, b.queue.Len())
	assert.Equal(t, []string{"stable-key-abc", "stable-key-abc"}, observedKeys, "idempotency key must be the same across replays")
}

// TestDrainQueue_ReplaysChargingStateChangeOnReconnect verifies that a
// TransactionEvent(Updated) emitted for a charging-state change while the
// charger is offline is enqueued and replayed on reconnect.
func TestDrainQueue_ReplaysChargingStateChangeOnReconnect(t *testing.T) {
	b := newTestBridge(t)
	b.queue = queue.NewInMemoryQueue(5)

	// Register an active transaction so SendTransactionEventUpdated has a
	// builder to attach to.
	b.mu.Lock()
	b.txBuilders[1] = NewTransactionEventBuilder(1, 1)
	b.mu.Unlock()
	b.connected.Store(false)

	// Charging-state change while offline.
	require.NoError(t, b.SendTransactionEventUpdated(1, "SuspendedEV", "ChargingStateChanged"))
	require.Equal(t, 1, b.queue.Len(), "TransactionEvent(Updated) must be enqueued when offline")

	// Reconnect: capture the replayed event.
	sent := make(chan *transactions.TransactionEventRequest, 1)
	b.cs = &stubChargingStation{
		bootNotification: func(reason provisioning.BootReason, model string, chargePointVendor string, props ...func(request *provisioning.BootNotificationRequest)) (*provisioning.BootNotificationResponse, error) {
			return provisioning.NewBootNotificationResponse(ocpp201types.NewDateTime(time.Now()), 300, provisioning.RegistrationStatusAccepted), nil
		},
		sendRequestAsync: func(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error {
			txReq, ok := request.(*transactions.TransactionEventRequest)
			require.True(t, ok)
			sent <- txReq
			if callback != nil {
				callback(transactions.NewTransactionEventResponse(), nil)
			}
			return nil
		},
	}

	b.connected.Store(true)
	b.drainQueue()

	select {
	case req := <-sent:
		assert.Equal(t, transactions.TransactionEventUpdated, req.EventType)
		assert.Equal(t, transactions.ChargingStateSuspendedEV, req.TransactionInfo.ChargingState)
		assert.Equal(t, transactions.TriggerReasonChargingStateChanged, req.TriggerReason)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for replayed charging-state change")
	}
	require.Eventually(t, func() bool {
		return b.queue.Len() == 0
	}, time.Second, 10*time.Millisecond)
}

// TestDrainQueue_ReplaysEndedEventOnReconnect verifies that a
// TransactionEvent(Ended) emitted while the charger is offline is
// enqueued and replayed on reconnect, with the correct idTag.
func TestDrainQueue_ReplaysEndedEventOnReconnect(t *testing.T) {
	b := newTestBridge(t)
	b.queue = queue.NewInMemoryQueue(5)

	b.mu.Lock()
	b.txBuilders[1] = NewTransactionEventBuilder(1, 1)
	b.txIntToEVSE[42] = 1
	b.mu.Unlock()
	b.connected.Store(false)

	tag := "RFID-END-TAG"
	require.NoError(t, b.SendTransactionStop(99.9, time.Now().UTC(), 42, "Local", &tag, nil))
	require.Equal(t, 1, b.queue.Len(), "TransactionEvent(Ended) must be enqueued when offline")

	sent := make(chan *transactions.TransactionEventRequest, 1)
	b.cs = &stubChargingStation{
		bootNotification: func(reason provisioning.BootReason, model string, chargePointVendor string, props ...func(request *provisioning.BootNotificationRequest)) (*provisioning.BootNotificationResponse, error) {
			return provisioning.NewBootNotificationResponse(ocpp201types.NewDateTime(time.Now()), 300, provisioning.RegistrationStatusAccepted), nil
		},
		sendRequestAsync: func(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error {
			txReq, ok := request.(*transactions.TransactionEventRequest)
			require.True(t, ok)
			sent <- txReq
			if callback != nil {
				callback(transactions.NewTransactionEventResponse(), nil)
			}
			return nil
		},
	}

	b.connected.Store(true)
	b.drainQueue()

	select {
	case req := <-sent:
		assert.Equal(t, transactions.TransactionEventEnded, req.EventType)
		assert.Equal(t, transactions.ReasonLocal, req.TransactionInfo.StoppedReason)
		require.NotNil(t, req.IDToken)
		assert.Equal(t, "RFID-END-TAG", req.IDToken.IdToken)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for replayed Ended event")
	}
	require.Eventually(t, func() bool {
		return b.queue.Len() == 0
	}, time.Second, 10*time.Millisecond)
}

// TestDrainQueue_ClearsTxProfileOnStopAfterReplay verifies that stopping
// a transaction while the charger is offline still cleans up
// transaction-scoped charging profiles (the cleanup happens in
// SendTransactionStop before the queue is touched).
func TestDrainQueue_ClearsTxProfileOnStopAfterReplay(t *testing.T) {
	b := newTestBridge(t)
	b.queue = queue.NewInMemoryQueue(5)

	b.mu.Lock()
	b.txBuilders[1] = NewTransactionEventBuilder(1, 1)
	b.txIntToEVSE[42] = 1
	b.mu.Unlock()
	txID := b.txBuilders[1].TransactionID()
	b.profileManager.SetProfile(1, ocpp201types.ChargingProfile{
		ID:                     1,
		StackLevel:             0,
		ChargingProfilePurpose: ocpp201types.ChargingProfilePurposeTxProfile,
		TransactionID:          txID,
		ChargingProfileKind:    ocpp201types.ChargingProfileKindAbsolute,
		ChargingSchedule: []ocpp201types.ChargingSchedule{{
			ID:               1,
			ChargingRateUnit: ocpp201types.ChargingRateUnitAmperes,
			ChargingSchedulePeriod: []ocpp201types.ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 16.0},
			},
		}},
	})

	tag := "TX-PROFILE-TAG"
	require.NoError(t, b.SendTransactionStop(99.9, time.Now().UTC(), 42, "Local", &tag, nil))

	profiles := b.profileManager.GetAllProfiles()
	for _, p := range profiles {
		assert.NotEqual(t, txID, p.TransactionID, "TxProfile scoped to the stopped transaction must be cleared")
	}
}

// TestIntegration_QueuedTransactionEventRoundTrip verifies the full
// offline-to-online lifecycle: send a TransactionEvent while disconnected
// (gets enqueued), reconnect, observe the message arrive at the CSMS,
// and verify the queue is drained.
func TestIntegration_QueuedTransactionEventRoundTrip(t *testing.T) {
	port := freeReplayPort(t)
	handler := newReplayMockCSMSHandler()
	stopCSMS := startReplayMockCSMS(t, port, handler)
	defer stopCSMS()

	queuePath := filepath.Join(t.TempDir(), "offline_replay_queue.json")
	persistedQueue, err := queue.NewJsonFileQueue(queuePath, 5)
	require.NoError(t, err)

	cfg := config.DefaultConfig()
	cfg.OCPPID = "test-cp-roundtrip-001"
	cfg.ConnectionURL = fmt.Sprintf("ws://127.0.0.1:%d", port)

	// Build bridge and verify it cannot reach the CSMS yet.
	e := engine.NewEngine(false, 55000)
	dispatcher := ocpppkg.NewCommandDispatcher()
	bridge := NewBridge(e, nil, cfg, dispatcher, persistedQueue, nil)

	// Simulate offline: enqueue a TransactionEvent while disconnected.
	// (Registration is sticky across a disconnect — only a reboot/reset
	// clears it — so a previously-registered station stays registered.)
	bridge.connected.Store(false)
	bridge.registered.Store(true)
	_, err = bridge.SendTransactionStart(1, "TAG-OFFLINE", 100.0, time.Now().UTC(), nil)
	require.NoError(t, err)
	require.Equal(t, 1, persistedQueue.Len(), "TransactionEvent must be persisted while offline")

	// Now start the bridge and let it connect; the queued message must be
	// drained automatically.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dispCtx, dispCancel := context.WithCancel(context.Background())
	defer dispCancel()
	go dispatcher.Run(dispCtx)
	go func() { _ = bridge.Start(ctx) }()

	select {
	case <-handler.bootReceived:
	case <-time.After(8 * time.Second):
		t.Fatal("timeout waiting for BootNotification")
	}
	select {
	case evt := <-handler.transactionEvents:
		assert.Equal(t, transactions.TransactionEventStarted, evt.EventType)
		require.NotNil(t, evt.IDToken)
		assert.Equal(t, "TAG-OFFLINE", evt.IDToken.IdToken)
	case <-time.After(8 * time.Second):
		t.Fatal("timeout waiting for offline TransactionEvent replay")
	}
	require.Eventually(t, func() bool {
		return persistedQueue.Len() == 0
	}, 2*time.Second, 50*time.Millisecond, "queue must be drained after reconnect")
}
