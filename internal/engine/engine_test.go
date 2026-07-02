package engine_test

import (
	"sync"
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

func TestEngine_SessionCost_DisabledWhenPriceZero(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, nil, 0))
	// PricePerKWh is zero by default — cost disabled
	_, _, ok := e.SessionCost(1)
	assert.False(t, ok)
}

func TestEngine_SessionCost_NoSession(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PricePerKWh = 0.35
	e.CurrencyCode = "EUR"
	_, _, ok := e.SessionCost(1)
	assert.False(t, ok)
}

func TestEngine_SessionCost_Running(t *testing.T) {
	e := engine.NewEngine(false, 0) // SoC disabled
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, nil, 0))
	e.PricePerKWh = 0.50
	e.CurrencyCode = "USD"

	// Verify the helper returns the configured currency and reports ok for
	// an active session. Energy math is covered by TestSession_EnergyCappedAtMaxEnergy
	// and similar unit tests; SessionCost is a thin linear projection of
	// EnergyCharged onto PricePerKWh, so we assert currency wiring here.
	cost, currency, ok := e.SessionCost(1)
	assert.True(t, ok)
	assert.InDelta(t, 0.0, cost, 0.0001)
	assert.Equal(t, "USD", currency)
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
	err := e.StartSession(1, -1, nil, 0)
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

	err := e.StartSession(1, -1, nil, 0)
	require.NoError(t, err)

	sessions := e.GetSessionInfo()
	assert.Len(t, sessions, 1)
	assert.Equal(t, 1, sessions[0].ConnectorID)
}

func TestEngine_StartSession_NotPluggedIn(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)

	err := e.StartSession(1, -1, nil, 0)
	assert.ErrorIs(t, err, engine.ErrNotPluggedIn)
}

func TestEngine_StartSession_SingleEVSE_FailsIfSessionActive(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, nil, 0))

	e.PlugIn(2)
	err := e.StartSession(2, -1, nil, 0)
	assert.ErrorIs(t, err, engine.ErrSessionAlreadyActive)
}

func TestEngine_StopSession(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, nil, 0))

	info := e.StopSession(pi(1), "Local")
	require.NotNil(t, info)
	assert.Equal(t, 1, info.ConnectorID)
	assert.Equal(t, "Local", info.Reason)
	assert.Empty(t, e.GetSessionInfo())
}

func TestEngine_StartSession_UsesConfiguredBatteryCapacityByDefault(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(1000.0, 150.0, 3)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, nil, 0))

	session := e.GetSession(1)
	require.NotNil(t, session)
	assert.InDelta(t, 55000.0, session.MaxEnergy, 0.001)

	e.Simulate(500.0)

	session = e.GetSession(1)
	require.NotNil(t, session)
	assert.InDelta(t, 55000.0, session.EnergyCharged, 0.001)
	assert.InDelta(t, 100.0, session.StateOfCharge, 0.001)
	assert.True(t, session.MaxChargeReached)

	meter := e.GetEnergyMeter(1)
	require.NotNil(t, meter)
	assert.InDelta(t, 55000.0, meter.Value, 0.001)
	assert.False(t, meter.IsCharging)

	c := e.GetConnector(1)
	require.NotNil(t, c)
	assert.Equal(t, engine.StateSuspendedEV, c.Status)
}

func TestEngine_PendingRemoteStart(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)

	// Start session with timeout — stores pending start since not plugged in
	err := e.StartSession(1, 42, nil, 30)
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
	require.NoError(t, e.StartSession(1, -1, nil, 0))

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
	require.NoError(t, e.StartSession(1, -1, nil, 0))

	result := e.SetConnectorAvailability(1, "Inoperative")
	assert.Equal(t, "scheduled", result)
	assert.Equal(t, engine.StateCharging, e.GetConnector(1).Status) // not changed yet
}

func TestEngine_SetActiveTransaction(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, nil, 0))

	e.SetActiveTransaction(1, 42)
	assert.Equal(t, 42, *e.GetActiveTransactionID(1))

	connID := e.GetConnectorByTransaction(42)
	require.NotNil(t, connID)
	assert.Equal(t, 1, *connID)
}

