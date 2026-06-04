package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngine_SaveLoadState_Connectors(t *testing.T) {
	dir := t.TempDir()

	e := NewEngine(false, 55000)
	e.AddConnector(230, 32, 1)
	e.AddConnector(400, 64, 3)

	require.NoError(t, e.SaveState(dir))

	e2 := NewEngine(false, 0)
	require.NoError(t, e2.LoadState(dir))

	ids := e2.GetConnectorIDs()
	assert.Len(t, ids, 2)
	assert.Equal(t, 3, e2.nextConnectorID) // preserved

	c1 := e2.GetConnector(1)
	require.NotNil(t, c1)
	assert.Equal(t, 230.0, c1.Voltage)
	assert.Equal(t, 32.0, c1.Current)
	assert.Equal(t, 1, c1.Phase)
	assert.Equal(t, StateAvailable, c1.Status)
}

func TestEngine_SaveLoadState_Sessions(t *testing.T) {
	dir := t.TempDir()

	e := NewEngine(false, 55000)
	e.AddConnector(230, 32, 1)

	// Plug in and start a session manually.
	c := e.GetConnector(1)
	require.NotNil(t, c)

	e.mu.Lock()
	_ = c.PlugIn()
	_ = c.StartCharging()
	tag := "TAG001"
	s := NewSession(1, 100, 55000, &tag, nil)
	s.EnergyCharged = 1500.0
	s.StateOfCharge = 2.73
	e.sessions[1] = s
	e.globalMeter.Value = 1500.0
	e.globalMeter.IsCharging = true
	e.mu.Unlock()

	require.NoError(t, e.SaveState(dir))

	e2 := NewEngine(false, 0)
	require.NoError(t, e2.LoadState(dir))

	sess := e2.GetSession(1)
	require.NotNil(t, sess)
	assert.Equal(t, 100, sess.TransactionID)
	assert.Equal(t, 1500.0, sess.EnergyCharged)
	assert.Equal(t, "TAG001", *sess.IDTag)

	meter := e2.GetEnergyMeter(1)
	require.NotNil(t, meter)
	assert.Equal(t, 1500.0, meter.Value)
	assert.True(t, meter.IsCharging)
}

func TestEngine_SaveLoadState_Reservations(t *testing.T) {
	dir := t.TempDir()

	e := NewEngine(false, 55000)
	e.AddConnector(230, 32, 1)

	e.mu.Lock()
	e.reservations[10] = &Reservation{
		ReservationID: 10, ConnectorID: 1, IDTag: "RES_TAG",
		ExpiryDate: time.Now().Add(1 * time.Hour),
	}
	// Add an already-expired reservation.
	e.reservations[11] = &Reservation{
		ReservationID: 11, ConnectorID: 1, IDTag: "EXPIRED",
		ExpiryDate: time.Now().Add(-1 * time.Hour),
	}
	e.mu.Unlock()

	require.NoError(t, e.SaveState(dir))

	e2 := NewEngine(false, 0)
	require.NoError(t, e2.LoadState(dir))

	e2.mu.RLock()
	defer e2.mu.RUnlock()

	// Active reservation should be loaded.
	assert.Contains(t, e2.reservations, 10)
	assert.Equal(t, "RES_TAG", e2.reservations[10].IDTag)

	// Expired reservation should be discarded.
	assert.NotContains(t, e2.reservations, 11)
}

// TestEngine_SaveLoadState_ReservationRestoresConnectorStatus verifies
// that when a connector is reserved and the process restarts, both the
// reservation record and the connector's Reserved status are restored.
// This is the property that lets the BootNotification post-accept flow
// (which sends a StatusNotification for each connector) re-broadcast
// the Reserved state to the CSMS, so the CSMS view stays consistent.
func TestEngine_SaveLoadState_ReservationRestoresConnectorStatus(t *testing.T) {
	dir := t.TempDir()

	e := NewEngine(false, 55000)
	e.AddConnector(230, 32, 1)
	require.Equal(t, "accepted", e.ReserveConnector(1, 42, "RES_TAG", time.Now().Add(1*time.Hour), nil))

	// Sanity check: connector is Reserved.
	c := e.GetConnector(1)
	require.NotNil(t, c)
	require.Equal(t, StateReserved, c.Status)

	require.NoError(t, e.SaveState(dir))

	e2 := NewEngine(false, 0)
	require.NoError(t, e2.LoadState(dir))

	c2 := e2.GetConnector(1)
	require.NotNil(t, c2)
	assert.Equal(t, StateReserved, c2.Status, "connector state must be restored as Reserved")

	res := e2.GetReservation(1)
	require.NotNil(t, res)
	assert.Equal(t, 42, res.ReservationID)
	assert.Equal(t, "RES_TAG", res.IDTag)
}

