package ocpp

import (
	"context"
	"errors"
	"sync"
	"time"
)

// RealFirmwareManager simulates firmware update with exact timing from the guide:
//
//	Idle → Downloading (0s after retrieve date) → Downloaded (3s) → Installing (1s) → Installed (2s)
type RealFirmwareManager struct {
	mu         sync.Mutex
	status     FirmwareStatus
	cancelFunc context.CancelFunc
	onStatus   func(status string)
}

// NewFirmwareManager creates a manager. onStatus is called on every status change
// (for WebSocket broadcast + OCPP FirmwareStatusNotification).
func NewFirmwareManager(onStatus func(status string)) *RealFirmwareManager {
	return &RealFirmwareManager{
		status:   FirmwareStatus{Status: "Idle"},
		onStatus: onStatus,
	}
}

func (m *RealFirmwareManager) GetStatus() FirmwareStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *RealFirmwareManager) TriggerUpdate(location string, retrieveDate time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.Status != "Idle" {
		return errors.New("firmware update already in progress")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.status = FirmwareStatus{Status: "Idle", Location: &location, RetrieveDate: &retrieveDate}
	go m.runUpdate(ctx, location, retrieveDate)
	return nil
}

func (m *RealFirmwareManager) CancelUpdate() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.Status == "Idle" {
		return errors.New("no firmware update in progress")
	}
	if m.cancelFunc != nil {
		m.cancelFunc()
		m.cancelFunc = nil
	}
	m.status = FirmwareStatus{Status: "Idle"}
	return nil
}

func (m *RealFirmwareManager) runUpdate(ctx context.Context, location string, retrieveDate time.Time) {
	// Wait until retrieve date.
	waitDur := time.Until(retrieveDate)
	if waitDur > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(waitDur):
		}
	}

	transitions := []struct {
		status string
		delay  time.Duration
	}{
		{"Downloading", 0},
		{"Downloaded", 3 * time.Second},
		{"Installing", 1 * time.Second},
		{"Installed", 2 * time.Second},
	}

	for _, t := range transitions {
		if t.delay > 0 {
			select {
			case <-ctx.Done():
				m.mu.Lock()
				m.status = FirmwareStatus{Status: "Idle"}
				m.mu.Unlock()
				return
			case <-time.After(t.delay):
			}
		}
		m.mu.Lock()
		m.status.Status = t.status
		m.mu.Unlock()
		if m.onStatus != nil {
			m.onStatus(t.status)
		}
	}

	// Final: clear cancel func.
	m.mu.Lock()
	m.cancelFunc = nil
	m.mu.Unlock()
}

// RealDiagnosticsManager simulates diagnostics upload:
//
//	Idle → Uploading (0s) → Uploaded (2s)
type RealDiagnosticsManager struct {
	mu         sync.Mutex
	status     DiagnosticsStatus
	cancelFunc context.CancelFunc
	onStatus   func(status string)
}

func NewDiagnosticsManager(onStatus func(status string)) *RealDiagnosticsManager {
	return &RealDiagnosticsManager{
		status:   DiagnosticsStatus{Status: "Idle"},
		onStatus: onStatus,
	}
}

func (m *RealDiagnosticsManager) GetStatus() DiagnosticsStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *RealDiagnosticsManager) TriggerUpload(location string, retries, retryInterval int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.Status != "Idle" {
		return errors.New("diagnostics upload already in progress")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.status = DiagnosticsStatus{Status: "Idle", Location: &location}
	go m.runUpload(ctx)
	return nil
}

func (m *RealDiagnosticsManager) CancelUpload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.Status == "Idle" {
		return errors.New("no diagnostics upload in progress")
	}
	if m.cancelFunc != nil {
		m.cancelFunc()
		m.cancelFunc = nil
	}
	m.status = DiagnosticsStatus{Status: "Idle"}
	return nil
}

func (m *RealDiagnosticsManager) runUpload(ctx context.Context) {
	// Uploading: immediate
	m.mu.Lock()
	m.status.Status = "Uploading"
	m.mu.Unlock()
	if m.onStatus != nil {
		m.onStatus("Uploading")
	}

	// Uploaded: after 2s
	select {
	case <-ctx.Done():
		m.mu.Lock()
		m.status = DiagnosticsStatus{Status: "Idle"}
		m.mu.Unlock()
		return
	case <-time.After(2 * time.Second):
	}

	m.mu.Lock()
	m.status.Status = "Uploaded"
	m.cancelFunc = nil
	m.mu.Unlock()
	if m.onStatus != nil {
		m.onStatus("Uploaded")
	}
}
