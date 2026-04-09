package v201

import (
	"testing"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/firmware"
	"github.com/stretchr/testify/assert"
)

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

func TestSendDataTransfer_Disconnected(t *testing.T) {
	b := newTestBridge(t)
	// No connection — should return an error, not panic
	_, _, err := b.SendDataTransfer("acme", "ping", "hello")
	assert.Error(t, err)
}
