# Plan 01 — Domain Core

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement all engine domain structs, the connector state machine, and full unit test coverage — no I/O, no HTTP, no goroutines.

**Architecture:** All domain logic lives in `internal/engine`. The `Engine` struct owns all mutable state behind a `sync.RWMutex`. Connector state transitions are a lookup table. No external dependencies except `testify`.

**Tech Stack:** Go 1.22, `github.com/stretchr/testify v1.9.0`

---

## File Map

| File | Responsibility |
|------|----------------|
| `go.mod` | Module definition |
| `internal/engine/state.go` | `ConnectorState` enum + 15-entry transition table + `applyTransition()` |
| `internal/engine/connector.go` | `Connector` struct + state machine methods + bypass transitions |
| `internal/engine/energy_meter.go` | `EnergyMeter` — cumulative Wh accumulator |
| `internal/engine/session.go` | `Session`, `MeterRecord` — charging session with SoC |
| `internal/engine/reservation.go` | `Reservation` with expiry |
| `internal/engine/engine.go` | `Engine` struct + all methods + sentinel errors + shared types (`MeterRecord`, `StoppedSessionInfo`, `PendingRemoteStart`, `ChargingProfile`, `SessionDetail`) |
| `internal/engine/engine_test.go` | All unit tests |

---

## Task 1: Initialize Go Module

**Files:**
- Create: `go.mod`

- [ ] **Step 1: Initialize the module**

```bash
cd /path/to/chargeghost-core
go mod init github.com/chargeghost/engine
```

Expected output: creates `go.mod` with `module github.com/chargeghost/engine` and `go 1.22`.

- [ ] **Step 2: Add testify dependency**

```bash
go get github.com/stretchr/testify@v1.9.0
go mod tidy
```

Expected: `go.mod` and `go.sum` updated.

- [ ] **Step 3: Commit**

```bash
git init
git add go.mod go.sum
git commit -m "chore: initialize go module"
```

---

## Task 2: Connector State Machine

**Files:**
- Create: `internal/engine/state.go`

- [ ] **Step 1: Write the failing test**

Create `internal/engine/engine_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/engine/... -run TestApplyTransition -v
```

Expected: compile error — `engine` package does not exist yet.

- [ ] **Step 3: Implement state.go**

Create `internal/engine/state.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/engine/... -run TestApplyTransition -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/state.go internal/engine/engine_test.go
git commit -m "feat(engine): connector state machine transition table"
```

---

## Task 3: Connector Struct

**Files:**
- Create: `internal/engine/connector.go`
- Modify: `internal/engine/engine_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/engine/engine_test.go`:

```go
func TestConnector_PlugIn_FromAvailable(t *testing.T) {
    c := engine.NewConnector(1, 230.0, 32.0, 1)
    assert.Equal(t, engine.StateAvailable, c.Status)

    err := c.PlugIn()
    require.NoError(t, err)
    assert.Equal(t, engine.StatePreparing, c.Status)
    assert.True(t, c.IsPluggedIn)
}

func TestConnector_PlugIn_FromReserved(t *testing.T) {
    c := engine.NewConnector(1, 230.0, 32.0, 1)
    c.SetReserved()
    assert.Equal(t, engine.StateReserved, c.Status)

    err := c.PlugIn()
    require.NoError(t, err)
    assert.Equal(t, engine.StatePreparing, c.Status)
    assert.True(t, c.IsPluggedIn)
}

func TestConnector_PlugIn_FromUnavailable_DoesNotChangeStatus(t *testing.T) {
    c := engine.NewConnector(1, 230.0, 32.0, 1)
    c.SetUnavailable()
    assert.Equal(t, engine.StateUnavailable, c.Status)

    err := c.PlugIn()
    require.NoError(t, err)
    assert.Equal(t, engine.StateUnavailable, c.Status) // status unchanged
    assert.True(t, c.IsPluggedIn)
}

func TestConnector_Unplug_RestoresPersistentStatus(t *testing.T) {
    c := engine.NewConnector(1, 230.0, 32.0, 1)
    c.SetUnavailable()
    _ = c.PlugIn()

    c.Unplug()
    assert.Equal(t, engine.StateUnavailable, c.Status) // restored to persistent
    assert.False(t, c.IsPluggedIn)
}

func TestConnector_BypassTransitions(t *testing.T) {
    c := engine.NewConnector(1, 230.0, 32.0, 1)

    c.SetUnavailable()
    assert.Equal(t, engine.StateUnavailable, c.Status)
    assert.Equal(t, engine.StateUnavailable, c.PersistentStatus)

    // SetUnavailable is no-op when already unavailable
    c.SetUnavailable()
    assert.Equal(t, engine.StateUnavailable, c.Status)

    c.SetOperative()
    assert.Equal(t, engine.StateAvailable, c.Status)
    assert.Equal(t, engine.StateAvailable, c.PersistentStatus)

    c.SetReserved()
    assert.Equal(t, engine.StateReserved, c.Status)
    assert.Equal(t, engine.StateReserved, c.PersistentStatus)

    c.ClearReservation()
    assert.Equal(t, engine.StateAvailable, c.Status)
}

func TestConnector_SetReserved_WhenPluggedIn_SetsPreparing(t *testing.T) {
    c := engine.NewConnector(1, 230.0, 32.0, 1)
    _ = c.PlugIn()
    assert.Equal(t, engine.StatePreparing, c.Status)

    c.SetReserved()
    assert.Equal(t, engine.StatePreparing, c.Status) // still preparing since plugged in
    assert.Equal(t, engine.StateReserved, c.PersistentStatus)
}

func TestConnector_Validation(t *testing.T) {
    assert.Panics(t, func() { engine.NewConnector(1, 50.0, 32.0, 1) })   // voltage too low
    assert.Panics(t, func() { engine.NewConnector(1, 230.0, 3.0, 1) })   // current too low
    assert.Panics(t, func() { engine.NewConnector(1, 230.0, 32.0, 2) })  // phase = 2 invalid
    assert.NotPanics(t, func() { engine.NewConnector(1, 230.0, 32.0, 3) }) // phase = 3 valid
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/engine/... -run TestConnector -v
```

