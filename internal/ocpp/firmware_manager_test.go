package ocpp_test

import (
	"testing"
	"time"

	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFirmwareManager_IdleByDefault(t *testing.T) {
	fm := ocpp.NewFirmwareManager(nil)
	assert.Equal(t, "Idle", fm.GetStatus().Status)
}

func TestFirmwareManager_TransitionsToDownloading(t *testing.T) {
	statuses := make([]string, 0)
	fm := ocpp.NewFirmwareManager(func(status string) {
		statuses = append(statuses, status)
	})

	// Retrieve date is now (immediate start).
	require.NoError(t, fm.TriggerUpdate("http://example.com/fw.bin", time.Now()))

	// Wait for Downloading transition (immediate) + Downloaded (3s) + Installing (1s) + Installed (2s) = ~6s
	time.Sleep(7 * time.Second)

	assert.Contains(t, statuses, "Downloading")
	assert.Contains(t, statuses, "Downloaded")
	assert.Contains(t, statuses, "Installing")
	assert.Contains(t, statuses, "Installed")
	assert.Equal(t, "Installed", fm.GetStatus().Status)
}

func TestFirmwareManager_CancelMidUpdate(t *testing.T) {
	fm := ocpp.NewFirmwareManager(nil)
	require.NoError(t, fm.TriggerUpdate("http://example.com/fw.bin", time.Now()))
	time.Sleep(100 * time.Millisecond) // allow Downloading to start
	require.NoError(t, fm.CancelUpdate())
	assert.Equal(t, "Idle", fm.GetStatus().Status)
}

func TestFirmwareManager_CancelWhenIdle(t *testing.T) {
	fm := ocpp.NewFirmwareManager(nil)
	err := fm.CancelUpdate()
	assert.Error(t, err, "cancel when idle should return error")
}

func TestDiagnosticsManager_Transitions(t *testing.T) {
	statuses := make([]string, 0)
	dm := ocpp.NewDiagnosticsManager(func(status string) {
		statuses = append(statuses, status)
	})

	require.NoError(t, dm.TriggerUpload("http://example.com/diag.tgz", 0, 0))
	time.Sleep(3 * time.Second) // Uploading (0s) → Uploaded (2s)

	assert.Contains(t, statuses, "Uploading")
	assert.Contains(t, statuses, "Uploaded")
}
