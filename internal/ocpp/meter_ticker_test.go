package ocpp

import (
	"context"
	"sync"
	"testing"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type meterTickerTestBridge struct {
	dispatcher         *CommandDispatcher
	connected          bool
	mu                 sync.Mutex
	intervalSeconds    int
	meterCalls         int
	lastMeterConnector int
	lastMeterTxID      int
	lastMeterContext   string
	meterValuesSent    chan struct{}
	configChanges      chan struct{}
}

func newMeterTickerTestBridge() *meterTickerTestBridge {
	return &meterTickerTestBridge{
		dispatcher:      NewCommandDispatcher(),
		intervalSeconds: 30,
		meterValuesSent: make(chan struct{}, 1),
		configChanges:   make(chan struct{}, 1),
	}
}

func (b *meterTickerTestBridge) Start(context.Context) error { return nil }

func (b *meterTickerTestBridge) Stop() {}

func (b *meterTickerTestBridge) IsConnected() bool { return b.connected }

func (b *meterTickerTestBridge) GetHeartbeatInterval() int { return 0 }

func (b *meterTickerTestBridge) Dispatcher() *CommandDispatcher { return b.dispatcher }

func (b *meterTickerTestBridge) Status() Status { return Status{Version: "test"} }

func (b *meterTickerTestBridge) SendBootNotification() error { return nil }

func (b *meterTickerTestBridge) SendHeartbeat() error { return nil }

func (b *meterTickerTestBridge) SendStatusNotification(connectorID int, errorCode, status string) error {
	return nil
}

func (b *meterTickerTestBridge) SendMeterValues(connectorID int, value float64, transactionID int, context string) error {
	b.meterCalls++
	b.lastMeterConnector = connectorID
	b.lastMeterTxID = transactionID
	b.lastMeterContext = context
	select {
	case b.meterValuesSent <- struct{}{}:
	default:
	}
	return nil
}

func (b *meterTickerTestBridge) SendAuthorize(idTag string) error { return nil }

func (b *meterTickerTestBridge) SendTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	return 0, nil
}

func (b *meterTickerTestBridge) SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error {
	return nil
}

func (b *meterTickerTestBridge) SendFirmwareStatusNotification(status string) error { return nil }

func (b *meterTickerTestBridge) SendDiagnosticsStatusNotification(status string) error { return nil }

func (b *meterTickerTestBridge) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
	return "", "", nil
}

func (b *meterTickerTestBridge) MaybeCompleteReset() {}
func (b *meterTickerTestBridge) DrainOfflineQueue()  {}

func (b *meterTickerTestBridge) SendTransactionEventUpdated(connectorID int, chargingState, trigger string) error {
	return nil
}

func (b *meterTickerTestBridge) SendConnectorEventNotification(connectorID int, component, instance, variable, actualValue string, evseComponent bool) error {
	return nil
}

func (b *meterTickerTestBridge) SendReservationStatusUpdate(reservationID int, status string) error {
	return nil
}

func (b *meterTickerTestBridge) GetMeterValueSampleInterval() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.intervalSeconds
}

func (b *meterTickerTestBridge) ConfigChanges() <-chan struct{} {
	return b.configChanges
}

func (b *meterTickerTestBridge) SetMeterValueSampleInterval(interval int) {
	b.mu.Lock()
	b.intervalSeconds = interval
	b.mu.Unlock()
	select {
	case b.configChanges <- struct{}{}:
	default:
	}
}

func TestStartMeterValueTicker_DisconnectedStillDispatchesMeterValues(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 16, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 0, nil, 0))
	e.SetActiveTransaction(1, 45)
	e.Simulate(1)

	bridge := newMeterTickerTestBridge()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.dispatcher.Run(ctx)
	bridge.SetMeterValueSampleInterval(1)
	go StartMeterValueTicker(ctx, e, bridge, bridge)

	select {
	case <-bridge.meterValuesSent:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SendMeterValues")
	}

	assert.Equal(t, 1, bridge.lastMeterConnector)
	assert.Equal(t, 45, bridge.lastMeterTxID)
	assert.Equal(t, "Sample.Periodic", bridge.lastMeterContext)
	assert.GreaterOrEqual(t, bridge.meterCalls, 1)
}

func TestStartMeterValueTicker_UsesUpdatedIntervalAfterStart(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 16, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 0, nil, 0))
	e.SetActiveTransaction(1, 45)
	e.Simulate(1)

	bridge := newMeterTickerTestBridge()
	bridge.SetMeterValueSampleInterval(2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go bridge.dispatcher.Run(ctx)
	go StartMeterValueTicker(ctx, e, bridge, bridge)

	select {
	case <-bridge.meterValuesSent:
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("timeout waiting for first SendMeterValues")
	}

	bridge.SetMeterValueSampleInterval(1)

	select {
	case <-bridge.meterValuesSent:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("timeout waiting for SendMeterValues after interval update")
	}

	assert.GreaterOrEqual(t, bridge.meterCalls, 2)
}