Expected: compile error — `NewConnector` not defined.

- [ ] **Step 3: Implement connector.go**

Create `internal/engine/connector.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/engine/... -run TestConnector -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/connector.go internal/engine/engine_test.go
git commit -m "feat(engine): Connector struct with state machine"
```

---

## Task 4: Energy Meter

**Files:**
- Create: `internal/engine/energy_meter.go`
- Modify: `internal/engine/engine_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/engine/engine_test.go`:

```go
func TestEnergyMeter_AccumulatesWhenCharging(t *testing.T) {
    m := engine.NewEnergyMeter()
    m.IsCharging = true

    // 230V × 32A × 1 phase × 3600s = 7360 Wh
    m.Update(230.0, 32.0, 1, 3600.0)
    assert.InDelta(t, 7360.0, m.Value, 0.001)
}

func TestEnergyMeter_DoesNotAccumulateWhenNotCharging(t *testing.T) {
    m := engine.NewEnergyMeter()
    m.IsCharging = false
    m.Update(230.0, 32.0, 1, 3600.0)
    assert.Equal(t, 0.0, m.Value)
}

func TestEnergyMeter_ThreePhasePower(t *testing.T) {
    m := engine.NewEnergyMeter()
    m.IsCharging = true
    // 400V × 16A × 3 phase × 3600s = 19200 Wh
    m.Update(400.0, 16.0, 3, 3600.0)
    assert.InDelta(t, 19200.0, m.Value, 0.001)
}

func TestEnergyMeter_CumulativeAcrossUpdates(t *testing.T) {
    m := engine.NewEnergyMeter()
    m.IsCharging = true
    m.Update(230.0, 32.0, 1, 1800.0) // 30 min
    m.Update(230.0, 32.0, 1, 1800.0) // another 30 min
    assert.InDelta(t, 7360.0, m.Value, 0.001) // same as 1 hour
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/engine/... -run TestEnergyMeter -v
```

Expected: compile error — `NewEnergyMeter` not defined.

- [ ] **Step 3: Implement energy_meter.go**

Create `internal/engine/energy_meter.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/engine/... -run TestEnergyMeter -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/energy_meter.go internal/engine/engine_test.go
git commit -m "feat(engine): EnergyMeter Wh accumulator"
```

---

## Task 5: Session

**Files:**
- Create: `internal/engine/session.go`
- Modify: `internal/engine/engine_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/engine/engine_test.go`:

