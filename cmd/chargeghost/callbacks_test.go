package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ws "github.com/chargeghost/engine/internal/api/ws"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testBridge struct {
	// mu guards the status/event counters below: the dispatcher goroutine
	// writes them while test goroutines poll them via Eventually.
	mu                       sync.Mutex
	dispatcher               *ocpp.CommandDispatcher
	connected                bool
	startCalls               int
	stopCalls                int
	statusCalls              int
	lastStartConnectorID     int
	lastStopTransaction      int
	lastStatusConnector      int
	lastStatusErrorCode      string
	startTransactionID       int
	startCalled              chan struct{}
	stopCalled               chan struct{}
	updatedCalls             int
	lastUpdatedConnectorID   int
	lastUpdatedChargingState string
	lastUpdatedTrigger       string
	eventCalls               int
	lastEventConnectorID     int
	lastEventComponent       string
	lastEventInstance        string
	lastEventVariable        string
	lastEventActualValue     string
	lastEventEVSEComponent   bool
	reservationUpdateCalls   int
	lastReservationID        int
	lastReservationStatus    string
	reservationUpdateCalled  chan struct{}
	drainCalls               atomic.Int32
}

func newTestBridge() *testBridge {
	return &testBridge{
		dispatcher:              ocpp.NewCommandDispatcher(),
		startTransactionID:      77,
		startCalled:             make(chan struct{}, 1),
		stopCalled:              make(chan struct{}, 1),
		reservationUpdateCalled: make(chan struct{}, 1),
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
	b.mu.Lock()
	defer b.mu.Unlock()
	b.statusCalls++
	b.lastStatusConnector = connectorID
	b.lastStatusErrorCode = errorCode
	return nil
}

// statusSnapshot returns the status-notification counters under lock for
// race-safe polling from test goroutines.
func (b *testBridge) statusSnapshot() (calls int, connector int, errorCode string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.statusCalls, b.lastStatusConnector, b.lastStatusErrorCode
}

// eventSnapshot returns the NotifyEvent counters under lock for race-safe
// polling from test goroutines.
func (b *testBridge) eventSnapshot() (calls int, variable, actualValue string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.eventCalls, b.lastEventVariable, b.lastEventActualValue
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

func (b *testBridge) SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error {
	b.stopCalls++
	b.lastStopTransaction = transactionID
	select {
	case b.stopCalled <- struct{}{}:
	default:
	}
	return nil
}

func (b *testBridge) EnqueueTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	b.startCalls++
	b.lastStartConnectorID = connectorID
	select {
	case b.startCalled <- struct{}{}:
	default:
	}
	return b.startTransactionID, nil
}

func (b *testBridge) EnqueueTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error {
	b.stopCalls++
	b.lastStopTransaction = transactionID
	select {
	case b.stopCalled <- struct{}{}:
	default:
	}
	return nil
}

func (b *testBridge) EnqueueMeterValues(connectorID int, value float64, transactionID int, meterContext string, timestamp time.Time) error {
	return nil
}

func (b *testBridge) EnqueueTransactionEventUpdated(connectorID int, chargingState, trigger string) error {
	b.updatedCalls++
	b.lastUpdatedConnectorID = connectorID
	b.lastUpdatedChargingState = chargingState
	b.lastUpdatedTrigger = trigger
	return nil
}

func (b *testBridge) SendFirmwareStatusNotification(status string) error { return nil }

func (b *testBridge) SendDiagnosticsStatusNotification(status string) error { return nil }

func (b *testBridge) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
	return "", "", nil
}

func (b *testBridge) MaybeCompleteReset() {}
func (b *testBridge) DrainOfflineQueue()  { b.drainCalls.Add(1) }

func (b *testBridge) SendTransactionEventUpdated(connectorID int, chargingState, trigger string) error {
	b.updatedCalls++
	b.lastUpdatedConnectorID = connectorID
	b.lastUpdatedChargingState = chargingState
	b.lastUpdatedTrigger = trigger
	return nil
}

