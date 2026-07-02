package v201

import (
	"testing"
	"time"

	ocpp201 "github.com/lorenzodonini/ocpp-go/ocpp2.0.1"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/diagnostics"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/firmware"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type diagnosticsNotificationCS struct {
	ocpp201.ChargingStation
	status    diagnostics.UploadLogStatus
	requestID int
	called    bool
}

func (cs *diagnosticsNotificationCS) LogStatusNotification(status diagnostics.UploadLogStatus, requestID int, props ...func(request *diagnostics.LogStatusNotificationRequest)) (*diagnostics.LogStatusNotificationResponse, error) {
	cs.called = true
	cs.status = status
	cs.requestID = requestID
	return diagnostics.NewLogStatusNotificationResponse(), nil
}

// bootCaptureCS captures the BootNotificationRequest received from the
// bridge so the test can assert on the chargingStation field.
type bootCaptureCS struct {
	ocpp201.ChargingStation
	capturedReq *provisioning.BootNotificationRequest
}

func (cs *bootCaptureCS) BootNotification(reason provisioning.BootReason, model string, chargePointVendor string, props ...func(request *provisioning.BootNotificationRequest)) (*provisioning.BootNotificationResponse, error) {
	req := provisioning.NewBootNotificationRequest(reason, model, chargePointVendor)
	for _, p := range props {
		p(req)
	}
	cs.capturedReq = req
	return provisioning.NewBootNotificationResponse(ocpp201types.NewDateTime(time.Now()), 300, provisioning.RegistrationStatusAccepted), nil
}

func TestMapFirmwareStatus(t *testing.T) {
	tests := []struct {
		input    string
		expected firmware.FirmwareStatus
	}{
		{"Idle", firmware.FirmwareStatusIdle},
		{"Downloading", firmware.FirmwareStatusDownloading},
		{"Downloaded", firmware.FirmwareStatusDownloaded},
		{"Installing", firmware.FirmwareStatusInstalling},
		{"Installed", firmware.FirmwareStatusInstalled},
		{"InstallationFailed", firmware.FirmwareStatusInstallationFailed},
		{"unknown", firmware.FirmwareStatusIdle},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, mapFirmwareStatus(tt.input))
		})
	}
}

func TestMapDiagnosticsStatus(t *testing.T) {
	tests := []struct {
		input    string
		expected diagnostics.UploadLogStatus
	}{
		{"Idle", diagnostics.UploadLogStatusIdle},
		{"Uploading", diagnostics.UploadLogStatusUploading},
		{"Uploaded", diagnostics.UploadLogStatusUploaded},
		{"UploadFailed", diagnostics.UploadLogStatusUploadFailure},
		{"unknown", diagnostics.UploadLogStatusIdle},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, mapDiagnosticsStatus(tt.input))
		})
	}
}

func TestSendDiagnosticsStatusNotification_UsesLogStatusNotification(t *testing.T) {
	b := newTestBridge(t)
	fakeCS := &diagnosticsNotificationCS{}
	b.cs = fakeCS
	b.diagRequestID.Store(42)

	err := b.SendDiagnosticsStatusNotification("Uploaded")
	require.NoError(t, err)
	assert.True(t, fakeCS.called)
	assert.Equal(t, diagnostics.UploadLogStatusUploaded, fakeCS.status)
	assert.Equal(t, 42, fakeCS.requestID)
}

func TestSendBootNotification_IncludesChargingStationFields(t *testing.T) {
	b := newTestBridge(t)
	b.cfg.ChargePointSerial = "SN-12345"
	b.cfg.FirmwareVersion = "1.2.3"
	b.cfg.ModemICCID = "8910101010101010101F"
	b.cfg.ModemIMSI = "310150123456789"
	b.cs = &bootCaptureCS{}

	require.NoError(t, b.SendBootNotification())

	captured := b.cs.(*bootCaptureCS).capturedReq
	require.NotNil(t, captured)
	assert.Equal(t, "ChargeGhostV1", captured.ChargingStation.Model)
	assert.Equal(t, "ChargeGhost", captured.ChargingStation.VendorName)
	assert.Equal(t, "SN-12345", captured.ChargingStation.SerialNumber)
	assert.Equal(t, "1.2.3", captured.ChargingStation.FirmwareVersion)
	require.NotNil(t, captured.ChargingStation.Modem)
	assert.Equal(t, "8910101010101010101F", captured.ChargingStation.Modem.Iccid)
	assert.Equal(t, "310150123456789", captured.ChargingStation.Modem.Imsi)
}

