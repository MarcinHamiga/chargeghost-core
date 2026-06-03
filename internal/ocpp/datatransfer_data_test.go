package ocpp_test

import (
	"testing"

	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/stretchr/testify/assert"
)

func TestDataTransferDataString(t *testing.T) {
	assert.Equal(t, "", ocpp.DataTransferDataString(nil))
	assert.Equal(t, "hello", ocpp.DataTransferDataString("hello"))
	assert.JSONEq(t, `{"a":1}`, ocpp.DataTransferDataString(map[string]int{"a": 1}))
}
