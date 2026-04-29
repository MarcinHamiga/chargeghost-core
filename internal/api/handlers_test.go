package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chargeghost/engine/internal/api"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
	v16 "github.com/chargeghost/engine/internal/ocpp/v16"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testOCPPAPI struct {
	startCalls           int
	stopCalls            int
	lastStartConnectorID int
	lastStartIDTag       string
	lastStartMeter       float64
	lastStartTimestamp   time.Time
	lastStartReservation *int
	startTransactionID   int
	startErr             error
	lastStopMeter        float64
	lastStopTimestamp    time.Time
	lastStopTransaction  int
	lastStopReason       string
	lastStopHistory      []engine.MeterRecord
	stopErr              error
}

func (o *testOCPPAPI) SendAuthorize(idTag string) error { return nil }

func (o *testOCPPAPI) SendHeartbeat() error { return nil }

func (o *testOCPPAPI) SendBootNotification() error { return nil }

func (o *testOCPPAPI) SendStatusNotification(connectorID int, errorCode, status string) error {
	return nil
}

func (o *testOCPPAPI) SendMeterValues(connectorID int, value float64, transactionID int, context string) error {
	return nil
}

func (o *testOCPPAPI) SendTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	o.startCalls++
	o.lastStartConnectorID = connectorID
	o.lastStartIDTag = idTag
	o.lastStartMeter = meterStart
	o.lastStartTimestamp = timestamp
	o.lastStartReservation = reservationID
	return o.startTransactionID, o.startErr
}

func (o *testOCPPAPI) SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error {
	o.stopCalls++
	o.lastStopMeter = meterStop
	o.lastStopTimestamp = timestamp
	o.lastStopTransaction = transactionID
	o.lastStopReason = reason
	o.lastStopHistory = append([]engine.MeterRecord(nil), meterHistory...)
	return o.stopErr
}

func (o *testOCPPAPI) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
	return "", "", nil
}

func (o *testOCPPAPI) IsConnected() bool { return true }

func newTestApp() *api.AppContext {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	return &api.AppContext{
		Engine:    e,
		Config:    config.DefaultConfig(),
		StartTime: time.Now(),
	}
}

func newTestAppWithOCPP(ocppAPI *testOCPPAPI) *api.AppContext {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	return &api.AppContext{
		Engine:    e,
		Config:    config.DefaultConfig(),
		StartTime: time.Now(),
		OCPP:      ocppAPI,
	}
}

func newTestLocalSessionAdmission(configKeys *v16.ConfigKeyManager, localAuth ocpp.LocalAuthManager, authCache ocpp.AuthorizationCacheStore, connected bool) func(*string) error {
	return func(idTag *string) error {
		return ocpp.AdmitLocalSession(idTag, connected, configKeys.GetLocalAuthListEnabled(), localAuth, configKeys.GetAuthorizationCacheEnabled(), authCache, time.Now())
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

func TestStartSessionUsesConfiguredBatteryCapacity(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	plug := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/plug_in", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, plug)
	assert.Equal(t, http.StatusOK, w.Code)

	start := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/start", strings.NewReader(`{"connector_id":1}`))
	start.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, start)
	assert.Equal(t, http.StatusOK, w2.Code)

	session := app.Engine.GetSession(1)
	require.NotNil(t, session)
	assert.InDelta(t, 55000.0, session.MaxEnergy, 0.001)
}

func TestStartSessionIgnoresMaxEnergyOverride(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	plug := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/plug_in", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, plug)
	assert.Equal(t, http.StatusOK, w.Code)

	start := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/start", strings.NewReader(`{"connector_id":1,"max_energy":0}`))
	start.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, start)
	assert.Equal(t, http.StatusOK, w2.Code)

	session := app.Engine.GetSession(1)
	require.NotNil(t, session)
	assert.InDelta(t, 55000.0, session.MaxEnergy, 0.001)
}

func TestGetConfig(t *testing.T) {
	app := newTestApp()
	secret := "secret"
	app.Config.OCPPPassword = &secret
	r := api.NewRouter(app)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var cfg map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&cfg))
	assert.Equal(t, "CP_1", cfg["ocpp_id"])
	_, hasPassword := cfg["ocpp_password"]
	assert.False(t, hasPassword)
}

