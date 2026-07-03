package v201

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpp "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

func TestNewBridge_Creates(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	dispatcher := ocpp.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)

	b := NewBridge(e, nil, cfg, dispatcher, q, nil)
	require.NotNil(t, b)
	assert.False(t, b.IsConnected())
	assert.Equal(t, 300, b.GetHeartbeatInterval())
	assert.NotNil(t, b.Dispatcher())
}

// TestBridge201_StartRetriesUntilCancel is a regression test: a dial
// failure on the very first connect attempt used to be logged and then the
// bridge sat there with Connected=false forever — ocpp-go's own
// auto-reconnect only engages after a connection that once succeeded drops.
// Start must now retry with backoff, recording each attempt on the status
// tracker, and must return promptly once ctx is cancelled instead of
// hanging until some external actor restarts it.
func TestBridge201_StartRetriesUntilCancel(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	// Port 1 refuses connections immediately (nothing listens there, and it
	// requires root anyway) — keeps the test fast and independent of DNS.
	cfg.ConnectionURL = "ws://127.0.0.1:1/CP_1"
	dispatcher := ocpp.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)

	b := NewBridge(e, nil, cfg, dispatcher, q, nil)
	b.connectBackoffBase = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Start(ctx) }()

	require.Eventually(t, func() bool {
		snap := b.statusTracker.Snapshot("", "", "")
		return snap.ConnectAttempts >= 2
	}, 2*time.Second, 5*time.Millisecond, "Start must retry the dial with backoff")

	snap := b.statusTracker.Snapshot("", "", "")
	assert.True(t, snap.Connecting)
	assert.False(t, snap.Connected)
	assert.NotEmpty(t, snap.LastError)

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return promptly after ctx cancellation")
	}
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.False(t, b.IsConnected())
}

func TestBridge201_SetManagers(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	dispatcher := ocpp.NewCommandDispatcher()
	q, _ := queue.NewQueue(false, "", 0)

	b := NewBridge(e, nil, cfg, dispatcher, q, nil)
	fw := ocpp.NewFirmwareManager(nil)
	diag := ocpp.NewDiagnosticsManager(nil)
	dt := ocpp.NewDataTransferRegistry()
	la := ocpp.NewLocalAuthListManager()
	authCache := ocpp.NewAuthorizationCache()

	// Should not panic
	b.SetManagers(authCache, la, fw, diag, dt)
}

func TestBridge201_GetHeartbeatIntervalReflectsLiveDeviceModel(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	dispatcher := ocpp.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)

	b := NewBridge(e, nil, cfg, dispatcher, q, nil)

	assert.Equal(t, 300, b.GetHeartbeatInterval())
	assert.Equal(t, "Accepted", b.DeviceModel().SetConfigValue("OCPPCommCtrlr.HeartbeatInterval", "42"))
	assert.Equal(t, 42, b.GetHeartbeatInterval())
}
