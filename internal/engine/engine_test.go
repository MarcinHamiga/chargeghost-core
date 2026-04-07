package engine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	engine "github.com/chargeghost/engine/internal/engine"
)

func TestApplyTransition_ValidTransitions(t *testing.T) {
	cases := []struct {
		from   engine.ConnectorState
		action string
		want   engine.ConnectorState
	}{
		{engine.StateAvailable, "plug_in", engine.StatePreparing},
		{engine.StateReserved, "plug_in", engine.StatePreparing},
		{engine.StatePreparing, "unplug", engine.StateAvailable},
		{engine.StateFinishing, "unplug", engine.StateAvailable},
		{engine.StateCharging, "unplug", engine.StateAvailable},
		{engine.StateSuspendedEV, "unplug", engine.StateAvailable},
		{engine.StateSuspendedEVSE, "unplug", engine.StateAvailable},
		{engine.StatePreparing, "start_charging", engine.StateCharging},
		{engine.StateCharging, "stop_charging", engine.StateFinishing},
		{engine.StateSuspendedEV, "stop_charging", engine.StateFinishing},
		{engine.StateSuspendedEVSE, "stop_charging", engine.StateFinishing},
		{engine.StateCharging, "suspend_ev", engine.StateSuspendedEV},
		{engine.StateSuspendedEV, "resume", engine.StateCharging},
		{engine.StateCharging, "suspend_evse", engine.StateSuspendedEVSE},
		{engine.StateSuspendedEVSE, "resume", engine.StateCharging},
	}
	for _, tc := range cases {
		next, err := engine.ApplyTransition(tc.from, tc.action)
		require.NoError(t, err, "from=%s action=%s", tc.from, tc.action)
		assert.Equal(t, tc.want, next, "from=%s action=%s", tc.from, tc.action)
	}
}

func TestApplyTransition_InvalidTransition(t *testing.T) {
	_, err := engine.ApplyTransition(engine.StateAvailable, "stop_charging")
	assert.ErrorIs(t, err, engine.ErrInvalidTransition)
}

func TestConnector_PlugIn_FromAvailable(t *testing.T) {
	c := engine.NewConnector(1, 230.0, 32.0, 1)
	assert.Equal(t, engine.StateAvailable, c.Status)

	err := c.PlugIn()
	require.NoError(t, err)
	assert.Equal(t, engine.StatePreparing, c.Status)
	assert.True(t, c.IsPluggedIn)
}

func TestConnector_PlugIn_FromReserved(t *testing.T) {
	c := engine.NewConnector(1, 230.0, 32.0, 1)
	c.SetReserved()
	assert.Equal(t, engine.StateReserved, c.Status)

	err := c.PlugIn()
	require.NoError(t, err)
	assert.Equal(t, engine.StatePreparing, c.Status)
	assert.True(t, c.IsPluggedIn)
}

func TestConnector_PlugIn_FromUnavailable_DoesNotChangeStatus(t *testing.T) {
	c := engine.NewConnector(1, 230.0, 32.0, 1)
	c.SetUnavailable()
	assert.Equal(t, engine.StateUnavailable, c.Status)

	err := c.PlugIn()
	require.NoError(t, err)
	assert.Equal(t, engine.StateUnavailable, c.Status) // status unchanged
	assert.True(t, c.IsPluggedIn)
}

func TestConnector_Unplug_RestoresPersistentStatus(t *testing.T) {
	c := engine.NewConnector(1, 230.0, 32.0, 1)
	c.SetUnavailable()
	_ = c.PlugIn()

	c.Unplug()
	assert.Equal(t, engine.StateUnavailable, c.Status) // restored to persistent
	assert.False(t, c.IsPluggedIn)
}

func TestConnector_BypassTransitions(t *testing.T) {
	c := engine.NewConnector(1, 230.0, 32.0, 1)

	c.SetUnavailable()
	assert.Equal(t, engine.StateUnavailable, c.Status)
	assert.Equal(t, engine.StateUnavailable, c.PersistentStatus)

	// SetUnavailable is no-op when already unavailable
	c.SetUnavailable()
	assert.Equal(t, engine.StateUnavailable, c.Status)

	c.SetOperative()
	assert.Equal(t, engine.StateAvailable, c.Status)
	assert.Equal(t, engine.StateAvailable, c.PersistentStatus)

	c.SetReserved()
	assert.Equal(t, engine.StateReserved, c.Status)
	assert.Equal(t, engine.StateReserved, c.PersistentStatus)

	c.ClearReservation()
	assert.Equal(t, engine.StateAvailable, c.Status)
}

func TestConnector_SetReserved_WhenPluggedIn_SetsPreparing(t *testing.T) {
	c := engine.NewConnector(1, 230.0, 32.0, 1)
	_ = c.PlugIn()
	assert.Equal(t, engine.StatePreparing, c.Status)

	c.SetReserved()
	assert.Equal(t, engine.StatePreparing, c.Status) // still preparing since plugged in
	assert.Equal(t, engine.StateReserved, c.PersistentStatus)
}

func TestConnector_Validation(t *testing.T) {
	assert.Panics(t, func() { engine.NewConnector(1, 50.0, 32.0, 1) })    // voltage too low
	assert.Panics(t, func() { engine.NewConnector(1, 230.0, 3.0, 1) })    // current too low
	assert.Panics(t, func() { engine.NewConnector(1, 230.0, 32.0, 2) })   // phase = 2 invalid
	assert.NotPanics(t, func() { engine.NewConnector(1, 230.0, 32.0, 3) }) // phase = 3 valid
}

func TestEnergyMeter_AccumulatesWhenCharging(t *testing.T) {
	m := engine.NewEnergyMeter()
	m.IsCharging = true

	// 230V × 32A × 1 phase × 3600s = 7360 Wh
	m.Update(230.0, 32.0, 1, 3600.0)
	assert.InDelta(t, 7360.0, m.Value, 0.001)
}

func TestEnergyMeter_DoesNotAccumulateWhenNotCharging(t *testing.T) {
	m := engine.NewEnergyMeter()
	m.IsCharging = false
	m.Update(230.0, 32.0, 1, 3600.0)
	assert.Equal(t, 0.0, m.Value)
}

func TestEnergyMeter_ThreePhasePower(t *testing.T) {
	m := engine.NewEnergyMeter()
	m.IsCharging = true
	// 400V × 16A × 3 phase × 3600s = 19200 Wh
	m.Update(400.0, 16.0, 3, 3600.0)
	assert.InDelta(t, 19200.0, m.Value, 0.001)
}

func TestEnergyMeter_CumulativeAcrossUpdates(t *testing.T) {
	m := engine.NewEnergyMeter()
	m.IsCharging = true
	m.Update(230.0, 32.0, 1, 1800.0) // 30 min
	m.Update(230.0, 32.0, 1, 1800.0) // another 30 min
	assert.InDelta(t, 7360.0, m.Value, 0.001) // same as 1 hour
}
