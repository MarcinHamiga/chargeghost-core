package engine

import (
	"errors"
	"sync"
	"time"
)

// Sentinel errors returned by Engine methods.
var (
	ErrConnectorNotFound     = errors.New("connector not found")
	ErrSessionNotFound       = errors.New("no active session")
	ErrSessionAlreadyActive  = errors.New("session already active on connector")
	ErrNotPluggedIn          = errors.New("connector not plugged in")
	ErrInvalidState          = errors.New("invalid connector state for this action")
	ErrLastConnector         = errors.New("cannot remove last connector")
	ErrSessionActiveOnRemove = errors.New("cannot remove connector with active session")
	ErrInvalidVoltage        = errors.New("voltage out of range (120–1000V)")
	ErrInvalidCurrent        = errors.New("current out of range (6–150A)")
	ErrInvalidPhase          = errors.New("phase must be 1 or 3 (not 2)")
)

// MeterRecord is a timestamped energy reading. Defined here (not session.go)
// because it is also used by StoppedSessionInfo and referenced by the OCPP adapter.
type MeterRecord struct {
	Timestamp string  `json:"timestamp"`
	Value     float64 `json:"value"`
}

// ChargingProfile holds smart charging constraints. Defined in the engine package
// to avoid circular imports; the OCPP layer imports from here.
type ChargingProfile struct {
	ProfileID      int
	ConnectorID    int
	StackLevel     int
	Purpose        string // "TxDefaultProfile" | "TxProfile" | "ChargePointMaxProfile"
	Kind           string // "Absolute" | "Recurring" | "Relative"
	RecurrencyKind string // "Daily" | "Weekly"
	ValidFrom      *time.Time
	ValidTo        *time.Time
	StartSchedule  *time.Time
	Schedule       ChargingSchedule
}

type ChargingSchedule struct {
	Duration         int // seconds
	StartSchedule    *time.Time
	ChargingRateUnit string // "A" | "W"
	MinChargingRate  float64
	Periods          []ChargingSchedulePeriod
}

type ChargingSchedulePeriod struct {
	StartPeriod  int     // seconds from schedule start
	Limit        float64 // A or kW
	NumberPhases *int
}

// StoppedSessionInfo captures the details of the most recently stopped session
// for use by the OCPP adapter when building StopTransaction.
type StoppedSessionInfo struct {
	TransactionID int
	ConnectorID   int
	EnergyCharged float64
	IDTag         *string
	MeterStop     float64
	Reason        string
	MeterHistory  []MeterRecord
	ReservationID *int
}

// PendingRemoteStart stores a RemoteStartTransaction that arrived before the EV plugged in.
type PendingRemoteStart struct {
	TransactionID   int
	MaxEnergy       float64
	IDTag           *string
	ChargingProfile *ChargingProfile
	Expiry          time.Time
}

// SessionDetail is returned by GetSessionInfo for active sessions.
type SessionDetail struct {
	TransactionID int
	ConnectorID   int
	EnergyCharged float64
	StateOfCharge float64
	MaxEnergy     float64
	StartTime     time.Time
	IDTag         *string
	IsCharging    bool
}

// Engine is the central coordinator — single source of truth for all simulation state.
// All state mutations are protected by mu.
type Engine struct {
	mu sync.RWMutex

	connectors      map[int]*Connector
	nextConnectorID int
	multiEVSEMode   bool

	sessions     map[int]*Session // keyed by connectorID
	globalMeter  *EnergyMeter
	energyMeters map[int]*EnergyMeter // multi-EVSE mode only

	LastStoppedSession *StoppedSessionInfo

	pendingRemoteStarts        map[int]*PendingRemoteStart
	pendingAvailabilityChanges map[int]string // connectorID → "Operative"|"Inoperative"

	reservations map[int]*Reservation // keyed by reservationID

	EVBatteryCapacity float64 // Wh

	// GetLimit is injected by the OCPP bridge to apply charging profile limits.
	// Returns nil when no limit applies (use connector's full current).
	GetLimit func(connectorID int, transactionID int) *float64

	// Engine event callbacks — called while the engine write lock is held.
	// IMPORTANT: Implementations must NOT call back into the engine — the write
	// lock is still held. All data needed by callbacks is passed as parameters.
	OnSessionStarted         func(connectorID int, idTag *string, meterStart float64, reservationID *int)
	OnSessionStopped         func(connectorID int, info *StoppedSessionInfo)
	OnConnectorStatusChanged func(connectorID int, status ConnectorState)
	OnConnectorParamsChanged func(connectorID int, voltage, current float64, phase int)
	OnReservationExpired     func(reservationID, connectorID int)
}

