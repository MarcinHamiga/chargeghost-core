package ocpp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewBridge_UnsupportedVersion(t *testing.T) {
	err := NewBridgeForVersion("3.0")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported OCPP version")
}