func TestSendBootNotification_OmitsModemWhenUnset(t *testing.T) {
	b := newTestBridge(t)
	b.cfg.ChargePointSerial = "SN-67890"
	b.cfg.FirmwareVersion = "2.0.0"
	b.cs = &bootCaptureCS{}

	require.NoError(t, b.SendBootNotification())

	captured := b.cs.(*bootCaptureCS).capturedReq
	require.NotNil(t, captured)
	assert.Equal(t, "SN-67890", captured.ChargingStation.SerialNumber)
	assert.Equal(t, "2.0.0", captured.ChargingStation.FirmwareVersion)
	assert.Nil(t, captured.ChargingStation.Modem)
}

func TestParseMeasurandList(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		expected []ocpp201types.Measurand
	}{
		{
			name:     "empty falls back to Energy",
			raw:      "",
			expected: []ocpp201types.Measurand{ocpp201types.MeasurandEnergyActiveImportRegister},
		},
		{
			name:     "single measurand",
			raw:      "Energy.Active.Import.Register",
			expected: []ocpp201types.Measurand{ocpp201types.MeasurandEnergyActiveImportRegister},
		},
		{
			name: "multiple measurands with whitespace",
			raw:  "Energy.Active.Import.Register, Voltage,Current.Import",
			expected: []ocpp201types.Measurand{
				ocpp201types.MeasurandEnergyActiveImportRegister,
				ocpp201types.MeasurandVoltage,
				ocpp201types.MeasurandCurrentImport,
			},
		},
		{
			name: "trailing comma is tolerated",
			raw:  "Energy.Active.Import.Register,Voltage,",
			expected: []ocpp201types.Measurand{
				ocpp201types.MeasurandEnergyActiveImportRegister,
				ocpp201types.MeasurandVoltage,
			},
		},
		{
			name: "unknown measurand is preserved (CSMS will reject)",
			raw:  "Energy.Active.Import.Register,Unknown.Measurand",
			expected: []ocpp201types.Measurand{
				ocpp201types.MeasurandEnergyActiveImportRegister,
				ocpp201types.Measurand("Unknown.Measurand"),
			},
		},
		{
			name:     "all whitespace falls back to Energy",
			raw:      "  ,  ,",
			expected: []ocpp201types.Measurand{ocpp201types.MeasurandEnergyActiveImportRegister},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseMeasurandList(tt.raw))
		})
	}
}