// NewEngine creates an Engine ready to use. evBatteryCapacityWh is the default
// battery size in Wh (0 disables SoC tracking).
func NewEngine(multiEVSEMode bool, evBatteryCapacityWh float64) *Engine {
	return &Engine{
		connectors:                 make(map[int]*Connector),
		nextConnectorID:            1,
		multiEVSEMode:              multiEVSEMode,
		sessions:                   make(map[int]*Session),
		globalMeter:                NewEnergyMeter(),
		energyMeters:               make(map[int]*EnergyMeter),
		pendingRemoteStarts:        make(map[int]*PendingRemoteStart),
		pendingAvailabilityChanges: make(map[int]string),
		reservations:               make(map[int]*Reservation),
		EVBatteryCapacity:          evBatteryCapacityWh,
	}
}

// AddConnector creates a connector with sequential ID and returns it.
func (e *Engine) AddConnector(voltage, current float64, phase int) *Connector {
	e.mu.Lock()
	defer e.mu.Unlock()

	c := NewConnector(e.nextConnectorID, voltage, current, phase)
	e.connectors[e.nextConnectorID] = c
	e.nextConnectorID++
	return c
}

// RemoveConnector removes a connector. Fails if it is the last one or has an active session.
func (e *Engine) RemoveConnector(id int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.connectors[id]; !ok {
		return ErrConnectorNotFound
	}
	if len(e.connectors) == 1 {
		return ErrLastConnector
	}
	if _, hasSession := e.sessions[id]; hasSession {
		return ErrSessionActiveOnRemove
	}
	delete(e.connectors, id)
	return nil
}

// UpdateConnector validates and applies partial updates to a connector's parameters.
// Nil pointers mean "no change".
func (e *Engine) UpdateConnector(id int, voltage, current *float64, phase *int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.connectors[id]
	if !ok {
		return ErrConnectorNotFound
	}

	if voltage != nil {
		if *voltage < MinVoltage || *voltage > MaxVoltage {
			return ErrInvalidVoltage
		}
		c.Voltage = *voltage
	}
	if current != nil {
		if *current < MinCurrent || *current > MaxCurrent {
			return ErrInvalidCurrent
		}
		c.Current = *current
	}
	if phase != nil {
		if *phase != 1 && *phase != 3 {
			return ErrInvalidPhase
		}
		c.Phase = *phase
	}

	if e.OnConnectorParamsChanged != nil {
		e.OnConnectorParamsChanged(id, c.Voltage, c.Current, c.Phase)
	}
	return nil
}

// GetConnector returns the connector for the given ID, or nil.
func (e *Engine) GetConnector(id int) *Connector {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.connectors[id]
}

// GetConnectorIDs returns all connector IDs in sorted order.
func (e *Engine) GetConnectorIDs() []int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	ids := make([]int, 0, len(e.connectors))
	for id := range e.connectors {
		ids = append(ids, id)
	}
	return ids
}

// PlugIn simulates an EV connecting to the given connector.
// In single-EVSE mode, any other plugged-in connector is unplugged first.
func (e *Engine) PlugIn(connectorID int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.expireReservations()

	c, ok := e.connectors[connectorID]
	if !ok {
		return
	}

	if !e.multiEVSEMode {
		for id, conn := range e.connectors {
			if id != connectorID && conn.IsPluggedIn {
				// Only flip the physical cable state; don't stop the active session.
				// The session stays alive so StartSession on another connector will
				// see ErrSessionAlreadyActive until the session is explicitly stopped.
				prevStatus := conn.Status
				conn.Unplug()
				if conn.Status != prevStatus && e.OnConnectorStatusChanged != nil {
					e.OnConnectorStatusChanged(id, conn.Status)
				}
			}
		}
	}

	prevStatus := c.Status
	_ = c.PlugIn()

	// Check for a pending remote start that is now consumable.
	if pending, exists := e.pendingRemoteStarts[connectorID]; exists {
		if time.Now().Before(pending.Expiry) {
			_ = e.startSessionLocked(connectorID, pending.TransactionID, pending.MaxEnergy, pending.IDTag, pending.ChargingProfile)
		}
		delete(e.pendingRemoteStarts, connectorID)
	}

	if c.Status != prevStatus && e.OnConnectorStatusChanged != nil {
		e.OnConnectorStatusChanged(connectorID, c.Status)
	}
}