```go
func TestSession_SoCCalculation(t *testing.T) {
    s := engine.NewSession(1, -1, 55000.0, nil, nil)
    assert.Equal(t, 0.0, s.StateOfCharge)

    s.UpdateEnergy(5500.0) // 10% of 55 kWh
    assert.InDelta(t, 10.0, s.StateOfCharge, 0.001)
    assert.InDelta(t, 5500.0, s.EnergyCharged, 0.001)
}

func TestSession_EnergyCappedAtMaxEnergy(t *testing.T) {
    s := engine.NewSession(1, -1, 1000.0, nil, nil)
    s.UpdateEnergy(1500.0) // more than max
    assert.InDelta(t, 1000.0, s.EnergyCharged, 0.001)
    assert.InDelta(t, 100.0, s.StateOfCharge, 0.001)
}

func TestSession_NoSoCWhenMaxEnergyZero(t *testing.T) {
    s := engine.NewSession(1, -1, 0.0, nil, nil)
    s.UpdateEnergy(5000.0)
    assert.InDelta(t, 5000.0, s.EnergyCharged, 0.001)
    assert.Equal(t, 0.0, s.StateOfCharge) // SoC not tracked
}

func TestSession_MeterHistory_KeepsLast10(t *testing.T) {
    s := engine.NewSession(1, -1, 0.0, nil, nil)
    for i := 0; i < 15; i++ {
        s.RecordMeter(float64(i * 100))
    }
    assert.Len(t, s.MeterHistory, 10)
    assert.InDelta(t, 500.0, s.MeterHistory[0].Value, 0.001) // oldest kept
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/engine/... -run TestSession -v
```

Expected: compile error.

- [ ] **Step 3: Implement session.go**

Create `internal/engine/session.go`:

```go
package engine

import "time"

// Session represents an active charging session on a connector.
type Session struct {
    TransactionID              int
    ConnectorID                int
    StartTime                  time.Time
    EnergyCharged              float64  // Wh accumulated this session
    StateOfCharge              float64  // 0.0–100.0; 0 when MaxEnergy == 0
    MaxEnergy                  float64  // Wh battery capacity; 0 = no limit
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/engine/... -run TestSession -v
```

Expected: compile error — `ChargingProfile` and `MeterRecord` not defined yet. Proceed to Task 6.

---

## Task 6: Reservation

**Files:**
- Create: `internal/engine/reservation.go`
- Modify: `internal/engine/engine_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/engine/engine_test.go`:

```go
func TestReservation_IsExpired(t *testing.T) {
    past := time.Now().Add(-1 * time.Second)
    r := engine.Reservation{ReservationID: 1, ExpiryDate: past}
    assert.True(t, r.IsExpired(time.Now()))
}

func TestReservation_IsNotExpired(t *testing.T) {
    future := time.Now().Add(10 * time.Minute)
    r := engine.Reservation{ReservationID: 1, ExpiryDate: future}
    assert.False(t, r.IsExpired(time.Now()))
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/engine/... -run TestReservation -v
```

Expected: compile error.

- [ ] **Step 3: Implement reservation.go**

Create `internal/engine/reservation.go`:

```go
package engine

import "time"

// Reservation represents a time-bounded slot on a connector.
type Reservation struct {
    ReservationID int
    ConnectorID   int
    IDTag         string
    ExpiryDate    time.Time
    ParentIDTag   *string
}

// IsExpired returns true when the reservation has passed its expiry time.
func (r *Reservation) IsExpired(now time.Time) bool {
    return !now.Before(r.ExpiryDate)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/engine/... -run TestReservation -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/reservation.go internal/engine/engine_test.go
git commit -m "feat(engine): Reservation with expiry"
```

---

## Task 7: Engine Core

**Files:**
- Create: `internal/engine/engine.go`
- Modify: `internal/engine/engine_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/engine/engine_test.go`:

