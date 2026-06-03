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
	// Called from Simulate() while the write lock is held — must NOT call back into the engine.
	// Returns nil when no limit applies (use connector's full current).
	GetLimit func(connectorID int, transactionID int, voltage float64, phases int, txStart *time.Time) *float64

	// Engine event callbacks — invoked AFTER the engine write lock is released.
	// Implementations may safely call back into the engine from these callbacks.
	OnSessionStarted         func(connectorID int, idTag *string, meterStart float64, reservationID *int)
	OnSessionStopped         func(connectorID int, info *StoppedSessionInfo)
	OnConnectorStatusChanged func(connectorID int, status ConnectorState)
	OnConnectorPlugChanged   func(connectorID int, isPluggedIn bool)
	OnConnectorIDTagChanged  func(connectorID int, idTag *string)
	OnConnectorParamsChanged func(connectorID int, voltage, current float64, phase int)
	OnTransactionIDChanged   func(connectorID, transactionID int)
	OnReservationExpired     func(reservationID, connectorID int)
}

// PendingRemoteStartDetail is a read-only view of a pending remote start.
type PendingRemoteStartDetail struct {
	ConnectorID   int
	TransactionID int
	IDTag         *string
	Expiry        time.Time
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

	c, ok := e.connectors[id]
	if !ok {
		e.mu.Unlock()
		return ErrConnectorNotFound
	}

	if voltage != nil {
		if *voltage < MinVoltage || *voltage > MaxVoltage {
			e.mu.Unlock()
			return ErrInvalidVoltage
		}
		c.Voltage = *voltage
	}
	if current != nil {
		if *current < MinCurrent || *current > MaxCurrent {
			e.mu.Unlock()
			return ErrInvalidCurrent
		}
		c.Current = *current
	}
	if phase != nil {
		if *phase != 1 && *phase != 3 {
			e.mu.Unlock()
			return ErrInvalidPhase
		}
		c.Phase = *phase
	}

	var cb func()
	if e.OnConnectorParamsChanged != nil {
		f := e.OnConnectorParamsChanged
		v, amp, ph := c.Voltage, c.Current, c.Phase
		cb = func() { f(id, v, amp, ph) }
	}
	e.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil
}

// GetConnector returns a snapshot copy of the connector, or nil if not found.
func (e *Engine) GetConnector(id int) *Connector {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if c, ok := e.connectors[id]; ok {
		copy := *c
		return &copy
	}
	return nil
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

	callbacks := e.expireReservations()

	c, ok := e.connectors[connectorID]
	if !ok {
		e.mu.Unlock()
		for _, cb := range callbacks {
			cb()
		}
		return
	}

	if !e.multiEVSEMode {
		for id, conn := range e.connectors {
			if id != connectorID && conn.IsPluggedIn {
				// Only flip the physical cable state; don't stop the active session.
				// The session stays alive so StartSession on another connector will
				// see ErrSessionAlreadyActive until the session is explicitly stopped.
				wasPlugged := conn.IsPluggedIn
				prevStatus := conn.Status
				conn.Unplug()
				e.appendConnectorPlugChangedCallback(id, wasPlugged, &callbacks)
				if conn.Status != prevStatus && e.OnConnectorStatusChanged != nil {
					cb := e.OnConnectorStatusChanged
					status := conn.Status
					cbID := id
					callbacks = append(callbacks, func() { cb(cbID, status) })
				}
			}
		}
	}

	wasPlugged := c.IsPluggedIn
	prevStatus := c.Status
	_ = c.PlugIn()
	e.appendConnectorPlugChangedCallback(connectorID, wasPlugged, &callbacks)

	// Notify the Preparing transition before potentially advancing to Charging.
	// This ensures callers always observe Preparing→Charging rather than only
	// seeing Charging (reported twice) when a pending remote start is present.
	if c.Status != prevStatus && e.OnConnectorStatusChanged != nil {
		cb := e.OnConnectorStatusChanged
		status := c.Status
		callbacks = append(callbacks, func() { cb(connectorID, status) })
	}
	prevStatus = c.Status // advance baseline so the block below does not re-fire

	// Check for a pending remote start that is now consumable.
	if pending, exists := e.pendingRemoteStarts[connectorID]; exists {
		if time.Now().Before(pending.Expiry) {
			_, pendingCBs := e.startSessionLocked(connectorID, pending.TransactionID, pending.IDTag, pending.ChargingProfile)
			callbacks = append(callbacks, pendingCBs...)
			// startSessionLocked already emitted a status callback for the new
			// state (Charging).  Advance prevStatus so the guard below does not
			// fire a duplicate notification.
			prevStatus = c.Status
		}
		delete(e.pendingRemoteStarts, connectorID)
	}

	// Emit a final status change notification only when the connector is still
	// at a state that has not yet been reported (e.g. stayed at Preparing when
	// no pending remote start existed).
	if c.Status != prevStatus && e.OnConnectorStatusChanged != nil {
		cb := e.OnConnectorStatusChanged
		status := c.Status
		callbacks = append(callbacks, func() { cb(connectorID, status) })
	}

	e.mu.Unlock()
	for _, cb := range callbacks {
		cb()
	}
}

