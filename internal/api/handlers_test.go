package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/chargeghost/engine/internal/api"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
)

func newTestApp() *api.AppContext {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	return &api.AppContext{
		Engine:    e,
		Config:    config.DefaultConfig(),
		StartTime: time.Now(),
	}
}

func TestGetStatus(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Contains(t, body, "connectors")
	assert.Contains(t, body, "active_sessions")
	assert.Contains(t, body, "energy_meters")
}

func TestListConnectors(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/connectors/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body []map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Len(t, body, 1)
}

func TestCreateConnector(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	body := `{"voltage":400,"current":16,"phase":3}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["success"])
}

func TestPlugInAndStartSession(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	plug := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/plug_in", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, plug)
	assert.Equal(t, http.StatusOK, w.Code)

	start := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/start-charging", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, start)
	assert.Equal(t, http.StatusOK, w2.Code)

	sessions := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/", nil)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, sessions)
	var list []map[string]interface{}
	require.NoError(t, json.NewDecoder(w3.Body).Decode(&list))
	assert.Len(t, list, 1)
}

func TestGetConfig(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var cfg map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&cfg))
	assert.Equal(t, "CP_1", cfg["ocpp_id"])
}
