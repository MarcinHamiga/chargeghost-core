package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chargeghost/engine/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Defaults(t *testing.T) {
	c := config.DefaultConfig()
	assert.Equal(t, "CP_1", c.OCPPID)
	assert.Equal(t, "ChargeGhostV1", c.ChargePointModel)
	assert.Equal(t, "1.6", c.OCPPVersion)
	assert.Equal(t, 55.0, c.EVBatteryCapacity) // kWh
	assert.False(t, c.MultiEVSEMode)
	assert.Len(t, c.Connectors, 1)
	assert.Equal(t, 230.0, c.Connectors[0].Voltage)
}

func TestConfig_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := config.DefaultConfig()
	cfg.OCPPID = "TestCP"
	secret := "secret"
	cfg.OCPPPassword = &secret
	require.NoError(t, cfg.Save(path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var stored map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &stored))
	_, hasPassword := stored["ocpp_password"]
	assert.False(t, hasPassword)

	loaded, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "TestCP", loaded.OCPPID)
	assert.Nil(t, loaded.OCPPPassword)
}

func TestConfig_LoadNonExistent_ReturnsDefault(t *testing.T) {
	loaded, err := config.Load("/tmp/nonexistent-chargeghost-config.json")
	require.NoError(t, err)
	assert.Equal(t, "CP_1", loaded.OCPPID)
}
