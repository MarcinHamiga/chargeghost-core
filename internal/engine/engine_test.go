package engine_test

import (
	"testing"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	assert.Panics(t, func() { engine.NewConnector(1, 50.0, 32.0, 1) })     // voltage too low
	assert.Panics(t, func() { engine.NewConnector(1, 230.0, 3.0, 1) })     // current too low
	assert.Panics(t, func() { engine.NewConnector(1, 230.0, 32.0, 2) })    // phase = 2 invalid
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
	m.Update(230.0, 32.0, 1, 1800.0)          // 30 min
	m.Update(230.0, 32.0, 1, 1800.0)          // another 30 min
	assert.InDelta(t, 7360.0, m.Value, 0.001) // same as 1 hour
}

func TestSession_SoCCalculation(t *testing.T) {
	s := engine.NewSession(1, -1, 55000.0, nil, nil)
	assert.Equal(t, 0.0, s.StateOfCharge)

	s.UpdateEnergy(5500.0) // 10% of 55 kWh
	assert.InDelta(t, 10.0, s.StateOfCharge, 0.001)
	assert.InDelta(t, 5500.0, s.EnergyCharged, 0.001)
}

func TestSession_EnergyCappedAtMaxEnergy(t *testing.T) {
	s := engine.NewSession(1, -1, 1000.0, nil, nil)
	s.UpdateEnergy(1500.0) // more than max
	assert.InDelta(t, 1000.0, s.EnergyCharged, 0.001)
	assert.InDelta(t, 100.0, s.StateOfCharge, 0.001)
}

func TestSession_NoSoCWhenMaxEnergyZero(t *testing.T) {
	s := engine.NewSession(1, -1, 0.0, nil, nil)
	s.UpdateEnergy(5000.0)
	assert.InDelta(t, 5000.0, s.EnergyCharged, 0.001)
	assert.Equal(t, 0.0, s.StateOfCharge) // SoC not tracked
}

func TestSession_MeterHistory_KeepsLast10(t *testing.T) {
	s := engine.NewSession(1, -1, 0.0, nil, nil)
	for i := 0; i < 15; i++ {
		s.RecordMeter(float64(i * 100))
	}
	assert.Len(t, s.MeterHistory, 10)
	assert.InDelta(t, 500.0, s.MeterHistory[0].Value, 0.001) // oldest kept
}

func TestReservation_IsExpired(t *testing.T) {
	past := time.Now().Add(-1 * time.Second)
	r := engine.Reservation{ReservationID: 1, ExpiryDate: past}
	assert.True(t, r.IsExpired(time.Now()))
}

func TestReservation_IsNotExpired(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	r := engine.Reservation{ReservationID: 1, ExpiryDate: future}
	assert.False(t, r.IsExpired(time.Now()))
}

func TestEngine_AddConnector(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	c := e.AddConnector(230.0, 32.0, 1)
	assert.Equal(t, 1, c.ID)
	assert.Equal(t, engine.StateAvailable, c.Status)

	c2 := e.AddConnector(400.0, 16.0, 3)
	assert.Equal(t, 2, c2.ID)
}

func TestEngine_RemoveConnector_LastConnectorFails(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	err := e.RemoveConnector(1)
	assert.ErrorIs(t, err, engine.ErrLastConnector)
}

func TestEngine_RemoveConnector_WithActiveSessionFails(t *testing.T) {
	e := engine.NewEngine(true, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	err := e.StartSession(1, -1, 0.0, nil, 0)
	require.NoError(t, err)

	err = e.RemoveConnector(1)
	assert.ErrorIs(t, err, engine.ErrSessionActiveOnRemove)
}

func TestEngine_UpdateConnector_ValidationErrors(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)

	err := e.UpdateConnector(1, pf(50.0), nil, nil) // voltage too low
	assert.ErrorIs(t, err, engine.ErrInvalidVoltage)

	err = e.UpdateConnector(1, nil, pf(3.0), nil) // current too low
	assert.ErrorIs(t, err, engine.ErrInvalidCurrent)

	err = e.UpdateConnector(1, nil, nil, pi(2)) // phase = 2
	assert.ErrorIs(t, err, engine.ErrInvalidPhase)
}

