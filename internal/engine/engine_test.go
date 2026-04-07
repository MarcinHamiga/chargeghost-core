package engine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	engine "github.com/chargeghost/engine/internal/engine"
)

func TestApplyTransition_ValidTransitions(t *testing.T) {
	cases := []struct {
		from   engine.ConnectorState
		action string
		want   engine.ConnectorState
	}{
		{engine.StateAvailable, "plug_in", engine.StatePreparing},
		{engine.StateReserved, "plug_in", engine.StatePreparing},
		{engine.StatePreparing, "unplug", engine.StateAvailable},
		{engine.StateFinishing, "unplug", engine.StateAvailable},
		{engine.StateCharging, "unplug", engine.StateAvailable},
		{engine.StateSuspendedEV, "unplug", engine.StateAvailable},
		{engine.StateSuspendedEVSE, "unplug", engine.StateAvailable},
		{engine.StatePreparing, "start_charging", engine.StateCharging},
		{engine.StateCharging, "stop_charging", engine.StateFinishing},
		{engine.StateSuspendedEV, "stop_charging", engine.StateFinishing},
		{engine.StateSuspendedEVSE, "stop_charging", engine.StateFinishing},
		{engine.StateCharging, "suspend_ev", engine.StateSuspendedEV},
		{engine.StateSuspendedEV, "resume", engine.StateCharging},
		{engine.StateCharging, "suspend_evse", engine.StateSuspendedEVSE},
		{engine.StateSuspendedEVSE, "resume", engine.StateCharging},
	}
	for _, tc := range cases {
		next, err := engine.ApplyTransition(tc.from, tc.action)
		require.NoError(t, err, "from=%s action=%s", tc.from, tc.action)
		assert.Equal(t, tc.want, next, "from=%s action=%s", tc.from, tc.action)
	}
}

func TestApplyTransition_InvalidTransition(t *testing.T) {
	_, err := engine.ApplyTransition(engine.StateAvailable, "stop_charging")
	assert.ErrorIs(t, err, engine.ErrInvalidTransition)
}
