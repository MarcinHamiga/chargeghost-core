package v16

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
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
	cfg.OCPPPassword = stringPtr(password)

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

func stringPtr(s string) *string {
	return &s
}

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