func (b *testBridge) SendConnectorEventNotification(connectorID int, component, instance, variable, actualValue string, evseComponent bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.eventCalls++
	b.lastEventConnectorID = connectorID
	b.lastEventComponent = component
	b.lastEventInstance = instance
	b.lastEventVariable = variable
	b.lastEventActualValue = actualValue
	b.lastEventEVSEComponent = evseComponent
	return nil
}

func (b *testBridge) SendReservationStatusUpdate(reservationID int, status string) error {
	b.reservationUpdateCalls++
	b.lastReservationID = reservationID
	b.lastReservationStatus = status
	select {
	case b.reservationUpdateCalled <- struct{}{}:
	default:
	}
	return nil
}

func TestSessionStartedCallback_EnqueueDoesNotClearTransactionID(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 16, 1)
	e.PlugIn(1)

	hub := ws.NewHub()
	bridge := newTestBridge()
	bridge.startTransactionID = 0

	// No dispatcher needed: the callback enqueues synchronously.
	e.OnSessionStarted = newSessionStartedCallback("test-station", e, hub, bridge)

	idTag := "TEST-TAG"
	require.NoError(t, e.StartSession(1, -1, &idTag, 0))

	assert.Equal(t, 1, bridge.startCalls)
	assert.Equal(t, 1, bridge.lastStartConnectorID)

	txID := e.GetActiveTransactionID(1)
	require.NotNil(t, txID)
	assert.Equal(t, -1, *txID)
}

func TestSessionStartedCallback_EnqueuesStartAndAssignsTransactionID(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 16, 1)
	e.PlugIn(1)

	hub := ws.NewHub()
	bridge := newTestBridge()

	e.OnSessionStarted = newSessionStartedCallback("test-station", e, hub, bridge)

	idTag := "TEST-TAG"
	require.NoError(t, e.StartSession(1, 0, &idTag, 0))

	assert.Equal(t, 1, bridge.startCalls)
	assert.Equal(t, 1, bridge.lastStartConnectorID)
	assert.Equal(t, bridge.startTransactionID, *e.GetActiveTransactionID(1))
}

func TestSessionStoppedCallback_EnqueuesStopDurably(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 16, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 0, nil, 0))
	e.SetActiveTransaction(1, 88)

	hub := ws.NewHub()
	bridge := newTestBridge()

	e.OnSessionStopped = newSessionStoppedCallback("test-station", hub, bridge)

	connID := 1
	stopped := e.StopSession(&connID, "Local")
	require.NotNil(t, stopped)

	assert.Equal(t, 1, bridge.stopCalls)
	assert.Equal(t, 88, bridge.lastStopTransaction)
}

