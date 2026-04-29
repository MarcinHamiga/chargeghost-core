package v16

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	engine "github.com/chargeghost/engine/internal/engine"
)

func TestQueuedStartTransaction16_JSONRoundTrip(t *testing.T) {
	reservationID := 42
	timestamp := time.Unix(1714348800, 123456789).UTC()
	payload := queuedStartTransaction16{
		ConnectorID:   1,
		IDTag:         "RFID-001",
		MeterStart:    1234.5,
		Timestamp:     timestamp,
		ReservationID: &reservationID,
	}

	raw, roundTripped := roundTripQueuedPayload[queuedStartTransaction16](t, payload)

	assert.Equal(t, float64(1), raw["connector_id"])
	assert.Equal(t, "RFID-001", raw["id_tag"])
	assert.Equal(t, float64(1234.5), raw["meter_start"])
	assert.Equal(t, timestamp.Format(time.RFC3339Nano), raw["timestamp"])
	assert.Equal(t, float64(42), raw["reservation_id"])
	assert.Equal(t, payload, roundTripped)
}

func TestQueuedStopTransaction16_JSONRoundTrip(t *testing.T) {
	timestamp := time.Unix(1714349800, 987654321).UTC()
	payload := queuedStopTransaction16{
		TransactionID: 77,
		MeterStop:     4567.89,
		Timestamp:     timestamp,
		Reason:        "EVDisconnected",
		MeterHistory: []engine.MeterRecord{
			{Timestamp: "2026-04-29T12:00:00Z", Value: 4300.1},
			{Timestamp: "2026-04-29T12:05:00Z", Value: 4567.89},
		},
	}

	raw, roundTripped := roundTripQueuedPayload[queuedStopTransaction16](t, payload)

	assert.Equal(t, float64(77), raw["transaction_id"])
	assert.Equal(t, float64(4567.89), raw["meter_stop"])
	assert.Equal(t, timestamp.Format(time.RFC3339Nano), raw["timestamp"])
	assert.Equal(t, "EVDisconnected", raw["reason"])
	require.Len(t, raw["meter_history"], 2)
	assert.Equal(t, payload, roundTripped)
}

func TestQueuedMeterValues16_JSONRoundTrip(t *testing.T) {
	timestamp := time.Unix(1714350800, 111222333).UTC()
	payload := queuedMeterValues16{
		ConnectorID:   2,
		Value:         6789.01,
		TransactionID: 88,
		Context:       "Transaction.End",
		Timestamp:     timestamp,
	}

	raw, roundTripped := roundTripQueuedPayload[queuedMeterValues16](t, payload)

	assert.Equal(t, float64(2), raw["connector_id"])
	assert.Equal(t, float64(6789.01), raw["value"])
	assert.Equal(t, float64(88), raw["transaction_id"])
	assert.Equal(t, "Transaction.End", raw["context"])
	assert.Equal(t, timestamp.Format(time.RFC3339Nano), raw["timestamp"])
	assert.Equal(t, payload, roundTripped)
}

func roundTripQueuedPayload[T any](t *testing.T, payload T) (map[string]interface{}, T) {
	t.Helper()

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	data, err = json.Marshal(raw)
	require.NoError(t, err)

	var roundTripped T
	require.NoError(t, json.Unmarshal(data, &roundTripped))

	return raw, roundTripped
}
