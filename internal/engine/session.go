package engine

import "time"

// Session represents an active charging session on a connector.
type Session struct {
	TransactionID              int
	ConnectorID                int
	StartTime                  time.Time
	EnergyCharged              float64 // Wh accumulated this session
	StateOfCharge              float64 // 0.0–100.0; 0 when MaxEnergy == 0
	MaxEnergy                  float64 // Wh battery capacity; 0 = no limit
	IDTag                      *string
	ReservationID              *int
	RemoteStartChargingProfile *ChargingProfile // forwarded to OCPP layer
	MaxChargeReached           bool             // fires exactly once per session
	MeterHistory               []MeterRecord
}

func NewSession(connectorID, transactionID int, maxEnergy float64, idTag *string, reservationID *int) *Session {
	return &Session{
		TransactionID: transactionID,
		ConnectorID:   connectorID,
		StartTime:     time.Now(),
		MaxEnergy:     maxEnergy,
		IDTag:         idTag,
		ReservationID: reservationID,
		MeterHistory:  make([]MeterRecord, 0, 10),
	}
}

// UpdateEnergy adds Wh to this session, capping at MaxEnergy when set.
func (s *Session) UpdateEnergy(energyWh float64) {
	if s.MaxEnergy > 0 {
		s.EnergyCharged = min(s.EnergyCharged+energyWh, s.MaxEnergy)
		s.StateOfCharge = (s.EnergyCharged / s.MaxEnergy) * 100
	} else {
		s.EnergyCharged += energyWh
	}
}

// RecordMeter appends a meter reading, keeping only the last 10.
func (s *Session) RecordMeter(value float64) {
	s.MeterHistory = append(s.MeterHistory, MeterRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Value:     value,
	})
	if len(s.MeterHistory) > 10 {
		s.MeterHistory = s.MeterHistory[len(s.MeterHistory)-10:]
	}
}