func TestEngine_PlugIn_SingleEVSEMode_UnplugsOthers(t *testing.T) {
	e := engine.NewEngine(false, 55000.0) // single-EVSE
	e.AddConnector(230.0, 32.0, 1)
	e.AddConnector(230.0, 32.0, 1)

	e.PlugIn(1)
	assert.True(t, e.GetConnector(1).IsPluggedIn)

	e.PlugIn(2) // should auto-unplug connector 1
	assert.False(t, e.GetConnector(1).IsPluggedIn)
	assert.True(t, e.GetConnector(2).IsPluggedIn)
}

func TestEngine_StartSession_Basic(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)

	err := e.StartSession(1, -1, 0.0, nil, 0)
	require.NoError(t, err)

	sessions := e.GetSessionInfo()
	assert.Len(t, sessions, 1)
	assert.Equal(t, 1, sessions[0].ConnectorID)
}

func TestEngine_StartSession_NotPluggedIn(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)

	err := e.StartSession(1, -1, 0.0, nil, 0)
	assert.ErrorIs(t, err, engine.ErrNotPluggedIn)
}

func TestEngine_StartSession_SingleEVSE_FailsIfSessionActive(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, 0.0, nil, 0))

	e.PlugIn(2)
	err := e.StartSession(2, -1, 0.0, nil, 0)
	assert.ErrorIs(t, err, engine.ErrSessionAlreadyActive)
}

func TestEngine_StopSession(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, 0.0, nil, 0))

	info := e.StopSession(pi(1), "Local")
	require.NotNil(t, info)
	assert.Equal(t, 1, info.ConnectorID)
	assert.Equal(t, "Local", info.Reason)
	assert.Empty(t, e.GetSessionInfo())
}

func TestEngine_PendingRemoteStart(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)

	// Start session with timeout — stores pending start since not plugged in
	err := e.StartSession(1, 42, 0.0, nil, 30)
	require.NoError(t, err)
	assert.Empty(t, e.GetSessionInfo()) // not started yet

	// Plug in — should consume pending start
	e.PlugIn(1)
	sessions := e.GetSessionInfo()
	require.Len(t, sessions, 1)
	assert.Equal(t, 42, sessions[0].TransactionID)
}

func TestEngine_ReserveConnector(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)

	expiry := time.Now().Add(10 * time.Minute)
	result := e.ReserveConnector(1, 100, "ABC", expiry, nil)
	assert.Equal(t, "accepted", result)
	assert.Equal(t, engine.StateReserved, e.GetConnector(1).Status)
}

func TestEngine_ReserveConnector_OccupiedWhenSessionActive(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, 0.0, nil, 0))

	result := e.ReserveConnector(1, 100, "ABC", time.Now().Add(time.Minute), nil)
	assert.Equal(t, "occupied", result)
}

func TestEngine_CancelReservation(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.ReserveConnector(1, 100, "ABC", time.Now().Add(time.Minute), nil)

	result := e.CancelReservation(100)
	assert.Equal(t, "accepted", result)
	assert.Equal(t, engine.StateAvailable, e.GetConnector(1).Status)
}

func TestEngine_SetConnectorAvailability(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)

	result := e.SetConnectorAvailability(1, "Inoperative")
	assert.Equal(t, "accepted", result)
	assert.Equal(t, engine.StateUnavailable, e.GetConnector(1).Status)

	result = e.SetConnectorAvailability(1, "Operative")
	assert.Equal(t, "accepted", result)
	assert.Equal(t, engine.StateAvailable, e.GetConnector(1).Status)
}

func TestEngine_SetConnectorAvailability_ScheduledWhenSessionActive(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, 0.0, nil, 0))

	result := e.SetConnectorAvailability(1, "Inoperative")
	assert.Equal(t, "scheduled", result)
	assert.Equal(t, engine.StateCharging, e.GetConnector(1).Status) // not changed yet
}

func TestEngine_SetActiveTransaction(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, 0.0, nil, 0))

	e.SetActiveTransaction(1, 42)
	assert.Equal(t, 42, *e.GetActiveTransactionID(1))

	connID := e.GetConnectorByTransaction(42)
	require.NotNil(t, connID)
	assert.Equal(t, 1, *connID)
}

// Helpers for pointer creation in tests.
func pf(v float64) *float64 { return &v }
func pi(v int) *int         { return &v }
func ps(v string) *string   { return &v }
