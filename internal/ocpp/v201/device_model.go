package v201

import (
	"sync"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
)

type MutabilityType string

const (
	MutabilityReadOnly  MutabilityType = "ReadOnly"
	MutabilityReadWrite MutabilityType = "ReadWrite"
	MutabilityWriteOnly MutabilityType = "WriteOnly"
)

type componentVariableKey struct {
	Component string
	Instance  string
	EVSEID    int // 0 for station, 1+ for specific EVSE
	Variable  string
}

type variableEntry struct {
	Value      string
	Mutability MutabilityType
}

type GetVariableResultInternal struct {
	Value  string
	Status provisioning.GetVariableStatus
}

// DeviceVariable represents a single variable in the model for reporting.
type DeviceVariable struct {
	Component  string
	Instance   string
	EVSEID     int
	Variable   string
	Value      string
	Mutability MutabilityType
}

// DeviceModel stores OCPP 2.0.1 Component/Variable data.
type DeviceModel struct {
	mu        sync.RWMutex
	variables map[componentVariableKey]variableEntry
}

func NewDeviceModel() *DeviceModel {
	return &DeviceModel{
		variables: make(map[componentVariableKey]variableEntry),
	}
}

// SetVariable sets a variable internally (used at startup and by engine).
func (dm *DeviceModel) SetVariable(component, instance string, evseID int, variable, value string, mutability MutabilityType) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	key := componentVariableKey{Component: component, Instance: instance, EVSEID: evseID, Variable: variable}
	dm.variables[key] = variableEntry{Value: value, Mutability: mutability}
}

// SetVariableExternal handles CSMS SetVariables requests. Rejects read-only.
func (dm *DeviceModel) SetVariableExternal(component, instance string, evseID int, variable, value string) provisioning.SetVariableStatus {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	key := componentVariableKey{Component: component, Instance: instance, EVSEID: evseID, Variable: variable}

	entry, ok := dm.variables[key]
	if !ok {
		return provisioning.SetVariableStatusUnknownComponent
	}
	if entry.Mutability == MutabilityReadOnly {
		return provisioning.SetVariableStatusRejected
	}

	entry.Value = value
	dm.variables[key] = entry
	return provisioning.SetVariableStatusAccepted
}

// GetVariable returns a variable value or an error status.
func (dm *DeviceModel) GetVariable(component, instance string, evseID int, variable string) GetVariableResultInternal {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	key := componentVariableKey{Component: component, Instance: instance, EVSEID: evseID, Variable: variable}

	entry, ok := dm.variables[key]
	if !ok {
		return GetVariableResultInternal{Status: provisioning.GetVariableStatusUnknownComponent}
	}
	return GetVariableResultInternal{Value: entry.Value, Status: provisioning.GetVariableStatusAccepted}
}

// AllVariables returns all stored variables for reporting.
func (dm *DeviceModel) AllVariables() []DeviceVariable {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	var result []DeviceVariable
	for key, entry := range dm.variables {
		result = append(result, DeviceVariable{
			Component:  key.Component,
			Instance:   key.Instance,
			EVSEID:     key.EVSEID,
			Variable:   key.Variable,
			Value:      entry.Value,
			Mutability: entry.Mutability,
		})
	}
	return result
}