func TestEngine_SaveLoadState_PendingRemoteStarts(t *testing.T) {
	dir := t.TempDir()

	e := NewEngine(false, 55000)
	e.AddConnector(230, 32, 1)

	tag := "REMOTE_TAG"
	e.mu.Lock()
	e.pendingRemoteStarts[1] = &PendingRemoteStart{
		TransactionID: 200, IDTag: &tag, Expiry: time.Now().Add(5 * time.Minute),
	}
	e.pendingRemoteStarts[2] = &PendingRemoteStart{
		TransactionID: 201, IDTag: &tag, Expiry: time.Now().Add(-1 * time.Minute),
	}
	e.mu.Unlock()

	require.NoError(t, e.SaveState(dir))

	e2 := NewEngine(false, 0)
	require.NoError(t, e2.LoadState(dir))

	e2.mu.RLock()
	defer e2.mu.RUnlock()

	assert.Contains(t, e2.pendingRemoteStarts, 1)
	assert.NotContains(t, e2.pendingRemoteStarts, 2) // expired
}

func TestEngine_SaveLoadState_LastStoppedSession(t *testing.T) {
	dir := t.TempDir()

	e := NewEngine(false, 55000)
	tag := "STOP_TAG"
	e.LastStoppedSession = &StoppedSessionInfo{
		TransactionID: 99, ConnectorID: 1, EnergyCharged: 5000,
		IDTag: &tag, MeterStop: 5000, Reason: "Local",
	}

	require.NoError(t, e.SaveState(dir))

	e2 := NewEngine(false, 0)
	require.NoError(t, e2.LoadState(dir))

	require.NotNil(t, e2.LastStoppedSession)
	assert.Equal(t, 99, e2.LastStoppedSession.TransactionID)
	assert.Equal(t, "STOP_TAG", *e2.LastStoppedSession.IDTag)
}

func TestEngine_LoadState_MissingFile(t *testing.T) {
	dir := t.TempDir()

	e := NewEngine(false, 55000)
	err := e.LoadState(dir)
	assert.NoError(t, err)
	// Engine should remain in fresh state.
	assert.Empty(t, e.GetConnectorIDs())
}

func TestEngine_SaveLoadState_ChargingProfile(t *testing.T) {
	dir := t.TempDir()

	e := NewEngine(false, 55000)
	e.AddConnector(230, 32, 1)

	now := time.Now()
	phases := 3
	e.mu.Lock()
	_ = e.connectors[1].PlugIn()
	_ = e.connectors[1].StartCharging()
	e.sessions[1] = &Session{
		TransactionID: 300, ConnectorID: 1, StartTime: now,
		RemoteStartChargingProfile: &ChargingProfile{
			ProfileID: 1, ConnectorID: 1, StackLevel: 0,
			Purpose: "TxProfile", Kind: "Absolute",
			StartSchedule: &now,
			Schedule: ChargingSchedule{
				Duration: 3600, ChargingRateUnit: "A",
				Periods: []ChargingSchedulePeriod{
					{StartPeriod: 0, Limit: 16.0, NumberPhases: &phases},
				},
			},
		},
	}
	e.mu.Unlock()

	require.NoError(t, e.SaveState(dir))

	e2 := NewEngine(false, 0)
	require.NoError(t, e2.LoadState(dir))

	sess := e2.GetSession(1)
	require.NotNil(t, sess)
	require.NotNil(t, sess.RemoteStartChargingProfile)
	assert.Equal(t, "TxProfile", sess.RemoteStartChargingProfile.Purpose)
	assert.Equal(t, 16.0, sess.RemoteStartChargingProfile.Schedule.Periods[0].Limit)
	assert.Equal(t, 3, *sess.RemoteStartChargingProfile.Schedule.Periods[0].NumberPhases)
}

func TestEngine_SaveLoadState_MultiEVSEMeters(t *testing.T) {
	dir := t.TempDir()

	e := NewEngine(true, 55000)
	e.AddConnector(230, 32, 1)
	e.AddConnector(230, 32, 3)

	e.mu.Lock()
	e.energyMeters[1] = &EnergyMeter{Value: 1000, IsCharging: true}
	e.energyMeters[2] = &EnergyMeter{Value: 2000, IsCharging: false}
	e.mu.Unlock()

	require.NoError(t, e.SaveState(dir))

	e2 := NewEngine(true, 0)
	require.NoError(t, e2.LoadState(dir))

	e2.mu.RLock()
	defer e2.mu.RUnlock()
	assert.Equal(t, 1000.0, e2.energyMeters[1].Value)
	assert.True(t, e2.energyMeters[1].IsCharging)
	assert.Equal(t, 2000.0, e2.energyMeters[2].Value)
	assert.False(t, e2.energyMeters[2].IsCharging)
}
