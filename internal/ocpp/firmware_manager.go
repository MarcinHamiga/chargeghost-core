package ocpp

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"sync"
	"time"
)

var diagnosticsAttemptDuration = 2 * time.Second

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

func validateLocation(raw string) error {
	if raw == "" {
		return errors.New("location is required")
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("location must be an absolute URI")
	}
	return nil
}

func (m *RealFirmwareManager) notifyStatus(status string) {
	if m.onStatus != nil {
		m.onStatus(status)
	}
}

func (m *RealFirmwareManager) GetStatus() FirmwareStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *RealFirmwareManager) TriggerUpdate(location string, retrieveDate time.Time) error {
	if err := validateLocation(location); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancelFunc != nil {
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
	if m.cancelFunc == nil {
		return errors.New("no firmware update in progress")
	}
	m.cancelFunc()
	m.cancelFunc = nil
	m.status = FirmwareStatus{Status: "Idle"}
	m.notifyStatus("Idle")
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
				m.cancelFunc = nil
				m.mu.Unlock()
				return
			case <-time.After(t.delay):
			}
		}
		// Check cancellation even for zero-delay transitions.
		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.status = FirmwareStatus{Status: "Idle"}
			m.cancelFunc = nil
			m.mu.Unlock()
			return
		default:
		}
		m.mu.Lock()
		m.status.Status = t.status
		m.mu.Unlock()
		// Check again before callback to avoid broadcasting stale status after cancel.
		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.status = FirmwareStatus{Status: "Idle"}
			m.cancelFunc = nil
			m.mu.Unlock()
			return
		default:
		}
		m.notifyStatus(t.status)
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

type diagnosticsUploadPlan struct {
	failures int
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
	if err := validateLocation(location); err != nil {
		return err
	}
	plan, err := parseDiagnosticsUploadPlan(location)
	if err != nil {
		return err
	}
	if retries < 0 {
		return errors.New("retries must be non-negative")
	}
	if retryInterval < 0 {
		return errors.New("retry_interval must be non-negative")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancelFunc != nil {
		return errors.New("diagnostics upload already in progress")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelFunc = cancel
	m.status = DiagnosticsStatus{Status: "Idle", Location: &location}
	go m.runUpload(ctx, plan, retries, retryInterval)
	return nil
}

func (m *RealDiagnosticsManager) CancelUpload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancelFunc == nil {
		return errors.New("no diagnostics upload in progress")
	}
	m.cancelFunc()
	m.cancelFunc = nil
	m.status = DiagnosticsStatus{Status: "Idle"}
	if m.onStatus != nil {
		m.onStatus("Idle")
	}
	return nil
}

func parseDiagnosticsUploadPlan(location string) (diagnosticsUploadPlan, error) {
	parsed, err := url.Parse(location)
	if err != nil {
		return diagnosticsUploadPlan{}, err
	}
	plan := diagnosticsUploadPlan{}
	if raw := parsed.Query().Get("chargeghost_failures"); raw != "" {
		failures, err := strconv.Atoi(raw)
		if err != nil || failures < 0 {
			return diagnosticsUploadPlan{}, errors.New("chargeghost_failures must be a non-negative integer")
		}
		plan.failures = failures
	}
	return plan, nil
}

func waitForDiagnosticsStep(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(delay):
		return true
	}
}

func (m *RealDiagnosticsManager) finalizeDiagnostics(status string) {
	m.mu.Lock()
	m.status.Status = status
	m.cancelFunc = nil
	m.mu.Unlock()
	if m.onStatus != nil {
		m.onStatus(status)
	}
}

func (m *RealDiagnosticsManager) runUpload(ctx context.Context, plan diagnosticsUploadPlan, retries, retryInterval int) {
	// Uploading: immediate — check ctx before writing status.
	select {
	case <-ctx.Done():
		m.mu.Lock()
		m.status = DiagnosticsStatus{Status: "Idle"}
		m.cancelFunc = nil
		m.mu.Unlock()
		return
	default:
	}
	m.mu.Lock()
	m.status.Status = "Uploading"
	m.mu.Unlock()
	// Check again before callback to avoid broadcasting stale status after cancel.
	select {
	case <-ctx.Done():
		m.mu.Lock()
		m.status = DiagnosticsStatus{Status: "Idle"}
		m.cancelFunc = nil
		m.mu.Unlock()
		return
	default:
	}
	if m.onStatus != nil {
		m.onStatus("Uploading")
	}

	remainingAttempts := retries + 1
	remainingFailures := plan.failures
	for remainingAttempts > 0 {
		if !waitForDiagnosticsStep(ctx, diagnosticsAttemptDuration) {
			m.mu.Lock()
			m.status = DiagnosticsStatus{Status: "Idle"}
			m.cancelFunc = nil
			m.mu.Unlock()
			return
		}

		remainingAttempts--
		if remainingFailures > 0 {
			remainingFailures--
			if remainingAttempts == 0 {
				m.finalizeDiagnostics("UploadFailed")
				return
			}
			if !waitForDiagnosticsStep(ctx, time.Duration(retryInterval)*time.Second) {
				m.mu.Lock()
				m.status = DiagnosticsStatus{Status: "Idle"}
				m.cancelFunc = nil
				m.mu.Unlock()
				return
			}
			continue
		}

		m.finalizeDiagnostics("Uploaded")
		return
	}
}
