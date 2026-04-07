package engine

import "errors"

// ConnectorState maps 1:1 to OCPP 1.6 ChargePointStatus values.
type ConnectorState string

const (
	StateAvailable     ConnectorState = "Available"
	StatePreparing     ConnectorState = "Preparing"
	StateCharging      ConnectorState = "Charging"
	StateSuspendedEVSE ConnectorState = "SuspendedEVSE"
	StateSuspendedEV   ConnectorState = "SuspendedEV"
	StateFinishing     ConnectorState = "Finishing"
	StateReserved      ConnectorState = "Reserved"
	StateUnavailable   ConnectorState = "Unavailable"
	StateFaulted       ConnectorState = "Faulted"
)

var ErrInvalidTransition = errors.New("invalid state transition")

type stateKey struct {
	state  ConnectorState
	action string
}

var validTransitions = map[stateKey]ConnectorState{
	{StateAvailable, "plug_in"}:           StatePreparing,
	{StateReserved, "plug_in"}:            StatePreparing,
	{StatePreparing, "unplug"}:            StateAvailable,
	{StateFinishing, "unplug"}:            StateAvailable,
	{StateCharging, "unplug"}:             StateAvailable,
	{StateSuspendedEV, "unplug"}:          StateAvailable,
	{StateSuspendedEVSE, "unplug"}:        StateAvailable,
	{StatePreparing, "start_charging"}:    StateCharging,
	{StateCharging, "stop_charging"}:      StateFinishing,
	{StateSuspendedEV, "stop_charging"}:   StateFinishing,
	{StateSuspendedEVSE, "stop_charging"}: StateFinishing,
	{StateCharging, "suspend_ev"}:         StateSuspendedEV,
	{StateSuspendedEV, "resume"}:          StateCharging,
	{StateCharging, "suspend_evse"}:       StateSuspendedEVSE,
	{StateSuspendedEVSE, "resume"}:        StateCharging,
}

// ApplyTransition returns the next state for the given (current, action) pair,
// or ErrInvalidTransition if no valid transition exists.
func ApplyTransition(current ConnectorState, action string) (ConnectorState, error) {
	if next, ok := validTransitions[stateKey{current, action}]; ok {
		return next, nil
	}
	return current, ErrInvalidTransition
}