// PopulateDefaults fills the device model with the minimal variable set.
func (dm *DeviceModel) PopulateDefaults(model, vendor, serialNumber, firmwareVersion, connectorType string, connectors int) {
	// Station-level variables (EVSEID 0)
	dm.SetVariable("ChargingStation", "", 0, "Model", model, MutabilityReadOnly)
	dm.SetVariable("ChargingStation", "", 0, "VendorName", vendor, MutabilityReadOnly)
	dm.SetVariable("ChargingStation", "", 0, "FirmwareVersion", firmwareVersion, MutabilityReadOnly)
	dm.SetVariable("ChargingStation", "", 0, "SerialNumber", serialNumber, MutabilityReadOnly)
	dm.SetVariable("ChargingStation", "", 0, "AvailabilityState", "Available", MutabilityReadOnly)

	dm.SetVariable("OCPPCommCtrlr", "", 0, "HeartbeatInterval", "300", MutabilityReadWrite)
	dm.SetVariable("OCPPCommCtrlr", "", 0, "WebSocketPingInterval", "30", MutabilityReadWrite)
	dm.SetVariable("OCPPCommCtrlr", "", 0, "RetryBackOffRepeatTimes", "3", MutabilityReadWrite)

	dm.SetVariable("SampledDataCtrlr", "", 0, "TxUpdatedInterval", "30", MutabilityReadWrite)
	dm.SetVariable("SampledDataCtrlr", "", 0, "TxUpdatedMeasurands", "Energy.Active.Import.Register", MutabilityReadWrite)

	dm.SetVariable("AuthCtrlr", "", 0, "Enabled", "true", MutabilityReadWrite)
	dm.SetVariable("AuthCtrlr", "", 0, "LocalAuthorizeOffline", "true", MutabilityReadWrite)
	dm.SetVariable("AuthCtrlr", "", 0, "AuthorizeRemoteStart", "true", MutabilityReadWrite)

	dm.SetVariable("TxCtrlr", "", 0, "StopTxOnInvalidId", "true", MutabilityReadWrite)
	dm.SetVariable("TxCtrlr", "", 0, "StopTxOnEVSideDisconnect", "true", MutabilityReadWrite)

	for i := 1; i <= connectors; i++ {
		// EVSE and Connector variables (EVSEID i)
		dm.SetVariable("EVSE", "", i, "AvailabilityState", "Available", MutabilityReadOnly)
		dm.SetVariable("EVSE", "", i, "Energy.Active.Import.Register", "0", MutabilityReadOnly)
		dm.SetVariable("Connector", "", i, "AvailabilityState", "Available", MutabilityReadOnly)
		dm.SetVariable("Connector", "", i, "ConnectorType", connectorType, MutabilityReadOnly)
	}
}

func getEVSEID(comp ocpp201types.Component) int {
	if comp.EVSE != nil {
		return comp.EVSE.ID
	}
	return 0
}

// BuildGetVariablesResponse processes a GetVariablesRequest.
func (dm *DeviceModel) BuildGetVariablesResponse(data []provisioning.GetVariableData) []provisioning.GetVariableResult {
	results := make([]provisioning.GetVariableResult, len(data))
	for i, d := range data {
		evseID := getEVSEID(d.Component)
		r := dm.GetVariable(d.Component.Name, d.Component.Instance, evseID, d.Variable.Name)
		results[i] = provisioning.GetVariableResult{
			AttributeStatus: r.Status,
			AttributeValue:  r.Value,
			Component:       d.Component,
			Variable:        d.Variable,
		}
	}
	return results
}

// BuildSetVariablesResponse processes a SetVariablesRequest.
func (dm *DeviceModel) BuildSetVariablesResponse(data []provisioning.SetVariableData) []provisioning.SetVariableResult {
	results := make([]provisioning.SetVariableResult, len(data))
	for i, d := range data {
		evseID := getEVSEID(d.Component)
		status := dm.SetVariableExternal(d.Component.Name, d.Component.Instance, evseID, d.Variable.Name, d.AttributeValue)
		results[i] = provisioning.SetVariableResult{
			AttributeStatus: status,
			Component:       d.Component,
			Variable:        d.Variable,
		}
	}
	return results
}

// BuildNotifyReportData builds ReportData for all variables (for GetBaseReport).
func (dm *DeviceModel) BuildNotifyReportData() []provisioning.ReportData {
	all := dm.AllVariables()
	data := make([]provisioning.ReportData, len(all))
	for i, v := range all {
		mut := provisioning.MutabilityReadOnly
		if v.Mutability == MutabilityReadWrite {
			mut = provisioning.MutabilityReadWrite
		} else if v.Mutability == MutabilityWriteOnly {
			mut = provisioning.MutabilityWriteOnly
		}
		comp := ocpp201types.Component{Name: v.Component, Instance: v.Instance}
		if v.EVSEID > 0 {
			comp.EVSE = &ocpp201types.EVSE{ID: v.EVSEID}
		}

		data[i] = provisioning.ReportData{
			Component: comp,
			Variable:  ocpp201types.Variable{Name: v.Variable},
			VariableAttribute: []provisioning.VariableAttribute{
				{
					Value:      v.Value,
					Mutability: mut,
				},
			},
		}
	}
	return data
}
