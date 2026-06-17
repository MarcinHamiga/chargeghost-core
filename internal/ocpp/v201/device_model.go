package v201

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"

	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
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
	mu         sync.RWMutex
	variables  map[componentVariableKey]variableEntry
	changes    chan struct{}
	persistDir string
}

func NewDeviceModel() *DeviceModel {
	return &DeviceModel{
		variables: make(map[componentVariableKey]variableEntry),
		changes:   make(chan struct{}, 1),
	}
}

// SetVariable sets a variable internally (used at startup and by engine).
func (dm *DeviceModel) SetVariable(component, instance string, evseID int, variable, value string, mutability MutabilityType) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	key := componentVariableKey{Component: component, Instance: instance, EVSEID: evseID, Variable: variable}
	if entry, ok := dm.variables[key]; ok && entry.Value == value && entry.Mutability == mutability {
		return
	}
	dm.variables[key] = variableEntry{Value: value, Mutability: mutability}
	dm.notifyChange()
	go dm.autoSave()
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
	if entry.Value == value {
		return provisioning.SetVariableStatusAccepted
	}

	entry.Value = value
	dm.variables[key] = entry
	dm.notifyChange()
	go dm.autoSave()
	return provisioning.SetVariableStatusAccepted
}

// ConfigChanges returns a signal channel for live device-model updates.
func (dm *DeviceModel) ConfigChanges() <-chan struct{} {
	return dm.changes
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
	dm.SetVariable("ChargingStation", "", 0, "Modem", "", MutabilityReadOnly)
	dm.SetVariable("ChargingStation", "", 0, "AvailabilityState", "Available", MutabilityReadOnly)
	dm.SetVariable("ChargingStation", "", 0, "ChargeProtocol", connectorType, MutabilityReadOnly)

	dm.SetVariable("OCPPCommCtrlr", "", 0, "HeartbeatInterval", "300", MutabilityReadWrite)
	dm.SetVariable("OCPPCommCtrlr", "", 0, "WebSocketPingInterval", "60", MutabilityReadWrite)
	dm.SetVariable("OCPPCommCtrlr", "", 0, "RetryBackOffRepeatTimes", "3", MutabilityReadWrite)
	dm.SetVariable("OCPPCommCtrlr", "", 0, "RetryBackOffRandomRange", "60", MutabilityReadWrite)
	dm.SetVariable("OCPPCommCtrlr", "", 0, "MessageTimeout", "30", MutabilityReadWrite)
	dm.SetVariable("OCPPCommCtrlr", "", 0, "NetworkProfilePriority", "1", MutabilityReadWrite)
	dm.SetVariable("OCPPCommCtrlr", "", 0, "NetworkProfileConnectionAttempts", "3", MutabilityReadWrite)

	dm.SetVariable("SampledDataCtrlr", "", 0, "TxUpdatedInterval", "30", MutabilityReadWrite)
	dm.SetVariable("SampledDataCtrlr", "", 0, "TxUpdatedMeasurands", "Energy.Active.Import.Register", MutabilityReadWrite)
	dm.SetVariable("SampledDataCtrlr", "", 0, "SignMeasurandSamples", "false", MutabilityReadWrite)

	// AuthCtrlr: enables local auth and remote start.
	dm.SetVariable("AuthCtrlr", "", 0, "Enabled", "true", MutabilityReadWrite)
	dm.SetVariable("AuthCtrlr", "", 0, "LocalAuthorizeOffline", "true", MutabilityReadWrite)
	dm.SetVariable("AuthCtrlr", "", 0, "AuthorizeRemoteStart", "true", MutabilityReadWrite)
	dm.SetVariable("AuthCtrlr", "", 0, "MasterPassGroupId", "Default", MutabilityReadWrite)
	dm.SetVariable("TxCtrlr", "", 0, "StopTxOnInvalidId", "true", MutabilityReadWrite)
	dm.SetVariable("TxCtrlr", "", 0, "StopTxOnEVSideDisconnect", "true", MutabilityReadWrite)
	dm.SetVariable("TxCtrlr", "", 0, "MaxEnergyOnInvalidId", "0", MutabilityReadWrite)
	dm.SetVariable("TxCtrlr", "", 0, "EvConnectionTimeOut", "30", MutabilityReadWrite)
	dm.SetVariable("TxCtrlr", "", 0, "TxStartPoint", "PowerPathClosed,Authorized", MutabilityReadWrite)
	dm.SetVariable("TxCtrlr", "", 0, "TxStopPoint", "PowerPathClosed,Deauthorized", MutabilityReadWrite)

	// ChargingCtrlr: schedule limits and max installed profiles.
	dm.SetVariable("ChargingCtrlr", "", 0, "Enabled", "true", MutabilityReadWrite)
	dm.SetVariable("ChargingCtrlr", "", 0, "EVChargingVoltage", "230", MutabilityReadWrite)
	dm.SetVariable("ChargingCtrlr", "", 0, "ChargingScheduleMaxPeriods", "24", MutabilityReadWrite)
	dm.SetVariable("ChargingCtrlr", "", 0, "MaxChargingProfilesInstalled", "10", MutabilityReadWrite)
	dm.SetVariable("ChargingCtrlr", "", 0, "ChargingRateUnit", "A,W", MutabilityReadWrite)

	dm.SetVariable("ACDCConverterCtrlr", "", 0, "Enabled", "false", MutabilityReadWrite)

	dm.SetVariable("ISO15118Ctrlr", "", 0, "Enabled", "false", MutabilityReadWrite)
	dm.SetVariable("ISO15118Ctrlr", "", 0, "SeamlessAuthenticationEnabled", "false", MutabilityReadWrite)

	dm.SetVariable("CustomizationCtrlr", "", 0, "Enabled", "false", MutabilityReadWrite)

	// TariffCostCtrlr surfaces the running total cost for the active
	// transaction.  The actual values are written by OnCostUpdated.
	dm.SetVariable("TariffCostCtrlr", "", 0, "Enabled", "false", MutabilityReadWrite)
	dm.SetVariable("TariffCostCtrlr", "", 0, "TotalCost", "0.0000", MutabilityReadOnly)

	// SmartChargingCtrlr exposes the charging profile limits enforced by
	// the CSMS.  Station-level "Enabled" is true so profiles sent by the
	// CSMS are honored by default.
	dm.SetVariable("SmartChargingCtrlr", "", 0, "Enabled", "true", MutabilityReadWrite)
	dm.SetVariable("SmartChargingCtrlr", "", 0, "ACPhaseSwitchingSupported", "false", MutabilityReadWrite)
	dm.SetVariable("SmartChargingCtrlr", "", 0, "ACPhaseSwitchingMaxCurrent", "32", MutabilityReadWrite)
	dm.SetVariable("SmartChargingCtrlr", "", 0, "DefaultChargeProfileMaxChargingRate", "0", MutabilityReadWrite)

	// DisplayCtrlr is required by OCPP 2.0.1 §3.20 even when the station
	// has no physical display.
	dm.SetVariable("DisplayCtrlr", "", 0, "Enabled", "false", MutabilityReadWrite)
	dm.SetVariable("DisplayCtrlr", "", 0, "DisplayMessageSupported", "true", MutabilityReadWrite)

	// ReservationCtrlr allows CSMS-driven reservations; the EVSE-level
	// EvseReservationSupported is filled in per-connector below.
	dm.SetVariable("ReservationCtrlr", "", 0, "Enabled", "true", MutabilityReadWrite)

	// DiagnosticsCtrlr: diagnostic upload is supported (see diagManager).
	dm.SetVariable("DiagnosticsCtrlr", "", 0, "Enabled", "true", MutabilityReadWrite)
	dm.SetVariable("DiagnosticsCtrlr", "", 0, "UploadLogs", "true", MutabilityReadWrite)

	// FirmwareCtrlr: firmware update is supported.
	dm.SetVariable("FirmwareCtrlr", "", 0, "Enabled", "true", MutabilityReadWrite)

	// LocalAuthListCtrlr: CSMS-driven local auth list is supported.
	dm.SetVariable("LocalAuthListCtrlr", "", 0, "Enabled", "true", MutabilityReadWrite)

	// MonitoringCtrlr exposes the variable-monitoring controller.  The
	// device model itself is the source of monitoring data for
	// GetBaseReport / GetReport.
	dm.SetVariable("MonitoringCtrlr", "", 0, "Enabled", "true", MutabilityReadWrite)
	dm.SetVariable("MonitoringCtrlr", "", 0, "MonitoringBase", "All", MutabilityReadWrite)

	// TxCtrlr.MaxEnergyOnInvalidId: enforce no limit unless CSMS sets it.
	dm.SetVariable("TxCtrlr", "", 0, "TxBeforeAcceptedEnabled", "false", MutabilityReadWrite)

	for i := 1; i <= connectors; i++ {
		// EVSE and Connector variables (EVSEID i)
		dm.SetVariable("EVSE", "", i, "AvailabilityState", "Available", MutabilityReadOnly)
		dm.SetVariable("EVSE", "", i, "PowerType", "AC", MutabilityReadOnly)
		dm.SetVariable("EVSE", "", i, "MaxCurrent", "32", MutabilityReadOnly)
		dm.SetVariable("EVSE", "", i, "Voltage", "230", MutabilityReadOnly)
		dm.SetVariable("EVSE", "", i, "SupplyPhases", "3", MutabilityReadOnly)
		dm.SetVariable("EVSE", "", i, "Energy.Active.Import.Register", "0", MutabilityReadOnly)
		// ReservationCtrlr per-EVSE flag
		dm.SetVariable("ReservationCtrlr", "", i, "EvseReservationSupported", "true", MutabilityReadOnly)
		// SmartChargingCtrlr per-EVSE
		dm.SetVariable("SmartChargingCtrlr", "", i, "Enabled", "true", MutabilityReadWrite)
		// TxCtrlr per-EVSE
		dm.SetVariable("TxCtrlr", "", i, "StopTxOnInvalidId", "true", MutabilityReadWrite)
		// Connector variables
		dm.SetVariable("Connector", "", i, "AvailabilityState", "Available", MutabilityReadOnly)
		dm.SetVariable("Connector", "", i, "ConnectorType", connectorType, MutabilityReadOnly)
		dm.SetVariable("Connector", "", i, "MaximumCurrent", "32", MutabilityReadOnly)
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

// --- ocpp.ConfigKeyAPI implementation ---
// Exposes the device model variables through the same REST API interface
// as v1.6 configuration keys.

// GetConfigKeyInfo returns all device model variables as config key entries.
func (dm *DeviceModel) GetConfigKeyInfo() []ocpppkg.ConfigKeyEntry {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	result := make([]ocpppkg.ConfigKeyEntry, 0, len(dm.variables))
	for key, entry := range dm.variables {
		label := key.Variable
		if key.Component != "" {
			label = key.Component + "." + key.Variable
		}
		if key.EVSEID > 0 {
			label = fmt.Sprintf("%s[EVSE%d]", label, key.EVSEID)
		}
		result = append(result, ocpppkg.ConfigKeyEntry{
			Key:      label,
			Value:    entry.Value,
			ReadOnly: entry.Mutability == MutabilityReadOnly,
			Type:     "string",
		})
	}
	return result
}

// SetConfigValue updates a device model variable by its flat key name.
// Returns "Accepted", "Rejected", or "NotSupported".
func (dm *DeviceModel) SetConfigValue(flatKey, value string) string {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	for key, entry := range dm.variables {
		label := key.Variable
		if key.Component != "" {
			label = key.Component + "." + key.Variable
		}
		if key.EVSEID > 0 {
			label = fmt.Sprintf("%s[EVSE%d]", label, key.EVSEID)
		}
		if label == flatKey {
			if entry.Mutability == MutabilityReadOnly {
				return "Rejected"
			}
			if entry.Value == value {
				return "Accepted"
			}
			entry.Value = value
			dm.variables[key] = entry
			dm.notifyChange()
			go dm.autoSave()
			return "Accepted"
		}
	}
	return "NotSupported"
}

// GetMeterValueSampleInterval returns the live TxUpdatedInterval setting in seconds.
func (dm *DeviceModel) GetMeterValueSampleInterval() int {
	result := dm.GetVariable("SampledDataCtrlr", "", 0, "TxUpdatedInterval")
	if result.Status == provisioning.GetVariableStatusAccepted {
		if n, err := strconv.Atoi(result.Value); err == nil {
			return n
		}
	}
	return 30
}

// GetHeartbeatInterval returns the live HeartbeatInterval setting in seconds.
func (dm *DeviceModel) GetHeartbeatInterval() int {
	result := dm.GetVariable("OCPPCommCtrlr", "", 0, "HeartbeatInterval")
	if result.Status == provisioning.GetVariableStatusAccepted {
		if n, err := strconv.Atoi(result.Value); err == nil {
			return n
		}
	}
	return 300
}

func (dm *DeviceModel) notifyChange() {
	select {
	case dm.changes <- struct{}{}:
	default:
	}
}