// Unplug simulates an EV disconnecting. Stops any active session first.
func (e *Engine) Unplug(connectorID int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.unplugConnectorLocked(connectorID)
}

func (e *Engine) unplugConnectorLocked(connectorID int) {
	c, ok := e.connectors[connectorID]
	if !ok {
		return
	}

	if _, hasSession := e.sessions[connectorID]; hasSession {
		e.stopSessionLocked(connectorID, "EVDisconnected")
	}

	prevStatus := c.Status
	c.Unplug()

	if c.Status != prevStatus && e.OnConnectorStatusChanged != nil {
		e.OnConnectorStatusChanged(connectorID, c.Status)
	}
}

// StartSession begins a charging session on the given connector.
// When timeout > 0 and the connector is not plugged in, stores a PendingRemoteStart
// that will be consumed when the EV connects within the timeout window.
func (e *Engine) StartSession(connectorID, transactionID int, maxEnergy float64, idTag *string, timeout int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.expireReservations()

	c, ok := e.connectors[connectorID]
	if !ok {
		return ErrConnectorNotFound
	}

	// Reservation compatibility check.
	if res, ok := e.findReservationForConnector(connectorID); ok {
		if !e.idTagMatchesReservation(idTag, res) {
			return ErrInvalidState
		}
	}

	if !c.IsPluggedIn {
		if timeout > 0 {
			e.pendingRemoteStarts[connectorID] = &PendingRemoteStart{
				TransactionID: transactionID,
				MaxEnergy:     maxEnergy,
				IDTag:         idTag,
				Expiry:        time.Now().Add(time.Duration(timeout) * time.Second),
			}
			return nil
		}
		return ErrNotPluggedIn
	}

	if c.Status != StateAvailable && c.Status != StatePreparing {
		return ErrInvalidState
	}

	if !e.multiEVSEMode {
		if len(e.sessions) > 0 {
			return ErrSessionAlreadyActive
		}
	} else {
		if _, exists := e.sessions[connectorID]; exists {
			return ErrSessionAlreadyActive
		}
	}

	return e.startSessionLocked(connectorID, transactionID, maxEnergy, idTag, nil)
}

func (e *Engine) startSessionLocked(connectorID, transactionID int, maxEnergy float64, idTag *string, profile *ChargingProfile) error {
	c := e.connectors[connectorID]

	if res, ok := e.findReservationForConnector(connectorID); ok {
		delete(e.reservations, res.ReservationID)
		c.ClearReservation()
	}

	if e.multiEVSEMode {
		e.energyMeters[connectorID] = NewEnergyMeter()
	}

	session := NewSession(connectorID, transactionID, maxEnergy, idTag, nil)
	session.RemoteStartChargingProfile = profile
	e.sessions[connectorID] = session

	meter := e.getEnergyMeterLocked(connectorID)
	meter.IsCharging = true

	if err := c.StartCharging(); err != nil {
		return err
	}

	if e.OnSessionStarted != nil {
		idTag := session.IDTag
		meterStart := meter.Value
		resID := session.ReservationID
		e.OnSessionStarted(connectorID, idTag, meterStart, resID)
	}
	if e.OnConnectorStatusChanged != nil {
		e.OnConnectorStatusChanged(connectorID, c.Status)
	}
	return nil
}

// StopSession stops the active session on connectorID. If connectorID is nil,
// stops the first active session found.
func (e *Engine) StopSession(connectorID *int, reason string) *StoppedSessionInfo {
	e.mu.Lock()
	defer e.mu.Unlock()

	if connectorID != nil {
		return e.stopSessionLocked(*connectorID, reason)
	}
	for id := range e.sessions {
		return e.stopSessionLocked(id, reason)
	}
	return nil
}

