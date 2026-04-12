package v201

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpp "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

func TestBridge201_UsesBasicAuthWhenPasswordConfigured(t *testing.T) {
	const password = "secret-password"

	server := newBasicAuthTestServer201(t, "ocpp2.0.1", func(r *http.Request) bool {
		user, pass, ok := r.BasicAuth()
		return ok && user == "CP_1" && pass == password
	})
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.ConnectionURL = wsURLForServer201(server, "/CP_1")
	cfg.OCPPVersion = "2.0.1"
	cfg.OCPPPassword = &[]string{password}[0]

	e := engine.NewEngine(false, 55000)
	dispatcher := ocpp.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)

	b := NewBridge(e, nil, cfg, dispatcher, q, nil)

	err = b.cs.Start(cfg.ConnectionURL)
	require.NoError(t, err)
	t.Cleanup(b.cs.Stop)
}

func newBasicAuthTestServer201(t *testing.T, subprotocol string, validAuth func(r *http.Request) bool) *httptest.Server {
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

func wsURLForServer201(server *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(server.URL, "http") + path
}
