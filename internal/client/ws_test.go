package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ws "github.com/chargeghost/engine/internal/api/ws"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/stretchr/testify/require"
)

// wsTestServer serves /ws backed by a real ws.Hub whose pointer can be
// swapped so the reconnect test can replace a cancelled hub underneath a
// live subscriber.
type wsTestServer struct {
	srv    *httptest.Server
	hubPtr chan *ws.Hub
}

func newWSHandshake(w http.ResponseWriter, r *http.Request, hub *ws.Hub) bool {
	upgrader := ws.NewUpgrader(nil)
	snapshot := ws.Message{Type: "state_snapshot", Data: map[string]interface{}{"scope": "all"}}
	stationID, scope := ws.ScopeFromRequest(r, "CP_1")
	hub.ServeWSWithUpgrader(w, r, upgrader, snapshot, scope, stationID)
	return true
}

func newWSTestServer(t *testing.T) *wsTestServer {
	t.Helper()
	hubPtr := make(chan *ws.Hub, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		select {
		case hub := <-hubPtr:
			hubPtr <- hub
			newWSHandshake(w, r, hub)
		default:
			http.Error(w, "no hub", http.StatusServiceUnavailable)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &wsTestServer{srv: srv, hubPtr: hubPtr}
}

func (s *wsTestServer) setHub(h *ws.Hub) {
	select {
	case <-s.hubPtr:
	default:
	}
	s.hubPtr <- h
}

func (s *wsTestServer) startHub(t *testing.T) (*ws.Hub, context.CancelFunc) {
	t.Helper()
	hub := ws.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	s.setHub(hub)
	return hub, cancel
}

func waitForEvent(t *testing.T, ch <-chan Event, timeout time.Duration, pred func(Event) bool) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			require.True(t, ok, "event channel closed unexpectedly")
			if pred(ev) {
				return ev
			}
		case <-deadline:
			t.Fatal("timed out waiting for expected event")
		}
	}
}

func TestWSPushReservesCapacityForNonPeriodicEvents(t *testing.T) {
	events := &Events{out: make(chan Event, eventChanCap)}

	for i := 0; i < eventChanCap; i++ {
		events.push(Event{Type: "tick"})
	}
	require.Len(t, events.out, eventChanCap-eventPriorityReserve)

	events.push(Event{Type: "station_lifecycle_changed"})
	require.Len(t, events.out, eventChanCap-eventPriorityReserve+1)

	for len(events.out) > 1 {
		<-events.out
	}
	require.Equal(t, "station_lifecycle_changed", (<-events.out).Type)
}

func TestWSSubscriberSnapshotAndFleetTick(t *testing.T) {
	ts := newWSTestServer(t)
	hub, stopHub := ts.startHub(t)
	defer stopHub()

	c := New(ts.srv.URL)
	events := c.Subscribe("scope=all")
	defer events.Stop()

	// First message after connect is the server's state_snapshot.
	snapEv := waitForEvent(t, events.Chan(), 5*time.Second, func(ev Event) bool {
		return ev.Type == "state_snapshot"
	})
	require.Equal(t, json.RawMessage(`{"scope":"all"}`), snapEv.Raw)

	// A broadcast fleet_tick arrives as an envelope-only event.
	hub.BroadcastMessage(ws.BuildFleetStatusSnapshot(map[string]*ws.EngineSnapshotSource{}))
	fleetEv := waitForEvent(t, events.Chan(), 5*time.Second, func(ev Event) bool {
		return ev.Type == "fleet_tick"
	})
	stations, err := DecodeFleetTick(fleetEv.Raw)
	require.NoError(t, err)
	require.Empty(t, stations)

	// Station-scoped tick, built from a real engine like the server does.
	e := engine.NewEngine(false, 0)
	e.AddConnector(230.0, 32.0, 1)
	hub.BroadcastMessage(ws.BuildStationStatusSnapshot("CP_1", e, true, 1.5))
	tickEv := waitForEvent(t, events.Chan(), 5*time.Second, func(ev Event) bool {
		return ev.Type == "tick" && ev.StationID == "CP_1"
	})
	tick, err := DecodeTick(tickEv.Raw)
	require.NoError(t, err)
	require.True(t, tick.OCPPConnected)
	require.Len(t, tick.Connectors, 1)
}

func TestWSSubscriberReconnectsAfterServerClose(t *testing.T) {
	ts := newWSTestServer(t)
	_, stopHub := ts.startHub(t)

	c := New(ts.srv.URL)
	events := c.Subscribe("scope=all")
	defer events.Stop()

	// Consume the initial snapshot so the link is established.
	waitForEvent(t, events.Chan(), 5*time.Second, func(ev Event) bool {
		return ev.Type == "state_snapshot"
	})

	// Cancelling the hub closes all client connections server-side.
	stopHub()

	waitForEvent(t, events.Chan(), 10*time.Second, func(ev Event) bool {
		return ev.Type == EventDisconnected
	})

	// Bring a fresh hub up on the same endpoint; the subscriber's
	// backoff re-dial must succeed and surface __reconnected.
	ts.startHub(t)

	waitForEvent(t, events.Chan(), 10*time.Second, func(ev Event) bool {
		return ev.Type == EventReconnected
	})

	// The new connection gets a fresh snapshot too.
	waitForEvent(t, events.Chan(), 5*time.Second, func(ev Event) bool {
		return ev.Type == "state_snapshot"
	})
}

func TestWSSubscriberStopTerminatesPromptly(t *testing.T) {
	ts := newWSTestServer(t)
	ts.startHub(t)

	c := New(ts.srv.URL)
	events := c.Subscribe("scope=all")

	waitForEvent(t, events.Chan(), 5*time.Second, func(ev Event) bool {
		return ev.Type == "state_snapshot"
	})

	done := make(chan struct{})
	go func() {
		events.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within 5s")
	}
}
