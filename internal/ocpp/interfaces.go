package ocpp

import (
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
)

// ChargingProfileManagerAPI is the version-agnostic interface used by the REST API
// to manage charging profiles regardless of OCPP version.
type ChargingProfileManagerAPI interface {
	GetChargingProfiles() []engine.ChargingProfile
	GetChargingProfile(connectorID, profileID int) (engine.ChargingProfile, bool)
	SetChargingProfile(connectorID int, profile engine.ChargingProfile) error
	ClearChargingProfile(connectorID, profileID *int, purpose, stackLevel *string) error
	GetCompositeSchedule(connectorID, txID int, now time.Time, duration int, voltage float64, txStart *time.Time, phases int) ([]engine.ChargingSchedulePeriod, error)
}

// LocalAuthEntry is a single entry in the local authorization list.
type LocalAuthEntry struct {
	IDTag       string
	Status      string // "Accepted" | "Blocked" | "Expired" | "ConcurrentTx"
	Expiry      *time.Time
	ParentIDTag *string
}

// LocalAuthManager manages the local authorization list.
// Plan 3b uses StubLocalAuthManager; Plan 5d replaces it with the real implementation.
type LocalAuthManager interface {
	GetVersion() int
	GetEntry(idTag string) *LocalAuthEntry
	GetAllEntries() []LocalAuthEntry
	UpdateList(version int, entries []LocalAuthEntry, updateType string) error
	RemoveEntry(idTag string) error
	Clear()
	GetStats() (version, count, maxEntries int, enabled bool)
}

// FirmwareStatus holds the current firmware update simulation state.
type FirmwareStatus struct {
	Status       string     // "Idle" | "Downloading" | "Downloaded" | "Installing" | "Installed" | "InstallationFailed"
	Location     *string
	RetrieveDate *time.Time
	FileName     *string
	FileHash     *string
}

// FirmwareManager manages simulated firmware updates.
// Plan 3b uses StubFirmwareManager; Plan 5e replaces it.
type FirmwareManager interface {
	GetStatus() FirmwareStatus
	TriggerUpdate(location string, retrieveDate time.Time) error
	CancelUpdate() error
}

// DiagnosticsStatus holds the current diagnostics upload simulation state.
type DiagnosticsStatus struct {
	Status   string  // "Idle" | "Uploading" | "Uploaded" | "UploadFailed"
	Location *string
}

// DiagnosticsManager manages simulated diagnostics uploads.
// Plan 3b uses StubDiagnosticsManager; Plan 5e replaces it.
type DiagnosticsManager interface {
	GetStatus() DiagnosticsStatus
	TriggerUpload(location string, retries, retryInterval int) error
	CancelUpload() error
}

// --- Stub implementations ---

// StubLocalAuthManager is an in-memory implementation used before Plan 5d.
type StubLocalAuthManager struct {
	version int
	entries map[string]LocalAuthEntry
	enabled bool
}

func NewStubLocalAuthManager() *StubLocalAuthManager {
	return &StubLocalAuthManager{entries: make(map[string]LocalAuthEntry), enabled: true}
}

func (m *StubLocalAuthManager) GetVersion() int { return m.version }

func (m *StubLocalAuthManager) GetEntry(idTag string) *LocalAuthEntry {
	if e, ok := m.entries[idTag]; ok {
		return &e
	}
	return nil
}

func (m *StubLocalAuthManager) GetAllEntries() []LocalAuthEntry {
	result := make([]LocalAuthEntry, 0, len(m.entries))
	for _, e := range m.entries {
		result = append(result, e)
	}
	return result
}

func (m *StubLocalAuthManager) UpdateList(version int, entries []LocalAuthEntry, updateType string) error {
	if updateType == "Full" {
		m.entries = make(map[string]LocalAuthEntry)
	}
	for _, e := range entries {
		m.entries[e.IDTag] = e
	}
	m.version = version
	return nil
}

func (m *StubLocalAuthManager) RemoveEntry(idTag string) error {
	delete(m.entries, idTag)
	return nil
}

func (m *StubLocalAuthManager) Clear() {
	m.entries = make(map[string]LocalAuthEntry)
	m.version = 0
}

func (m *StubLocalAuthManager) GetStats() (version, count, maxEntries int, enabled bool) {
	return m.version, len(m.entries), 1000, m.enabled
}

// StubFirmwareManager always reports "Idle".
type StubFirmwareManager struct{}

func NewStubFirmwareManager() *StubFirmwareManager { return &StubFirmwareManager{} }

func (m *StubFirmwareManager) GetStatus() FirmwareStatus {
	return FirmwareStatus{Status: "Idle"}
}

func (m *StubFirmwareManager) TriggerUpdate(location string, retrieveDate time.Time) error {
	return nil // no-op in stub
}

func (m *StubFirmwareManager) CancelUpdate() error { return nil }

// StubDiagnosticsManager always reports "Idle".
type StubDiagnosticsManager struct{}

func NewStubDiagnosticsManager() *StubDiagnosticsManager { return &StubDiagnosticsManager{} }

func (m *StubDiagnosticsManager) GetStatus() DiagnosticsStatus {
	return DiagnosticsStatus{Status: "Idle"}
}

func (m *StubDiagnosticsManager) TriggerUpload(location string, retries, retryInterval int) error {
	return nil
}

func (m *StubDiagnosticsManager) CancelUpload() error { return nil }
