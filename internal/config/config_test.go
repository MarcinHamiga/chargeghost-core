package config_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/chargeghost/engine/internal/config"
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