func (e *Engine) stopSessionLocked(connectorID int, reason string) *StoppedSessionInfo {
	session, ok := e.sessions[connectorID]
	if !ok {
		return nil
	}

	meter := e.getEnergyMeterLocked(connectorID)
	info := &StoppedSessionInfo{
		TransactionID: session.TransactionID,
		ConnectorID:   connectorID,
		EnergyCharged: session.EnergyCharged,
		IDTag:         session.IDTag,
		MeterStop:     meter.Value,
		Reason:        reason,
		MeterHistory:  session.MeterHistory,
		ReservationID: session.ReservationID,
	}
	e.LastStoppedSession = info

	delete(e.sessions, connectorID)
	meter.IsCharging = false
	if e.multiEVSEMode {
		delete(e.energyMeters, connectorID)
	}

	c := e.connectors[connectorID]
	if c != nil {
		_ = c.StopCharging()
		if e.OnSessionStopped != nil {
			e.OnSessionStopped(connectorID, info)
		}
		if e.OnConnectorStatusChanged != nil {
			e.OnConnectorStatusChanged(connectorID, c.Status)
		}
	}

	// Apply any deferred availability change.
	if change, ok := e.pendingAvailabilityChanges[connectorID]; ok {
		delete(e.pendingAvailabilityChanges, connectorID)
		e.setAvailabilityLocked(connectorID, change)
	}

	return info
}

// SuspendEV transitions the connector from Charging to SuspendedEV.
func (e *Engine) SuspendEV(connectorID int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.connectors[connectorID]
	if !ok {
		return ErrConnectorNotFound
	}
	if err := c.SuspendEV(); err != nil {
		return err
	}
	meter := e.getEnergyMeterLocked(connectorID)
	meter.IsCharging = false

	if e.OnConnectorStatusChanged != nil {
		e.OnConnectorStatusChanged(connectorID, c.Status)
	}
	return nil
}

// ResumeCharging transitions SuspendedEV or SuspendedEVSE → Charging.
func (e *Engine) ResumeCharging(connectorID int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	c, ok := e.connectors[connectorID]
	if !ok {
		return ErrConnectorNotFound
	}
	if err := c.ResumeCharging(); err != nil {
		return err
	}
	meter := e.getEnergyMeterLocked(connectorID)
	meter.IsCharging = true

	if e.OnConnectorStatusChanged != nil {
		e.OnConnectorStatusChanged(connectorID, c.Status)
	}
	return nil
}

// SetConnectorAvailability returns "accepted", "scheduled", or "rejected".
func (e *Engine) SetConnectorAvailability(id int, availType string) string {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.expireReservations()

	if _, ok := e.connectors[id]; !ok {
		return "rejected"
	}

	if _, hasSession := e.sessions[id]; hasSession {
		e.pendingAvailabilityChanges[id] = availType
		return "scheduled"
	}

	return e.setAvailabilityLocked(id, availType)
}

func (e *Engine) setAvailabilityLocked(id int, availType string) string {
	c := e.connectors[id]
	prevStatus := c.Status
	switch availType {
	case "Inoperative":
		c.SetUnavailable()
	case "Operative":
		c.SetOperative()
	default:
		return "rejected"
	}
	if c.Status != prevStatus && e.OnConnectorStatusChanged != nil {
		e.OnConnectorStatusChanged(id, c.Status)
	}
	return "accepted"
}

// ReserveConnector returns "accepted", "occupied", "faulted", "unavailable", or "rejected".
func (e *Engine) ReserveConnector(connectorID, reservationID int, idTag string, expiry time.Time, parentIDTag *string) string {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.expireReservations()

	c, ok := e.connectors[connectorID]
	if !ok {
		return "rejected"
	}
	if c.Status == StateFaulted {
		return "faulted"
	}
	if c.Status == StateUnavailable {
		return "unavailable"
	}
	if _, hasSession := e.sessions[connectorID]; hasSession {
		return "occupied"
	}
	if c.IsPluggedIn {
		return "occupied"
	}
	// No duplicate reservation IDs.
	for id := range e.reservations {
		if id == reservationID {
			return "rejected"
		}
	}
	// No existing reservation on this connector.
	for _, res := range e.reservations {
		if res.ConnectorID == connectorID {
			return "occupied"
		}
	}

	e.reservations[reservationID] = &Reservation{
		ReservationID: reservationID,
		ConnectorID:   connectorID,
		IDTag:         idTag,
		ExpiryDate:    expiry,
		ParentIDTag:   parentIDTag,
	}
	prevStatus := c.Status
	c.SetReserved()
	if c.Status != prevStatus && e.OnConnectorStatusChanged != nil {
		e.OnConnectorStatusChanged(connectorID, c.Status)
	}
	return "accepted"
}

