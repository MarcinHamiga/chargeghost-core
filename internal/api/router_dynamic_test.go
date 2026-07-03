package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chargeghost/engine/internal/api"
	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
	v16 "github.com/chargeghost/engine/internal/ocpp/v16"
	"github.com/chargeghost/engine/internal/timeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRunningAppContext builds a populated AppContext backed by a real engine,
// suitable for exercising operational routes end to end through the router.
func newRunningAppContext(t *testing.T, stationID string, connectors int) *api.AppContext {
	t.Helper()
	hub := ws.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go hub.Run(ctx)

	e := engine.NewEngine(false, 55000)
	for i := 0; i < connectors; i++ {
		e.AddConnector(230, 32, 1)
	}
	return &api.AppContext{
		Engine:            e,
		Config:            &config.Config{OCPPID: stationID, OCPPVersion: "1.6", ConnectionURL: "wss://" + stationID},
		GlobalConfig:      config.DefaultConfig(),
		AdmitLocalSession: func(*string) error { return nil },
		Timeline:          timeline.NewStore(100),
		LocalAuth:         ocpp.NewLocalAuthListManager(),
		Firmware:          ocpp.NewFirmwareManager(nil),
		Diagnostics:       ocpp.NewDiagnosticsManager(nil),
		ProfileManager:    v16.NewChargingProfileManager(),
		ConfigKeys:        v16.NewConfigKeyManager(),
		Hub:               hub,
		StartTime:         time.Now(),
		StationID:         stationID,
		OCPP:              &ocppTestAPI{},
		OCPPBridge:        &ocppTestBridge{},
		MultiStation:      true,
	}
}

// TestFleetRouter_StationOperationsReachable is a regression test for the
// verified route-shadowing bug: NewFleetRouter used to mount a static legacy
// subrouter at /api/v1/stations/{id} for every station present when the
// router was built, which shadowed the dynamic fleet-admin routes registered
// at the same pattern — stop/start/restart/delete/queue all 404'd for any
// station that existed at startup. Routing is now resolved per request, so
// both the fleet-admin surface and the station's operational routes must be
// reachable on the same combined subrouter.
func TestFleetRouter_StationOperationsReachable(t *testing.T) {
	app := newRunningAppContext(t, "CP_1", 1)
	fleet := &mockFleet{
		configVal:   config.DefaultConfig(),
		appContexts: map[string]*api.AppContext{"CP_1": app},
	}
	router := api.NewFleetRouter(fleet)

	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, "/api/v1/stations/CP_1/stop", http.StatusAccepted},
		{http.MethodPost, "/api/v1/stations/CP_1/restart", http.StatusAccepted},
		{http.MethodGet, "/api/v1/stations/CP_1/queue/status", http.StatusOK},
		{http.MethodGet, "/api/v1/stations/CP_1/connectors/", http.StatusOK},
		{http.MethodGet, "/api/v1/stations/CP_1/status", http.StatusOK},
		{http.MethodDelete, "/api/v1/stations/CP_1", http.StatusOK},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, c.want, rec.Code, "%s %s -> %d, want %d: %s", c.method, c.path, rec.Code, c.want, rec.Body.String())
	}

	// GET /stations/{id}/status must return the fleet snapshot shape
	// (lifecycle_state), not the legacy operational status payload.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stations/CP_1/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var snap api.StationSnapshot
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &snap))
	assert.Equal(t, "CP_1", snap.StationID)
	assert.NotEmpty(t, snap.LifecycleState)
}

// TestFleetRouter_StationBecomesReachableAfterAppContextAppears is a
// regression test for the one-time Registry() snapshot: a station created
// (or started) after the router was built used to be completely unreachable
// for the lifetime of the process. Routing must now resolve dynamically, so
// a station with no runtime yet still exposes fleet-admin routes (so it can
// be started), and its operational routes become reachable the moment a
// runtime appears — no router rebuild required.
func TestFleetRouter_StationBecomesReachableAfterAppContextAppears(t *testing.T) {
	fleet := &mockFleet{
		configVal:   config.DefaultConfig(),
		appContexts: map[string]*api.AppContext{},
	}
	router := api.NewFleetRouter(fleet)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stations/CP_NEW/connectors/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "no runtime yet")

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/stations/CP_NEW/start", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusAccepted, rec2.Code, "fleet-admin routes must work even with no runtime")

	fleet.appContexts["CP_NEW"] = newRunningAppContext(t, "CP_NEW", 2)

	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/stations/CP_NEW/connectors/", nil)
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	require.Equal(t, http.StatusOK, rec3.Code)
	var body []map[string]interface{}
	require.NoError(t, json.Unmarshal(rec3.Body.Bytes(), &body))
	assert.Len(t, body, 2)
}