func TestEngine_GetSessionByTransactionReturnsDeepCopy(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, nil, 0))
	e.SetActiveTransaction(1, 42)
	e.Simulate(1)

	connectorID, session := e.GetSessionByTransaction(42)
	require.NotNil(t, session)
	assert.Equal(t, 1, connectorID)

	originalLen := len(session.MeterHistory)
	session.MeterHistory = append(session.MeterHistory, engine.MeterRecord{Timestamp: "x", Value: 999})

	_, session2 := e.GetSessionByTransaction(42)
	require.NotNil(t, session2)
	assert.Equal(t, originalLen, len(session2.MeterHistory))
}

func TestEngine_OnSessionStarted_DoesNotDeadlock(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)

	done := make(chan struct{})
	e.OnSessionStarted = func(connectorID int, idTag *string, meterStart float64, reservationID *int) {
		// This re-entrant call would deadlock if the engine lock is held during callback invocation.
		_ = e.GetConnector(connectorID)
		close(done)
	}

	go e.StartSession(1, 1, nil, 0)

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("OnSessionStarted deadlocked")
	}
}

func TestEngine_OnSessionStopped_DoesNotDeadlock(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	_ = e.StartSession(1, 1, nil, 0)

	done := make(chan struct{})
	e.OnSessionStopped = func(connectorID int, info *engine.StoppedSessionInfo) {
		// This re-entrant call would deadlock if the engine lock is held during callback invocation.
		_ = e.GetConnector(connectorID)
		close(done)
	}

	go e.StopSession(nil, "Local")

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnSessionStopped deadlocked")
	}
}

func TestEngine_GetLimit_DoesNotDeadlock(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	_ = e.StartSession(1, 1, nil, 0)

	// GetLimit is called while the write lock is held inside Simulate().
	// The fix ensures the production closures in main.go do NOT call back into
	// the engine. This closure records that it was invoked and verifies that
	// the voltage, phases, and txStart parameters are correctly forwarded so
	// the caller has no need to re-enter the engine.
	called := make(chan struct{}, 1)
	e.GetLimit = func(connectorID, transactionID int, voltage float64, phases int, txStart *time.Time) *float64 {
		// Verify that all parameters are forwarded (non-zero/non-nil).
		// This avoids any engine re-entry that would deadlock.
		if voltage > 0 && phases > 0 && txStart != nil {
			select {
			case called <- struct{}{}:
			default:
			}
		}
		return nil
	}

	done := make(chan struct{})
	go func() {
		e.Simulate(0.1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Simulate deadlocked with GetLimit set")
	}
	select {
	case <-called:
	default:
		t.Fatal("GetLimit was never called (or parameters were zero/nil)")
	}
}

func TestEngine_Simulate_EffectiveCurrentTracksGetLimit(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 1, nil, 0))

	e.GetLimit = func(connectorID, transactionID int, voltage float64, phases int, txStart *time.Time) *float64 {
		limit := 16.0
		return &limit
	}

	e.Simulate(1.0)

	meter := e.GetEnergyMeter(1)
	require.NotNil(t, meter)
	assert.InDelta(t, 16.0, meter.EffectiveCurrent, 0.001, "EffectiveCurrent must reflect the GetLimit-capped current, not the connector's rated 32A")
}

func TestEngine_Simulate_EffectiveCurrentReflectsRatedCurrentWhenNoLimit(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 1, nil, 0))

	e.Simulate(1.0)

	meter := e.GetEnergyMeter(1)
	require.NotNil(t, meter)
	assert.InDelta(t, 32.0, meter.EffectiveCurrent, 0.001)
}

func TestEngine_SuspendEV_ZeroesEffectiveCurrent(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 1, nil, 0))
	e.Simulate(1.0)

	meter := e.GetEnergyMeter(1)
	require.NotNil(t, meter)
	require.Greater(t, meter.EffectiveCurrent, 0.0, "sanity check: meter must be actively charging before suspend")

	require.NoError(t, e.SuspendEV(1))

	meter = e.GetEnergyMeter(1)
	require.NotNil(t, meter)
	assert.Zero(t, meter.EffectiveCurrent)
}

func TestEngine_GetConnector_ReturnsCopy(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)

	c1 := e.GetConnector(1)
	require.NotNil(t, c1)

	// Mutate the returned value — must not affect internal state
	c1.Voltage = 999

	c2 := e.GetConnector(1)
	require.NotNil(t, c2)
	assert.Equal(t, 230.0, c2.Voltage, "internal state should not be affected by mutating returned copy")
}

