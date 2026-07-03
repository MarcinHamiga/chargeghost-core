package v16

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	wsapi "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpp "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

func TestBridge16_UsesBasicAuthWhenPasswordConfigured(t *testing.T) {
	const password = "secret-password"

	server := newBasicAuthTestServer(t, "ocpp1.6", func(r *http.Request) bool {
		user, pass, ok := r.BasicAuth()
		return ok && user == "CP_1" && pass == password
	})
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.ConnectionURL = wsURLForServer(server, "/CP_1")
	cfg.OCPPPassword = strPtr(password)

	e := engine.NewEngine(false, 55000)
	dispatcher := ocpp.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)

	b := NewBridge(
		e,
		wsapi.NewHub(),
		cfg,
		dispatcher,
		NewChargingProfileManager(),
		NewConfigKeyManager(),
		ocpp.NewAuthorizationCache(),
		ocpp.NewLocalAuthListManager(),
		q,
		ocpp.NewFirmwareManager(nil),
		ocpp.NewDiagnosticsManager(nil),
		ocpp.NewDataTransferRegistry(),
		nil,
	)

	err = b.cp.Start(cfg.ConnectionURL)
	require.NoError(t, err)
	t.Cleanup(b.cp.Stop)
}

func newBasicAuthTestServer(t *testing.T, subprotocol string, validAuth func(r *http.Request) bool) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{
		CheckOrigin:  func(r *http.Request) bool { return true },
		Subprotocols: []string{subprotocol},
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validAuth(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
}

func wsURLForServer(server *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(server.URL, "http") + path
}

func strPtr(s string) *string { return &s }

func TestNewBridge_Creates(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	dispatcher := ocpp.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)

	b := NewBridge(
		e,
		nil,
		cfg,
		dispatcher,
		NewChargingProfileManager(),
		NewConfigKeyManager(),
		ocpp.NewAuthorizationCache(),
		ocpp.NewLocalAuthListManager(),
		q,
		ocpp.NewFirmwareManager(nil),
		ocpp.NewDiagnosticsManager(nil),
		ocpp.NewDataTransferRegistry(),
		nil,
	)

	require.NotNil(t, b)
	assert.False(t, b.IsConnected())
	assert.Equal(t, 300, b.GetHeartbeatInterval())
	assert.NotNil(t, b.Dispatcher())
}

// TestBridge16_StartRetriesUntilCancel is a regression test: a dial failure
// on the very first connect attempt used to be logged and then the bridge
// sat there with Connected=false forever — ocpp-go's own auto-reconnect only
// engages after a connection that once succeeded drops. Start must now retry
// with backoff, recording each attempt on the status tracker, and must
// return promptly once ctx is cancelled instead of hanging until some
// external actor restarts it.
func TestBridge16_StartRetriesUntilCancel(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	// Port 1 refuses connections immediately (nothing listens there, and it
	// requires root anyway) — keeps the test fast and independent of DNS.
	cfg.ConnectionURL = "ws://127.0.0.1:1/CP_1"
	dispatcher := ocpp.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)

	b := NewBridge(
		e,
		nil,
		cfg,
		dispatcher,
		NewChargingProfileManager(),
		NewConfigKeyManager(),
		ocpp.NewAuthorizationCache(),
		ocpp.NewLocalAuthListManager(),
		q,
		ocpp.NewFirmwareManager(nil),
		ocpp.NewDiagnosticsManager(nil),
		ocpp.NewDataTransferRegistry(),
		nil,
	)
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

func TestBridge16_GetHeartbeatIntervalReflectsLiveConfig(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	dispatcher := ocpp.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)

	keys := NewConfigKeyManager()
	b := NewBridge(
		e,
		nil,
		cfg,
		dispatcher,
		NewChargingProfileManager(),
		keys,
		ocpp.NewAuthorizationCache(),
		ocpp.NewLocalAuthListManager(),
		q,
		ocpp.NewFirmwareManager(nil),
		ocpp.NewDiagnosticsManager(nil),
		ocpp.NewDataTransferRegistry(),
		nil,
	)

	assert.Equal(t, 300, b.GetHeartbeatInterval())
	assert.Equal(t, "Accepted", keys.SetConfigValue("HeartbeatInterval", "42"))
	assert.Equal(t, 42, b.GetHeartbeatInterval())
}

func TestConvertChargingProfile_CopiesTransactionId(t *testing.T) {
	schedule := &types.ChargingSchedule{
		ChargingRateUnit: types.ChargingRateUnitAmperes,
		ChargingSchedulePeriod: []types.ChargingSchedulePeriod{
			{StartPeriod: 0, Limit: 16},
		},
	}
	p := types.NewChargingProfile(1, 0, types.ChargingProfilePurposeTxProfile, types.ChargingProfileKindAbsolute, schedule)
	p.TransactionId = 100

	profile := convertChargingProfile(p, 1)
	require.NotNil(t, profile)
	assert.Equal(t, "100", profile.TransactionID)
}

func TestConvertChargingProfile_UnsetTransactionIdStaysEmpty(t *testing.T) {
	schedule := &types.ChargingSchedule{
		ChargingRateUnit: types.ChargingRateUnitAmperes,
		ChargingSchedulePeriod: []types.ChargingSchedulePeriod{
			{StartPeriod: 0, Limit: 16},
		},
	}
	p := types.NewChargingProfile(1, 0, types.ChargingProfilePurposeTxDefaultProfile, types.ChargingProfileKindAbsolute, schedule)

	profile := convertChargingProfile(p, 1)
	require.NotNil(t, profile)
	assert.Empty(t, profile.TransactionID)
}
