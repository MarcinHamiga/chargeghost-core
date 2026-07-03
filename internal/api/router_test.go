package api_test

import (
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMultiStationRegistry(t *testing.T) *api.StationRegistry {
	hub := ws.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	e1 := engine.NewEngine(false, 55000)
	e1.AddConnector(230, 32, 1)
	e2 := engine.NewEngine(false, 55000)
	e2.AddConnector(230, 32, 3)

	return &api.StationRegistry{
		DefaultID: "CP_A",
		Stations: map[string]*api.AppContext{
			"CP_A": {
				Engine:       e1,
				Config:       &config.Config{OCPPID: "CP_A", OCPPVersion: "1.6", ConnectionURL: "wss://a"},
				GlobalConfig: config.DefaultConfig(),
				Hub:          hub,
				StartTime:    time.Now(),
				StationID:    "CP_A",
				OCPP:         &ocppTestAPI{},
				OCPPBridge:   &ocppTestBridge{},
				MultiStation: true,
			},
			"CP_B": {
				Engine:       e2,
				Config:       &config.Config{OCPPID: "CP_B", OCPPVersion: "2.0.1", ConnectionURL: "wss://b"},
				GlobalConfig: config.DefaultConfig(),
				Hub:          hub,
				StartTime:    time.Now(),
				StationID:    "CP_B",
				OCPP:         &ocppTestAPI{},
				OCPPBridge:   &ocppTestBridge{},
				MultiStation: true,
			},
		},
	}
}

type ocppTestAPI struct{}

func (o *ocppTestAPI) SendAuthorize(idTag string) error { return nil }
func (o *ocppTestAPI) SendHeartbeat() error             { return nil }
func (o *ocppTestAPI) SendBootNotification() error      { return nil }
func (o *ocppTestAPI) SendStatusNotification(connectorID int, errorCode, status string) error {
	return nil
}
func (o *ocppTestAPI) SendMeterValues(connectorID int, value float64, transactionID int, context string) error {
	return nil
}
func (o *ocppTestAPI) SendTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	return 0, nil
}
func (o *ocppTestAPI) SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error {
	return nil
}
func (o *ocppTestAPI) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
	return "", "", nil
}
func (o *ocppTestAPI) IsConnected() bool { return true }

type ocppTestBridge struct{}

func (o *ocppTestBridge) Start(context.Context) error         { return nil }
func (o *ocppTestBridge) Stop()                               {}
func (o *ocppTestBridge) IsConnected() bool                   { return true }
func (o *ocppTestBridge) GetHeartbeatInterval() int           { return 0 }
func (o *ocppTestBridge) Dispatcher() *ocpp.CommandDispatcher { return nil }
func (o *ocppTestBridge) Status() ocpp.Status                 { return ocpp.Status{} }
func (o *ocppTestBridge) SendBootNotification() error         { return nil }
func (o *ocppTestBridge) SendHeartbeat() error                { return nil }
func (o *ocppTestBridge) SendStatusNotification(connectorID int, errorCode, status string) error {
	return nil
}
func (o *ocppTestBridge) SendMeterValues(connectorID int, value float64, transactionID int, context string) error {
	return nil
}
func (o *ocppTestBridge) SendAuthorize(idTag string) error { return nil }
func (o *ocppTestBridge) SendTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	return 0, nil
}
func (o *ocppTestBridge) SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error {
	return nil
}
func (o *ocppTestBridge) SendTransactionEventUpdated(connectorID int, chargingState, trigger string) error {
	return nil
}
func (o *ocppTestBridge) SendFirmwareStatusNotification(status string) error    { return nil }
func (o *ocppTestBridge) SendDiagnosticsStatusNotification(status string) error { return nil }
func (o *ocppTestBridge) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
	return "", "", nil
}
func (o *ocppTestBridge) SendConnectorEventNotification(connectorID int, component, instance, variable, actualValue string, evseComponent bool) error {
	return nil
}
func (o *ocppTestBridge) SendReservationStatusUpdate(reservationID int, status string) error {
	return nil
}
func (o *ocppTestBridge) MaybeCompleteReset() {}
func (o *ocppTestBridge) DrainOfflineQueue()  {}

func TestMultiRouter_DefaultStationRoutes(t *testing.T) {
	registry := newMultiStationRegistry(t)
	r := api.NewMultiRouter(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stations", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var stations []map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&stations))
	require.Len(t, stations, 2)
}

func TestMultiRouter_StationScopedRoutes(t *testing.T) {
	registry := newMultiStationRegistry(t)
	r := api.NewMultiRouter(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stations/CP_B/connectors/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var body []map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Len(t, body, 1)
	assert.Equal(t, 3, int(body[0]["phase"].(float64)))
}

func TestMultiRouter_DefaultStationScopedRoute(t *testing.T) {
	registry := newMultiStationRegistry(t)
	r := api.NewMultiRouter(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stations/CP_A/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMultiRouter_UnknownStationReturns404(t *testing.T) {
	registry := newMultiStationRegistry(t)
	r := api.NewMultiRouter(registry)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stations/CP_Z/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMultiRouter_StationSaveConfigRejected(t *testing.T) {
	registry := newMultiStationRegistry(t)
	r := api.NewMultiRouter(registry)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/stations/CP_B/config/save", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMultiRouter_GlobalSaveConfigAllowed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	registry := newMultiStationRegistry(t)
	r := api.NewMultiRouter(registry)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/save", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
