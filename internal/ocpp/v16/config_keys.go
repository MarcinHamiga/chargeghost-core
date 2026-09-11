package v16

import (
	"strconv"
	"sync"

	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
)

// ConfigKeyInfo describes a single OCPP configuration key.
type ConfigKeyInfo struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	ReadOnly bool   `json:"readonly"`
	Type     string `json:"type"` // "string" | "int" | "bool"
}

// ConfigKeyManager manages OCPP 1.6 standard configuration keys.
type ConfigKeyManager struct {
	mu         sync.RWMutex
	keys       map[string]*ConfigKeyInfo
	changes    chan struct{}
	persistDir string
}

// NewConfigKeyManager creates a manager pre-populated with OCPP 1.6 standard keys and defaults.
func NewConfigKeyManager() *ConfigKeyManager {
	m := &ConfigKeyManager{
		keys:    make(map[string]*ConfigKeyInfo),
		changes: make(chan struct{}, 1),
	}
	for _, k := range defaultOCPPKeys() {
		copy := k
		m.keys[k.Key] = &copy
	}
	return m
}

func defaultOCPPKeys() []ConfigKeyInfo {
	return []ConfigKeyInfo{
		{Key: "HeartbeatInterval", Value: "300", ReadOnly: false, Type: "int"},
		{Key: "ConnectionTimeOut", Value: "30", ReadOnly: false, Type: "int"},
		{Key: "MeterValueSampleInterval", Value: "30", ReadOnly: false, Type: "int"},
		{Key: "ClockAlignedDataInterval", Value: "0", ReadOnly: false, Type: "int"},
		{Key: "MeterValuesAlignedData", Value: "Energy.Active.Import.Register", ReadOnly: false, Type: "string"},
		{Key: "MeterValuesSampledData", Value: "Energy.Active.Import.Register", ReadOnly: false, Type: "string"},
		{Key: "NumberOfConnectors", Value: "1", ReadOnly: true, Type: "int"},
		{Key: "SupportedFeatureProfiles", Value: "Core,SmartCharging,LocalAuthListManagement,RemoteTrigger,Reservation,FirmwareManagement", ReadOnly: true, Type: "string"},
		{Key: "AuthorizationCacheEnabled", Value: "true", ReadOnly: false, Type: "bool"},
		{Key: "LocalAuthListEnabled", Value: "true", ReadOnly: false, Type: "bool"},
		{Key: "LocalAuthListMaxLength", Value: "1000", ReadOnly: true, Type: "int"},
		{Key: "SendLocalListMaxLength", Value: "1000", ReadOnly: true, Type: "int"},
		{Key: "ReserveConnectorZeroSupported", Value: "false", ReadOnly: true, Type: "bool"},
		{Key: "ChargeProfileMaxStackLevel", Value: "5", ReadOnly: true, Type: "int"},
		{Key: "ChargingScheduleMaxPeriods", Value: "10", ReadOnly: true, Type: "int"},
		{Key: "MaxChargingProfilesInstalled", Value: "20", ReadOnly: true, Type: "int"},
		{Key: "ChargingScheduleAllowedChargingRateUnit", Value: "Current,Power", ReadOnly: true, Type: "string"},
		{Key: "TransactionMessageAttempts", Value: "3", ReadOnly: false, Type: "int"},
		{Key: "TransactionMessageRetryInterval", Value: "60", ReadOnly: false, Type: "int"},
		{Key: "StopTransactionOnInvalidId", Value: "true", ReadOnly: false, Type: "bool"},
		{Key: "StopTransactionOnEVSideDisconnect", Value: "true", ReadOnly: false, Type: "bool"},
		{Key: "UnlockConnectorOnEVSideDisconnect", Value: "true", ReadOnly: false, Type: "bool"},
		{Key: "GetConfigurationMaxKeys", Value: "0", ReadOnly: true, Type: "int"},
	}
}

