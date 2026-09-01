package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chargeghost/engine/internal/api"
	"github.com/stretchr/testify/require"
)

// newFakeAPI spins an httptest.Server serving the endpoints the client
// exercises, with responses shaped exactly like the real handlers
// (internal/api handlers_fleet.go).
func newFakeAPI(t *testing.T, mutate func(mux *http.ServeMux)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/stations", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.OperationResponse{
				Success: true, OperationID: "op-1", Message: "station created",
			})
			return
		}
		writeJSON(w, []api.StationSnapshot{{
			StationID: "CP_1", OCPPID: "CP_1", Enabled: true,
			LifecycleState: "running", OCPPVersion: "1.6",
			Connected: true, ConnectorCount: 2, ActiveSessionCount: 1,
		}})
	})
	mux.HandleFunc("/api/v1/stations/CP_1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, api.StationSnapshot{StationID: "CP_1", LifecycleState: "running"})
	})
	mux.HandleFunc("/api/v1/stations/CP_1/start", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.OperationResponse{
			Success: true, OperationID: "op-start-1", Message: "station start requested",
		})
	})
	mux.HandleFunc("/api/v1/stations/CP_1/stop", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, api.Response{Success: false, Message: "station already stopping"})
	})
	mux.HandleFunc("/api/v1/fleet/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, api.FleetStatusResponse{Stations: []api.StationSnapshot{{StationID: "CP_1"}}})
	})
	mux.HandleFunc("/api/v1/fleet/operations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []api.Operation{{ID: "op-1", Type: "start", State: "completed"}})
	})
	mux.HandleFunc("/api/v1/fleet/operations/op-1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, api.Operation{ID: "op-1", Type: "start", State: "completed"})
	})
	if mutate != nil {
		mutate(mux)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestClientListStations(t *testing.T) {
	srv := newFakeAPI(t, nil)
	c := New(srv.URL)

	stations, err := c.ListStations()
	require.NoError(t, err)
	require.Len(t, stations, 1)
	require.Equal(t, "CP_1", stations[0].StationID)
	require.True(t, stations[0].Connected)
	require.Equal(t, 2, stations[0].ConnectorCount)
}

func TestClientStationStatus(t *testing.T) {
	srv := newFakeAPI(t, nil)
	c := New(srv.URL)

	snap, err := c.StationStatus("CP_1")
	require.NoError(t, err)
	require.Equal(t, "running", snap.LifecycleState)
}

func TestClientMutation202ReturnsOperationResponse(t *testing.T) {
	srv := newFakeAPI(t, nil)
	c := New(srv.URL)

	op, err := c.StartStation("CP_1")
	require.NoError(t, err)
	require.Equal(t, "op-start-1", op.OperationID)
	require.True(t, op.Success)
}

func TestClientCreateStationReturnsOperation(t *testing.T) {
	srv := newFakeAPI(t, nil)
	c := New(srv.URL)

	op, err := c.CreateStation(api.CreateStationRequest{OCPPID: "CP_9", OCPPVersion: "1.6"})
	require.NoError(t, err)
	require.Equal(t, "op-1", op.OperationID)
}

func TestClientNon2xxBecomesAPIError(t *testing.T) {
	srv := newFakeAPI(t, nil)
	c := New(srv.URL)

	_, err := c.StopStation("CP_1")
	require.Error(t, err)
	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusConflict, apiErr.Status)
	require.Equal(t, "station already stopping", apiErr.Message)
}

func TestClientFleetStatusAndOperations(t *testing.T) {
	srv := newFakeAPI(t, nil)
	c := New(srv.URL)

	status, err := c.FleetStatus()
	require.NoError(t, err)
	require.Len(t, status.Stations, 1)

	ops, err := c.Operations()
	require.NoError(t, err)
	require.Len(t, ops, 1)
	require.Equal(t, "completed", ops[0].State)

	op, err := c.Operation("op-1")
	require.NoError(t, err)
	require.Equal(t, "start", op.Type)
}

func TestClientDecodeTickPayloads(t *testing.T) {
	tickData := `{"ocpp_connected":true,"connectors":[{"id":1,"status":"Charging","voltage":230,"current":16,"phase":1,"is_plugged_in":true}],"active_sessions":[],"energy_meters":{"1":{"reading_wh":1234.5,"is_charging":true}},"reservations":[],"pending_remote_starts":[],"uptime_seconds":42.5}`
	tick, err := DecodeTick(json.RawMessage(tickData))
	require.NoError(t, err)
	require.True(t, tick.OCPPConnected)
	require.Len(t, tick.Connectors, 1)
	require.Equal(t, "Charging", tick.Connectors[0].Status)
	require.InDelta(t, 1234.5, tick.EnergyMeters["1"].ReadingWh, 0.001)

	fleetData := `{"stations":{"CP_1":` + tickData + `}}`
	stations, err := DecodeFleetTick(json.RawMessage(fleetData))
	require.NoError(t, err)
	require.Len(t, stations, 1)
	require.InDelta(t, 42.5, stations["CP_1"].UptimeSeconds, 0.001)
}
