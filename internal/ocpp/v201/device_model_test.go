package v201

import (
	"testing"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeviceModel_GetStaticVariable(t *testing.T) {
	dm := NewDeviceModel()
	dm.SetVariable("ChargingStation", "", 0, "Model", "TestModel", MutabilityReadOnly)

	result := dm.GetVariable("ChargingStation", "", 0, "Model")
	require.NotNil(t, result)
	assert.Equal(t, "TestModel", result.Value)
	assert.Equal(t, provisioning.GetVariableStatusAccepted, result.Status)
}

func TestDeviceModel_SetReadOnlyRejected(t *testing.T) {
	dm := NewDeviceModel()
	dm.SetVariable("ChargingStation", "", 0, "Model", "Original", MutabilityReadOnly)

	status := dm.SetVariableExternal("ChargingStation", "", 0, "Model", "Changed")
	assert.Equal(t, provisioning.SetVariableStatusRejected, status)

	// Value unchanged
	result := dm.GetVariable("ChargingStation", "", 0, "Model")
	assert.Equal(t, "Original", result.Value)
}

func TestDeviceModel_SetWritableAccepted(t *testing.T) {
	dm := NewDeviceModel()
	dm.SetVariable("OCPPCommCtrlr", "", 0, "HeartbeatInterval", "300", MutabilityReadWrite)

	status := dm.SetVariableExternal("OCPPCommCtrlr", "", 0, "HeartbeatInterval", "60")
	assert.Equal(t, provisioning.SetVariableStatusAccepted, status)

	result := dm.GetVariable("OCPPCommCtrlr", "", 0, "HeartbeatInterval")
	assert.Equal(t, "60", result.Value)
}

func TestDeviceModel_GetUnknownVariable(t *testing.T) {
	dm := NewDeviceModel()

	result := dm.GetVariable("Unknown", "", 0, "Var")
	assert.Equal(t, provisioning.GetVariableStatusUnknownComponent, result.Status)
}

func TestDeviceModel_AllVariables(t *testing.T) {
	dm := NewDeviceModel()
	dm.SetVariable("ChargingStation", "", 0, "Model", "M1", MutabilityReadOnly)
	dm.SetVariable("ChargingStation", "", 0, "Vendor", "V1", MutabilityReadOnly)
	dm.SetVariable("EVSE", "", 1, "Energy.Active.Import.Register", "0", MutabilityReadOnly)

	all := dm.AllVariables()
	assert.Len(t, all, 3)
}

func TestDeviceModel_EVSEID(t *testing.T) {
	dm := NewDeviceModel()
	dm.SetVariable("EVSE", "", 1, "AvailabilityState", "Available", MutabilityReadOnly)
	dm.SetVariable("EVSE", "", 2, "AvailabilityState", "Occupied", MutabilityReadOnly)

	// Test GetVariables for EVSE 2
	req := []provisioning.GetVariableData{
		{
			Component: ocpp201types.Component{
				Name: "EVSE",
				EVSE: &ocpp201types.EVSE{ID: 2},
			},
			Variable: ocpp201types.Variable{Name: "AvailabilityState"},
		},
	}
	results := dm.BuildGetVariablesResponse(req)
	require.Len(t, results, 1)
	assert.Equal(t, provisioning.GetVariableStatusAccepted, results[0].AttributeStatus)
	assert.Equal(t, "Occupied", results[0].AttributeValue)

	// Test GetVariables for EVSE 1
	req[0].Component.EVSE.ID = 1
	results = dm.BuildGetVariablesResponse(req)
	require.Len(t, results, 1)
	assert.Equal(t, provisioning.GetVariableStatusAccepted, results[0].AttributeStatus)
	assert.Equal(t, "Available", results[0].AttributeValue)
}
