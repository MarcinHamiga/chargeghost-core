package engine

// EnergyMeter is an odometer-style cumulative Wh accumulator.
// Value persists across sessions in single-EVSE mode.
// In multi-EVSE mode, a per-connector meter is created and destroyed per session.
type EnergyMeter struct {
	Value      float64 // Cumulative Wh
	IsCharging bool
	// EffectiveCurrent is the actual current (A) delivered on the most
	// recent simulation tick — the connector's rated current capped by any
	// active charging-profile limit. Zero when not charging. OCPP senders
	// should use this for Current.Import/Power.Active.Import measurands and
	// the connector's rated Current for Current.Offered/Power.Offered,
	// which report what's available rather than what's flowing.
	EffectiveCurrent float64
}

func NewEnergyMeter() *EnergyMeter {
	return &EnergyMeter{}
}

// Update adds energy for one simulation interval.
// No-op when IsCharging is false.
func (m *EnergyMeter) Update(voltage, current float64, phase int, intervalSeconds float64) {
	if !m.IsCharging {
		return
	}
	powerW := voltage * current * float64(phase)
	m.Value += (powerW * intervalSeconds) / 3600.0
}

func (m *EnergyMeter) GetMeterReading() float64 {
	return m.Value
}
