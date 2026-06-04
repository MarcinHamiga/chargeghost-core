package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chargeghost/engine/internal/api/handlers"
	"github.com/chargeghost/engine/internal/ocpp"
)

// statusProviderStub implements handlers.OCPPStatusProvider for the handler
// test. It returns a predetermined Status snapshot and records calls.
type statusProviderStub struct {
	status   ocpp.Status
	hasQueue bool
	calls    int
}

func (s *statusProviderStub) Status() ocpp.Status {
	s.calls++
	return s.status
}

func TestGetOCPPStatus_ReturnsJSON(t *testing.T) {
	now := time.Now().UTC()
	provider := &statusProviderStub{
		status: ocpp.Status{
			Version:            "1.6",
			Connected:          true,
			ConnectedAt:        now,
			ReconnectCount:     2,
			UpSince:            now.Add(-5 * time.Minute),
			CSMSURL:            "wss://csms.example.com/ocpp/CP_1",
			OCPPID:             "CP_1",
			HeartbeatSuccesses: 17,
			HeartbeatFailures:  1,
			LastHeartbeatRTTMs: 84,
		},
	}

	handler := handlers.GetOCPPStatus(provider)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ocpp/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, 1, provider.calls)

	var body ocpp.Status
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "1.6", body.Version)
	assert.True(t, body.Connected)
	assert.Equal(t, 2, body.ReconnectCount)
	assert.Equal(t, "wss://csms.example.com/ocpp/CP_1", body.CSMSURL)
	assert.Equal(t, "CP_1", body.OCPPID)
	assert.Equal(t, int64(17), body.HeartbeatSuccesses)
	assert.Equal(t, int64(1), body.HeartbeatFailures)
	assert.Equal(t, int64(84), body.LastHeartbeatRTTMs)
}

func TestGetOCPPStatus_IncludesV2QueueFields(t *testing.T) {
	provider := &statusProviderStub{
		status: ocpp.Status{
			Version:         "2.0.1",
			Connected:       false,
			QueueDepth:      4,
			QueueExhausted:  1,
			DrainInProgress: true,
		},
	}

	handler := handlers.GetOCPPStatus(provider)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ocpp/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body ocpp.Status
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "2.0.1", body.Version)
	assert.False(t, body.Connected)
	assert.Equal(t, 4, body.QueueDepth)
	assert.Equal(t, 1, body.QueueExhausted)
	assert.True(t, body.DrainInProgress)
}

func TestGetOCPPStatus_NilProviderReturns503(t *testing.T) {
	handler := handlers.GetOCPPStatus(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ocpp/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, false, body["success"])
	assert.Equal(t, "OCPP bridge is not configured", body["message"])
}