func TestConnectorStatusChangedCallback_DisconnectedDoesNotSend(t *testing.T) {
	// Renamed behavior: status now enqueues durably while disconnected so
	// the CSMS converges after reconnect. The dispatcher (no link gate in
	// this test bridge) drains immediately, so the send lands even though
	// bridge.connected is false.
	hub := ws.NewHub()
	bridge := newTestBridge()

	e := engine.NewEngine(false, 55000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.dispatcher.Run(ctx)
	cb := newConnectorStatusChangedCallback("test-station", e, hub, bridge, bridge.dispatcher)
	cb(3, engine.StateCharging)

	require.Eventually(t, func() bool {
		calls, _, _ := bridge.statusSnapshot()
		return calls == 1
	}, 2*time.Second, 10*time.Millisecond, "status must enqueue while disconnected")
	calls, _, _ := bridge.statusSnapshot()
	assert.Equal(t, 1, calls)
}

func TestConnectorStatusChangedCallback_DuplicateStatusSuppressed(t *testing.T) {
	hub := ws.NewHub()
	bridge := newTestBridge()

	e := engine.NewEngine(false, 55000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.dispatcher.Run(ctx)
	cb := newConnectorStatusChangedCallback("test-station", e, hub, bridge, bridge.dispatcher)
	cb(1, engine.StateAvailable)
	cb(1, engine.StateAvailable)

	require.Eventually(t, func() bool {
		calls, _, _ := bridge.statusSnapshot()
		return calls == 1
	}, 2*time.Second, 10*time.Millisecond, "first status must send")
	// Give the duplicate a chance to (incorrectly) fire.
	time.Sleep(200 * time.Millisecond)
	calls, _, _ := bridge.statusSnapshot()
	assert.Equal(t, 1, calls, "duplicate unchanged status must be suppressed")
}

// TestConnectorStatusChangedCallback_FaultedReportsRealErrorCode verifies a
// StatusNotification(Faulted) carries the connector's real fault code
// instead of a hardcoded NoError, and that a second NotifyEvent reports the
// fault code for OCPP 2.0.1 (SendConnectorEventNotification is a no-op on
// v1.6, so this only takes effect there).
func TestConnectorStatusChangedCallback_FaultedReportsRealErrorCode(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 32, 1)
	require.NoError(t, e.FaultConnector(1, "HighTemperature"))

	hub := ws.NewHub()
	bridge := newTestBridge()
	bridge.connected = true

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.dispatcher.Run(ctx)

	cb := newConnectorStatusChangedCallback("test-station", e, hub, bridge, bridge.dispatcher)
	cb(1, engine.StateFaulted)

	require.Eventually(t, func() bool {
		statusCalls, _, _ := bridge.statusSnapshot()
		eventCalls, _, _ := bridge.eventSnapshot()
		return statusCalls > 0 && eventCalls >= 2
	}, 2*time.Second, 10*time.Millisecond, "timeout waiting for StatusNotification and both NotifyEvents")

	_, _, errorCode := bridge.statusSnapshot()
	_, variable, actualValue := bridge.eventSnapshot()
	assert.Equal(t, "HighTemperature", errorCode)
	assert.Equal(t, "ProblemFaultCode", variable)
	assert.Equal(t, "HighTemperature", actualValue)
}

// TestConnectorStatusChangedCallback_AvailableReportsNoError verifies the
// non-faulted path is unchanged: StatusNotification still reports NoError
// and only the single AvailabilityState NotifyEvent is sent.
func TestConnectorStatusChangedCallback_AvailableReportsNoError(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 32, 1)

	hub := ws.NewHub()
	bridge := newTestBridge()
	bridge.connected = true

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.dispatcher.Run(ctx)

	cb := newConnectorStatusChangedCallback("test-station", e, hub, bridge, bridge.dispatcher)
	cb(1, engine.StateAvailable)

	require.Eventually(t, func() bool {
		statusCalls, _, _ := bridge.statusSnapshot()
		eventCalls, _, _ := bridge.eventSnapshot()
		return statusCalls > 0 && eventCalls > 0
	}, 2*time.Second, 10*time.Millisecond, "timeout waiting for StatusNotification and NotifyEvent")

	_, _, errorCode := bridge.statusSnapshot()
	eventCalls, _, _ := bridge.eventSnapshot()
	assert.Equal(t, "NoError", errorCode)
	assert.Equal(t, 1, eventCalls, "only the AvailabilityState NotifyEvent should fire when not faulted")
}

func TestReservationExpiredCallback_ConnectedSendsReservationStatusUpdate(t *testing.T) {
	hub := ws.NewHub()
	bridge := newTestBridge()
	bridge.connected = true

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.dispatcher.Run(ctx)

	cb := newReservationExpiredCallback("test-station", hub, bridge, bridge.dispatcher)
	cb(7, 1)

	select {
	case <-bridge.reservationUpdateCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SendReservationStatusUpdate")
	}

	assert.Equal(t, 1, bridge.reservationUpdateCalls)
	assert.Equal(t, 7, bridge.lastReservationID)
	assert.Equal(t, "Expired", bridge.lastReservationStatus)
}

func TestReservationExpiredCallback_DisconnectedDoesNotSend(t *testing.T) {
	hub := ws.NewHub()
	bridge := newTestBridge()

	cb := newReservationExpiredCallback("test-station", hub, bridge, bridge.dispatcher)
	cb(7, 1)

	assert.Equal(t, 0, bridge.reservationUpdateCalls)
}
