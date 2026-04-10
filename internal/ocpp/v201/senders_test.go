package v201

import (
	"testing"

	ocpp201 "github.com/lorenzodonini/ocpp-go/ocpp2.0.1"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/diagnostics"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/firmware"
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

func TestSendDataTransfer_Disconnected(t *testing.T) {
	b := newTestBridge(t)
	// No connection — should return an error, not panic
	_, _, err := b.SendDataTransfer("acme", "ping", "hello")
	assert.Error(t, err)
}