// Unplug simulates an EV disconnecting. Stops any active session first.
func (e *Engine) Unplug(connectorID int) {
	e.mu.Lock()
	callbacks := e.unplugConnectorLocked(connectorID)
	e.mu.Unlock()
	for _, cb := range callbacks {
		cb()
	}
}

func (e *Engine) unplugConnectorLocked(connectorID int) []func() {
	c, ok := e.connectors[connectorID]
	if !ok {
		return nil
	}

	var callbacks []func()
	if _, hasSession := e.sessions[connectorID]; hasSession {
		_, stopCBs := e.stopSessionLocked(connectorID, "EVDisconnected")
		callbacks = append(callbacks, stopCBs...)
	}

	wasPlugged := c.IsPluggedIn
	prevStatus := c.Status
	c.Unplug()
	e.appendConnectorPlugChangedCallback(connectorID, wasPlugged, &callbacks)

	if c.Status != prevStatus && e.OnConnectorStatusChanged != nil {
		cb := e.OnConnectorStatusChanged
		status := c.Status
		callbacks = append(callbacks, func() { cb(connectorID, status) })
	}
	return callbacks
}

// StartSession begins a charging session on the given connector.
// The engine's configured EV battery capacity is always authoritative for SoC
// tracking and full-charge suspension.
// When timeout > 0 and the connector is not plugged in, stores a PendingRemoteStart
// that will be consumed when the EV connects within the timeout window.
func (e *Engine) StartSession(connectorID, transactionID int, idTag *string, timeout int) error {
	e.mu.Lock()

	var callbacks []func()
	unlock := func() {
		e.mu.Unlock()
		for _, cb := range callbacks {
			cb()
		}
	}

	callbacks = append(callbacks, e.expireReservations()...)

	c, ok := e.connectors[connectorID]
	if !ok {
		unlock()
		return ErrConnectorNotFound
	}

	// Reservation compatibility check.
	if res, ok := e.findReservationForConnector(connectorID); ok {
		if !e.idTagMatchesReservation(idTag, res) {
			unlock()
			return ErrInvalidState
		}
	}

	if !c.IsPluggedIn {
		if timeout > 0 {
			e.pendingRemoteStarts[connectorID] = &PendingRemoteStart{
				TransactionID: transactionID,
				IDTag:         idTag,
				Expiry:        time.Now().Add(time.Duration(timeout) * time.Second),
			}
			unlock()
			return nil
		}
		unlock()
		return ErrNotPluggedIn
	}

	if c.Status != StateAvailable && c.Status != StatePreparing {
		unlock()
		return ErrInvalidState
	}

	if !e.multiEVSEMode {
		if len(e.sessions) > 0 {
			unlock()
			return ErrSessionAlreadyActive
		}
	} else {
		if _, exists := e.sessions[connectorID]; exists {
			unlock()
			return ErrSessionAlreadyActive
		}
	}

	err, sessionCBs := e.startSessionLocked(connectorID, transactionID, idTag, nil)
	callbacks = append(callbacks, sessionCBs...)
	unlock()
	return err
}