// CancelReservation returns "accepted" or "rejected".
func (e *Engine) CancelReservation(reservationID int) string {
	e.mu.Lock()
	defer e.mu.Unlock()

	res, ok := e.reservations[reservationID]
	if !ok {
		return "rejected"
	}

	connectorID := res.ConnectorID
	delete(e.reservations, reservationID)

	if c, ok := e.connectors[connectorID]; ok {
		prevStatus := c.Status
		c.ClearReservation()
		if c.Status != prevStatus && e.OnConnectorStatusChanged != nil {
			e.OnConnectorStatusChanged(connectorID, c.Status)
		}
	}
	return "accepted"
}

// GetSessionInfo returns details for all active sessions.
func (e *Engine) GetSessionInfo() []SessionDetail {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]SessionDetail, 0, len(e.sessions))
	for connID, s := range e.sessions {
		meter := e.getEnergyMeterReadLocked(connID)
		result = append(result, SessionDetail{
			TransactionID: s.TransactionID,
			ConnectorID:   connID,
			EnergyCharged: s.EnergyCharged,
			StateOfCharge: s.StateOfCharge,
			MaxEnergy:     s.MaxEnergy,
			StartTime:     s.StartTime,
			IDTag:         s.IDTag,
			IsCharging:    meter != nil && meter.IsCharging,
		})
	}
	return result
}

// SetActiveTransaction updates the transaction ID for a session after CSMS assigns it.
func (e *Engine) SetActiveTransaction(connectorID, transactionID int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.sessions[connectorID]; ok {
		s.TransactionID = transactionID
	}
}

// ClearActiveTransaction clears the transaction ID on session stop (called by OCPP layer).
func (e *Engine) ClearActiveTransaction(connectorID int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.sessions[connectorID]; ok {
		s.TransactionID = 0
	}
}

// GetActiveTransactionID returns the transaction ID for the active session, or nil.
func (e *Engine) GetActiveTransactionID(connectorID int) *int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if s, ok := e.sessions[connectorID]; ok && s.TransactionID != 0 {
		id := s.TransactionID
		return &id
	}
	return nil
}

// GetConnectorByTransaction returns the connectorID for a given transactionID, or nil.
func (e *Engine) GetConnectorByTransaction(transactionID int) *int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for id, s := range e.sessions {
		if s.TransactionID == transactionID {
			cid := id
			return &cid
		}
	}
	return nil
}

// GetSession returns the active session for a connector, or nil.
func (e *Engine) GetSession(connectorID int) *Session {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.sessions[connectorID]
}

// GetEnergyMeter returns the energy meter for a connector (for read-only use).
func (e *Engine) GetEnergyMeter(connectorID int) *EnergyMeter {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.getEnergyMeterReadLocked(connectorID)
}

// GetLastStoppedSession returns info about the most recently stopped session.
func (e *Engine) GetLastStoppedSession() *StoppedSessionInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.LastStoppedSession
}

// GetConnectorStatus returns the connector status as a string (for EngineView interface).
func (e *Engine) GetConnectorStatus(connectorID int) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if c, ok := e.connectors[connectorID]; ok {
		return string(c.Status)
	}
	return ""
}

// GetMeterSnapshot returns (meterReading, transactionID) for a connector.
func (e *Engine) GetMeterSnapshot(connectorID int) (float64, int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	meter := e.getEnergyMeterReadLocked(connectorID)
	reading := 0.0
	if meter != nil {
		reading = meter.Value
	}
	txID := 0
	if s, ok := e.sessions[connectorID]; ok {
		txID = s.TransactionID
	}
	return reading, txID
}