func TestMakeMeterValueForMeasurands(t *testing.T) {
	ts := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	voltage, phases := 230.0, 1
	// offeredCurrent (rated) and actualCurrent (profile-limited) are
	// deliberately different so the test can verify Current.Offered and
	// Current.Import render distinct values instead of both echoing the
	// connector's rated current.
	offeredCurrent, actualCurrent := 32.0, 16.0
	energy := 12345.67

	// All six measurands
	mv := makeMeterValueForMeasurands(energy, voltage, offeredCurrent, actualCurrent, phases, ts,
		string(ocpp201types.ReadingContextSamplePeriodic),
		[]ocpp201types.Measurand{
			ocpp201types.MeasurandEnergyActiveImportRegister,
			ocpp201types.MeasurandVoltage,
			ocpp201types.MeasurandCurrentImport,
			ocpp201types.MeasurandCurrentOffered,
			ocpp201types.MeasurandPowerActiveImport,
			ocpp201types.MeasurandPowerOffered,
		})
	assert.Equal(t, ts, mv.Timestamp.Time)
	require.Len(t, mv.SampledValue, 6)
	assert.Equal(t, ocpp201types.MeasurandEnergyActiveImportRegister, mv.SampledValue[0].Measurand)
	assert.Equal(t, energy, mv.SampledValue[0].Value)
	assert.Equal(t, "Wh", mv.SampledValue[0].UnitOfMeasure.Unit)
	assert.Equal(t, ocpp201types.MeasurandVoltage, mv.SampledValue[1].Measurand)
	assert.Equal(t, voltage, mv.SampledValue[1].Value)
	assert.Equal(t, "V", mv.SampledValue[1].UnitOfMeasure.Unit)
	assert.Equal(t, ocpp201types.MeasurandCurrentImport, mv.SampledValue[2].Measurand)
	assert.Equal(t, actualCurrent, mv.SampledValue[2].Value, "Current.Import must reflect the profile-limited current, not the rated current")
	assert.Equal(t, ocpp201types.MeasurandCurrentOffered, mv.SampledValue[3].Measurand)
	assert.Equal(t, offeredCurrent, mv.SampledValue[3].Value, "Current.Offered must reflect the connector's rated current regardless of any active limit")
	assert.NotEqual(t, mv.SampledValue[2].Value, mv.SampledValue[3].Value)
	assert.Equal(t, ocpp201types.MeasurandPowerActiveImport, mv.SampledValue[4].Measurand)
	assert.InDelta(t, voltage*actualCurrent*float64(phases), mv.SampledValue[4].Value, 0.001)
	assert.Equal(t, "W", mv.SampledValue[4].UnitOfMeasure.Unit)
	assert.Equal(t, ocpp201types.MeasurandPowerOffered, mv.SampledValue[5].Measurand)
	assert.InDelta(t, voltage*offeredCurrent*float64(phases), mv.SampledValue[5].Value, 0.001)
	for _, sv := range mv.SampledValue {
		assert.Equal(t, ocpp201types.LocationOutlet, sv.Location)
		assert.Equal(t, ocpp201types.ReadingContextSamplePeriodic, sv.Context)
	}

	// Empty measurand list produces an empty SampledValue slice (still a valid MeterValue)
	mv2 := makeMeterValueForMeasurands(energy, voltage, offeredCurrent, actualCurrent, phases, ts,
		string(ocpp201types.ReadingContextSamplePeriodic), nil)
	assert.Empty(t, mv2.SampledValue)
	assert.Equal(t, ts, mv2.Timestamp.Time)

	// Unknown measurand is omitted
	mv3 := makeMeterValueForMeasurands(energy, voltage, offeredCurrent, actualCurrent, phases, ts,
		string(ocpp201types.ReadingContextSamplePeriodic),
		[]ocpp201types.Measurand{ocpp201types.Measurand("SoC")})
	assert.Empty(t, mv3.SampledValue)
}

func TestSendDataTransfer_Disconnected(t *testing.T) {
	b := newTestBridge(t)
	// No connection — should return an error, not panic
	_, _, err := b.SendDataTransfer("acme", "ping", "hello")
	assert.Error(t, err)
}

func TestMapStopReason(t *testing.T) {
	cases := map[string]transactions.Reason{
		"EVDisconnected": transactions.ReasonEVDisconnected,
		"Remote":         transactions.ReasonRemote,
		"Local":          transactions.ReasonLocal,
		"user_requested": transactions.ReasonLocal,
		"PowerLoss":      transactions.ReasonPowerLoss,
		"Reboot":         transactions.ReasonReboot,
		"EmergencyStop":  transactions.ReasonEmergencyStop,
		"HardReset":      transactions.ReasonImmediateReset,
		"SoftReset":      transactions.ReasonReboot,
		"DeAuthorized":   transactions.ReasonDeAuthorized,
		"UnlockCommand":  transactions.ReasonOther,
		"Faulted":        transactions.ReasonOther,
		"SomethingElse":  transactions.ReasonOther,
	}
	for engineReason, want := range cases {
		assert.Equal(t, want, mapStopReason(engineReason), "engine reason %q", engineReason)
	}
}

func TestMapTriggerReasonForStop(t *testing.T) {
	cases := map[string]transactions.TriggerReason{
		"Remote":         transactions.TriggerReasonRemoteStop,
		"DeAuthorized":   transactions.TriggerReasonDeAuthorized,
		"HardReset":      transactions.TriggerReasonResetCommand,
		"SoftReset":      transactions.TriggerReasonResetCommand,
		"UnlockCommand":  transactions.TriggerReasonUnlockCommand,
		"EVDisconnected": transactions.TriggerReasonEVDeparted,
		"Faulted":        transactions.TriggerReasonAbnormalCondition,
		"Local":          transactions.TriggerReasonStopAuthorized,
		"user_requested": transactions.TriggerReasonStopAuthorized,
		"SomethingElse":  transactions.TriggerReasonStopAuthorized,
	}
	for engineReason, want := range cases {
		assert.Equal(t, want, mapTriggerReasonForStop(engineReason), "engine reason %q", engineReason)
	}
}
