package v201

import (
	"testing"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/availability"
	"github.com/stretchr/testify/assert"
)

func TestMapConnectorStatus(t *testing.T) {
	tests := []struct {
		input    string
		expected availability.ConnectorStatus
	}{
		{"Available", availability.ConnectorStatusAvailable},
		{"Preparing", availability.ConnectorStatusOccupied},
		{"Charging", availability.ConnectorStatusOccupied},
		{"SuspendedEV", availability.ConnectorStatusOccupied},
		{"SuspendedEVSE", availability.ConnectorStatusOccupied},
		{"Finishing", availability.ConnectorStatusOccupied},
		{"Reserved", availability.ConnectorStatusReserved},
		{"Unavailable", availability.ConnectorStatusUnavailable},
		{"Faulted", availability.ConnectorStatusFaulted},
		{"Unknown", availability.ConnectorStatusAvailable}, // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := mapConnectorStatus(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