```go
func TestEngine_AddConnector(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    c := e.AddConnector(230.0, 32.0, 1)
    assert.Equal(t, 1, c.ID)
    assert.Equal(t, engine.StateAvailable, c.Status)

    c2 := e.AddConnector(400.0, 16.0, 3)
    assert.Equal(t, 2, c2.ID)
}

func TestEngine_RemoveConnector_LastConnectorFails(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)
    err := e.RemoveConnector(1)
    assert.ErrorIs(t, err, engine.ErrLastConnector)
}

func TestEngine_RemoveConnector_WithActiveSessionFails(t *testing.T) {
    e := engine.NewEngine(true, 55000.0)
    e.AddConnector(230.0, 32.0, 1)
    e.AddConnector(230.0, 32.0, 1)
    e.PlugIn(1)
    err := e.StartSession(1, -1, 0.0, nil, 0)
    require.NoError(t, err)

    err = e.RemoveConnector(1)
    assert.ErrorIs(t, err, engine.ErrSessionActiveOnRemove)
}

func TestEngine_UpdateConnector_ValidationErrors(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)

    err := e.UpdateConnector(1, pf(50.0), nil, nil) // voltage too low
    assert.ErrorIs(t, err, engine.ErrInvalidVoltage)

    err = e.UpdateConnector(1, nil, pf(3.0), nil) // current too low
    assert.ErrorIs(t, err, engine.ErrInvalidCurrent)

    err = e.UpdateConnector(1, nil, nil, pi(2)) // phase = 2
    assert.ErrorIs(t, err, engine.ErrInvalidPhase)
}

func TestEngine_PlugIn_SingleEVSEMode_UnplugsOthers(t *testing.T) {
    e := engine.NewEngine(false, 55000.0) // single-EVSE
    e.AddConnector(230.0, 32.0, 1)
    e.AddConnector(230.0, 32.0, 1)

    e.PlugIn(1)
    assert.True(t, e.GetConnector(1).IsPluggedIn)

    e.PlugIn(2) // should auto-unplug connector 1
    assert.False(t, e.GetConnector(1).IsPluggedIn)
    assert.True(t, e.GetConnector(2).IsPluggedIn)
}

func TestEngine_StartSession_Basic(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)
    e.PlugIn(1)

    err := e.StartSession(1, -1, 0.0, nil, 0)
    require.NoError(t, err)

    sessions := e.GetSessionInfo()
    assert.Len(t, sessions, 1)
    assert.Equal(t, 1, sessions[0].ConnectorID)
}

func TestEngine_StartSession_NotPluggedIn(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)

    err := e.StartSession(1, -1, 0.0, nil, 0)
    assert.ErrorIs(t, err, engine.ErrNotPluggedIn)
}

func TestEngine_StartSession_SingleEVSE_FailsIfSessionActive(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)
    e.AddConnector(230.0, 32.0, 1)
    e.PlugIn(1)
    require.NoError(t, e.StartSession(1, -1, 0.0, nil, 0))

    e.PlugIn(2)
    err := e.StartSession(2, -1, 0.0, nil, 0)
    assert.ErrorIs(t, err, engine.ErrSessionAlreadyActive)
}

func TestEngine_StopSession(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)
    e.PlugIn(1)
    require.NoError(t, e.StartSession(1, -1, 0.0, nil, 0))

    info := e.StopSession(pi(1), "Local")
    require.NotNil(t, info)
    assert.Equal(t, 1, info.ConnectorID)
    assert.Equal(t, "Local", info.Reason)
    assert.Empty(t, e.GetSessionInfo())
}

func TestEngine_PendingRemoteStart(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)

    // Start session with timeout — stores pending start since not plugged in
    err := e.StartSession(1, 42, 0.0, nil, 30)
    require.NoError(t, err)
    assert.Empty(t, e.GetSessionInfo()) // not started yet

    // Plug in — should consume pending start
    e.PlugIn(1)
    sessions := e.GetSessionInfo()
    require.Len(t, sessions, 1)
    assert.Equal(t, 42, sessions[0].TransactionID)
}

func TestEngine_ReserveConnector(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)

    expiry := time.Now().Add(10 * time.Minute)
    result := e.ReserveConnector(1, 100, "ABC", expiry, nil)
    assert.Equal(t, "accepted", result)
    assert.Equal(t, engine.StateReserved, e.GetConnector(1).Status)
}

func TestEngine_ReserveConnector_OccupiedWhenSessionActive(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)
    e.PlugIn(1)
    require.NoError(t, e.StartSession(1, -1, 0.0, nil, 0))

    result := e.ReserveConnector(1, 100, "ABC", time.Now().Add(time.Minute), nil)
    assert.Equal(t, "occupied", result)
}

func TestEngine_CancelReservation(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)
    e.ReserveConnector(1, 100, "ABC", time.Now().Add(time.Minute), nil)

    result := e.CancelReservation(100)
    assert.Equal(t, "accepted", result)
    assert.Equal(t, engine.StateAvailable, e.GetConnector(1).Status)
}

func TestEngine_SetConnectorAvailability(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)

    result := e.SetConnectorAvailability(1, "Inoperative")
    assert.Equal(t, "accepted", result)
    assert.Equal(t, engine.StateUnavailable, e.GetConnector(1).Status)

    result = e.SetConnectorAvailability(1, "Operative")
    assert.Equal(t, "accepted", result)
    assert.Equal(t, engine.StateAvailable, e.GetConnector(1).Status)
}

func TestEngine_SetConnectorAvailability_ScheduledWhenSessionActive(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)
    e.PlugIn(1)
    require.NoError(t, e.StartSession(1, -1, 0.0, nil, 0))

    result := e.SetConnectorAvailability(1, "Inoperative")
    assert.Equal(t, "scheduled", result)
    assert.Equal(t, engine.StateCharging, e.GetConnector(1).Status) // not changed yet
}

func TestEngine_SetActiveTransaction(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)
    e.PlugIn(1)
    require.NoError(t, e.StartSession(1, -1, 0.0, nil, 0))

    e.SetActiveTransaction(1, 42)
    assert.Equal(t, 42, *e.GetActiveTransactionID(1))

    connID := e.GetConnectorByTransaction(42)
    require.NotNil(t, connID)
    assert.Equal(t, 1, *connID)
}

// Helpers for pointer creation in tests.
func pf(v float64) *float64 { return &v }
func pi(v int) *int          { return &v }
func ps(v string) *string    { return &v }
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/engine/... -run TestEngine -v
```