func TestPatchConfigMarksSecurityAndTLSFieldsRestartRequired(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	body := `{"security_profile":2,"tls_ca_path":"/tmp/ca.pem","tls_client_cert_path":"/tmp/client.crt","tls_client_key_path":"/tmp/client.key"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 2, app.Config.SecurityProfile)
	assert.Equal(t, "/tmp/ca.pem", app.Config.TLSCAPath)
	assert.Equal(t, "/tmp/client.crt", app.Config.TLSClientCertPath)
	assert.Equal(t, "/tmp/client.key", app.Config.TLSClientKeyPath)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["success"])
	assert.Equal(t, "restart_required", resp["action"])
	assert.Equal(t, []interface{}{
		"security_profile",
		"tls_ca_path",
		"tls_client_cert_path",
		"tls_client_key_path",
	}, resp["changed_fields"])
	assert.Equal(t, "Configuration updated in memory. Restart the process to apply startup-only changes.", resp["message"])
}

func TestPatchConfigRejectsInvalidSecurityProfile(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	body := `{"security_profile":22}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/config/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, app.Config.SecurityProfile)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, false, resp["success"])
	assert.Equal(t, "security_profile must be 0, 1, or 2", resp["message"])
}

func TestStartSession_DefersWhenTimeoutProvided(t *testing.T) {
	app := newTestApp()
	app.Config.RFIDTag = strPtr("DEFAULT-TAG")
	r := api.NewRouter(app)

	body := `{"connector_id":1,"timeout_seconds":30}`
	start := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/start", strings.NewReader(body))
	start.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, start)
	assert.Equal(t, http.StatusOK, w.Code)

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/", nil)
	listResp := httptest.NewRecorder()
	r.ServeHTTP(listResp, listReq)
	var before []map[string]interface{}
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&before))
	assert.Len(t, before, 0)

	plug := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/plug_in", nil)
	plugResp := httptest.NewRecorder()
	r.ServeHTTP(plugResp, plug)
	assert.Equal(t, http.StatusOK, plugResp.Code)

	listResp = httptest.NewRecorder()
	r.ServeHTTP(listResp, listReq)
	var after []map[string]interface{}
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&after))
	require.Len(t, after, 1)
	assert.Equal(t, "DEFAULT-TAG", after[0]["id_tag"])
}

func TestStartCharging_LocalAuthAcceptsOfflineDefaultRFID(t *testing.T) {
	app := newTestApp()
	app.Config.RFIDTag = strPtr("DEFAULT-TAG")
	configKeys := v16.NewConfigKeyManager()
	localAuth := ocpp.NewLocalAuthListManager()
	require.NoError(t, localAuth.UpdateList(1, []ocpp.LocalAuthEntry{{IDTag: "DEFAULT-TAG", Status: "Accepted"}}, "Full"))
	app.AdmitLocalSession = newTestLocalSessionAdmission(configKeys, localAuth, ocpp.NewAuthorizationCache(), false)
	r := api.NewRouter(app)

	plug := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/plug_in", nil)
	plugResp := httptest.NewRecorder()
	r.ServeHTTP(plugResp, plug)
	require.Equal(t, http.StatusOK, plugResp.Code)

	start := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/start-charging", nil)
	startResp := httptest.NewRecorder()
	r.ServeHTTP(startResp, start)
	assert.Equal(t, http.StatusOK, startResp.Code)

	session := app.Engine.GetSession(1)
	require.NotNil(t, session)
	require.NotNil(t, session.IDTag)
	assert.Equal(t, "DEFAULT-TAG", *session.IDTag)
}

