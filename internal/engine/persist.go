package engine

import (
	"time"

	"github.com/chargeghost/engine/internal/persistence"
)

const engineFile = "engine.json"

// Snapshot structs — JSON-tagged mirrors of domain types.
// Keeps domain types clean and decouples wire format from internal representation.

type engineSnapshot struct {
	Connectors             map[int]*connectorSnapshot        `json:"connectors"`
	NextConnectorID        int                               `json:"next_connector_id"`
	Sessions               map[int]*sessionSnapshot          `json:"sessions"`
	GlobalMeter            *meterSnapshot                    `json:"global_meter"`
	EnergyMeters           map[int]*meterSnapshot            `json:"energy_meters"`
	LastStoppedSession     *stoppedSessionSnapshot           `json:"last_stopped_session,omitempty"`
	PendingRemoteStarts    map[int]*pendingRemoteSnapshot    `json:"pending_remote_starts"`
	PendingAvailChanges    map[int]string                    `json:"pending_availability_changes"`
	Reservations           map[int]*reservationSnapshot      `json:"reservations"`
	EVBatteryCapacity      float64                           `json:"ev_battery_capacity"`
}

type connectorSnapshot struct {
	ID               int            `json:"id"`
	Voltage          float64        `json:"voltage"`
	Current          float64        `json:"current"`
	Phase            int            `json:"phase"`
	Status           ConnectorState `json:"status"`
	PersistentStatus ConnectorState `json:"persistent_status"`
	IsPluggedIn      bool           `json:"is_plugged_in"`
	IDTag            *string        `json:"id_tag,omitempty"`
}

type sessionSnapshot struct {
	TransactionID              int                     `json:"transaction_id"`
	ConnectorID                int                     `json:"connector_id"`
	StartTime                  time.Time               `json:"start_time"`
	EnergyCharged              float64                 `json:"energy_charged"`
	StateOfCharge              float64                 `json:"state_of_charge"`
	MaxEnergy                  float64                 `json:"max_energy"`
	IDTag                      *string                 `json:"id_tag,omitempty"`
	ReservationID              *int                    `json:"reservation_id,omitempty"`
	RemoteStartChargingProfile *chargingProfileSnapshot `json:"remote_start_charging_profile,omitempty"`
	MaxChargeReached           bool                    `json:"max_charge_reached"`
	MeterHistory               []MeterRecord           `json:"meter_history"`
}

type meterSnapshot struct {
	Value      float64 `json:"value"`
	IsCharging bool    `json:"is_charging"`
}

type reservationSnapshot struct {
	ReservationID int        `json:"reservation_id"`
	ConnectorID   int        `json:"connector_id"`
	IDTag         string     `json:"id_tag"`
	ExpiryDate    time.Time  `json:"expiry_date"`
	ParentIDTag   *string    `json:"parent_id_tag,omitempty"`
}

type pendingRemoteSnapshot struct {
	TransactionID   int                      `json:"transaction_id"`
	MaxEnergy       float64                  `json:"max_energy"`
	IDTag           *string                  `json:"id_tag,omitempty"`
	ChargingProfile *chargingProfileSnapshot `json:"charging_profile,omitempty"`
	Expiry          time.Time                `json:"expiry"`
}

type stoppedSessionSnapshot struct {
	TransactionID int           `json:"transaction_id"`
	ConnectorID   int           `json:"connector_id"`
	EnergyCharged float64       `json:"energy_charged"`
	IDTag         *string       `json:"id_tag,omitempty"`
	MeterStop     float64       `json:"meter_stop"`
	Reason        string        `json:"reason"`
	MeterHistory  []MeterRecord `json:"meter_history"`
	ReservationID *int          `json:"reservation_id,omitempty"`
}