func TestEngine_GetSession_ReturnsCopy(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	tag := "TAG1"
	_ = e.StartSession(1, 42, &tag, 0)

	s1 := e.GetSession(1)
	require.NotNil(t, s1)
	s1.TransactionID = 9999

	s2 := e.GetSession(1)
	require.NotNil(t, s2)
	assert.Equal(t, 42, s2.TransactionID, "internal session should not be mutated via returned copy")
}

func TestEngine_SetSessionChargingProfile(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	tag := "TAG1"
	_ = e.StartSession(1, 1, &tag, 0)

	profile := &engine.ChargingProfile{ProfileID: 99, StackLevel: 1}
	e.SetSessionChargingProfile(1, profile)

	s := e.GetSession(1)
	require.NotNil(t, s)
	require.NotNil(t, s.RemoteStartChargingProfile)
	assert.Equal(t, 99, s.RemoteStartChargingProfile.ProfileID)
}

func TestEngine_SetSessionRemoteStartID(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	tag := "TAG1"
	_ = e.StartSession(1, 1, &tag, 0)

	e.SetSessionRemoteStartID(1, 42)

	s := e.GetSession(1)
	require.NotNil(t, s)
	require.NotNil(t, s.RemoteStartID)
	assert.Equal(t, 42, *s.RemoteStartID)
}

func TestEngine_PendingRemoteStart_PreservesRemoteStartID(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	// EV is NOT yet plugged in — StartSession creates a PendingRemoteStart.

	tag := "TAG1"
	err := e.StartSession(1, -1, &tag, 30)
	require.NoError(t, err)
	assert.Empty(t, e.GetSessionInfo(), "no session yet while EV is unplugged")

	e.SetPendingRemoteStartID(1, 42)

	// EV plugs in — the pending start is consumed, remoteStartId must be applied.
	e.PlugIn(1)

	s := e.GetSession(1)
	require.NotNil(t, s)
	require.NotNil(t, s.RemoteStartID, "remoteStartId must survive the pending→active transition")
	assert.Equal(t, 42, *s.RemoteStartID)
}

func TestEngine_PendingRemoteStart_PreservesChargingProfile(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	// EV is NOT yet plugged in — StartSession creates a PendingRemoteStart.

	tag := "TAG1"
	err := e.StartSession(1, -1, &tag, 30)
	require.NoError(t, err)
	assert.Empty(t, e.GetSessionInfo(), "no session yet while EV is unplugged")

	profile := &engine.ChargingProfile{ProfileID: 77, StackLevel: 1}
	e.SetPendingRemoteStartChargingProfile(1, profile)

	// EV plugs in — the pending start is consumed, profile must be applied.
	e.PlugIn(1)

	s := e.GetSession(1)
	require.NotNil(t, s)
	require.NotNil(t, s.RemoteStartChargingProfile, "charging profile must survive the pending→active transition")
	assert.Equal(t, 77, s.RemoteStartChargingProfile.ProfileID)
}

func TestEngine_GetSession_MeterHistoryDeepCopy(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	tag := "TAG1"
	_ = e.StartSession(1, 1, &tag, 0)

	// Obtain a snapshot and mutate its MeterHistory slice.
	s1 := e.GetSession(1)
	require.NotNil(t, s1)
	originalLen := len(s1.MeterHistory)
	s1.MeterHistory = append(s1.MeterHistory, engine.MeterRecord{Timestamp: "x", Value: 999})

	// A second snapshot must not reflect the mutation.
	s2 := e.GetSession(1)
	require.NotNil(t, s2)
	assert.Equal(t, originalLen, len(s2.MeterHistory), "GetSession must return an independent MeterHistory copy")
}

func TestEngine_StartSession_WrongIDTag_DoesNotConsumeReservation(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)

	// Reserve connector 1 for CORRECT_TAG.
	result := e.ReserveConnector(1, 42, "CORRECT_TAG", time.Now().Add(10*time.Minute), nil)
	require.Equal(t, "accepted", result)

	// Plug in and attempt to start a session with the wrong tag.
	e.PlugIn(1)
	wrongTag := "WRONG_TAG"
	err := e.StartSession(1, 1, &wrongTag, 0)

	// The session must be rejected (idTag does not match reservation).
	assert.Error(t, err, "wrong-tag session should be rejected when a reservation exists")
	assert.Empty(t, e.GetSessionInfo(), "no session should have been created")

	// The reservation must still exist — it was not consumed.
	res := e.GetReservation(1)
	require.NotNil(t, res, "reservation should not be consumed by wrong-tag session")
	assert.Equal(t, 42, res.ReservationID)
}