Expected: compile error — engine.go, engine types not defined.

- [ ] **Step 3: Implement engine.go**

Create `internal/engine/engine.go`:

```go
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
    // Implementations must be non-blocking.
    OnSessionStarted         func(connectorID int)
    OnSessionStopped         func(connectorID int)
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
                e.unplugConnectorLocked(id)
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

    // Consume reservation if present.
    if res, ok := e.findReservationForConnector(connectorID); ok {
        delete(e.reservations, res.ReservationID)
        c.ClearReservation()
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
        e.OnSessionStarted(connectorID)
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
            e.OnSessionStopped(connectorID)
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

        energyBefore := session.EnergyCharged
        meter.Update(c.Voltage, effectiveCurrent, c.Phase, intervalSeconds)
        session.UpdateEnergy(meter.Value - e.sessionMeterOffset(connectorID, meter, energyBefore))

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

// sessionMeterOffset computes the incremental Wh for this tick.
// In single-EVSE mode, meter.Value is cumulative across sessions; we need the delta.
func (e *Engine) sessionMeterOffset(connectorID int, meter *EnergyMeter, energyBefore float64) float64 {
    // In multi-EVSE mode, meter is session-scoped so total == session energy.
    if e.multiEVSEMode {
        return 0 // session energy = meter.Value directly
    }
    // In single-EVSE mode, delta = meter.Value - (globalMeter.Value before update)
    // We track session energy separately; meter.Update already added to meter.Value
    // so the delta was implicitly applied. Return 0 here and rely on UpdateEnergy
    // receiving the per-tick Wh.
    return energyBefore // signal to UpdateEnergy that we're passing delta only
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
```

Note: The `Simulate` energy accounting has a subtle issue with single-EVSE mode. Fix it by tracking session energy as a delta, not from global meter:

Replace the `Simulate` loop body's energy update with:

```go
        // Calculate incremental Wh for this tick.
        prevMeterValue := meter.Value
        meter.Update(c.Voltage, effectiveCurrent, c.Phase, intervalSeconds)
        deltaWh := meter.Value - prevMeterValue
        session.UpdateEnergy(deltaWh)
```

And remove the `sessionMeterOffset` method entirely. Also update `startSessionLocked` to create a per-session meter in multi-EVSE mode:

```go
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
        e.OnSessionStarted(connectorID)
    }
    if e.OnConnectorStatusChanged != nil {
        e.OnConnectorStatusChanged(connectorID, c.Status)
    }
    return nil
}
```

- [ ] **Step 4: Run all tests**

```bash
go test ./internal/engine/... -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/engine.go internal/engine/session.go internal/engine/engine_test.go
git commit -m "feat(engine): Engine core with all domain methods"
```

---

## Task 8: Final Verification

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -v -count=1
```

Expected: all tests PASS, no build errors.

- [ ] **Step 2: Verify go vet passes**

```bash
go vet ./...
```

Expected: no output (no issues).

- [ ] **Step 3: Commit cleanup if needed**

```bash
git add -A
git commit -m "chore(engine): go vet clean"
```
