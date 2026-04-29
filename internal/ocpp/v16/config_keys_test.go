package v16_test

import (
	"testing"

	v16 "github.com/chargeghost/engine/internal/ocpp/v16"
	"github.com/stretchr/testify/assert"
)

func TestConfigKeyManager_GetAndSet(t *testing.T) {
	m := v16.NewConfigKeyManager()

	val := m.GetConfigValue("HeartbeatInterval")
	assert.Equal(t, "300", val) // default

	result := m.SetConfigValue("HeartbeatInterval", "60")
	assert.Equal(t, "Accepted", result)
	assert.Equal(t, "60", m.GetConfigValue("HeartbeatInterval"))
}

func TestConfigKeyManager_ReadOnlyKey(t *testing.T) {
	m := v16.NewConfigKeyManager()
	result := m.SetConfigValue("NumberOfConnectors", "2")
	assert.Equal(t, "Rejected", result) // read-only
}

func TestConfigKeyManager_UnknownKey(t *testing.T) {
	m := v16.NewConfigKeyManager()
	result := m.SetConfigValue("SomeUnknownKey", "value")
	assert.Equal(t, "NotSupported", result)
}

func TestConfigKeyManager_GetConfigKeyInfo(t *testing.T) {
	m := v16.NewConfigKeyManager()
	keys := m.GetConfigKeyInfo()
	assert.NotEmpty(t, keys)
	found := false
	for _, k := range keys {
		if k.Key == "MeterValueSampleInterval" {
			found = true
			assert.Equal(t, "30", k.Value) // default 30s
		}
	}
	assert.True(t, found)
}

func TestConfigKeyManager_TransactionReplaySettings(t *testing.T) {
	m := v16.NewConfigKeyManager()

	assert.Equal(t, 3, m.GetTransactionMessageAttempts())
	assert.Equal(t, 60, m.GetTransactionMessageRetryInterval())

	assert.Equal(t, "Accepted", m.SetConfigValue("TransactionMessageAttempts", "7"))
	assert.Equal(t, "Accepted", m.SetConfigValue("TransactionMessageRetryInterval", "15"))

	assert.Equal(t, 7, m.GetTransactionMessageAttempts())
	assert.Equal(t, 15, m.GetTransactionMessageRetryInterval())
}
