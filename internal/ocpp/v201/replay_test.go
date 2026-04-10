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
