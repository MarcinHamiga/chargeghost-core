package v201

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeviceModel_SaveLoadState(t *testing.T) {
	dir := t.TempDir()

	dm := NewDeviceModel()
	dm.PopulateDefaults("Model", "Vendor", "SN001", "1.0.0", "cType2", 2)
	dm.SetVariable("OCPPCommCtrlr", "", 0, "HeartbeatInterval", "120", MutabilityReadWrite)

	require.NoError(t, dm.SaveState(dir))

	dm2 := NewDeviceModel()
	require.NoError(t, dm2.LoadState(dir))

	r := dm2.GetVariable("OCPPCommCtrlr", "", 0, "HeartbeatInterval")
	assert.Equal(t, "120", r.Value)

	r2 := dm2.GetVariable("ChargingStation", "", 0, "Model")
	assert.Equal(t, "Model", r2.Value)
}

func TestDeviceModel_LoadState_MissingFile(t *testing.T) {
	dm := NewDeviceModel()
	err := dm.LoadState(t.TempDir())
	assert.NoError(t, err)
	assert.Empty(t, dm.AllVariables())
}

func TestMonitoringManager_SaveLoadState(t *testing.T) {
	dir := t.TempDir()

	dm := NewDeviceModel()
	dm.SetVariable("EVSE", "", 1, "Power", "0", MutabilityReadOnly)

	mm := NewMonitoringManager(dm)
	id, err := mm.AddMonitor("EVSE", "", 1, "Power", MonitorTypeUpperThreshold, 7000, 5)
	require.NoError(t, err)
	require.Greater(t, id, 0)

	require.NoError(t, mm.SaveState(dir))

	mm2 := NewMonitoringManager(dm)
	require.NoError(t, mm2.LoadState(dir))

	monitors := mm2.GetAllMonitors()
	require.Len(t, monitors, 1)
	assert.Equal(t, "Power", monitors[0].Variable)
	assert.Equal(t, MonitorTypeUpperThreshold, monitors[0].Type)
	assert.Equal(t, 7000.0, monitors[0].Value)
}

func TestDisplayMessageStore_SaveLoadState(t *testing.T) {
	dir := t.TempDir()

	s := NewDisplayMessageStore()
	s.Set(DisplayMessage{ID: 1, Priority: "NormalCycle", State: "Charging", Text: "Hello", Language: "en"})

	require.NoError(t, s.SaveState(dir))

	s2 := NewDisplayMessageStore()
	require.NoError(t, s2.LoadState(dir))

	msg, ok := s2.Get(1)
	require.True(t, ok)
	assert.Equal(t, "Hello", msg.Text)
	assert.Equal(t, "en", msg.Language)
}

func TestCostStore_SaveLoadState(t *testing.T) {
	dir := t.TempDir()

	cs := NewCostStore()
	cs.Update("tx-001", 12.50)
	cs.Update("tx-002", 5.00)

	require.NoError(t, cs.SaveState(dir))

	cs2 := NewCostStore()
	require.NoError(t, cs2.LoadState(dir))

	cost, ok := cs2.Get("tx-001")
	assert.True(t, ok)
	assert.Equal(t, 12.50, cost)

	cost2, ok := cs2.Get("tx-002")
	assert.True(t, ok)
	assert.Equal(t, 5.00, cost2)
}
