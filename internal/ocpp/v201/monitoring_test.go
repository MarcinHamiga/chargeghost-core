package v201

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMonitoringManager_AddMonitor(t *testing.T) {
	dm := NewDeviceModel()
	dm.SetVariable("EVSE", "", 1, "Power", "1000", MutabilityReadOnly)
	mm := NewMonitoringManager(dm)

	id, err := mm.AddMonitor("EVSE", "", 1, "Power", MonitorTypeUpperThreshold, 7000, 5)
	require.NoError(t, err)
	assert.Greater(t, id, 0)
}

func TestMonitoringManager_ClearMonitor(t *testing.T) {
	dm := NewDeviceModel()
	dm.SetVariable("EVSE", "", 1, "Power", "1000", MutabilityReadOnly)
	mm := NewMonitoringManager(dm)

	id, _ := mm.AddMonitor("EVSE", "", 1, "Power", MonitorTypeUpperThreshold, 7000, 5)
	ok := mm.ClearMonitor(id)
	assert.True(t, ok)

	ok = mm.ClearMonitor(id)
	assert.False(t, ok) // already cleared
}

func TestMonitoringManager_GetAllMonitors(t *testing.T) {
	dm := NewDeviceModel()
	dm.SetVariable("EVSE", "", 1, "Power", "1000", MutabilityReadOnly)
	mm := NewMonitoringManager(dm)

	mm.AddMonitor("EVSE", "", 1, "Power", MonitorTypeUpperThreshold, 7000, 5)
	mm.AddMonitor("EVSE", "", 1, "Power", MonitorTypeLowerThreshold, 100, 3)

	monitors := mm.GetAllMonitors()
	assert.Len(t, monitors, 2)
}

func TestMonitoringManager_UnknownVariable(t *testing.T) {
	dm := NewDeviceModel()
	mm := NewMonitoringManager(dm)

	_, err := mm.AddMonitor("Unknown", "", 0, "Var", MonitorTypeUpperThreshold, 100, 5)
	assert.Error(t, err)
}
