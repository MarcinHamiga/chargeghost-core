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
	Delete      bool
}

// LocalAuthManager manages the local authorization list.
// Plan 3b uses StubLocalAuthManager; Plan 5d replaces it with the real implementation.
type LocalAuthManager interface {
	GetVersion() int
	GetEntry(idTag string) *LocalAuthEntry
	Decision(idTag string, now time.Time) AuthorizationDecision
	GetAllEntries() []LocalAuthEntry
	UpdateList(version int, entries []LocalAuthEntry, updateType string) error
	RemoveEntry(idTag string) error
	Clear()
	GetStats() (version, count, maxEntries int, enabled bool)
}

// FirmwareStatus holds the current firmware update simulation state.
type FirmwareStatus struct {
	Status       string // Current simulator emits: "Idle" | "Downloading" | "Downloaded" | "Installing" | "Installed"
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
	Status   string // Current simulator emits: "Idle" | "Uploading" | "Uploaded" | "UploadFailed"
	Location *string
}

// DiagnosticsManager manages simulated diagnostics uploads.
// Plan 3b uses StubDiagnosticsManager; Plan 5e replaces it.
type DiagnosticsManager interface {
	GetStatus() DiagnosticsStatus
	TriggerUpload(location string, retries, retryInterval int) error
	CancelUpload() error
}

// ConfigKeyEntry describes a single OCPP configuration key/variable.
// Used by both v1.6 (configuration keys) and v2.0.1 (device model variables).
type ConfigKeyEntry struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	ReadOnly bool   `json:"readonly"`
	Type     string `json:"type"` // "string" | "int" | "bool"
}

// ConfigKeyAPI is the version-agnostic interface for reading/writing OCPP
// configuration keys (v1.6) or device-model variables (v2.0.1) via the REST API.
type ConfigKeyAPI interface {
	GetConfigKeyInfo() []ConfigKeyEntry
	GetMeterValueSampleInterval() int
	SetConfigValue(key, value string) string
}

// HeartbeatIntervalProvider exposes the live heartbeat interval used by a bridge.
type HeartbeatIntervalProvider interface {
	GetHeartbeatInterval() int
}

// MeterValueIntervalProvider exposes the live MeterValues sampling interval.
type MeterValueIntervalProvider interface {
	GetMeterValueSampleInterval() int
}

// ConfigChangeNotifier optionally exposes a signal channel for live config updates.
// Implementations should send a value whenever a relevant config value changes.
type ConfigChangeNotifier interface {
	ConfigChanges() <-chan struct{}
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

func (m *StubLocalAuthManager) Decision(idTag string, now time.Time) AuthorizationDecision {
	e, ok := m.entries[idTag]
	if !ok {
		return AuthorizationDecisionMissing
	}
	return authorizationDecision(e.Status, e.Expiry, now)
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