// Simulate runs one simulation tick, advancing energy meters for all active sessions.
// Called by the simulation loop goroutine. Acquires the write lock.
func (e *Engine) Simulate(intervalSeconds float64) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.expireReservations()

	for connectorID, session := range e.sessions {
		c := e.connectors[connectorID]
		meter := e.getEnergyMeterLocked(connectorID)
		if c == nil || meter == nil || !meter.IsCharging {
			continue
		}

		effectiveCurrent := c.Current
		if e.GetLimit != nil {
			if limit := e.GetLimit(connectorID, session.TransactionID); limit != nil {
				effectiveCurrent = min(c.Current, *limit)
			}
		}

		if effectiveCurrent == 0 && c.Status == StateCharging {
			_ = c.SuspendEVSE()
			if e.OnConnectorStatusChanged != nil {
				e.OnConnectorStatusChanged(connectorID, c.Status)
			}
			meter.IsCharging = false
			continue
		} else if effectiveCurrent > 0 && c.Status == StateSuspendedEVSE {
			_ = c.ResumeCharging()
			if e.OnConnectorStatusChanged != nil {
				e.OnConnectorStatusChanged(connectorID, c.Status)
			}
			meter.IsCharging = true
		}

		// Calculate incremental Wh for this tick.
		prevMeterValue := meter.Value
		meter.Update(c.Voltage, effectiveCurrent, c.Phase, intervalSeconds)
		deltaWh := meter.Value - prevMeterValue
		session.UpdateEnergy(deltaWh)

		// Check max charge reached.
		if !session.MaxChargeReached && session.MaxEnergy > 0 && session.EnergyCharged >= session.MaxEnergy {
			session.MaxChargeReached = true
			meter.IsCharging = false
			_ = c.SuspendEV()
			if e.OnConnectorStatusChanged != nil {
				e.OnConnectorStatusChanged(connectorID, c.Status)
			}
		}
	}
}

// getEnergyMeterLocked returns the meter for a connector (caller holds write lock).
func (e *Engine) getEnergyMeterLocked(connectorID int) *EnergyMeter {
	if e.multiEVSEMode {
		if m, ok := e.energyMeters[connectorID]; ok {
			return m
		}
		m := NewEnergyMeter()
		e.energyMeters[connectorID] = m
		return m
	}
	return e.globalMeter
}

// getEnergyMeterReadLocked returns the meter for a connector (caller holds read lock).
func (e *Engine) getEnergyMeterReadLocked(connectorID int) *EnergyMeter {
	if e.multiEVSEMode {
		return e.energyMeters[connectorID]
	}
	return e.globalMeter
}

func (e *Engine) expireReservations() {
	now := time.Now()
	for id, res := range e.reservations {
		if res.IsExpired(now) {
			connectorID := res.ConnectorID
			delete(e.reservations, id)
			if c, ok := e.connectors[connectorID]; ok {
				c.ClearReservation()
			}
			if e.OnReservationExpired != nil {
				e.OnReservationExpired(id, connectorID)
			}
		}
	}
}

func (e *Engine) findReservationForConnector(connectorID int) (*Reservation, bool) {
	for _, res := range e.reservations {
		if res.ConnectorID == connectorID {
			return res, true
		}
	}
	return nil, false
}

func (e *Engine) idTagMatchesReservation(idTag *string, res *Reservation) bool {
	if idTag == nil {
		return false
	}
	if *idTag == res.IDTag {
		return true
	}
	if res.ParentIDTag != nil && *idTag == *res.ParentIDTag {
		return true
	}
	return false
}

// SetIDTag sets the IDTag on a connector, returning an error if not found.
func (e *Engine) SetIDTag(connectorID int, tag string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.connectors[connectorID]
	if !ok {
		return ErrConnectorNotFound
	}
	c.IDTag = &tag
	return nil
}

// ClearIDTag clears the IDTag on a connector, returning an error if not found.
func (e *Engine) ClearIDTag(connectorID int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.connectors[connectorID]
	if !ok {
		return ErrConnectorNotFound
	}
	c.IDTag = nil
	return nil
}

// GetReservation returns the reservation for a connector, or nil.
func (e *Engine) GetReservation(connectorID int) *Reservation {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, res := range e.reservations {
		if res.ConnectorID == connectorID {
			return res
		}
	}
	return nil
}