// startSessionLocked performs the session start while the write lock is held.
// It returns any error and a list of callbacks to invoke AFTER the lock is released.
func (e *Engine) startSessionLocked(connectorID, transactionID int, idTag *string, profile *ChargingProfile) (error, []func()) {
	c := e.connectors[connectorID]

	if res, ok := e.findReservationForConnector(connectorID); ok {
		// Only consume the reservation when the idTag matches. A remote start
		// with a mismatched idTag must not silently take a reservation that
		// belongs to someone else.
		if !e.idTagMatchesReservation(idTag, res) {
			return ErrInvalidState, nil
		}
		delete(e.reservations, res.ReservationID)
		c.ClearReservation()
	}

	if e.multiEVSEMode {
		e.energyMeters[connectorID] = NewEnergyMeter()
	}

	session := NewSession(connectorID, transactionID, e.EVBatteryCapacity, idTag, nil)
	session.RemoteStartChargingProfile = profile
	e.sessions[connectorID] = session

	meter := e.getEnergyMeterLocked(connectorID)
	meter.IsCharging = true

	if err := c.StartCharging(); err != nil {
		return err, nil
	}

	var callbacks []func()
	if e.OnSessionStarted != nil {
		cb := e.OnSessionStarted
		cbIDTag := session.IDTag
		meterStart := meter.Value
		resID := session.ReservationID
		callbacks = append(callbacks, func() { cb(connectorID, cbIDTag, meterStart, resID) })
	}
	if e.OnConnectorStatusChanged != nil {
		cb := e.OnConnectorStatusChanged
		status := c.Status
		callbacks = append(callbacks, func() { cb(connectorID, status) })
	}
	return nil, callbacks
}

// StopSession stops the active session on connectorID. If connectorID is nil,
// stops the first active session found.
func (e *Engine) StopSession(connectorID *int, reason string) *StoppedSessionInfo {
	e.mu.Lock()

	var info *StoppedSessionInfo
	var callbacks []func()
	if connectorID != nil {
		info, callbacks = e.stopSessionLocked(*connectorID, reason)
	} else {
		for id := range e.sessions {
			info, callbacks = e.stopSessionLocked(id, reason)
			break
		}
	}
	e.mu.Unlock()
	for _, cb := range callbacks {
		cb()
	}
	return info
}

// StopAllSessions stops every active session and returns the stopped session info objects.
func (e *Engine) StopAllSessions(reason string) []*StoppedSessionInfo {
	e.mu.Lock()
	ids := make([]int, 0, len(e.sessions))
	for id := range e.sessions {
		ids = append(ids, id)
	}

	infos := make([]*StoppedSessionInfo, 0, len(ids))
	var callbacks []func()
	for _, id := range ids {
		info, stopCBs := e.stopSessionLocked(id, reason)
		if info != nil {
			infos = append(infos, info)
		}
		callbacks = append(callbacks, stopCBs...)
	}
	e.mu.Unlock()
	for _, cb := range callbacks {
		cb()
	}
	return infos
}