type chargingProfileSnapshot struct {
	ProfileID      int                          `json:"profile_id"`
	ConnectorID    int                          `json:"connector_id"`
	StackLevel     int                          `json:"stack_level"`
	Purpose        string                       `json:"purpose"`
	Kind           string                       `json:"kind"`
	RecurrencyKind string                       `json:"recurrency_kind,omitempty"`
	ValidFrom      *time.Time                   `json:"valid_from,omitempty"`
	ValidTo        *time.Time                   `json:"valid_to,omitempty"`
	StartSchedule  *time.Time                   `json:"start_schedule,omitempty"`
	Schedule       chargingScheduleSnapshot     `json:"schedule"`
}

type chargingScheduleSnapshot struct {
	Duration         int                             `json:"duration"`
	StartSchedule    *time.Time                      `json:"start_schedule,omitempty"`
	ChargingRateUnit string                          `json:"charging_rate_unit"`
	MinChargingRate  float64                         `json:"min_charging_rate"`
	Periods          []chargingSchedulePeriodSnapshot `json:"periods"`
}

type chargingSchedulePeriodSnapshot struct {
	StartPeriod  int      `json:"start_period"`
	Limit        float64  `json:"limit"`
	NumberPhases *int     `json:"number_phases,omitempty"`
}

// SaveState writes the full engine state to dir/engine.json.
func (e *Engine) SaveState(dir string) error {
	e.mu.RLock()
	snap := e.buildSnapshot()
	e.mu.RUnlock()
	return persistence.WriteJSON(dir, engineFile, snap)
}

