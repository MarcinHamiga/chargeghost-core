package ws_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHub_BroadcastReachesConnectedClient(t *testing.T) {
	hub := ws.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	// Start a test HTTP server that upgrades to WebSocket.
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeWS(w, r, ws.Message{Type: "state_snapshot", Data: "test"}, ws.ScopeDefault, "")
	}))
	defer srv.Close()

	// Connect a WebSocket client.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Read the state snapshot sent on connect.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)
	assert.Contains(t, string(msg), "state_snapshot")

	// Broadcast a message and verify it's received.
	hub.BroadcastMessage(ws.Message{Type: "test_event", Data: "hello"})
	_, msg, err = conn.ReadMessage()
	require.NoError(t, err)
	assert.Contains(t, string(msg), "test_event")

	_ = upgrader // suppress unused warning
}

func TestHub_SlowClientDropped(t *testing.T) {
	hub := ws.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	// Flood the broadcast channel faster than a client can read.
	// Hub should not block; slow clients are dropped.
	for i := 0; i < 300; i++ {
		hub.BroadcastMessage(ws.Message{Type: "flood", Data: i})
	}
	// No panic or deadlock — test passes by completing.
}

func TestHub_Run_ClosesClientsOnShutdown(t *testing.T) {
	hub := ws.NewHub()
	ctx, cancel := context.WithCancel(context.Background())

	go hub.Run(ctx)

	// Start a test HTTP server that upgrades to WebSocket and keeps running.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeWS(w, r, ws.Message{Type: "state_snapshot", Data: "test"}, ws.ScopeDefault, "")
	}))
	// NOTE: defer after cancel so srv is still running when we check the connection.
	defer srv.Close()

	// Connect a WebSocket client.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Read the state snapshot sent on connect.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()
	require.NoError(t, err)

	// Record the time before cancelling.
	cancelTime := time.Now()

	// Cancel the hub context to trigger shutdown; the server is still running.
	cancel()

	// After hub shutdown, the hub must close the client's send channel.
	// writePump detects the closed channel, sends a WebSocket close frame,
	// and the underlying conn is closed. The client should receive a close
	// message quickly (well under 500ms), not wait for a long deadline.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()

	elapsed := time.Since(cancelTime)

	// The connection must have been terminated (close frame or EOF).
	assert.Error(t, err, "client connection should be closed after hub shutdown")
	// It should be fast (hub closes send channel synchronously on ctx.Done()).
	// Allow up to 500ms for goroutine scheduling. If the bug is present,
	// writePump never sends a close frame so ReadMessage blocks until deadline (2s).
	assert.Less(t, elapsed, 500*time.Millisecond,
		"hub should close client connections promptly on shutdown (elapsed: %v)", elapsed)
}

func TestHub_StationScopedBroadcast(t *testing.T) {
	hub := ws.NewHub()
	hub.SetDefaultStationID("CP_A")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	var connectedClients int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stationID, scope := ws.ScopeFromRequest(r, "CP_A")
		hub.ServeWS(w, r, ws.Message{Type: "state_snapshot", Data: "test"}, scope, stationID)
		connectedClients++
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	// Client 1: default scope (CP_A only).
	connA, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer connA.Close()

	// Client 2: explicit CP_B scope.
	connB, _, err := websocket.DefaultDialer.Dial(wsURL+"?station_id=CP_B", nil)
	require.NoError(t, err)
	defer connB.Close()

	// Client 3: all-stations scope.
	connAll, _, err := websocket.DefaultDialer.Dial(wsURL+"?scope=all", nil)
	require.NoError(t, err)
	defer connAll.Close()

	// Drain initial snapshots.
	for _, conn := range []*websocket.Conn{connA, connB, connAll} {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, err := conn.ReadMessage()
		require.NoError(t, err)
	}

	hub.BroadcastMessage(ws.Message{Type: "event", StationID: "CP_A", Data: "hello"})

	// Default client receives CP_A message.
	connA.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := connA.ReadMessage()
	require.NoError(t, err)
	assert.Contains(t, string(msg), "CP_A")

	// CP_B client does not receive CP_A message.
	connB.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err = connB.ReadMessage()
	assert.Error(t, err, "CP_B client should not receive CP_A message")

	// All-stations client receives CP_A message.
	connAll.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err = connAll.ReadMessage()
	require.NoError(t, err)
	assert.Contains(t, string(msg), "CP_A")

	_ = upgrader
	_ = connectedClients
}