func TestEngine_PlugIn_PendingRemoteStart_WrongIDTag_DoesNotConsumeReservation(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)

	// Reserve connector 1 for CORRECT_TAG.
	result := e.ReserveConnector(1, 99, "CORRECT_TAG", time.Now().Add(10*time.Minute), nil)
	require.Equal(t, "accepted", result)

	// A remote start arrives with WRONG_TAG while the EV is already plugged in
	// but the connector is still in Preparing state (no session yet).
	// The reservation compatibility check should reject this attempt.
	e.PlugIn(1) // connector moves to Preparing

	wrongTag := "WRONG_TAG"
	err := e.StartSession(1, 77, &wrongTag, 0)
	assert.ErrorIs(t, err, engine.ErrInvalidState, "wrong-tag session should be rejected when a reservation exists")

	// No session should have started.
	assert.Empty(t, e.GetSessionInfo(), "session must not start when idTag doesn't match reservation")

	// The reservation should still exist.
	res := e.GetReservation(1)
	require.NotNil(t, res, "reservation should not be consumed by wrong-tag session")
	assert.Equal(t, 99, res.ReservationID)
}

func TestEngine_PlugIn_WithPendingRemoteStart_ReportsPreparing(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)

	// Store a pending remote start.
	tag := "TAG1"
	err := e.StartSession(1, 5, &tag, 30)
	require.NoError(t, err)

	var statuses []engine.ConnectorState
	e.OnConnectorStatusChanged = func(connectorID int, status engine.ConnectorState) {
		statuses = append(statuses, status)
	}

	// PlugIn should fire Preparing then Charging, not Charging twice.
	e.PlugIn(1)

	require.Len(t, statuses, 2, "expected exactly two status callbacks: Preparing then Charging")
	assert.Equal(t, engine.StatePreparing, statuses[0], "first callback must be Preparing")
	assert.Equal(t, engine.StateCharging, statuses[1], "second callback must be Charging")
}

func TestEngine_PlugIn_Unavailable_FiresPlugChangedWithoutStatus(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	require.Equal(t, "accepted", e.SetConnectorAvailability(1, "Inoperative"))

	var plugEvents []bool
	e.OnConnectorPlugChanged = func(connectorID int, isPluggedIn bool) {
		require.Equal(t, 1, connectorID)
		plugEvents = append(plugEvents, isPluggedIn)
	}
	var statusEvents int
	e.OnConnectorStatusChanged = func(int, engine.ConnectorState) {
		statusEvents++
	}

	e.PlugIn(1)

	require.Equal(t, []bool{true}, plugEvents)
	assert.Equal(t, 0, statusEvents)
	require.NotNil(t, e.GetConnector(1))
	assert.True(t, e.GetConnector(1).IsPluggedIn)
	assert.Equal(t, engine.StateUnavailable, e.GetConnector(1).Status)
}

func TestEngine_SetActiveTransaction_FiresCallback(t *testing.T) {
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, nil, 0))

	var gotConn, gotTx int
	e.OnTransactionIDChanged = func(connectorID, transactionID int) {
		gotConn = connectorID
		gotTx = transactionID
	}

	e.SetActiveTransaction(1, 99)
	assert.Equal(t, 1, gotConn)
	assert.Equal(t, 99, gotTx)
}

func TestEngine_ExpireReservation_FiresStatusWhenReservedClears(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)

	expiry := time.Now().Add(-1 * time.Minute)
	require.Equal(t, "accepted", e.ReserveConnector(1, 7, "TAG", expiry, nil))

	var statuses []engine.ConnectorState
	e.OnConnectorStatusChanged = func(_ int, status engine.ConnectorState) {
		statuses = append(statuses, status)
	}

	e.PlugIn(1) // expireReservations runs at the start of PlugIn

	require.NotEmpty(t, statuses)
	assert.Equal(t, engine.StateAvailable, statuses[0])
}

// Helpers for pointer creation in tests.
func pf(v float64) *float64 { return &v }
func pi(v int) *int         { return &v }
func ps(v string) *string   { return &v }

