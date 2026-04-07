package ocpp_test

import (
	"testing"

	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/stretchr/testify/assert"
)

func TestDataTransferRegistry_Dispatch(t *testing.T) {
	r := ocpp.NewDataTransferRegistry()
	r.Register("MyVendor", "GetFoo", func(messageID, data string) (string, string) {
		return "Accepted", "response-data"
	})

	status, resp := r.Dispatch("MyVendor", "GetFoo", "GetFoo", "input")
	assert.Equal(t, "Accepted", status)
	assert.Equal(t, "response-data", resp)
}

func TestDataTransferRegistry_UnknownVendor(t *testing.T) {
	r := ocpp.NewDataTransferRegistry()
	status, _ := r.Dispatch("UnknownVendor", "Msg", "Msg", "")
	assert.Equal(t, "UnknownVendorId", status)
}