func TestStartCharging_LocalAuthRejectsOfflineDefaultRFID(t *testing.T) {
	app := newTestApp()
	app.Config.RFIDTag = strPtr("DEFAULT-TAG")
	configKeys := v16.NewConfigKeyManager()
	localAuth := ocpp.NewLocalAuthListManager()
	require.NoError(t, localAuth.UpdateList(1, []ocpp.LocalAuthEntry{{IDTag: "DEFAULT-TAG", Status: "Blocked"}}, "Full"))
	app.AdmitLocalSession = newTestLocalSessionAdmission(configKeys, localAuth, ocpp.NewAuthorizationCache(), false)
	r := api.NewRouter(app)

	plug := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/plug_in", nil)
	plugResp := httptest.NewRecorder()
	r.ServeHTTP(plugResp, plug)
	require.Equal(t, http.StatusOK, plugResp.Code)

	start := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/start-charging", nil)
	startResp := httptest.NewRecorder()
	r.ServeHTTP(startResp, start)
	assert.Equal(t, http.StatusForbidden, startResp.Code)
	assert.Nil(t, app.Engine.GetSession(1))
}

func TestStartSession_AuthCacheAcceptsOfflineIDTag(t *testing.T) {
	app := newTestApp()
	configKeys := v16.NewConfigKeyManager()
	authCache := ocpp.NewAuthorizationCache()
	authCache.Put("CACHE-TAG", "Accepted", nil)
	app.AdmitLocalSession = newTestLocalSessionAdmission(configKeys, ocpp.NewLocalAuthListManager(), authCache, false)
	r := api.NewRouter(app)

	plug := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/plug_in", nil)
	plugResp := httptest.NewRecorder()
	r.ServeHTTP(plugResp, plug)
	require.Equal(t, http.StatusOK, plugResp.Code)

	start := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/start", strings.NewReader(`{"connector_id":1,"id_tag":"CACHE-TAG"}`))
	start.Header.Set("Content-Type", "application/json")
	startResp := httptest.NewRecorder()
	r.ServeHTTP(startResp, start)
	assert.Equal(t, http.StatusOK, startResp.Code)

	session := app.Engine.GetSession(1)
	require.NotNil(t, session)
	require.NotNil(t, session.IDTag)
	assert.Equal(t, "CACHE-TAG", *session.IDTag)
}

func TestStartSession_AuthCacheRejectsOfflineIDTag(t *testing.T) {
	app := newTestApp()
	configKeys := v16.NewConfigKeyManager()
	authCache := ocpp.NewAuthorizationCache()
	authCache.Put("CACHE-TAG", "Blocked", nil)
	app.AdmitLocalSession = newTestLocalSessionAdmission(configKeys, ocpp.NewLocalAuthListManager(), authCache, false)
	r := api.NewRouter(app)

	plug := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/plug_in", nil)
	plugResp := httptest.NewRecorder()
	r.ServeHTTP(plugResp, plug)
	require.Equal(t, http.StatusOK, plugResp.Code)

	start := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/start", strings.NewReader(`{"connector_id":1,"id_tag":"CACHE-TAG"}`))
	start.Header.Set("Content-Type", "application/json")
	startResp := httptest.NewRecorder()
	r.ServeHTTP(startResp, start)
	assert.Equal(t, http.StatusForbidden, startResp.Code)
	assert.Nil(t, app.Engine.GetSession(1))
}

func TestStopAllSessions_StopsEverySession(t *testing.T) {
	app := newTestApp()
	app.Engine = engine.NewEngine(true, 55000)
	app.Engine.AddConnector(230, 32, 1)
	app.Engine.AddConnector(230, 32, 1)
	app.Engine.PlugIn(1)
	app.Engine.PlugIn(2)
	require.NoError(t, app.Engine.StartSession(1, -1, nil, 0))
	require.NoError(t, app.Engine.StartSession(2, -1, nil, 0))
	r := api.NewRouter(app)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/stop", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Nil(t, app.Engine.GetSession(1))
	assert.Nil(t, app.Engine.GetSession(2))

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, float64(2), resp["details"].(map[string]interface{})["stopped_count"])
}

