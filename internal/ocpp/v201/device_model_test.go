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

func TestDeviceModel_PopulateDefaults_AllRequiredComponents(t *testing.T) {
	dm := NewDeviceModel()
	dm.PopulateDefaults("M", "V", "SN", "1.0", "cType2", 1)

	required := []struct {
		component, variable string
		evseID              int
	}{
		{"ChargingStation", "Model", 0},
		{"ChargingStation", "VendorName", 0},
		{"ChargingStation", "FirmwareVersion", 0},
		{"ChargingStation", "SerialNumber", 0},
		{"OCPPCommCtrlr", "HeartbeatInterval", 0},
		{"OCPPCommCtrlr", "MessageTimeout", 0},
		{"SampledDataCtrlr", "TxUpdatedInterval", 0},
		{"SampledDataCtrlr", "TxUpdatedMeasurands", 0},
		{"AuthCtrlr", "Enabled", 0},
		{"TxCtrlr", "StopTxOnInvalidId", 0},
		{"TxCtrlr", "StopTxOnEVSideDisconnect", 0},
		{"ChargingCtrlr", "Enabled", 0},
		{"ACDCConverterCtrlr", "Enabled", 0},
		{"ISO15118Ctrlr", "Enabled", 0},
		{"EVSE", "PowerType", 1},
		{"EVSE", "MaxCurrent", 1},
		{"Connector", "ConnectorType", 1},
	}

	for _, r := range required {
		res := dm.GetVariable(r.component, "", r.evseID, r.variable)
		assert.Equal(t, provisioning.GetVariableStatusAccepted, res.Status, "%s.%s (EVSE %d) missing", r.component, r.variable, r.evseID)
	}
}

func TestDeviceModel_PopulateDefaults_ExpandedOptionalVariables(t *testing.T) {
	dm := NewDeviceModel()
	dm.PopulateDefaults("M", "V", "SN", "1.0", "cType2", 1)

	// Expanded optional/commonly-used variables per Phase 4.1.
	optional := []struct {
		component, instance, variable, expectedValue string
		evseID                                       int
	}{
		{"ChargingStation", "", "Modem", "", 0},
		{"OCPPCommCtrlr", "", "WebSocketPingInterval", "60", 0},
		{"OCPPCommCtrlr", "", "NetworkProfileConnectionAttempts", "3", 0},
		{"AuthCtrlr", "", "MasterPassGroupId", "Default", 0},
		{"TxCtrlr", "", "TxStartPoint", "PowerPathClosed,Authorized", 0},
		{"TxCtrlr", "", "TxStopPoint", "PowerPathClosed,Deauthorized", 0},
		{"TxCtrlr", "", "EvConnectionTimeOut", "30", 0},
		{"ChargingCtrlr", "", "ChargingScheduleMaxPeriods", "24", 0},
		{"SmartChargingCtrlr", "", "ACPhaseSwitchingSupported", "false", 0},
		{"Connector", "", "ConnectorType", "cType2", 1},
	}

	for _, o := range optional {
		res := dm.GetVariable(o.component, o.instance, o.evseID, o.variable)
		assert.Equal(t, provisioning.GetVariableStatusAccepted, res.Status, "%s/%s.%s (EVSE %d) missing", o.component, o.instance, o.variable, o.evseID)
		if o.expectedValue != "" {
			assert.Equal(t, o.expectedValue, res.Value, "%s.%s value mismatch", o.component, o.variable)
		}
	}
}