// stopSessionLocked performs the session stop while the write lock is held.
// It returns the stopped session info and a list of callbacks to invoke AFTER the lock is released.
func (e *Engine) stopSessionLocked(connectorID int, reason string) (*StoppedSessionInfo, []func()) {
	session, ok := e.sessions[connectorID]
	if !ok {
		return nil, nil
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

	var callbacks []func()
	c := e.connectors[connectorID]
	if c != nil {
		_ = c.StopCharging()
		if e.OnSessionStopped != nil {
			cb := e.OnSessionStopped
			cbInfo := info
			callbacks = append(callbacks, func() { cb(connectorID, cbInfo) })
		}
		if e.OnConnectorStatusChanged != nil {
			cb := e.OnConnectorStatusChanged
			status := c.Status
			callbacks = append(callbacks, func() { cb(connectorID, status) })
		}
	}

	// Apply any deferred availability change.
	if change, ok := e.pendingAvailabilityChanges[connectorID]; ok {
		delete(e.pendingAvailabilityChanges, connectorID)
		extraCallbacks := e.setAvailabilityAndCollectCallbacks(connectorID, change)
		callbacks = append(callbacks, extraCallbacks...)
	}

	return info, callbacks
}

// SuspendEV transitions the connector from Charging to SuspendedEV.
func (e *Engine) SuspendEV(connectorID int) error {
	e.mu.Lock()

	c, ok := e.connectors[connectorID]
	if !ok {
		e.mu.Unlock()
		return ErrConnectorNotFound
	}
	if err := c.SuspendEV(); err != nil {
		e.mu.Unlock()
		return err
	}
	meter := e.getEnergyMeterLocked(connectorID)
	meter.IsCharging = false

	var cb func()
	if e.OnConnectorStatusChanged != nil {
		f := e.OnConnectorStatusChanged
		status := c.Status
		cb = func() { f(connectorID, status) }
	}
	e.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil
}

// ResumeCharging transitions SuspendedEV or SuspendedEVSE → Charging.
func (e *Engine) ResumeCharging(connectorID int) error {
	e.mu.Lock()

	c, ok := e.connectors[connectorID]
	if !ok {
		e.mu.Unlock()
		return ErrConnectorNotFound
	}
	if err := c.ResumeCharging(); err != nil {
		e.mu.Unlock()
		return err
	}
	meter := e.getEnergyMeterLocked(connectorID)
	meter.IsCharging = true

	var cb func()
	if e.OnConnectorStatusChanged != nil {
		f := e.OnConnectorStatusChanged
		status := c.Status
		cb = func() { f(connectorID, status) }
	}
	e.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil
}

// SetConnectorAvailability returns "accepted", "scheduled", or "rejected".
func (e *Engine) SetConnectorAvailability(id int, availType string) string {
	e.mu.Lock()

	callbacks := e.expireReservations()

	unlock := func() {
		e.mu.Unlock()
		for _, cb := range callbacks {
			cb()
		}
	}

	if _, ok := e.connectors[id]; !ok {
		unlock()
		return "rejected"
	}

	if _, hasSession := e.sessions[id]; hasSession {
		e.pendingAvailabilityChanges[id] = availType
		unlock()
		return "scheduled"
	}

	result, availCBs := e.setAvailabilityAndCollectCallbacksInner(id, availType)
	callbacks = append(callbacks, availCBs...)
	unlock()
	return result
}

func (e *Engine) setAvailabilityLocked(id int, availType string) string {
	result, _ := e.setAvailabilityAndCollectCallbacksInner(id, availType)
	return result
}

// setAvailabilityAndCollectCallbacks mutates availability state and returns callbacks
// to invoke after the lock is released.
func (e *Engine) setAvailabilityAndCollectCallbacks(id int, availType string) []func() {
	_, callbacks := e.setAvailabilityAndCollectCallbacksInner(id, availType)
	return callbacks
}

func (e *Engine) setAvailabilityAndCollectCallbacksInner(id int, availType string) (string, []func()) {
	c := e.connectors[id]
	prevStatus := c.Status
	switch availType {
	case "Inoperative":
		c.SetUnavailable()
	case "Operative":
		c.SetOperative()
	default:
		return "rejected", nil
	}
	var callbacks []func()
	if c.Status != prevStatus && e.OnConnectorStatusChanged != nil {
		cb := e.OnConnectorStatusChanged
		status := c.Status
		callbacks = append(callbacks, func() { cb(id, status) })
	}
	return "accepted", callbacks
}

// ReserveConnector returns "accepted", "occupied", "faulted", "unavailable", or "rejected".
func (e *Engine) ReserveConnector(connectorID, reservationID int, idTag string, expiry time.Time, parentIDTag *string) string {
	e.mu.Lock()

	callbacks := e.expireReservations()
	unlock := func() {
		e.mu.Unlock()
		for _, cb := range callbacks {
			cb()
		}
	}

	c, ok := e.connectors[connectorID]
	if !ok {
		unlock()
		return "rejected"
	}
	if c.Status == StateFaulted {
		unlock()
		return "faulted"
	}
	if c.Status == StateUnavailable {
		unlock()
		return "unavailable"
	}
	if _, hasSession := e.sessions[connectorID]; hasSession {
		unlock()
		return "occupied"
	}
	if c.IsPluggedIn {
		unlock()
		return "occupied"
	}
	// No duplicate reservation IDs.
	for id := range e.reservations {
		if id == reservationID {
			unlock()
			return "rejected"
		}
	}
	// No existing reservation on this connector.
	for _, res := range e.reservations {
		if res.ConnectorID == connectorID {
			unlock()
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
		f := e.OnConnectorStatusChanged
		status := c.Status
		callbacks = append(callbacks, func() { f(connectorID, status) })
	}
	unlock()
	return "accepted"
}

// CancelReservation returns "accepted" or "rejected".
func (e *Engine) CancelReservation(reservationID int) string {
	e.mu.Lock()

	res, ok := e.reservations[reservationID]
	if !ok {
		e.mu.Unlock()
		return "rejected"
	}

	connectorID := res.ConnectorID
	delete(e.reservations, reservationID)

	var cb func()
	if c, ok := e.connectors[connectorID]; ok {
		prevStatus := c.Status
		c.ClearReservation()
		if c.Status != prevStatus && e.OnConnectorStatusChanged != nil {
			f := e.OnConnectorStatusChanged
			status := c.Status
			cb = func() { f(connectorID, status) }
		}
	}
	e.mu.Unlock()
	if cb != nil {
		cb()
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
	var cb func()
	if s, ok := e.sessions[connectorID]; ok && s.TransactionID != transactionID {
		s.TransactionID = transactionID
		if e.OnTransactionIDChanged != nil {
			txID := transactionID
			cbConn := connectorID
			f := e.OnTransactionIDChanged
			cb = func() { f(cbConn, txID) }
		}
	}
	e.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// SetSessionChargingProfile sets the remote-start charging profile on an active session.
// Safe to call from any goroutine — acquires the write lock internally.
func (e *Engine) SetSessionChargingProfile(connectorID int, profile *ChargingProfile) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.sessions[connectorID]; ok {
		s.RemoteStartChargingProfile = profile
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

// GetSessionByTransaction returns a snapshot copy of the active session for a
// given transaction ID, along with its connector ID.
// MeterHistory is deep-copied to prevent callers from aliasing the internal slice.
func (e *Engine) GetSessionByTransaction(transactionID int) (int, *Session) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for connectorID, s := range e.sessions {
		if s.TransactionID != transactionID {
			continue
		}
		cp := *s
		if s.MeterHistory != nil {
			cp.MeterHistory = append([]MeterRecord(nil), s.MeterHistory...)
		}
		return connectorID, &cp
	}
	return 0, nil
}

// GetSession returns a snapshot copy of the active session, or nil.
// MeterHistory is deep-copied to prevent callers from aliasing the internal slice.
func (e *Engine) GetSession(connectorID int) *Session {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if s, ok := e.sessions[connectorID]; ok {
		cp := *s
		if s.MeterHistory != nil {
			cp.MeterHistory = append([]MeterRecord(nil), s.MeterHistory...)
		}
		return &cp
	}
	return nil
}

// SetPendingRemoteStartChargingProfile stores a charging profile in an existing
// PendingRemoteStart entry.  Called from the OCPP layer after StartSession has
// stored the pending entry (EV not yet connected).
func (e *Engine) SetPendingRemoteStartChargingProfile(connectorID int, profile *ChargingProfile) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if pending, ok := e.pendingRemoteStarts[connectorID]; ok {
		pending.ChargingProfile = profile
	}
}

// GetEnergyMeter returns a snapshot copy of the energy meter for a connector, or nil.
func (e *Engine) GetEnergyMeter(connectorID int) *EnergyMeter {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if m := e.getEnergyMeterReadLocked(connectorID); m != nil {
		copy := *m
		return &copy
	}
	return nil
}

// GetLastStoppedSession returns a snapshot copy of the most recently stopped session info.
// MeterHistory is deep-copied to prevent callers from aliasing the internal slice.
func (e *Engine) GetLastStoppedSession() *StoppedSessionInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.LastStoppedSession == nil {
		return nil
	}
	cp := *e.LastStoppedSession
	if e.LastStoppedSession.MeterHistory != nil {
		cp.MeterHistory = append([]MeterRecord(nil), e.LastStoppedSession.MeterHistory...)
	}
	return &cp
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

	callbacks := e.expireReservations()

	for connectorID, session := range e.sessions {
		c := e.connectors[connectorID]
		meter := e.getEnergyMeterLocked(connectorID)
		if c == nil || meter == nil || !meter.IsCharging {
			continue
		}

		effectiveCurrent := c.Current
		if e.GetLimit != nil {
			txStart := session.StartTime
			if limit := e.GetLimit(connectorID, session.TransactionID, c.Voltage, c.Phase, &txStart); limit != nil {
				effectiveCurrent = min(c.Current, *limit)
			}
		}

		if effectiveCurrent == 0 && c.Status == StateCharging {
			_ = c.SuspendEVSE()
			if e.OnConnectorStatusChanged != nil {
				cb := e.OnConnectorStatusChanged
				status := c.Status
				cbID := connectorID
				callbacks = append(callbacks, func() { cb(cbID, status) })
			}
			meter.IsCharging = false
			continue
		} else if effectiveCurrent > 0 && c.Status == StateSuspendedEVSE {
			_ = c.ResumeCharging()
			if e.OnConnectorStatusChanged != nil {
				cb := e.OnConnectorStatusChanged
				status := c.Status
				cbID := connectorID
				callbacks = append(callbacks, func() { cb(cbID, status) })
			}
			meter.IsCharging = true
		}

		// Calculate incremental Wh for this tick.
		prevMeterValue := meter.Value
		meter.Update(c.Voltage, effectiveCurrent, c.Phase, intervalSeconds)
		deltaWh := meter.Value - prevMeterValue
		if session.MaxEnergy > 0 {
			remainingWh := max(0.0, session.MaxEnergy-session.EnergyCharged)
			if deltaWh > remainingWh {
				deltaWh = remainingWh
				meter.Value = prevMeterValue + deltaWh
			}
		}
		session.UpdateEnergy(deltaWh)

		// Check max charge reached.
		if !session.MaxChargeReached && session.MaxEnergy > 0 && session.EnergyCharged >= session.MaxEnergy {
			session.MaxChargeReached = true
			meter.IsCharging = false
			_ = c.SuspendEV()
			if e.OnConnectorStatusChanged != nil {
				cb := e.OnConnectorStatusChanged
				status := c.Status
				cbID := connectorID
				callbacks = append(callbacks, func() { cb(cbID, status) })
			}
		}
	}

	e.mu.Unlock()
	for _, cb := range callbacks {
		cb()
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

// expireReservations removes expired reservations and returns callbacks to invoke
// after the lock is released.
func (e *Engine) expireReservations() []func() {
	now := time.Now()
	var callbacks []func()
	for id, res := range e.reservations {
		if res.IsExpired(now) {
			connectorID := res.ConnectorID
			delete(e.reservations, id)
			if c, ok := e.connectors[connectorID]; ok {
				prevStatus := c.Status
				c.ClearReservation()
				if c.Status != prevStatus && e.OnConnectorStatusChanged != nil {
					cb := e.OnConnectorStatusChanged
					status := c.Status
					callbacks = append(callbacks, func() { cb(connectorID, status) })
				}
			}
			if e.OnReservationExpired != nil {
				cb := e.OnReservationExpired
				cbID := id
				callbacks = append(callbacks, func() { cb(cbID, connectorID) })
			}
		}
	}
	return callbacks
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
	c, ok := e.connectors[connectorID]
	if !ok {
		e.mu.Unlock()
		return ErrConnectorNotFound
	}
	tagCopy := tag
	c.IDTag = &tagCopy
	var cb func()
	if e.OnConnectorIDTagChanged != nil {
		idTag := c.IDTag
		f := e.OnConnectorIDTagChanged
		cb = func() { f(connectorID, idTag) }
	}
	e.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil
}

// ClearIDTag clears the IDTag on a connector, returning an error if not found.
func (e *Engine) ClearIDTag(connectorID int) error {
	e.mu.Lock()
	c, ok := e.connectors[connectorID]
	if !ok {
		e.mu.Unlock()
		return ErrConnectorNotFound
	}
	c.IDTag = nil
	var cb func()
	if e.OnConnectorIDTagChanged != nil {
		f := e.OnConnectorIDTagChanged
		cb = func() { f(connectorID, nil) }
	}
	e.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil
}

// SetEVBatteryCapacity updates the configured EV battery capacity in Wh.
func (e *Engine) SetEVBatteryCapacity(capacityWh float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.EVBatteryCapacity = capacityWh
}

// NormalizeAfterReset clears transient control state and restores connectors to
// their post-boot physical state without dropping persistent configuration.
func (e *Engine) NormalizeAfterReset() {
	e.mu.Lock()
	clear(e.pendingRemoteStarts)
	clear(e.pendingAvailabilityChanges)

	var callbacks []func()
	for id, connector := range e.connectors {
		meter := e.getEnergyMeterLocked(id)
		if meter != nil {
			meter.IsCharging = false
		}

		if connector.Status == StateFaulted || connector.Status == StateUnavailable || connector.Status == StateReserved {
			continue
		}

		nextStatus := StateAvailable
		if connector.IsPluggedIn {
			nextStatus = StatePreparing
		}
		if connector.Status == nextStatus {
			continue
		}
		connector.Status = nextStatus
		if e.OnConnectorStatusChanged != nil {
			cb := e.OnConnectorStatusChanged
			status := connector.Status
			connectorID := id
			callbacks = append(callbacks, func() { cb(connectorID, status) })
		}
	}
	e.mu.Unlock()
	for _, cb := range callbacks {
		cb()
	}
}

// GetReservation returns the reservation for a connector, or nil.
func (e *Engine) GetReservation(connectorID int) *Reservation {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, res := range e.reservations {
		if res.ConnectorID == connectorID {
			copy := *res
			return &copy
		}
	}
	return nil
}

// ListReservations returns copies of all active reservations.
func (e *Engine) ListReservations() []Reservation {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]Reservation, 0, len(e.reservations))
	for _, res := range e.reservations {
		result = append(result, *res)
	}
	return result
}

// ListPendingRemoteStarts returns copies of pending remote starts awaiting plug-in.
func (e *Engine) ListPendingRemoteStarts() []PendingRemoteStartDetail {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]PendingRemoteStartDetail, 0, len(e.pendingRemoteStarts))
	for connectorID, pending := range e.pendingRemoteStarts {
		result = append(result, PendingRemoteStartDetail{
			ConnectorID:   connectorID,
			TransactionID: pending.TransactionID,
			IDTag:         pending.IDTag,
			Expiry:        pending.Expiry,
		})
	}
	return result
}

func (e *Engine) appendConnectorPlugChangedCallback(connectorID int, wasPluggedIn bool, callbacks *[]func()) {
	c := e.connectors[connectorID]
	if c == nil || c.IsPluggedIn == wasPluggedIn || e.OnConnectorPlugChanged == nil {
		return
	}
	plugged := c.IsPluggedIn
	cb := e.OnConnectorPlugChanged
	*callbacks = append(*callbacks, func() { cb(connectorID, plugged) })
}
