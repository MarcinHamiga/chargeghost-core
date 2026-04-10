package v201

import (
	"testing"

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