// TestFleetRouter_StationRouterRebuildsOnRuntimeReplacement verifies the
// router-cache generation check: when a station's *AppContext pointer
// changes (a restart replaced its runtime), the very next request must be
// served by a freshly built subrouter bound to the new AppContext, not a
// stale cached one bound to the old (possibly dead) engine.
func TestFleetRouter_StationRouterRebuildsOnRuntimeReplacement(t *testing.T) {
	appV1 := newRunningAppContext(t, "CP_1", 1)
	fleet := &mockFleet{
		configVal:   config.DefaultConfig(),
		appContexts: map[string]*api.AppContext{"CP_1": appV1},
	}
	router := api.NewFleetRouter(fleet)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stations/CP_1/connectors/", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var body []map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body, 1)

	appV2 := newRunningAppContext(t, "CP_1", 3)
	fleet.appContexts["CP_1"] = appV2

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/stations/CP_1/connectors/", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	var body2 []map[string]interface{}
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &body2))
	assert.Len(t, body2, 3, "router must serve the replacement runtime's engine, not the stale one")
}

// TestFleetRouter_DefaultStationRoutesReachable walks the default-station
// operational routes documented in REST_API.md and asserts every one is
// reachable (not 404/405) through the dynamically-resolved default-station
// dispatcher. This is the safety net for chi Mount/precedence mistakes: a
// duplicate-registration bug panics at router construction, and a precedence
// bug (wildcard shadowing a static sibling, or vice versa) surfaces here as
// an unexpected 404 or 405.
func TestFleetRouter_DefaultStationRoutesReachable(t *testing.T) {
	app := newRunningAppContext(t, "default", 1)
	fleet := &mockFleet{
		configVal:   config.DefaultConfig(),
		appContexts: map[string]*api.AppContext{"default": app},
	}
	router := api.NewFleetRouter(fleet)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/about"},
		{http.MethodGet, "/api/v1/status"},
		{http.MethodGet, "/api/v1/connectors"},
		{http.MethodPost, "/api/v1/connectors"},
		{http.MethodGet, "/api/v1/connectors/1"},
		{http.MethodPut, "/api/v1/connectors/1/availability"},
		{http.MethodPut, "/api/v1/connectors/1"},
		{http.MethodDelete, "/api/v1/connectors/1"},
		{http.MethodPost, "/api/v1/connectors/1/plug_in"},
		{http.MethodPost, "/api/v1/connectors/1/unplug"},
		{http.MethodPost, "/api/v1/connectors/1/suspend_ev"},
		{http.MethodPost, "/api/v1/connectors/1/resume_charging"},
		{http.MethodPost, "/api/v1/connectors/1/start-charging"},
		{http.MethodPost, "/api/v1/connectors/1/stop-charging"},
		{http.MethodPut, "/api/v1/connectors/1/rfid"},
		{http.MethodDelete, "/api/v1/connectors/1/rfid"},
		{http.MethodGet, "/api/v1/sessions"},
		{http.MethodPost, "/api/v1/sessions/start"},
		{http.MethodPost, "/api/v1/sessions/stop"},
		{http.MethodGet, "/api/v1/sessions/last-stopped"},
		{http.MethodGet, "/api/v1/sessions/active"},
		{http.MethodGet, "/api/v1/sessions/info"},
		{http.MethodGet, "/api/v1/sessions/1"},
		{http.MethodGet, "/api/v1/config"},
		{http.MethodPatch, "/api/v1/config"},
		{http.MethodPost, "/api/v1/config/save"},
		{http.MethodGet, "/api/v1/reservations"},
		{http.MethodPost, "/api/v1/reservations"},
		{http.MethodDelete, "/api/v1/reservations/1"},
		{http.MethodGet, "/api/v1/timeline"},
		{http.MethodGet, "/api/v1/timeline/count"},
		{http.MethodDelete, "/api/v1/timeline"},
		{http.MethodGet, "/api/v1/local-auth-list"},
		{http.MethodGet, "/api/v1/local-auth-list/tag1"},
		{http.MethodPut, "/api/v1/local-auth-list"},
		{http.MethodDelete, "/api/v1/local-auth-list/tag1"},
		{http.MethodDelete, "/api/v1/local-auth-list"},
		{http.MethodGet, "/api/v1/firmware/status"},
		{http.MethodPost, "/api/v1/firmware/trigger"},
		{http.MethodPost, "/api/v1/firmware/cancel"},
		{http.MethodGet, "/api/v1/diagnostics/status"},
		{http.MethodPost, "/api/v1/diagnostics/trigger"},
		{http.MethodPost, "/api/v1/diagnostics/cancel"},
		{http.MethodGet, "/api/v1/charging-profiles"},
		{http.MethodPost, "/api/v1/charging-profiles"},
		{http.MethodDelete, "/api/v1/charging-profiles"},
		{http.MethodGet, "/api/v1/charging-profiles/1"},
		{http.MethodPost, "/api/v1/charging-profiles/composite-schedule"},
		{http.MethodGet, "/api/v1/ocpp/status"},
		{http.MethodGet, "/api/v1/ocpp/config-keys"},
		{http.MethodPatch, "/api/v1/ocpp/config-keys"},
		{http.MethodPost, "/api/v1/ocpp/authorize"},
		{http.MethodPost, "/api/v1/ocpp/heartbeat"},
		{http.MethodPost, "/api/v1/ocpp/raw/status-notification"},
		{http.MethodPost, "/api/v1/ocpp/raw/meter-values"},
		{http.MethodPost, "/api/v1/ocpp/raw/data-transfer"},
		{http.MethodPost, "/api/v1/ocpp/raw/start-transaction"},
		{http.MethodPost, "/api/v1/ocpp/raw/stop-transaction"},
		// Fleet-level and admin surface, documented separately from the
		// per-station operational routes above.
		{http.MethodGet, "/api/v1/stations"},
		{http.MethodPost, "/api/v1/stations"},
		{http.MethodGet, "/api/v1/fleet/status"},
		{http.MethodGet, "/api/v1/fleet/config"},
		{http.MethodPost, "/api/v1/fleet/config/save"},
		{http.MethodGet, "/api/v1/fleet/operations"},
		{http.MethodPost, "/api/v1/fleet/reload"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.NotEqual(t, http.StatusMethodNotAllowed, rec.Code, "%s %s must be routed (405)", c.method, c.path)
		if rec.Code == http.StatusNotFound {
			// A 404 is only acceptable if it came from a real handler
			// reporting an application-level "no such resource" (e.g. no
			// session on connector 1 in this fixture) — those always render
			// the JSON Response{success,message} envelope. Chi's own
			// route-not-matched 404 is plain text, not JSON, and means the
			// path was never wired up at all.
			var body map[string]interface{}
			err := json.Unmarshal(rec.Body.Bytes(), &body)
			assert.NoError(t, err, "%s %s: 404 body is not JSON, route was never matched: %q", c.method, c.path, rec.Body.String())
		}
	}
}

