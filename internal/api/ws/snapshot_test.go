package ws_test

import (
	"testing"
	"time"

	ws "github.com/chargeghost/engine/internal/api/ws"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildStatusSnapshot_IncludesReservationsAndUptime(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 32, 3)

	expiry, err := time.Parse(time.RFC3339, "2030-01-01T00:00:00Z")
	require.NoError(t, err)
	require.Equal(t, "accepted", e.ReserveConnector(1, 42, "TAG1", expiry, nil))

	snap := ws.BuildStatusSnapshot(e, true, 12.5)

	assert.Equal(t, true, snap["ocpp_connected"])
	assert.InDelta(t, 12.5, snap["uptime_seconds"], 0.001)

	reservations, ok := snap["reservations"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, reservations, 1)
	assert.Equal(t, 42, reservations[0]["reservation_id"])
	assert.Equal(t, 1, reservations[0]["connector_id"])

	connectors, ok := snap["connectors"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, connectors, 1)
	assert.Equal(t, 1, connectors[0]["id"])
}
