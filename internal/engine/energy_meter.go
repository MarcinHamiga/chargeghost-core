package engine

// EnergyMeter is an odometer-style cumulative Wh accumulator.
// Value persists across sessions in single-EVSE mode.
// In multi-EVSE mode, a per-connector meter is created and destroyed per session.
type EnergyMeter struct {
	Value      float64 // Cumulative Wh
	IsCharging bool
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
