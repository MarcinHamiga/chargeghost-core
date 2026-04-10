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

func TestEngine_GetSessionByTransactionReturnsDeepCopy(t *testing.T) {
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, -1, 0.0, nil, 0))
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

	go e.StartSession(1, 1, 0, nil, 0)

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
	_ = e.StartSession(1, 1, 0, nil, 0)

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
	_ = e.StartSession(1, 1, 0, nil, 0)

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
	_ = e.StartSession(1, 42, 0, &tag, 0)

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
	_ = e.StartSession(1, 1, 0, &tag, 0)

	profile := &engine.ChargingProfile{ProfileID: 99, StackLevel: 1}
	e.SetSessionChargingProfile(1, profile)

	s := e.GetSession(1)
	require.NotNil(t, s)
	require.NotNil(t, s.RemoteStartChargingProfile)
	assert.Equal(t, 99, s.RemoteStartChargingProfile.ProfileID)
}

func TestEngine_PendingRemoteStart_PreservesChargingProfile(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	// EV is NOT yet plugged in — StartSession creates a PendingRemoteStart.

	tag := "TAG1"
	err := e.StartSession(1, -1, 0.0, &tag, 30)
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
	_ = e.StartSession(1, 1, 0, &tag, 0)

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
	err := e.StartSession(1, 1, 0, &wrongTag, 0)

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
	err := e.StartSession(1, 77, 0, &wrongTag, 0)
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
	err := e.StartSession(1, 5, 0, &tag, 30)
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

// Helpers for pointer creation in tests.
func pf(v float64) *float64 { return &v }
func pi(v int) *int         { return &v }
func ps(v string) *string   { return &v }