// LoadState restores engine state from dir/engine.json.
// Returns nil if the file does not exist (engine stays in its fresh state).
func (e *Engine) LoadState(dir string) error {
	var snap engineSnapshot
	if err := persistence.ReadJSON(dir, engineFile, &snap); err != nil {
		return err
	}
	// No file found — nothing to restore.
	if snap.Connectors == nil {
		return nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()

	// Connectors.
	e.connectors = make(map[int]*Connector, len(snap.Connectors))
	for id, cs := range snap.Connectors {
		e.connectors[id] = &Connector{
			ID:               cs.ID,
			Voltage:          cs.Voltage,
			Current:          cs.Current,
			Phase:            cs.Phase,
			Status:           cs.Status,
			PersistentStatus: cs.PersistentStatus,
			IsPluggedIn:      cs.IsPluggedIn,
			IDTag:            cs.IDTag,
		}
	}
	e.nextConnectorID = snap.NextConnectorID

	// Sessions.
	e.sessions = make(map[int]*Session, len(snap.Sessions))
	for cid, ss := range snap.Sessions {
		e.sessions[cid] = &Session{
			TransactionID:              ss.TransactionID,
			ConnectorID:                ss.ConnectorID,
			StartTime:                  ss.StartTime,
			EnergyCharged:              ss.EnergyCharged,
			StateOfCharge:              ss.StateOfCharge,
			MaxEnergy:                  ss.MaxEnergy,
			IDTag:                      ss.IDTag,
			ReservationID:              ss.ReservationID,
			RemoteStartChargingProfile: snapshotToChargingProfile(ss.RemoteStartChargingProfile),
			MaxChargeReached:           ss.MaxChargeReached,
			MeterHistory:               ss.MeterHistory,
		}
	}

	// Meters.
	if snap.GlobalMeter != nil {
		e.globalMeter = &EnergyMeter{Value: snap.GlobalMeter.Value, IsCharging: snap.GlobalMeter.IsCharging}
	}
	e.energyMeters = make(map[int]*EnergyMeter, len(snap.EnergyMeters))
	for id, ms := range snap.EnergyMeters {
		e.energyMeters[id] = &EnergyMeter{Value: ms.Value, IsCharging: ms.IsCharging}
	}

	// Last stopped session.
	if snap.LastStoppedSession != nil {
		ss := snap.LastStoppedSession
		e.LastStoppedSession = &StoppedSessionInfo{
			TransactionID: ss.TransactionID,
			ConnectorID:   ss.ConnectorID,
			EnergyCharged: ss.EnergyCharged,
			IDTag:         ss.IDTag,
			MeterStop:     ss.MeterStop,
			Reason:        ss.Reason,
			MeterHistory:  ss.MeterHistory,
			ReservationID: ss.ReservationID,
		}
	}

	// Pending remote starts — discard expired.
	e.pendingRemoteStarts = make(map[int]*PendingRemoteStart)
	for cid, ps := range snap.PendingRemoteStarts {
		if now.After(ps.Expiry) {
			continue
		}
		e.pendingRemoteStarts[cid] = &PendingRemoteStart{
			TransactionID:   ps.TransactionID,
			MaxEnergy:       ps.MaxEnergy,
			IDTag:           ps.IDTag,
			ChargingProfile: snapshotToChargingProfile(ps.ChargingProfile),
			Expiry:          ps.Expiry,
		}
	}

	// Pending availability changes.
	e.pendingAvailabilityChanges = make(map[int]string, len(snap.PendingAvailChanges))
	for k, v := range snap.PendingAvailChanges {
		e.pendingAvailabilityChanges[k] = v
	}

	// Reservations — discard expired.
	e.reservations = make(map[int]*Reservation)
	for rid, rs := range snap.Reservations {
		if now.After(rs.ExpiryDate) {
			continue
		}
		e.reservations[rid] = &Reservation{
			ReservationID: rs.ReservationID,
			ConnectorID:   rs.ConnectorID,
			IDTag:         rs.IDTag,
			ExpiryDate:    rs.ExpiryDate,
			ParentIDTag:   rs.ParentIDTag,
		}
	}

	e.EVBatteryCapacity = snap.EVBatteryCapacity

	return nil
}

// buildSnapshot creates a serializable snapshot of the engine state.
// Must be called under at least a read lock.
func (e *Engine) buildSnapshot() *engineSnapshot {
	snap := &engineSnapshot{
		Connectors:          make(map[int]*connectorSnapshot, len(e.connectors)),
		NextConnectorID:     e.nextConnectorID,
		Sessions:            make(map[int]*sessionSnapshot, len(e.sessions)),
		EnergyMeters:        make(map[int]*meterSnapshot, len(e.energyMeters)),
		PendingRemoteStarts: make(map[int]*pendingRemoteSnapshot, len(e.pendingRemoteStarts)),
		PendingAvailChanges: make(map[int]string, len(e.pendingAvailabilityChanges)),
		Reservations:        make(map[int]*reservationSnapshot, len(e.reservations)),
		EVBatteryCapacity:   e.EVBatteryCapacity,
	}

	for id, c := range e.connectors {
		snap.Connectors[id] = &connectorSnapshot{
			ID: c.ID, Voltage: c.Voltage, Current: c.Current, Phase: c.Phase,
			Status: c.Status, PersistentStatus: c.PersistentStatus,
			IsPluggedIn: c.IsPluggedIn, IDTag: c.IDTag,
		}
	}

	for cid, s := range e.sessions {
		snap.Sessions[cid] = &sessionSnapshot{
			TransactionID:              s.TransactionID,
			ConnectorID:                s.ConnectorID,
			StartTime:                  s.StartTime,
			EnergyCharged:              s.EnergyCharged,
			StateOfCharge:              s.StateOfCharge,
			MaxEnergy:                  s.MaxEnergy,
			IDTag:                      s.IDTag,
			ReservationID:              s.ReservationID,
			RemoteStartChargingProfile: chargingProfileToSnapshot(s.RemoteStartChargingProfile),
			MaxChargeReached:           s.MaxChargeReached,
			MeterHistory:               s.MeterHistory,
		}
	}

	if e.globalMeter != nil {
		snap.GlobalMeter = &meterSnapshot{Value: e.globalMeter.Value, IsCharging: e.globalMeter.IsCharging}
	}
	for id, m := range e.energyMeters {
		snap.EnergyMeters[id] = &meterSnapshot{Value: m.Value, IsCharging: m.IsCharging}
	}

	if e.LastStoppedSession != nil {
		ss := e.LastStoppedSession
		snap.LastStoppedSession = &stoppedSessionSnapshot{
			TransactionID: ss.TransactionID, ConnectorID: ss.ConnectorID,
			EnergyCharged: ss.EnergyCharged, IDTag: ss.IDTag,
			MeterStop: ss.MeterStop, Reason: ss.Reason,
			MeterHistory: ss.MeterHistory, ReservationID: ss.ReservationID,
		}
	}

	for cid, p := range e.pendingRemoteStarts {
		snap.PendingRemoteStarts[cid] = &pendingRemoteSnapshot{
			TransactionID:   p.TransactionID,
			MaxEnergy:       p.MaxEnergy,
			IDTag:           p.IDTag,
			ChargingProfile: chargingProfileToSnapshot(p.ChargingProfile),
			Expiry:          p.Expiry,
		}
	}

	for k, v := range e.pendingAvailabilityChanges {
		snap.PendingAvailChanges[k] = v
	}

	for rid, r := range e.reservations {
		snap.Reservations[rid] = &reservationSnapshot{
			ReservationID: r.ReservationID, ConnectorID: r.ConnectorID,
			IDTag: r.IDTag, ExpiryDate: r.ExpiryDate, ParentIDTag: r.ParentIDTag,
		}
	}

	return snap
}

// Conversion helpers for ChargingProfile ↔ snapshot.

func chargingProfileToSnapshot(cp *ChargingProfile) *chargingProfileSnapshot {
	if cp == nil {
		return nil
	}
	periods := make([]chargingSchedulePeriodSnapshot, len(cp.Schedule.Periods))
	for i, p := range cp.Schedule.Periods {
		periods[i] = chargingSchedulePeriodSnapshot{
			StartPeriod: p.StartPeriod, Limit: p.Limit, NumberPhases: p.NumberPhases,
		}
	}
	return &chargingProfileSnapshot{
		ProfileID: cp.ProfileID, ConnectorID: cp.ConnectorID,
		StackLevel: cp.StackLevel, Purpose: cp.Purpose,
		Kind: cp.Kind, RecurrencyKind: cp.RecurrencyKind,
		ValidFrom: cp.ValidFrom, ValidTo: cp.ValidTo,
		StartSchedule: cp.StartSchedule,
		Schedule: chargingScheduleSnapshot{
			Duration: cp.Schedule.Duration, StartSchedule: cp.Schedule.StartSchedule,
			ChargingRateUnit: cp.Schedule.ChargingRateUnit,
			MinChargingRate:  cp.Schedule.MinChargingRate,
			Periods:          periods,
		},
	}
}

func snapshotToChargingProfile(s *chargingProfileSnapshot) *ChargingProfile {
	if s == nil {
		return nil
	}
	periods := make([]ChargingSchedulePeriod, len(s.Schedule.Periods))
	for i, p := range s.Schedule.Periods {
		periods[i] = ChargingSchedulePeriod{
			StartPeriod: p.StartPeriod, Limit: p.Limit, NumberPhases: p.NumberPhases,
		}
	}
	return &ChargingProfile{
		ProfileID: s.ProfileID, ConnectorID: s.ConnectorID,
		StackLevel: s.StackLevel, Purpose: s.Purpose,
		Kind: s.Kind, RecurrencyKind: s.RecurrencyKind,
		ValidFrom: s.ValidFrom, ValidTo: s.ValidTo,
		StartSchedule: s.StartSchedule,
		Schedule: ChargingSchedule{
			Duration: s.Schedule.Duration, StartSchedule: s.Schedule.StartSchedule,
			ChargingRateUnit: s.Schedule.ChargingRateUnit,
			MinChargingRate:  s.Schedule.MinChargingRate,
			Periods:          periods,
		},
	}
}