// TestFleetRouter_DefaultConfigPatchWritesThrough is a regression test for
// finding 10: in multi-station mode, PATCH /api/v1/config used to mutate an
// in-memory Config clone that was discarded on restart despite the response
// claiming "Configuration updated in memory. Restart to apply." — the
// change was never actually reachable again. It must now write through to
// the default station's entry in the global config via the same
// fleet.UpdateStation path the station-scoped PATCH already used, while
// still rendering the legacy PatchConfigResponse envelope so existing
// /api/v1/config clients see no shape change.
func TestFleetRouter_DefaultConfigPatchWritesThrough(t *testing.T) {
	app := newRunningAppContext(t, "CP_1", 1)
	fleet := &mockFleet{
		configVal:   config.DefaultConfig(),
		defaultID:   "CP_1",
		appContexts: map[string]*api.AppContext{"CP_1": app},
	}
	router := api.NewFleetRouter(fleet)

	url := "wss://example.com/CP_1"
	body, _ := json.Marshal(api.PatchStationConfigRequest{
		PatchConfigRequest: api.PatchConfigRequest{ConnectionURL: &url},
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, fleet.updated, "PATCH /api/v1/config must go through fleet.UpdateStation, not an in-memory-only patch")
	assert.Equal(t, "CP_1", fleet.updatedID, "must target the default station, not a hardcoded id")
	assert.Equal(t, url, *fleet.updated.ConnectionURL)

	var resp api.PatchConfigResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Contains(t, resp.ChangedFields, "connection_url")
}

// TestFleetRouter_DefaultConfigSave200 is a regression test for finding 10's
// other half: POST /api/v1/config/save always 500'd in multi-station mode
// because AppContext.GlobalConfig was never populated by any code path. It
// must now call fleet.Save() directly.
func TestFleetRouter_DefaultConfigSave200(t *testing.T) {
	app := newRunningAppContext(t, "default", 1)
	fleet := &mockFleet{
		configVal:   config.DefaultConfig(),
		appContexts: map[string]*api.AppContext{"default": app},
	}
	router := api.NewFleetRouter(fleet)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/save", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, fleet.saveCalled)
}

// TestFleetRouter_StationScopedConfigSaveStillRejected verifies the
// station-scoped save endpoint keeps its existing "not supported" behavior
// (400, not 404 or 500) now that it's registered inside the switch in
// mountStationRoutes rather than unconditionally.
func TestFleetRouter_StationScopedConfigSaveStillRejected(t *testing.T) {
	app := newRunningAppContext(t, "CP_1", 1)
	fleet := &mockFleet{
		configVal:   config.DefaultConfig(),
		appContexts: map[string]*api.AppContext{"CP_1": app},
	}
	router := api.NewFleetRouter(fleet)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/stations/CP_1/config/save", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
