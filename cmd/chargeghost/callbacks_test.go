package main

import (
	"context"
	"testing"
	"time"

	ws "github.com/chargeghost/engine/internal/api/ws"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testBridge struct {
	dispatcher           *ocpp.CommandDispatcher
	connected            bool
	startCalls           int
	stopCalls            int
	statusCalls          int
	lastStartConnectorID int
	lastStopTransaction  int
	lastStatusConnector  int
	startTransactionID   int
	startCalled          chan struct{}
	stopCalled           chan struct{}
}

func newTestBridge() *testBridge {
	return &testBridge{
		dispatcher:         ocpp.NewCommandDispatcher(),
		startTransactionID: 77,
		startCalled:        make(chan struct{}, 1),
		stopCalled:         make(chan struct{}, 1),
	}
}

func (b *testBridge) Start(context.Context) error { return nil }

func (b *testBridge) Stop() {}

func (b *testBridge) IsConnected() bool { return b.connected }

func (b *testBridge) GetHeartbeatInterval() int { return 0 }

func (b *testBridge) Status() ocpp.Status { return ocpp.Status{} }

func (b *testBridge) Dispatcher() *ocpp.CommandDispatcher { return b.dispatcher }

func (b *testBridge) SendBootNotification() error { return nil }

func (b *testBridge) SendHeartbeat() error { return nil }

func (b *testBridge) SendStatusNotification(connectorID int, errorCode, status string) error {
	b.statusCalls++
	b.lastStatusConnector = connectorID
	return nil
}

func (b *testBridge) SendMeterValues(connectorID int, value float64, transactionID int, context string) error {
	return nil
}

func (b *testBridge) SendAuthorize(idTag string) error { return nil }

func (b *testBridge) SendTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	b.startCalls++
	b.lastStartConnectorID = connectorID
	select {
	case b.startCalled <- struct{}{}:
	default:
	}
	return b.startTransactionID, nil
}

func (b *testBridge) SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error {
	b.stopCalls++
	b.lastStopTransaction = transactionID
	select {
	case b.stopCalled <- struct{}{}:
	default:
	}
	return nil
}

func (b *testBridge) SendFirmwareStatusNotification(status string) error { return nil }

func (b *testBridge) SendDiagnosticsStatusNotification(status string) error { return nil }

func (b *testBridge) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
	return "", "", nil
}

func TestSessionStartedCallback_OfflineStartDoesNotClearTransactionID(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 16, 1)
	e.PlugIn(1)

	hub := ws.NewHub()
	bridge := newTestBridge()
	bridge.startTransactionID = 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.dispatcher.Run(ctx)

	e.OnSessionStarted = newSessionStartedCallback(e, hub, bridge, bridge.dispatcher)

	idTag := "TEST-TAG"
	require.NoError(t, e.StartSession(1, -1, &idTag, 0))

	select {
	case <-bridge.startCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SendTransactionStart")
	}

	txID := e.GetActiveTransactionID(1)
	require.NotNil(t, txID)
	assert.Equal(t, -1, *txID)
}

func TestSessionStartedCallback_DisconnectedStillHandsOffToBridge(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 16, 1)
	e.PlugIn(1)

	hub := ws.NewHub()
	bridge := newTestBridge()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.dispatcher.Run(ctx)

	e.OnSessionStarted = newSessionStartedCallback(e, hub, bridge, bridge.dispatcher)

	idTag := "TEST-TAG"
	require.NoError(t, e.StartSession(1, 0, &idTag, 0))

	select {
	case <-bridge.startCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SendTransactionStart")
	}

	assert.Equal(t, 1, bridge.startCalls)
	assert.Equal(t, 1, bridge.lastStartConnectorID)
	assert.Equal(t, bridge.startTransactionID, *e.GetActiveTransactionID(1))
}

func TestSessionStoppedCallback_DisconnectedStillHandsOffToBridge(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 16, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 0, nil, 0))
	e.SetActiveTransaction(1, 88)

	hub := ws.NewHub()
	bridge := newTestBridge()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.dispatcher.Run(ctx)

	e.OnSessionStopped = newSessionStoppedCallback(hub, bridge, bridge.dispatcher)

	connID := 1
	stopped := e.StopSession(&connID, "Local")
	require.NotNil(t, stopped)

	select {
	case <-bridge.stopCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SendTransactionStop")
	}

	assert.Equal(t, 1, bridge.stopCalls)
	assert.Equal(t, 88, bridge.lastStopTransaction)
}

func TestConnectorStatusChangedCallback_DisconnectedDoesNotSend(t *testing.T) {
	hub := ws.NewHub()
	bridge := newTestBridge()

	e := engine.NewEngine(false, 55000)
	cb := newConnectorStatusChangedCallback(e, hub, bridge, bridge.dispatcher)
	cb(3, engine.StateCharging)

	assert.Equal(t, 0, bridge.statusCalls)
}
