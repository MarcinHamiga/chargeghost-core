package engine

// Validation constants matching Python util/config.py.
const (
	MinVoltage = 120.0
	MaxVoltage = 1000.0
	MinCurrent = 6.0
	MaxCurrent = 150.0
)

// Connector represents a single EV charging outlet.
type Connector struct {
	ID               int
	Voltage          float64
	Current          float64
	Phase            int
	Status           ConnectorState
	PersistentStatus ConnectorState
	IsPluggedIn      bool
	IDTag            *string
}

// NewConnector creates a connector with validated parameters. Panics on invalid input
// so the caller catches misconfiguration at startup, not runtime.
func NewConnector(id int, voltage, current float64, phase int) *Connector {
	validateParams(voltage, current, phase)
	return &Connector{
		ID:               id,
		Voltage:          voltage,
		Current:          current,
		Phase:            phase,
		Status:           StateAvailable,
		PersistentStatus: StateAvailable,
	}
}

func validateParams(voltage, current float64, phase int) {
	if voltage < MinVoltage || voltage > MaxVoltage {
		panic("voltage out of range")
	}
	if current < MinCurrent || current > MaxCurrent {
		panic("current out of range")
	}
	if phase != 1 && phase != 3 {
		panic("phase must be 1 or 3")
	}
}

// PlugIn simulates an EV connecting to this connector.
// If the connector is Unavailable or Faulted, IsPluggedIn is set but status does not change.
func (c *Connector) PlugIn() error {
	c.IsPluggedIn = true
	if c.Status == StateUnavailable || c.Status == StateFaulted {
		return nil
	}
	next, err := ApplyTransition(c.Status, "plug_in")
	if err != nil {
		return err
	}
	c.Status = next
	return nil
}

// Unplug simulates an EV disconnecting. Status is restored to PersistentStatus.
func (c *Connector) Unplug() {
	c.IsPluggedIn = false
	c.Status = c.PersistentStatus
}

// StartCharging transitions Preparing → Charging.
func (c *Connector) StartCharging() error {
	return c.applyAction("start_charging")
}

// StopCharging transitions Charging/SuspendedEV/SuspendedEVSE → Finishing.
func (c *Connector) StopCharging() error {
	return c.applyAction("stop_charging")
}

// SuspendEV transitions Charging → SuspendedEV.
func (c *Connector) SuspendEV() error {
	return c.applyAction("suspend_ev")
}

// SuspendEVSE transitions Charging → SuspendedEVSE.
func (c *Connector) SuspendEVSE() error {
	return c.applyAction("suspend_evse")
}

// ResumeCharging transitions SuspendedEV or SuspendedEVSE → Charging.
func (c *Connector) ResumeCharging() error {
	return c.applyAction("resume")
}

// SetUnavailable bypasses the state machine. No-op if already Unavailable or Faulted.
func (c *Connector) SetUnavailable() {
	if c.Status == StateUnavailable || c.Status == StateFaulted {
		return
	}
	c.PersistentStatus = StateUnavailable
	c.Status = StateUnavailable
}

// SetReserved bypasses the state machine. No-op if Unavailable or Faulted.
// If plugged in, current status becomes Preparing; otherwise Reserved.
func (c *Connector) SetReserved() {
	if c.Status == StateUnavailable || c.Status == StateFaulted {
		return
	}
	c.PersistentStatus = StateReserved
	if c.IsPluggedIn {
		c.Status = StatePreparing
	} else {
		c.Status = StateReserved
	}
}

// SetOperative restores Available as persistent status. No-op if Faulted.
func (c *Connector) SetOperative() {
	if c.Status == StateFaulted {
		return
	}
	c.PersistentStatus = StateAvailable
	if c.IsPluggedIn {
		c.Status = StatePreparing
	} else {
		c.Status = StateAvailable
	}
}

// ClearReservation removes the reserved state. Same logic as SetOperative.
func (c *Connector) ClearReservation() {
	c.SetOperative()
}

func (c *Connector) applyAction(action string) error {
	next, err := ApplyTransition(c.Status, action)
	if err != nil {
		return err
	}
	c.Status = next
	return nil
}