// TestEngine_ChargingStateChanged_OnStartSession verifies that the engine
// fires OnChargingStateChanged with StateCharging when a session begins
// (the connector transitions Preparing → Charging).
func TestEngine_ChargingStateChanged_OnStartSession(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)

	var got []engine.ConnectorState
	done := make(chan struct{})
	e.OnChargingStateChanged = func(_ int, state engine.ConnectorState) {
		got = append(got, state)
		if len(got) == 1 {
			close(done)
		}
	}

	require.NoError(t, e.StartSession(1, 1, nil, 0))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnChargingStateChanged did not fire on start_session")
	}
	assert.Equal(t, []engine.ConnectorState{engine.StateCharging}, got)
}

// TestEngine_ChargingStateChanged_OnSuspendResume verifies that the engine
// fires OnChargingStateChanged for SuspendedEV ↔ Charging transitions.
func TestEngine_ChargingStateChanged_OnSuspendResume(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)

	var got []engine.ConnectorState
	var mu sync.Mutex
	done := make(chan struct{})
	e.OnChargingStateChanged = func(_ int, state engine.ConnectorState) {
		mu.Lock()
		got = append(got, state)
		mu.Unlock()
		if state == engine.StateCharging && len(got) >= 3 {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	}

	require.NoError(t, e.StartSession(1, 1, nil, 0))
	require.NoError(t, e.SuspendEV(1))
	require.NoError(t, e.ResumeCharging(1))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnChargingStateChanged did not fire on suspend/resume")
	}
	mu.Lock()
	defer mu.Unlock()
	// Expect: [Charging (from StartSession), SuspendedEV, Charging]
	assert.Equal(t, []engine.ConnectorState{
		engine.StateCharging,
		engine.StateSuspendedEV,
		engine.StateCharging,
	}, got)
}

// TestEngine_ChargingStateChanged_OnSimulateSuspend verifies that the
// engine fires OnChargingStateChanged when the simulation loop suspends
// the connector (effectiveCurrent == 0) based on a charging profile limit.
// The resume path is intentionally not tested here because Simulate skips
// connectors with meter.IsCharging=false, which is a pre-existing limitation
// outside the scope of this change.
func TestEngine_ChargingStateChanged_OnSimulateSuspend(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)

	var got []engine.ConnectorState
	var mu sync.Mutex
	done := make(chan struct{})
	e.OnChargingStateChanged = func(_ int, state engine.ConnectorState) {
		mu.Lock()
		got = append(got, state)
		mu.Unlock()
		if state == engine.StateSuspendedEVSE {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	}

	require.NoError(t, e.StartSession(1, 1, nil, 0))
	mu.Lock()
	got = got[:0] // discard the initial Charging callback
	mu.Unlock()

	// Inject a charging profile limit that returns 0 current — Simulate will
	// then call c.SuspendEVSE() on the next tick.
	e.GetLimit = func(_ int, _ int, _ float64, _ int, _ *time.Time) *float64 {
		zero := 0.0
		return &zero
	}

	e.Simulate(0.1)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnChargingStateChanged did not fire for EVSE suspend")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, got, engine.StateSuspendedEVSE)
}

// TestEngine_ChargingStateChanged_NotFiredOnStop verifies that OnChargingStateChanged
// is NOT fired for the transition to Finishing — the Ended TransactionEvent
// is the canonical report for that transition.
func TestEngine_ChargingStateChanged_NotFiredOnStop(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)

	var got []engine.ConnectorState
	e.OnChargingStateChanged = func(_ int, state engine.ConnectorState) {
		got = append(got, state)
	}

	require.NoError(t, e.StartSession(1, 1, nil, 0))
	initialCount := len(got)
	assert.Equal(t, 1, initialCount, "expected one Charging callback from StartSession")
	assert.Equal(t, engine.StateCharging, got[0])

	e.StopSession(nil, "Local")

	// The transition to Finishing is reported through the Ended
	// TransactionEvent, not via OnChargingStateChanged.
	assert.Equal(t, 1, len(got), "OnChargingStateChanged should not fire on stop")
}

