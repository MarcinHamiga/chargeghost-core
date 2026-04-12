package ocpp_test

import (
	"sync"
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
	var statuses []string
	var mu sync.Mutex
	fm := ocpp.NewFirmwareManager(func(status string) {
		mu.Lock()
		statuses = append(statuses, status)
		mu.Unlock()
	})

	// Retrieve date is now (immediate start).
	require.NoError(t, fm.TriggerUpdate("http://example.com/fw.bin", time.Now()))

	// Wait for Downloading transition (immediate) + Downloaded (3s) + Installing (1s) + Installed (2s) = ~6s
	time.Sleep(7 * time.Second)

	mu.Lock()
	defer mu.Unlock()
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
	var statuses []string
	var mu sync.Mutex
	dm := ocpp.NewDiagnosticsManager(func(status string) {
		mu.Lock()
		statuses = append(statuses, status)
		mu.Unlock()
	})

	require.NoError(t, dm.TriggerUpload("http://example.com/diag.tgz", 0, 0))
	time.Sleep(3 * time.Second) // Uploading (0s) → Uploaded (2s)

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, statuses, "Uploading")
	assert.Contains(t, statuses, "Uploaded")
}

func TestDiagnosticsManager_FailsWithoutRetryWhenFailuresRequested(t *testing.T) {
	var statuses []string
	var mu sync.Mutex
	dm := ocpp.NewDiagnosticsManager(func(status string) {
		mu.Lock()
		statuses = append(statuses, status)
		mu.Unlock()
	})

	require.NoError(t, dm.TriggerUpload("http://example.com/diag.tgz?chargeghost_failures=1", 0, 0))
	time.Sleep(3 * time.Second)

	mu.Lock()
	statusCopy := append([]string(nil), statuses...)
	mu.Unlock()
	assert.Equal(t, []string{"Uploading", "UploadFailed"}, statusCopy)
	assert.Equal(t, "UploadFailed", dm.GetStatus().Status)
}

func TestDiagnosticsManager_RetryThenSucceeds(t *testing.T) {
	dm := ocpp.NewDiagnosticsManager(nil)

	require.NoError(t, dm.TriggerUpload("http://example.com/diag.tgz?chargeghost_failures=1", 1, 0))
	time.Sleep(5 * time.Second)

	assert.Equal(t, "Uploaded", dm.GetStatus().Status)
}

func TestDiagnosticsManager_InvalidFailureHintRejected(t *testing.T) {
	dm := ocpp.NewDiagnosticsManager(nil)
	err := dm.TriggerUpload("http://example.com/diag.tgz?chargeghost_failures=abc", 0, 0)
	assert.EqualError(t, err, "chargeghost_failures must be a non-negative integer")
}

func TestFirmwareManager_DoubleTriggerRejected(t *testing.T) {
	m := ocpp.NewFirmwareManager(nil)
	_ = m.TriggerUpdate("http://x.com/fw.bin", time.Now().Add(10*time.Second))
	err := m.TriggerUpdate("http://x.com/fw.bin", time.Now().Add(10*time.Second))
	assert.Error(t, err, "second TriggerUpdate should fail while one is in progress")
	// Clean up
	_ = m.CancelUpdate()
}

func TestFirmwareManager_CancelNotPossibleWhenIdle(t *testing.T) {
	m := ocpp.NewFirmwareManager(nil)
	err := m.CancelUpdate()
	assert.Error(t, err, "CancelUpdate should fail when no update is in progress")
}

func TestFirmwareManager_CancelBeforeFirstTransition_NoSpuriousCallback(t *testing.T) {
	var statuses []string
	var mu sync.Mutex
	m := ocpp.NewFirmwareManager(func(s string) {
		mu.Lock()
		statuses = append(statuses, s)
		mu.Unlock()
	})

	// Trigger with retrieve date far in the future so it parks at the wait.
	retrieveDate := time.Now().Add(10 * time.Second)
	_ = m.TriggerUpdate("http://example.com/fw.bin", retrieveDate)

	// Immediately cancel.
	err := m.CancelUpdate()
	assert.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	count := len(statuses)
	statusCopy := append([]string(nil), statuses...)
	mu.Unlock()
	assert.Equal(t, 1, count, "cancellation should emit a visible Idle transition")
	assert.Equal(t, []string{"Idle"}, statusCopy)
	assert.Equal(t, "Idle", m.GetStatus().Status)
}
