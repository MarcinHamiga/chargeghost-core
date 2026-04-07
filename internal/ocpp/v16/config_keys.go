package v16

import (
	"strconv"
	"sync"
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
	mu   sync.RWMutex
	keys map[string]*ConfigKeyInfo
}

// NewConfigKeyManager creates a manager pre-populated with OCPP 1.6 standard keys and defaults.
func NewConfigKeyManager() *ConfigKeyManager {
	m := &ConfigKeyManager{
		keys: make(map[string]*ConfigKeyInfo),
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

// SetConfigValue updates a key value. Returns "Accepted", "Rejected" (read-only), or "NotSupported" (unknown).
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
	k.Value = value
	return "Accepted"
}

// GetConfigKeyInfo returns all keys (for GetConfiguration OCPP response).
func (m *ConfigKeyManager) GetConfigKeyInfo() []ConfigKeyInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]ConfigKeyInfo, 0, len(m.keys))
	for _, k := range m.keys {
		result = append(result, *k)
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