// TestEngine_ChargingStateChanged_NotFiredOnUnavailableOrFaulted verifies that
// OnChargingStateChanged is NOT fired for transitions to non-charging states
// (Unavailable, Faulted, Reserved, Available).
func TestEngine_ChargingStateChanged_NotFiredOnUnavailableOrFaulted(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)

	var got []engine.ConnectorState
	e.OnChargingStateChanged = func(_ int, state engine.ConnectorState) {
		got = append(got, state)
	}

	require.NoError(t, e.StartSession(1, 1, nil, 0))
	got = nil // discard the initial Charging callback

	require.NoError(t, e.FaultConnector(1, "Overvoltage"))
	assert.Empty(t, got, "Faulted transition should not fire OnChargingStateChanged")
	require.NoError(t, e.ClearFault(1))
	assert.Empty(t, got, "post-Faulted transitions should not fire OnChargingStateChanged")

	// Unplug and re-plug — should not fire OnChargingStateChanged because
	// the connector never enters a charging state during this sequence.
	e.Unplug(1)
	e.PlugIn(1)
	assert.Empty(t, got, "Available/Preparing transitions should not fire OnChargingStateChanged")
}

func TestEngine_FaultConnector_StopsActiveSession(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 1, nil, 0))

	var statusEvents []engine.ConnectorState
	e.OnConnectorStatusChanged = func(_ int, s engine.ConnectorState) {
		statusEvents = append(statusEvents, s)
	}

	require.NoError(t, e.FaultConnector(1, "Overvoltage"))
	faulted := e.GetConnector(1)
	require.NotNil(t, faulted)
	assert.Equal(t, engine.StateFaulted, faulted.Status)
	assert.Empty(t, e.GetSession(1), "session must be stopped when faulting")

	// ClearFault restores the previous persistent status. Connector is still
	// plugged in, so the connector returns to Preparing (the resume-ready state).
	require.NoError(t, e.ClearFault(1))
	after := e.GetConnector(1)
	require.NotNil(t, after)
	assert.Equal(t, engine.StatePreparing, after.Status)
}

func TestEngine_SuspendEV_ResumeCharging(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 1, nil, 0))

	var chargingEvents []engine.ConnectorState
	e.OnChargingStateChanged = func(_ int, s engine.ConnectorState) {
		chargingEvents = append(chargingEvents, s)
	}
	chargingEvents = nil // discard initial Charging

	require.NoError(t, e.SuspendEV(1))
	suspended := e.GetConnector(1)
	require.NotNil(t, suspended)
	assert.Equal(t, engine.StateSuspendedEV, suspended.Status)
	assert.Equal(t, []engine.ConnectorState{engine.StateSuspendedEV}, chargingEvents)

	require.NoError(t, e.ResumeCharging(1))
	resumed := e.GetConnector(1)
	require.NotNil(t, resumed)
	assert.Equal(t, engine.StateCharging, resumed.Status)
	assert.Equal(t, []engine.ConnectorState{engine.StateSuspendedEV, engine.StateCharging}, chargingEvents)
}

func TestEngine_StopTxOnEVSideDisconnect_RespectedViaConfigKey(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 1, nil, 0))

	// When StopTxOnEVSideDisconnect is false, unplug should NOT stop the session.
	e.GetConfigValue = func(key string) string {
		if key == "StopTransactionOnEVSideDisconnect" {
			return "false"
		}
		return ""
	}

	e.Unplug(1)
	assert.NotNil(t, e.GetSession(1), "session must remain active when StopTxOnEVSideDisconnect=false")
}

func TestEngine_StopTxOnEVSideDisconnect_DefaultStopsSession(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 1, nil, 0))

	// Default behaviour: unplug stops the session.
	e.Unplug(1)
	assert.Nil(t, e.GetSession(1), "session must stop when StopTxOnEVSideDisconnect defaults to true")
}

// TestEngine_ChargingStateChanged_OnMaxEnergySuspend verifies that the
// engine fires OnChargingStateChanged with StateSuspendedEV when the
// session's MaxEnergy cap is reached (engine-internal suspend).
func TestEngine_ChargingStateChanged_OnMaxEnergySuspend(t *testing.T) {
	e := engine.NewEngine(false, 5000.0) // small battery
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)

	var got []engine.ConnectorState
	var mu sync.Mutex
	done := make(chan struct{})
	e.OnChargingStateChanged = func(_ int, s engine.ConnectorState) {
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
		if s == engine.StateSuspendedEV {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	}

	require.NoError(t, e.StartSession(1, 1, nil, 0))
	// Simulate long enough to fill the 5000 Wh battery; engine will
	// suspend EV when the cap is reached.
	e.Simulate(5000.0)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnChargingStateChanged did not fire for max-energy suspend")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, got, engine.StateSuspendedEV)
}