func TestUpdateAvailabilityEndpoint(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	body := `{"type":"Inoperative"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/connectors/1/availability", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Unavailable", app.Engine.GetConnectorStatus(1))
}

func TestStopChargingReturnsConflictWithoutSession(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/stop-charging", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestHealthEndpoint(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])
}

func TestAboutEndpoint(t *testing.T) {
	app := newTestApp()
	r := api.NewRouter(app)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/about", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "0.5.0", body["version"])
	assert.Equal(t, "ChargeGhost EVSE Simulator", body["description"])
	assert.Equal(t, []interface{}{"1.6J", "2.0.1"}, body["ocpp_versions"])
	assert.Equal(t, []interface{}{
		"OCPP 1.6J and 2.0.1 charging station simulation",
		"Charging profile management and composite schedules",
		"Local authorization list",
		"Firmware and diagnostics simulation",
		"REST API and WebSocket event streaming",
		"Offline message queue with JSON persistence",
	}, body["features"])
}

func TestRawStartTransactionRoutesToOCPPBridge(t *testing.T) {
	ocppAPI := &testOCPPAPI{startTransactionID: 77}
	app := newTestAppWithOCPP(ocppAPI)
	app.Engine.PlugIn(1)
	r := api.NewRouter(app)

	body := `{"connector_id":1,"id_tag":"TAG-123","meter_start":12.5,"timestamp":"2026-04-10T12:00:00Z"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ocpp/raw/start-transaction", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, ocppAPI.startCalls)
	assert.Equal(t, 1, ocppAPI.lastStartConnectorID)
	assert.Equal(t, "TAG-123", ocppAPI.lastStartIDTag)
	assert.Equal(t, 12.5, ocppAPI.lastStartMeter)
	assert.Equal(t, time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC), ocppAPI.lastStartTimestamp)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["success"])
	assert.Equal(t, "StartTransaction sent", resp["message"])
	assert.Equal(t, float64(77), resp["details"].(map[string]interface{})["transaction_id"])
}

func TestRawStopTransactionRoutesToOCPPBridgeV16(t *testing.T) {
	ocppAPI := &testOCPPAPI{}
	app := newTestAppWithOCPP(ocppAPI)
	app.Engine.PlugIn(1)
	require.NoError(t, app.Engine.StartSession(1, -1, nil, 0))
	app.Engine.SetActiveTransaction(1, 42)
	r := api.NewRouter(app)

	body := `{"transaction_id":42,"meter_stop":24.5,"timestamp":"2026-04-10T12:05:00Z","reason":"Remote"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ocpp/raw/stop-transaction", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, ocppAPI.stopCalls)
	assert.Equal(t, 42, ocppAPI.lastStopTransaction)
	assert.Equal(t, 24.5, ocppAPI.lastStopMeter)
	assert.Equal(t, "Remote", ocppAPI.lastStopReason)
	assert.Equal(t, time.Date(2026, time.April, 10, 12, 5, 0, 0, time.UTC), ocppAPI.lastStopTimestamp)
}

func TestRawStopTransactionRoutesToOCPPBridgeV201SyntheticID(t *testing.T) {
	ocppAPI := &testOCPPAPI{}
	app := newTestAppWithOCPP(ocppAPI)
	app.Engine.PlugIn(1)
	require.NoError(t, app.Engine.StartSession(1, -1, nil, 0))
	app.Engine.SetActiveTransaction(1, 9001)
	r := api.NewRouter(app)

	body := `{"transaction_id":9001,"reason":"Remote"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ocpp/raw/stop-transaction", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, ocppAPI.stopCalls)
	assert.Equal(t, 9001, ocppAPI.lastStopTransaction)
	assert.Equal(t, "Remote", ocppAPI.lastStopReason)
}

func TestRawStopTransactionReturnsConflictWhenTransactionMissing(t *testing.T) {
	ocppAPI := &testOCPPAPI{}
	app := newTestAppWithOCPP(ocppAPI)
	r := api.NewRouter(app)

	body := `{"transaction_id":404,"reason":"Remote"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ocpp/raw/stop-transaction", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, 0, ocppAPI.stopCalls)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, false, resp["success"])
	assert.Equal(t, "transaction not found", resp["message"])
}

func TestPatchOCPPConfigKeyUpdatesConfigManager(t *testing.T) {
	app := newTestApp()
	keys := v16.NewConfigKeyManager()
	app.ConfigKeys = keys
	r := api.NewRouter(app)

	body := `{"key":"MeterValueSampleInterval","value":"5"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/ocpp/config-keys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 5, keys.GetMeterValueSampleInterval())

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["success"])
	assert.Equal(t, "Key updated", resp["message"])
}

func strPtr(s string) *string {
	return &s
}
