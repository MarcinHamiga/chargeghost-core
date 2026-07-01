package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chargeghost/engine/internal/api"
	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockFleet struct {
	configVal  *config.Config
	snapshots  []api.StationSnapshot
	operations []api.Operation
	created    *api.CreateStationRequest
	updated    *api.PatchStationConfigRequest
	deleted    *api.DeleteStationOptions
}

func (m *mockFleet) Registry() *api.StationRegistry {
	return &api.StationRegistry{DefaultID: "default", Stations: map[string]*api.AppContext{}}
}
func (m *mockFleet) DefaultStationID() string { return "default" }
func (m *mockFleet) AllStationIDs() []string  { return []string{"default", "s1"} }
func (m *mockFleet) Config() *config.Config   { return m.configVal }
func (m *mockFleet) Hub() *ws.Hub             { return ws.NewHub() }

func (m *mockFleet) CreateStation(ctx context.Context, req api.CreateStationRequest) (api.StationSnapshot, string, error) {
	m.created = &req
	return api.StationSnapshot{StationID: req.ID, OCPPID: req.OCPPID, Enabled: req.Enabled}, "op-1", nil
}
func (m *mockFleet) UpdateStation(ctx context.Context, id string, req api.PatchStationConfigRequest) (api.StationSnapshot, string, error) {
	m.updated = &req
	return api.StationSnapshot{StationID: id, RestartRequired: req.Restart}, "op-1", nil
}
func (m *mockFleet) DeleteStation(ctx context.Context, id string, opts api.DeleteStationOptions) error {
	m.deleted = &opts
	return nil
}
func (m *mockFleet) StartStation(ctx context.Context, id string) (string, error) { return "op-1", nil }
func (m *mockFleet) StopStation(ctx context.Context, id string) (string, error)  { return "op-1", nil }
func (m *mockFleet) RestartStation(ctx context.Context, id string) (string, error) {
	return "op-1", nil
}
func (m *mockFleet) EnableStation(ctx context.Context, id string) (string, error) { return "op-1", nil }
func (m *mockFleet) DisableStation(ctx context.Context, id string) (string, error) {
	return "op-1", nil
}
func (m *mockFleet) Reload(ctx context.Context) error { return nil }
func (m *mockFleet) Snapshot(id string) (api.StationSnapshot, bool) {
	return api.StationSnapshot{StationID: id, OCPPID: "CP_1", LifecycleState: "configured"}, true
}
func (m *mockFleet) AllSnapshots() []api.StationSnapshot { return m.snapshots }
func (m *mockFleet) Operations() []api.Operation         { return m.operations }
func (m *mockFleet) Operation(id string) (api.Operation, bool) {
	return api.Operation{ID: id, State: "succeeded"}, true
}
func (m *mockFleet) QueueStatus(id string) (api.QueueStatus, error) {
	return api.QueueStatus{Depth: 0}, nil
}
func (m *mockFleet) QueueDrain(id string) (string, error)                     { return "op-1", nil }
func (m *mockFleet) QueueClear(id string) error                               { return nil }
func (m *mockFleet) QueueDeadLetter(id string) ([]api.DeadLetterEntry, error) { return nil, nil }
func (m *mockFleet) QueueDeadLetterClear(id string) error                     { return nil }
func (m *mockFleet) PersistStation(id string) error                           { return nil }
func (m *mockFleet) SetOCPPPassword(id string, password string) error         { return nil }
func (m *mockFleet) ClearOCPPPassword(id string) error                        { return nil }
func (m *mockFleet) TestCredentials(id string) error                          { return nil }
func (m *mockFleet) Save() error                                              { return nil }

func TestFleetRouter_ListStations(t *testing.T) {
	fleet := &mockFleet{snapshots: []api.StationSnapshot{{StationID: "s1", OCPPID: "CP_1"}}}
	router := api.NewFleetRouter(fleet)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stations", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp []api.StationSnapshot
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
	assert.Equal(t, "s1", resp[0].StationID)
}

func TestFleetRouter_CreateStation(t *testing.T) {
	fleet := &mockFleet{configVal: config.DefaultConfig()}
	router := api.NewFleetRouter(fleet)

	body, _ := json.Marshal(api.CreateStationRequest{ID: "s2", OCPPID: "CP_2", Enabled: true})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/stations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	require.NotNil(t, fleet.created)
	assert.Equal(t, "s2", fleet.created.ID)
}

func TestFleetRouter_GetStationStatus(t *testing.T) {
	fleet := &mockFleet{configVal: config.DefaultConfig()}
	router := api.NewFleetRouter(fleet)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stations/s1/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp api.StationSnapshot
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "s1", resp.StationID)
}

func TestFleetRouter_PatchStationConfig(t *testing.T) {
	fleet := &mockFleet{configVal: config.DefaultConfig()}
	router := api.NewFleetRouter(fleet)

	capacity := 75.0
	body, _ := json.Marshal(api.PatchStationConfigRequest{
		PatchConfigRequest: api.PatchConfigRequest{EVBatteryCapacity: &capacity},
		Restart:            true,
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/stations/s1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, fleet.updated)
	assert.True(t, fleet.updated.Restart)
}

func TestFleetRouter_DeleteStation(t *testing.T) {
	fleet := &mockFleet{configVal: config.DefaultConfig()}
	router := api.NewFleetRouter(fleet)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/stations/s1?force=true", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, fleet.deleted)
	assert.True(t, fleet.deleted.Force)
}

func TestFleetRouter_GetFleetStatus(t *testing.T) {
	fleet := &mockFleet{snapshots: []api.StationSnapshot{{StationID: "s1", OCPPID: "CP_1"}}}
	router := api.NewFleetRouter(fleet)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp api.FleetStatusResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Len(t, resp.Stations, 1)
}

func TestFleetRouter_AdminAuthRequired(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AdminAuthEnabled = true
	fleet := &mockFleet{configVal: cfg}
	router := api.NewFleetRouter(fleet)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)

	t.Setenv("CHARGEGHOST_ADMIN_TOKEN", "secret")
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/status", nil)
	req2.Header.Set("Authorization", "Bearer wrong-token")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusUnauthorized, rec2.Code)
}

func TestFleetRouter_AdminAuthAllowed(t *testing.T) {
	t.Setenv("CHARGEGHOST_ADMIN_TOKEN", "secret")
	cfg := config.DefaultConfig()
	cfg.AdminAuthEnabled = true
	fleet := &mockFleet{configVal: cfg}
	router := api.NewFleetRouter(fleet)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/status", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestFleetRouter_AdminAuthDisabled(t *testing.T) {
	fleet := &mockFleet{configVal: config.DefaultConfig()}
	router := api.NewFleetRouter(fleet)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fleet/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