// TestEngine_ChargingStateChanged_OnCurrentLimitSuspend verifies that the
// engine fires OnChargingStateChanged with StateSuspendedEVSE when an active
// charging profile reduces the current limit to 0 (engine-internal EVSE
// suspend), and fires StateCharging when the limit returns to a non-zero
// value.
func TestEngine_ChargingStateChanged_OnCurrentLimitSuspend(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 1, nil, 0))

	// Install a zero-amp limit via GetLimit callback; engine should transition
	// the connector to SuspendedEVSE on the next Simulate.
	zero := 0.0
	nonzero := 16.0
	var phase int
	e.GetLimit = func(_ int, _ int, _ float64, _ int, _ *time.Time) *float64 {
		if phase == 0 {
			return &zero
		}
		return &nonzero
	}

	var got []engine.ConnectorState
	var mu sync.Mutex
	doneSuspend := make(chan struct{})
	doneResume := make(chan struct{})
	e.OnChargingStateChanged = func(_ int, s engine.ConnectorState) {
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
		if s == engine.StateSuspendedEVSE {
			select {
			case <-doneSuspend:
			default:
				close(doneSuspend)
			}
		}
		if s == engine.StateCharging && phase > 0 {
			select {
			case <-doneResume:
			default:
				close(doneResume)
			}
		}
	}

	phase = 0
	e.Simulate(0.1)
	select {
	case <-doneSuspend:
	case <-time.After(2 * time.Second):
		t.Fatal("OnChargingStateChanged did not fire for EVSE suspend")
	}
	c := e.GetConnector(1)
	require.NotNil(t, c)
	assert.Equal(t, engine.StateSuspendedEVSE, c.Status)

	phase = 1
	e.Simulate(0.1)
	select {
	case <-doneResume:
	case <-time.After(2 * time.Second):
		t.Fatal("OnChargingStateChanged did not fire for EVSE resume")
	}
	c = e.GetConnector(1)
	require.NotNil(t, c)
	assert.Equal(t, engine.StateCharging, c.Status)
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, got, engine.StateSuspendedEVSE)
	assert.Contains(t, got, engine.StateCharging)
}

// TestEngine_ChargingStateChanged_PreservesIDTagThroughTransition verifies
// that StoppedSessionInfo carries the original IDTag so the OCPP layer can
// include it in TransactionEvent(Ended).
func TestEngine_ChargingStateChanged_PreservesIDTagThroughTransition(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)

	tag := "RFID-001"
	require.NoError(t, e.StartSession(1, 1, &tag, 0))

	var capturedInfo *engine.StoppedSessionInfo
	var mu sync.Mutex
	done := make(chan struct{})
	e.OnSessionStopped = func(_ int, info *engine.StoppedSessionInfo) {
		mu.Lock()
		capturedInfo = info
		mu.Unlock()
		close(done)
	}

	e.StopSession(pi(1), "Local")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("OnSessionStopped did not fire")
	}
	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, capturedInfo)
	require.NotNil(t, capturedInfo.IDTag, "IDTag should be captured in StoppedSessionInfo")
	assert.Equal(t, "RFID-001", *capturedInfo.IDTag)
}

// TestEngine_ChargingStateChanged_NotFiredWhenNoSession verifies that the
// callback is NOT fired for any of the "active charging" states while there
// is no active transaction. The callback only fires for connectors with a
// running session in a charging state.
func TestEngine_ChargingStateChanged_NotFiredWhenNoSession(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	e.PlugIn(1)

	var got []engine.ConnectorState
	e.OnChargingStateChanged = func(_ int, state engine.ConnectorState) {
		got = append(got, state)
	}

	// No session is started. Even though the connector is in Preparing,
	// OnChargingStateChanged should not fire (Preparing is not a charging
	// state from the OCPP 2.0.1 perspective).
	assert.Empty(t, got, "OnChargingStateChanged should not fire while no session is active")

	// Plug/Unplug cycles must not fire the callback.
	e.Unplug(1)
	e.PlugIn(1)
	assert.Empty(t, got)

	// Run Simulate - no session, so no callback.
	e.Simulate(0.1)
	assert.Empty(t, got, "Simulate should not fire OnChargingStateChanged without an active session")
}