// GetConfigValue returns the current value for a key, or "" if unknown.
func (m *ConfigKeyManager) GetConfigValue(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if k, ok := m.keys[key]; ok {
		return k.Value
	}
	return ""
}

// SetConfigValue updates a key value. Returns "Accepted", "Rejected"
// (read-only or failed type/range validation), or "NotSupported" (unknown).
// Per OCPP 1.6 §5.16 an out-of-range or mistyped value must be Rejected,
// not persisted: persisting e.g. a non-numeric HeartbeatInterval would
// corrupt the heartbeat and meter loops that parse it.
func (m *ConfigKeyManager) SetConfigValue(key, value string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	k, ok := m.keys[key]
	if !ok {
		return "NotSupported"
	}
	if k.ReadOnly {
		return "Rejected"
	}
	if k.Value == value {
		return "Accepted"
	}
	if !validConfigValue(k.Type, value) {
		return "Rejected"
	}
	k.Value = value
	m.notifyChange()
	go m.autoSave()
	return "Accepted"
}

// validConfigValue checks value against the key's declared type. Int keys
// must parse and be non-negative (intervals, counts, timeouts); bool keys
// must parse as booleans. String keys accept any value.
func validConfigValue(keyType, value string) bool {
	switch keyType {
	case "int":
		n, err := strconv.Atoi(value)
		return err == nil && n >= 0
	case "bool":
		_, err := strconv.ParseBool(value)
		return err == nil
	default:
		return true
	}
}

// ConfigChanges returns a signal channel for live config updates.
func (m *ConfigKeyManager) ConfigChanges() <-chan struct{} {
	return m.changes
}

// GetConfigKeyInfo returns all keys as version-agnostic entries.
// Satisfies the ocpp.ConfigKeyAPI interface.
func (m *ConfigKeyManager) GetConfigKeyInfo() []ocpppkg.ConfigKeyEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ocpppkg.ConfigKeyEntry, 0, len(m.keys))
	for _, k := range m.keys {
		result = append(result, ocpppkg.ConfigKeyEntry{
			Key:      k.Key,
			Value:    k.Value,
			ReadOnly: k.ReadOnly,
			Type:     k.Type,
		})
	}
	return result
}

// GetMeterValueSampleInterval returns the configured interval as a duration (seconds).
func (m *ConfigKeyManager) GetMeterValueSampleInterval() int {
	val := m.GetConfigValue("MeterValueSampleInterval")
	if n, err := strconv.Atoi(val); err == nil {
		return n
	}
	return 30
}

// GetHeartbeatInterval returns the configured heartbeat interval in seconds.
func (m *ConfigKeyManager) GetHeartbeatInterval() int {
	val := m.GetConfigValue("HeartbeatInterval")
	if n, err := strconv.Atoi(val); err == nil {
		return n
	}
	return 300
}

func (m *ConfigKeyManager) GetTransactionMessageAttempts() int {
	val := m.GetConfigValue("TransactionMessageAttempts")
	if n, err := strconv.Atoi(val); err == nil && n > 0 {
		return n
	}
	return 3
}

func (m *ConfigKeyManager) GetTransactionMessageRetryInterval() int {
	val := m.GetConfigValue("TransactionMessageRetryInterval")
	if n, err := strconv.Atoi(val); err == nil && n >= 0 {
		return n
	}
	return 60
}

func (m *ConfigKeyManager) GetAuthorizationCacheEnabled() bool {
	val := m.GetConfigValue("AuthorizationCacheEnabled")
	enabled, err := strconv.ParseBool(val)
	if err != nil {
		return true
	}
	return enabled
}

func (m *ConfigKeyManager) GetLocalAuthListEnabled() bool {
	val := m.GetConfigValue("LocalAuthListEnabled")
	enabled, err := strconv.ParseBool(val)
	if err != nil {
		return true
	}
	return enabled
}

func (m *ConfigKeyManager) notifyChange() {
	select {
	case m.changes <- struct{}{}:
	default:
	}
}
