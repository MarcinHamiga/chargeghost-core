# ChargeGhost EVSE Engine — Go Reimplementation Guide

> Complete specification for reimplementing the ChargeGhost simulation engine in Go,
> controlled exclusively via REST + WebSocket API, with OCPP delegated to an external library.

> **OCPP Version Scope**: This guide covers OCPP 1.6J as the primary protocol.
> OCPP 2.0.1 support is out of scope for the initial Go implementation; see
> [Section 17 - OCPP 2.0.1 Decision](#17-ocpp-201-decision) for rationale and
> implications.

---

## Table of Contents

 1. [Architecture Overview](#1-architecture-overview)
 2. [Project Structure](#2-project-structure)
 3. [Domain Models](#3-domain-models)
 4. [Engine Core](#4-engine-core)
 5. [Connector State Machine](#5-connector-state-machine)
 6. [Session Lifecycle](#6-session-lifecycle)
 7. [Energy Meter](#7-energy-meter)
 8. [Reservation System](#8-reservation-system)
 9. [Simulation Loop](#9-simulation-loop)
10. [REST API Specification](#10-rest-api-specification)
11. [WebSocket Event Streaming](#11-websocket-event-streaming)
12. [OCPP Integration Contract](#12-ocpp-integration-contract)
13. [Configuration](#13-configuration)
14. [Concurrency Model](#14-concurrency-model)
15. [Implementation Order](#15-implementation-order)
16. [Reference: Python Entity Crosswalk](#16-reference-python-entity-crosswalk)
17. [OCPP 2.0.1 Decision](#17-ocpp-201-decision)

---

## 1. Architecture Overview

```
┌──────────────────────────────────────────────────────┐
│                   Go Binary                           │
│                                                      │
│  ┌─────────────┐  ┌──────────────┐  ┌─────────────┐ │
│  │  REST API    │  │  Engine      │  │  OCPP       │ │
│  │  (net/http   │  │  (domain     │  │  Adapter    │ │
│  │   + chi/echo)│  │   logic)     │  │  (interface)│ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬──────┘ │
│         │                  │                  │        │
│         │    ┌─────────────┘                  │        │
│         ▼    ▼                                ▼        │
│  ┌─────────────────┐              ┌──────────────────┐ │
│  │ WebSocket Hub    │◄────────────│ External OCPP    │ │
│  │ (event streaming)│  events     │ Library calls    │ │
│  └─────────────────┘              │ engine methods   │ │
│                                   └──────────────────┘ │
└──────────────────────────────────────────────────────┘
         ▲                              ▲
         │  HTTP/WS                     │  WebSocket
         │                              │  (OCPP 1.6J)
    3rd-party GUI                  Central System
    (any language)                  (CSMS)
```

**Key design principles:**

- The engine is the single source of truth for all simulation state.
- The REST API is the sole control surface — no GUI coupling.
- The OCPP adapter calls engine methods; the engine never calls OCPP directly.
- All state mutations flow through the engine; the API is a thin transport layer.
- Events are broadcast via WebSocket for real-time GUI updates.

---

## 2. Project Structure

```
chargeghost-engine/
├── cmd/
│   └── chargeghost/
│       └── main.go                  # Entry point, wires everything together
├── internal/
│   ├── engine/
│   │   ├── engine.go                # Core simulation engine
│   │   ├── connector.go             # Connector entity + state machine
│   │   ├── session.go               # Charging session
│   │   ├── energy_meter.go          # Cumulative energy metering
│   │   ├── reservation.go           # Connector reservation
│   │   └── state.go                 # ConnectorState enum + transitions
│   ├── api/
│   │   ├── server.go                # HTTP server setup, middleware
│   │   ├── router.go                # Route registration
│   │   ├── handlers/
│   │   │   ├── connectors.go        # /api/v1/connectors/*
│   │   │   ├── sessions.go          # /api/v1/sessions/*
│   │   │   ├── status.go            # /api/v1/status
│   │   │   ├── config.go            # /api/v1/config/*
│   │   │   ├── ocpp.go              # /api/v1/ocpp/*
│   │   │   ├── reservations.go      # /api/v1/reservations/*
│   │   │   ├── charging_profiles.go # /api/v1/charging-profiles/*
│   │   │   ├── timeline.go          # /api/v1/timeline
│   │   │   ├── local_auth.go         # /api/v1/local-auth-list
│   │   │   ├── firmware.go          # /api/v1/firmware, /api/v1/diagnostics
│   │   │   └── about.go             # /api/v1/about
│   │   ├── dto.go                   # JSON request/response structs
│   │   └── ws/
│   │       ├── hub.go               # WebSocket connection manager
│   │       └── messages.go          # WS message types
│   ├── ocpp/
│   │   ├── adapter.go               # Interface that external OCPP lib implements
│   │   ├── config_keys.go           # OCPP configuration key management
│   │   ├── profile_manager.go       # Smart charging profile manager (1.6)
│   │   ├── firmware_manager.go      # Firmware + diagnostics simulation
│   │   ├── local_auth_list.go       # Local authorization list
│   │   ├── auth_cache.go            # Authorization cache
│   │   ├── data_transfer.go         # Vendor-specific data transfer
│   │   ├── command.go               # CommandDispatcher — ordered OCPP message delivery
│   │   ├── queue/
│   │   │   ├── queue.go             # MessageQueue interface + factory
│   │   │   ├── memory.go            # InMemoryQueue backend
│   │   │   └── json_file.go         # JsonFileQueue backend
│   │   └── types.go                 # Shared OCPP-related types
│   ├── config/
│   │   └── config.go                # Configuration loading/saving
│   ├── timeline/
│   │   ├── store.go                 # OCPP event timeline store (in-memory ring buffer)
│   │   └── models.go               # TimelineEvent, TimelineFilter types
│   └── event/
│       └── bus.go                   # Simple in-process event bus
├── pkg/                             # Public API (if external consumers)
│   └── types/
│       └── types.go                 # Shared public types
├── go.mod
├── go.sum
└── README.md
```

---

## 3. Domain Models

### 3.1 ConnectorState

```go
// internal/engine/state.go

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
```

These map 1:1 to OCPP 1.6 `ChargePointStatus` values. The state machine
that governs transitions is defined in Section 5.

### 3.2 Connector

```go
// internal/engine/connector.go

type Connector struct {
    ID             int
    Voltage        float64   // Volts (120.0–1000.0)
    Current        float64   // Amperes (6.0–150.0)
    Phase          int       // 1 or 3 (not 2 — represents single-phase or three-phase)
    Status         ConnectorState
    PersistentStatus ConnectorState  // Survives plug/unplug cycles
    IsPluggedIn    bool
    IDTag          *string   // RFID tag, nil if none
}
```

**Validation constants** (from `util/config.py`):

| Parameter | Valid Values      | Default |
|-----------|-------------------|---------|
| Voltage   | 120V – 1000V      | 230V    |
| Current   | 6A – 150A         | 32A     |
| Phase     | 1 or 3 (not 2)    | 1       |

**Power calculation:** `Power(W) = Voltage × Current × Phase`

### 3.3 Session

```go
// internal/engine/engine.go
// MeterRecord is defined alongside Engine (not in session.go) because it is
// also used by StoppedSessionInfo and referenced by the OCPP adapter.

type MeterRecord struct {
    Timestamp string  `json:"timestamp"`
    Value     float64 `json:"value"`
}

// internal/engine/session.go

type Session struct {
    TransactionID              int
    ConnectorID                int
    StartTime                  time.Time
    EnergyCharged              float64   // Watt-hours (Wh)
    StateOfCharge              float64   // 0.0–100.0
    MaxEnergy                  float64   // Wh (battery capacity)
    IDTag                      *string
    ReservationID              *int
    RemoteStartChargingProfile *ChargingProfile // From RemoteStartTransaction; forwarded to OCPP layer
    MaxChargeReached           bool      // Fires exactly once per session
    MeterHistory               []MeterRecord
}
```

**SoC calculation:**
```
energy_charged = min(energy_charged + delivered_wh, max_energy)
state_of_charge = (energy_charged / max_energy) * 100
```
When `MaxEnergy == 0`, SoC is disabled and energy accumulates without bound.

### 3.4 EnergyMeter

```go
// internal/engine/energy_meter.go

type EnergyMeter struct {
    Value       float64  // Cumulative Wh (like an odometer)
    IsCharging  bool
}

// Update calculates energy for a time interval:
//   power_w = voltage × current × phase
//   energy_wh = (power_w × interval_seconds) / 3600.0
//   meter.value += energy_wh
```

The meter is **cumulative across sessions** (in single-EVSE mode). In multi-EVSE
mode, each connector gets its own meter that is destroyed when the session ends.

### 3.5 Reservation

```go
// internal/engine/reservation.go

type Reservation struct {
    ReservationID int
    ConnectorID   int
    IDTag         string
    ExpiryDate    time.Time
    ParentIDTag   *string
}

func (r *Reservation) IsExpired(now time.Time) bool {
    return !now.Before(r.ExpiryDate)
}
```

---

## 4. Engine Core

The engine is the central coordinator. It owns all connectors, sessions, and
energy meters. All state mutations go through the engine.

```go
// internal/engine/engine.go

type Engine struct {
    mu sync.RWMutex // Protects all mutable state; RWMutex allows concurrent reads

    // Topology
    connectors       map[int]*Connector
    nextConnectorID  int
    multiEVSEMode    bool

    // Sessions keyed by connector ID
    sessions         map[int]*Session

    // Energy meters
    globalMeter      *EnergyMeter
    energyMeters     map[int]*EnergyMeter  // multi-EVSE mode only

    // Last stopped session details (for StopTransaction)
    LastStoppedSession *StoppedSessionInfo

    // Pending state
    pendingRemoteStarts         map[int]*PendingRemoteStart
    pendingAvailabilityChanges  map[int]string  // connectorID → "Operative"|"Inoperative"

    // Reservations
    reservations     map[int]*Reservation

    // EV simulation
    EVBatteryCapacity float64  // Wh (default 55000 = 55 kWh)

    // Callback for external charging limits (injected by OCPP bridge)
    GetLimit        func(connectorID int, transactionID int) *float64

    // Events
    OnSessionStarted          func(connectorID int)
    OnSessionStopped          func(connectorID int)
    OnConnectorStatusChanged  func(connectorID int, status ConnectorState)
    OnConnectorParamsChanged  func(connectorID int, voltage, current float64, phase int)
    OnReservationExpired      func(reservationID, connectorID int)
}

type StoppedSessionInfo struct {
    TransactionID  int
    ConnectorID    int
    EnergyCharged  float64
    IDTag          *string
    MeterStop      float64
    Reason         string
    MeterHistory   []MeterRecord
    ReservationID  *int
}

type PendingRemoteStart struct {
    TransactionID    int
    MaxEnergy        float64
    IDTag            *string
    ChargingProfile  *ChargingProfile
    Expiry           time.Time
}
```

### Engine Methods

| Method | Description |
|--------|-------------|
| `AddConnector(voltage, current, phase) *Connector` | Creates connector, assigns sequential ID |
| `RemoveConnector(id) error` | Fails if last connector or has active session |
| `UpdateConnector(id, voltage?, current?, phase?) error` | Validates and updates params |
| `GetConnector(id) *Connector` | Lookup by ID |
| `PlugIn(connectorID)` | Simulates EV plug-in; auto-unplugs others in single-EVSE mode |
| `Unplug(connectorID)` | Simulates EV unplug; stops session if active |
| `StartSession(connectorID, txID int, maxEnergy float64, idTag *string, timeout int) error` | Starts charging; validates state, reservation. `timeout > 0` stores a `PendingRemoteStart` if not plugged in yet |
| `StopSession(connectorID?, reason) *StoppedSessionInfo` | Stops session, records details |
| `SuspendEV(connectorID) error` | EV-side suspension |
| `ResumeCharging(connectorID) error` | Resume from EV suspension |
| `SetConnectorAvailability(id, type) string` | "accepted"/"scheduled"/"rejected" |
| `Simulate(interval time.Duration)` | One simulation tick (processes all active sessions) |
| `ReserveConnector(id, reservationID, idTag, expiry) string` | "accepted"/"occupied"/etc. |
| `CancelReservation(reservationID) string` | "accepted"/"rejected" |
| `GetSessionInfo() []SessionDetail` | Info for all active sessions |
| `SetActiveTransaction(connectorID, transactionID int)` | Updates Session.TransactionID after CSMS assigns it (OCPP 1.6 flow) |
| `ClearActiveTransaction(connectorID int)` | Clears transaction ID on session stop |

### Key Engine Invariants

1. **Single-EVSE mode:** Only one session across all connectors at any time.
   Plug-in auto-unplugs any other connected connector.
2. **Multi-EVSE mode:** Each connector may have one independent session.
   No auto-unplug.
3. Sessions are keyed by `connectorID` in a map — O(1) lookup.
4. Connector IDs are 1-indexed, matching OCPP convention.
5. The `GetLimit` callback is injected externally (by the OCPP bridge). When it
   returns a non-nil value, the effective current is `min(connector.current, *limit)`.
   When the limit is 0 and the connector is CHARGING, it transitions to SUSPENDED_EVSE.
6. Expired reservations are lazily cleaned on every engine method call.

### Engine Error Handling

Engine methods that can fail return `error` (Go convention) or a status string
for OCPP-result-pattern methods. Define sentinel errors in
`internal/engine/engine.go`:

```go
var (
    ErrConnectorNotFound    = errors.New("connector not found")
    ErrSessionNotFound      = errors.New("no active session")
    ErrSessionAlreadyActive = errors.New("session already active on connector")
    ErrNotPluggedIn         = errors.New("connector not plugged in")
    ErrInvalidState         = errors.New("invalid connector state for this action")
    ErrInvalidTransition    = errors.New("invalid state transition")
    ErrLastConnector        = errors.New("cannot remove last connector")
    ErrSessionActiveOnRemove = errors.New("cannot remove connector with active session")
    ErrInvalidVoltage       = errors.New("voltage out of range (120–1000V)")
    ErrInvalidCurrent       = errors.New("current out of range (6–150A)")
    ErrInvalidPhase         = errors.New("phase must be 1 or 3 (not 2)")
)
```

Methods returning OCPP-style status strings (`ReserveConnector`,
`CancelReservation`, `SetConnectorAvailability`) continue to return strings
(`"accepted"`, `"rejected"`, `"occupied"`, etc.) matching the OCPP 1.6 response values.

---

## 5. Connector State Machine

### 5.1 Valid Transitions

The state machine is a lookup table: `(current_state, action) → new_state`.

| Current State     | Action           | New State        |
|-------------------|------------------|------------------|
| Available         | plug_in          | Preparing        |
| Reserved          | plug_in          | Preparing        |
| Preparing         | unplug           | Available        |
| Finishing         | unplug           | Available        |
| Charging          | unplug           | Available        |
| SuspendedEV       | unplug           | Available        |
| SuspendedEVSE     | unplug           | Available        |
| Preparing         | start_charging   | Charging         |
| Charging          | stop_charging    | Finishing        |
| SuspendedEV       | stop_charging    | Finishing        |
| SuspendedEVSE     | stop_charging    | Finishing        |
| Charging          | suspend_ev       | SuspendedEV      |
| SuspendedEV       | resume           | Charging         |
| Charging          | suspend_evse     | SuspendedEVSE    |
| SuspendedEVSE     | resume           | Charging         |

**Invalid transitions** are rejected with an error string. The Python code
stores these in `VALID_TRANSITIONS` as a `dict[tuple[ConnectorState, str], ConnectorState]`.

### 5.2 Persistent Status

The connector maintains a `_persistent_status` that survives plug/unplug cycles:

- `RESERVED`, `UNAVAILABLE`, `FAULTED`, `AVAILABLE` are persistent.
- When `Unplug()` is called, the connector restores to its persistent status
  (not necessarily AVAILABLE).
- This handles cases like: connector is UNAVAILABLE → EV was plugged in (by
  external physical action) → EV unplugs → connector returns to UNAVAILABLE.

### 5.3 Bypass Transitions (not via state machine)

These operations bypass the transition table and set status directly:

- **`SetUnavailable()`**: Sets to UNAVAILABLE. No-op if already UNAVAILABLE or FAULTED.
- **`SetReserved()`**: Sets persistent to RESERVED. If plugged in, current status
  becomes PREPARING; otherwise RESERVED. No-op if UNAVAILABLE/FAULTED.
- **`SetOperative()`**: Restores AVAILABLE as persistent. If plugged in, current
  becomes PREPARING; otherwise AVAILABLE. No-op if FAULTED.
- **`ClearReservation()`**: Same as SetOperative but only called when a reservation
  is consumed or cancelled. No-op if UNAVAILABLE/FAULTED.

### 5.4 Event Emission

Every status change fires `OnConnectorStatusChanged(connectorID, newStatus)`.
This is the primary integration point for:
- Sending OCPP `StatusNotification`
- Broadcasting WebSocket events to GUIs

---

## 6. Session Lifecycle

### 6.1 Starting a Session

`StartSession(connectorID, transactionID, maxEnergy?, idTag?)` performs:

1. Expire reservations.
2. Look up connector. Fail if not found.
3. Check reservation compatibility: if connector is reserved, the idTag must
   match either the reservation's `idTag` or `parentIdTag`.
4. **If connector is not plugged in:**
   - If a `timeout` was given and > 0: store as `PendingRemoteStart` with
     an expiry time. Return without error (the session will start when the
     EV plugs in before the timeout).
   - Otherwise: fail with "not plugged in" error.
5. Validate connector state is AVAILABLE or PREPARING.
6. In single-EVSE mode: fail if any session is active.
   In multi-EVSE mode: fail only if this connector already has a session.
7. Clear any pending remote start for this connector.
8. Consume reservation if present.
9. Create `Session` with provided parameters.
10. Set `meter.IsCharging = true`.
11. Fire `OnSessionStarted(connectorID)` → triggers connector transition to CHARGING.

### 6.2 Pending Remote Start Flow

```
RemoteStartTransaction request arrives at OCPP adapter
    │
    ▼
Engine.StartSession(connectorID, txID, timeout=30)
    │
    ├─ Connector not plugged in
    │   └─ Store as PendingRemoteStart(expiry=now+30s)
    │
    ├─ Later: EV plugs in → Connector enters PREPARING
    │   └─ Engine.handleConnectorStatusChange fires
    │       └─ Finds pending start for this connector
    │           ├─ Not expired → StartSession(connectorID, txID)
    │           └─ Expired → Log warning, discard
    │
    └─ Connector already plugged in → Start immediately
```

### 6.3 Stopping a Session

`StopSession(connectorID?, reason)` performs:

1. Find the session. If `connectorID` is nil, stop the first active session.
2. Pop session from the sessions map.
3. Record `LastStoppedSession` with:
   - TransactionID, ConnectorID, EnergyCharged, IDTag
   - MeterStop (current meter reading), Reason
   - MeterHistory (last 10 readings)
   - ReservationID
4. Set `meter.IsCharging = false`.
5. In multi-EVSE mode, destroy the per-connector meter.
6. Fire `OnSessionStopped(connectorID)` → triggers connector transition to FINISHING.
7. Apply any deferred `ChangeAvailability` for this connector.

### 6.4 Energy Max Reached

When `session.EnergyCharged >= session.MaxEnergy` (and `MaxEnergy > 0`):

1. Set `session.MaxChargeReached = true` (fires exactly once).
2. The event causes the connector to transition to `SUSPENDED_EV`.
3. The energy meter stops accumulating (`IsCharging = false`).

### 6.5 Suspension

**EV-side suspension** (`SuspendEV`):
- Valid only from CHARGING state.
- Sets `meter.IsCharging = false`.
- Connector transitions to SUSPENDED_EV.
- Session remains active.

**EVSE-side suspension** (automatic via charging profile limit = 0):
- Happens during `Simulate()` when `GetLimit` returns 0.
- Connector transitions from CHARGING to SUSPENDED_EVSE.
- Meter does not accumulate (effective current is 0).

**Resume** works identically for both: sets `meter.IsCharging = true`,
transitions connector back to CHARGING.

---

## 7. Energy Meter

The energy meter is an odometer-style accumulator:

```go
func (m *EnergyMeter) Update(voltage, current float64, phase int, intervalSeconds float64) {
    if !m.IsCharging {
        return
    }
    powerW := voltage * current * float64(phase)
    whConsumed := (powerW * intervalSeconds) / 3600.0
    m.Value += whConsumed
}
```

**Key behaviors:**

- `Value` is cumulative and persists across sessions (single-EVSE mode).
- In multi-EVSE mode, a per-connector meter is created on session start and
  destroyed on session stop.
- `IsCharging` is the gate: when false, `Update()` is a no-op.
- `GetMeterReading()` simply returns `Value`.
- The event queue (rolling buffer of 1000 readings) in the Python version can
  be replaced by the WebSocket tick broadcast in Go.

---

## 8. Reservation System

### ReserveConnector

```go
func (e *Engine) ReserveConnector(connectorID, reservationID int, idTag string, expiry time.Time, parentIDTag *string) string
```

Returns one of: `"accepted"`, `"occupied"`, `"faulted"`, `"unavailable"`, `"rejected"`.

Validation order:
1. Connector must exist.
2. Connector must not be FAULTED → "faulted".
3. Connector must not be UNAVAILABLE → "unavailable".
4. No active session on this connector → "occupied".
5. Connector must not be plugged in → "occupied".
6. No duplicate reservation_id across all connectors → "rejected".
7. No existing reservation on this connector → "occupied".
8. Create reservation, set connector to RESERVED → "accepted".

### CancelReservation

```go
func (e *Engine) CancelReservation(reservationID int) string
```

Searches all reservations for the given ID. If found, removes it and
clears the connector's reservation status. Returns `"accepted"` or `"rejected"`.

### Expiry

Reservations are lazily expired on every engine method call (`_expire_reservations`).
Expired reservations trigger `OnReservationExpired(reservationID, connectorID)`.

---

## 9. Simulation Loop

The simulation loop is the heartbeat of the engine. It runs at a fixed tick
rate (the Python version uses 0.05s sleep / 0.1s step = 10 Hz).

```go
func (e *Engine) Simulate(interval time.Duration) {
    e.mu.Lock()
    defer e.mu.Unlock()

    e.expireReservations()

    for connectorID, session := range e.sessions {
        connector := e.connectors[connectorID]
        meter := e.getEnergyMeter(connectorID)
        if connector == nil || meter == nil || !meter.IsCharging {
            continue
        }

        // Apply charging profile limit
        effectiveCurrent := connector.Current
        if e.GetLimit != nil {
            if limit := e.GetLimit(connectorID, session.TransactionID); limit != nil {
                effectiveCurrent = min(connector.Current, *limit)
            }
        }

        // EVSE-side auto-suspend/resume based on limit
        if effectiveCurrent == 0 && connector.Status == StateCharging {
            connector.suspendEVSE()
        } else if effectiveCurrent > 0 && connector.Status == StateSuspendedEVSE {
            connector.resumeCharging()
        }

        // Update energy meter
        meter.Update(connector.Voltage, effectiveCurrent, connector.Phase, interval.Seconds())
    }
}
```

### Runtime Loop (main goroutine)

```go
func (r *Runtime) Run(ctx context.Context) {
    ticker := time.NewTicker(50 * time.Millisecond)  // 20 Hz wake-up
    defer ticker.Stop()

    accumulator := 0.0
    stepInterval := 0.1  // 100ms simulation step

    for {
        select {
        case <-ctx.Done():
            return
        case now := <-ticker.C:
            delta := now.Sub(r.lastTick).Seconds()
            r.lastTick = now
            accumulator += delta

            steps := 0
            for accumulator >= stepInterval && steps < 5 {
                r.engine.Simulate(time.Duration(stepInterval * float64(time.Second)))
                accumulator -= stepInterval
                steps++
            }
        }
    }
}
```

This fixed-timestep loop with accumulation ensures deterministic simulation
regardless of system load. Maximum 5 steps per cycle prevents spiral-of-death.

---

## 10. REST API Specification

All endpoints are under `/api/v1`. JSON request/response bodies.

### 10.1 Status

#### `GET /api/v1/status`

Returns the full system state:

```json
{
    "ocpp_connected": true,
    "uptime_seconds": 3600,
    "connectors": [
        {
            "id": 1,
            "status": "Charging",
            "voltage": 230.0,
            "current": 32.0,
            "phase": 1,
            "is_plugged_in": true,
            "id_tag": "ABC123"
        }
    ],
    "active_sessions": [
        {
            "transaction_id": 1,
            "connector_id": 1,
            "energy_charged_wh": 1500.5,
            "state_of_charge": 2.73,
            "start_time": "2025-04-05T10:00:00Z",
            "id_tag": "ABC123",
            "is_charging": true
        }
    ],
    "energy_meters": {
        "1": {
            "reading_wh": 54321.0,
            "is_charging": true
        }
    }
}
```

### 10.2 Connectors

#### `GET /api/v1/connectors`
Returns all connectors.

#### `GET /api/v1/connectors/{id}`
Returns a single connector.

#### `POST /api/v1/connectors`
Create a new connector.

```json
// Request
{
    "voltage": 230.0,
    "current": 32.0,
    "phase": 1
}

// Response
{
    "success": true,
    "message": "Created connector 2",
    "details": { "connector": { "id": 2, "status": "Available", ... } }
}
```

#### `PUT /api/v1/connectors/{id}`
Update connector parameters.

```json
// Request
{
    "voltage": 240.0,
    "current": 16.0
}
```

#### `DELETE /api/v1/connectors/{id}`
Remove a connector. Fails if last connector or has active session.

#### `POST /api/v1/connectors/{id}/plug_in`
Simulate EV plug-in.

#### `POST /api/v1/connectors/{id}/unplug`
Simulate EV unplug.

#### `POST /api/v1/connectors/{id}/suspend_ev`
EV-side suspension.

#### `POST /api/v1/connectors/{id}/resume_charging`
Resume from EV or EVSE suspension.

#### `POST /api/v1/connectors/{id}/start-charging`
Start a local charging session (engine auto-generates transaction ID).

#### `POST /api/v1/connectors/{id}/stop-charging`
Stop the session on this connector.

#### `PUT /api/v1/connectors/{id}/rfid`
Set RFID tag for a connector.

```json
// Query param: rfid_tag=ABC123
```

#### `DELETE /api/v1/connectors/{id}/rfid`
Clear RFID tag.

### 10.3 Sessions

#### `POST /api/v1/sessions/start`
Start a session. Query param: `connector_id=1`.

#### `POST /api/v1/sessions/stop`
Stop all active sessions.

#### `GET /api/v1/sessions`
List all active sessions.

#### `GET /api/v1/sessions/last-stopped`
Get details of the most recently stopped session.

```json
{
    "transaction_id": 1,
    "connector_id": 1,
    "energy_charged_wh": 5000.0,
    "meter_stop": 54321,
    "reason": "Local",
    "id_tag": "ABC123"
}
```

#### `GET /api/v1/sessions/active`
Get active session for a specific connector. Query param: `connector_id=1`.

#### `GET /api/v1/sessions/{connector_id}`
Get active session by connector ID.

#### `GET /api/v1/sessions/info`
Detailed session info including max_energy and SoC.

### 10.4 Configuration

#### `GET /api/v1/config`
Current configuration.

```json
{
    "connection_url": "wss://localhost:3000/CP_1",
    "ocpp_id": "CP_1",
    "charge_point_model": "ChargeGhostV1",
    "charge_point_vendor": "ChargeGhost",
    "connectors": [{ "voltage": 230.0, "current": 32.0, "phase": 1 }],
    "skip_tls_verify": false,
    "log_mode": "shallow",
    "multi_evse_mode": false,
    "ev_battery_capacity": 55.0,
    "ocpp_version": "1.6",
    "persist_message_queue": false,
    "rfid_tag": null
}
```

#### `PATCH /api/v1/config`
Update configuration fields. Returns the action required:

```json
// Request
{
    "connection_url": "wss://new-server.example.com/CP_1"
}

// Response
{
    "success": true,
    "action": "bridge_restart_required",
    "changed_fields": ["connection_url"],
    "message": "Configuration updated in memory. Save required to restart bridge."
}
```

Actions: `"no-op"`, `"bridge_restart_required"`, `"runtime_rebuild_required"`, `"rejected"`.

Topology changes (multi_evse_mode, connectors, ev_battery_capacity) require a
full runtime rebuild. Bridge fields (connection_url, ocpp_id, password, etc.)
require a bridge restart. Changes are rejected if sessions are active.

#### `POST /api/v1/config/save`
Persist configuration and apply pending actions.

### 10.5 OCPP

#### `GET /api/v1/ocpp/config-keys`
List all OCPP configuration keys.

#### `PATCH /api/v1/ocpp/config-keys`
Update an OCPP config key.

```json
{ "key": "MeterValueSampleInterval", "value": "30" }
```

#### `POST /api/v1/ocpp/authorize`
Send Authorize request. Body: `{ "id_tag": "ABC123" }`.

#### `POST /api/v1/ocpp/heartbeat`
Send manual heartbeat.

#### `POST /api/v1/ocpp/raw/start-transaction`
Send raw StartTransaction.

```json
{
    "connector_id": 1,
    "id_tag": "ABC123",
    "meter_start": 0,
    "timestamp": "2025-04-05T10:00:00Z"
}
```

#### `POST /api/v1/ocpp/raw/stop-transaction`
Send raw StopTransaction.

#### `POST /api/v1/ocpp/raw/status-notification`
Send raw StatusNotification.

#### `POST /api/v1/ocpp/raw/meter-values`
Send raw MeterValues.

#### `POST /api/v1/ocpp/raw/data-transfer`
Send raw DataTransfer.

### 10.6 Reservations

#### `GET /api/v1/reservations`
List active reservations.

#### `POST /api/v1/reservations`
Create a reservation.

```json
{
    "connector_id": 1,
    "reservation_id": 100,
    "id_tag": "ABC123",
    "expiry_date": "2025-04-05T12:00:00Z",
    "parent_id_tag": null
}
```

#### `DELETE /api/v1/reservations/{reservation_id}`
Cancel a reservation.

### 10.7 Charging Profiles

#### `GET /api/v1/charging-profiles`
List all installed charging profiles.

#### `GET /api/v1/charging-profiles/{profile_id}`
Get a specific profile.

#### `POST /api/v1/charging-profiles`
Install a charging profile.

```json
{
    "connector_id": 1,
    "profile": {
        "chargingProfileId": 1,
        "stackLevel": 0,
        "chargingProfilePurpose": "TxDefaultProfile",
        "chargingProfileKind": "Absolute",
        "chargingSchedule": {
            "chargingRateUnit": "A",
            "chargingSchedulePeriod": [
                { "startPeriod": 0, "limit": 16.0 }
            ]
        }
    }
}
```

#### `DELETE /api/v1/charging-profiles`
Clear profiles. Optional filters: `profile_id`, `connector_id`, `purpose`, `stack_level`.

#### `POST /api/v1/charging-profiles/composite-schedule`
Get composite schedule.

```json
{ "connector_id": 1, "duration": 3600 }
```

### 10.8 Response Envelope

All mutation endpoints return:

```json
{
    "success": true,
    "message": "Human-readable description",
    "details": { ... }  // optional
}
```

On error:

```json
{
    "success": false,
    "message": "Error description"
}
```

HTTP 404 for missing resources, 400 for validation errors, 409 for conflicts
(e.g., session already active).

### 10.9 Timeline

Requires `TimelineStore` backend — can be in-memory or persisted.

#### `GET /api/v1/timeline`
List timeline events with optional filters. Query params: `source`, `direction`,
`event_type`, `action`, `limit` (default 100), `offset`, `connector_id`,
`transaction_id`, `min_level`, `search`.

```json
{
    "events": [
        {
            "event_id": "abc123",
            "timestamp": "2025-04-05T10:00:00.000Z",
            "source": "ocpp_adapter",
            "direction": "outbound",
            "event_type": "call_result",
            "action": "BootNotification",
            "message_id": "msg-456",
            "connector_id": 1,
            "transaction_id": null,
            "level": "info",
            "summary": "BootNotification accepted",
            "payload": { ... },
            "correlation_key": null,
            "tags": []
        }
    ],
    "total": 42
}
```

#### `GET /api/v1/timeline/count`
Returns `{ "count": 42 }`.

#### `DELETE /api/v1/timeline`
Clears all timeline events. Returns `204 No Content`.

### 10.10 Local Auth List

#### `GET /api/v1/local-auth-list`
Returns the full local authorization list.

```json
{
    "version": 3,
    "entry_count": 10,
    "max_entries": 1000,
    "enabled": true,
    "entries": [
        {
            "id_tag": "ABC123",
            "id_tag_info": {
                "status": "Accepted",
                "expiry_date": null
            },
            "is_expired": false,
            "authorization_status": "Accepted"
        }
    ]
}
```

#### `GET /api/v1/local-auth-list/{id_tag}`
Returns a single entry or `404 Not Found`.

#### `PUT /api/v1/local-auth-list`
Full or differential update. Body:

```json
{
    "list_version": 4,
    "entries": [
        { "id_tag": "XYZ789", "id_tag_info": { "status": "Blocked" } }
    ],
    "update_type": "differential"
}
```

Returns `{ "success": true, "message": "List updated to version 4", "added": 1, "removed": 0 }`.

#### `DELETE /api/v1/local-auth-list/{id_tag}`
Remove a specific entry. Returns `204 No Content` or `404 Not Found`.

#### `DELETE /api/v1/local-auth-list`
Clear the entire list. Returns `{ "success": true, "message": "Local auth list cleared" }`.

### 10.11 Firmware & Diagnostics

#### `GET /api/v1/firmware/status`
```json
{
    "status": "Idle",
    "location": null,
    "retrieve_date": null,
    "file_name": null,
    "file_hash": null
}
```

#### `POST /api/v1/firmware/trigger`
Body: `{ "location": "https://example.com/firmware.bin", "retrieve_date": "2025-04-05T12:00:00Z" }`.
Starts simulated firmware download. Returns `{ "success": true, "message": "Firmware update started" }`.

#### `POST /api/v1/firmware/cancel`
Cancels an in-progress firmware update. Returns `409 Conflict` if no update in progress.

#### `GET /api/v1/diagnostics/status`
```json
{
    "status": "Idle",
    "location": null
}
```

#### `POST /api/v1/diagnostics/trigger`
Body: `{ "location": "https://example.com/diagnostics.tgz", "retries": 3, "retry_interval": 30 }`.
Starts simulated diagnostics upload.

#### `POST /api/v1/diagnostics/cancel`
Cancels an in-progress diagnostics upload.

**Firmware simulation timing** (must match Python `FirmwareManager` for realism):

| Transition | Delay | Status Change |
|---|---|---|
| Start → Downloading | 0s (after `waitUntilRetrieveDate` gate) | `Idle` → `Downloading` |
| Downloading → Downloaded | 3s | `Downloading` → `Downloaded` |
| Downloaded → Installing | 1s | `Downloaded` → `Installing` |
| Installing → Installed | 2s | `Installing` → `Installed` |

**Diagnostics simulation timing:**

| Transition | Delay | Status Change |
|---|---|---|
| Start → Uploading | 0s | `Idle` → `Uploading` |
| Uploading → Uploaded | 2s | `Uploading` → `Uploaded` |

Both managers emit status change events (`on_firmware_status_changed`,
`on_diagnostics_status_changed`) at each transition for WebSocket broadcast.

### 10.12 About

#### `GET /api/v1/about`
```json
{
    "version": "0.5.0",
    "description": "ChargeGhost EVSE Simulator",
    "ocpp_versions": ["1.6J"],
    "features": [
        "OCPP 1.6J charging station simulation",
        "Smart charging profiles (TxDefaultProfile, TxProfile, ChargePointMaxProfile)",
        "Local authorization list",
        "Firmware and diagnostics simulation",
        "REST API and WebSocket event streaming",
        "Offline message queue with JSON persistence"
    ],
    "license": "MIT",
    "copyright": "2025 ChargeGhost"
}
```

---

## 11. WebSocket Event Streaming

### 11.1 Connection

Connect to `ws://host:port/ws`. On connection, the server sends a full state
snapshot:

```json
{
    "type": "state_snapshot",
    "timestamp": "2025-04-05T10:00:00.000Z",
    "data": {
        "ocpp_connected": true,
        "connectors": [...],
        "active_sessions": [...],
        "energy_meters": {...}
    }
}
```

### 11.2 Event Types

All subsequent messages follow this format:

```json
{
    "type": "<event_type>",
    "timestamp": "2025-04-05T10:00:00.000Z",
    "data": { ... }
}
```

| Event Type | Trigger | Data |
|---|---|---|
| `connector_status_changed` | Connector state transition | `{ "connector_id": 1, "status": "Charging" }` |
| `connector_params_changed` | Connector voltage/current/phase updated | `{ "connector_id": 1, "voltage": 240.0, "current": 16.0, "phase": 1 }` |
| `session_started` | Session begins | Full session info (tx_id, connector_id, SoC, etc.) |
| `session_stopped` | Session ends | Stopped session info (tx_id, energy, reason, etc.) |
| `connection_state_changed` | OCPP WebSocket connects/disconnects | `{ "connected": true }` |
| `tick` | Periodic (~1s) | Full system status (same as GET /status) |
| `firmware_status_changed` | Firmware update simulation status | `{ "status": "Downloading" }` |
| `ocpp_config_key_changed` | OCPP configuration key changed | `{ "key": "MeterValueSampleInterval", "value": "30" }` |
| `charging_profile_changed` | Charging profile installed/cleared | `{ "action": "set" \| "cleared", "profile_id": 1 }` |
| `reservation_changed` | Reservation created or cancelled | `{ "action": "created" \| "cancelled", "connector_id": 1, "reservation_id": 100 }` |

### 11.3 Hub Implementation

The hub uses a single goroutine for all state mutations (register, unregister,
broadcast). This eliminates the need for mutexes entirely — a classic Go pattern
where "don't communicate by sharing memory; share memory by communicating."

```go
// internal/api/ws/messages.go

// Message is the JSON envelope sent to all WebSocket clients.
// Callers construct a Message and call hub.BroadcastMessage — the hub
// marshals it to JSON internally so callers never deal with raw bytes.
type Message struct {
    Type      string    `json:"type"`
    Timestamp time.Time `json:"timestamp"`
    Data      any       `json:"data"`
}

// internal/api/ws/hub.go

type Hub struct {
    clients    map[*Client]bool
    register   chan *Client
    unregister chan *Client
    broadcast  chan []byte
}

type Client struct {
    hub  *Hub
    conn *websocket.Conn
    send chan []byte
}

func NewHub() *Hub {
    return &Hub{
        clients:    make(map[*Client]bool),
        register:   make(chan *Client),
        unregister: make(chan *Client),
        broadcast:  make(chan []byte, 256),
    }
}

func (h *Hub) Run() {
    for {
        select {
        case client := <-h.register:
            h.clients[client] = true
        case client := <-h.unregister:
            if _, ok := h.clients[client]; ok {
                delete(h.clients, client)
                close(client.send)
            }
        case message := <-h.broadcast:
            dead := make([]*Client, 0)
            for client := range h.clients {
                select {
                case client.send <- message:
                default:
                    dead = append(dead, client)
                }
            }
            // Remove slow clients after iteration to avoid map mutation during range
            for _, client := range dead {
                close(client.send)
                delete(h.clients, client)
            }
        }
    }
}

// BroadcastAsync enqueues a pre-marshaled message for broadcast without blocking.
// Safe to call from engine event callbacks (which hold the engine mutex).
func (h *Hub) BroadcastAsync(msg []byte) {
    select {
    case h.broadcast <- msg:
    default:
        // Channel full — drop message rather than block the simulation tick.
    }
}

// BroadcastMessage marshals a Message to JSON and enqueues it for broadcast.
// Use this from engine event callbacks instead of BroadcastAsync.
func (h *Hub) BroadcastMessage(msg Message) {
    msg.Timestamp = time.Now()
    b, err := json.Marshal(msg)
    if err != nil {
        slog.Error("ws: failed to marshal message", "type", msg.Type, "error", err)
        return
    }
    h.BroadcastAsync(b)
}
```

The hub subscribes to engine events and broadcasts them. The tick loop runs
on a 1-second timer, sending full state to all connected clients.

---

## 12. OCPP Integration Contract

The OCPP layer is an external library that calls into the engine. The engine
exposes a clear interface boundary.

### 12.1 Engine → OCPP (events the OCPP layer must handle)

| Engine Event | OCPP Action (1.6) | OCPP Action (2.0.1) |
|---|---|---|
| `OnSessionStarted` | Send `StartTransaction` | Send `TransactionEvent(Started)` |
| `OnSessionStopped` | Send `StopTransaction` | Send `TransactionEvent(Ended)` |
| `OnConnectorStatusChanged` | Send `StatusNotification` | Send `StatusNotification` or `TransactionEvent(Updated)` |
| `OnReservationExpired` | — | Send `ReservationStatusUpdate` |

### 12.2 OCPP → Engine (commands the OCPP layer sends to the engine)

The OCPP adapter calls these engine methods when it receives messages from
the Central System:

| OCPP Message | Engine Method | Notes |
|---|---|---|
| `RemoteStartTransaction` | `Engine.StartSession(connectorID, txID, timeout=30, ...)` | Stores as pending if not plugged in |
| `RemoteStopTransaction` | `Engine.StopSession(connectorID, "Remote")` | Looks up connector by tx_id |
| `Reset` | `Engine.StopSession(..., "SoftReset"/"HardReset")` | Clears pending starts, stops all sessions |
| `ChangeAvailability` | `Engine.SetConnectorAvailability(connectorID, type)` | Deferred if session active |
| `ReserveNow` | `Engine.ReserveConnector(...)` | — |
| `CancelReservation` | `Engine.CancelReservation(reservationID)` | — |
| `SetChargingProfile` | Injected via `Engine.GetLimit` callback | OCPP layer manages profile, engine queries limit |
| `ClearChargingProfile` | Same injection mechanism | — |
| `GetCompositeSchedule` | OCPP layer calculates from profiles | Needs connector voltage/phases from engine |

### 12.3 Interface Definition

```go
// internal/ocpp/adapter.go

// OCPPAdapter is the interface the external OCPP library must satisfy.
// It is decomposed into smaller interfaces below for composability.
// Implementations should embed OCPPSender, OCPPReceiver, OCPPConfigManager,
// OCPPProfileManager, and OCPPFirmwareManager as appropriate.
type OCPPAdapter interface {
    OCPPSender
    OCPPReceiver
    OCPPConfigManager
    OCPPProfileManager
    OCPPFirmwareManager

    // Connection lifecycle
    IsConnected() bool
    GetHeartbeatInterval() int

    // Authorization cache
    GetAuthorizationCache() AuthorizationCache
    ClearAuthorizationCache() error

    // Local auth list
    GetLocalAuthList() LocalAuthListView

    // Data transfer
    RegisterDataTransferHandler(vendorID, messageID string, handler DataTransferHandler)
}

// OCPPSender — outbound OCPP messages (engine events trigger these).
// All methods are non-blocking when called from engine event callbacks;
// they enqueue to the OCPP command channel for serialized delivery.
type OCPPSender interface {
    SendStartTransaction(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error)
    SendStopTransaction(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []MeterRecord) error
    SendStatusNotification(connectorID int, errorCode string, status string) error
    SendMeterValues(connectorID int, value float64, transactionID int, context string) error
    SendBootNotification() error
    SendHeartbeat() error
    SendAuthorize(idTag string) error
    SendDataTransfer(vendorID string, messageID string, data string) (string, string, error)
    SendDiagnosticsStatusNotification(status string) error
    SendFirmwareStatusNotification(status string) error
}

// OCPPReceiver — inbound OCPP message callbacks (set by Bridge on adapter).
// These map 1:1 to OCPP 1.6 request messages from the CSMS.
type OCPPReceiver interface {
    // Core transaction flow
    OnRemoteStart(func(connectorID int, idTag string, chargingProfile *ChargingProfile))
    OnRemoteStop(func(transactionID int))
    OnReset(func(resetType string))  // "Soft" or "Hard"

    // Connector management
    OnChangeAvailability(func(connectorID int, availabilityType string) string)
    OnUnlockConnector(func(connectorID int) string)  // returns "Unlocked" or "UnlockFailed"

    // Reservations
    OnReserveNow(func(connectorID int, reservationID int, idTag string, expiry time.Time, parentIDTag *string) string)
    OnCancelReservation(func(reservationID int) string)

    // Configuration
    OnGetConfiguration(func(keys []string) ([]ConfigKeyInfo, []string))  // returns (knownKeys, unknownKeys)
    OnChangeConfiguration(func(key, value string) string)                // returns "Accepted"/"Rejected"/"NotSupported"/"RebootRequired"

    // Data transfer
    OnDataTransfer(func(vendorID string, messageID string, data string) (string, string))  // returns (status, data)

    // Charging profiles
    OnSetChargingProfile(func(connectorID int, profile ChargingProfile) string)  // returns "Accepted"/"Rejected"/"NotSupported"
    OnClearChargingProfile(func(profileID *int, connectorID *int, purpose *string, stackLevel *int) string)
    OnGetCompositeSchedule(func(connectorID int, duration int, chargingRateUnit *string) ([]ChargingSchedulePeriod, error))

    // Local auth list
    OnGetLocalListVersion(func() int)
    OnSendLocalList(func(listVersion int, entries []LocalAuthEntry, updateType string) string)

    // Firmware / diagnostics
    OnUpdateFirmware(func(location string, retrieveDate time.Time, retries *int, retryInterval *int) string)
    OnGetDiagnostics(func(location string, retries *int, retryInterval *int, startTime *time.Time, stopTime *time.Time) string)

    // Trigger message
    OnTriggerMessage(func(requestedMessage string, connectorID *int) string)  // returns "Accepted"/"Rejected"/"NotImplemented"

    // Cache
    OnClearCache(func() string)  // returns "Accepted"
}

// OCPPConfigManager — OCPP configuration key management.
type OCPPConfigManager interface {
    GetConfigValue(key string) string
    SetConfigValue(key, value string) string
    GetConfigKeyInfo() []ConfigKeyInfo  // Returns all keys with metadata
}

// OCPPProfileManager — smart charging profile management.
type OCPPProfileManager interface {
    GetChargingProfiles() []ChargingProfileInfo
    SetChargingProfile(connectorID int, profile ChargingProfile) error
    ClearChargingProfile(connectorID int, profileID int, stackLevel int, purpose string) error
    GetCompositeLimit(connectorID int, transactionID int, now time.Time, connectorVoltage float64, transactionStart *time.Time, phases int) *float64
    GetCompositeSchedule(connectorID int, transactionID int, startTime time.Time, duration int, connectorVoltage float64, transactionStart *time.Time, phases int) ([]ChargingSchedulePeriod, error)
}

// OCPPFirmwareManager — firmware and diagnostics simulation.
type OCPPFirmwareManager interface {
    GetFirmwareStatus() FirmwareStatus
    TriggerFirmwareUpdate(location string, retrieveDate time.Time) error
    CancelFirmwareUpdate() error
    GetDiagnosticsStatus() DiagnosticsStatus
    TriggerDiagnosticsUpload(location string, retries int, retryInterval int) error
    CancelDiagnosticsUpload() error
}

// DataTransferHandler is called when a DataTransfer request arrives for a
// registered vendorID/messageID pair.
type DataTransferHandler func(messageID string, data string) (status string, responseData string)

// EngineView is the read-only interface the OCPP layer uses to query engine state.
type EngineView interface {
    GetConnector(connectorID int) *Connector
    GetSession(connectorID int) *Session
    GetEnergyMeter(connectorID int) *EnergyMeter
    GetConnectorIDs() []int
    GetLastStoppedSession() *StoppedSessionInfo
    GetConnectorStatus(connectorID int) string       // Returns current ConnectorState as string
    GetMeterSnapshot(connectorID int) (float64, int)  // Returns (meterReading, transactionID)

    // Transaction ID tracking — critical for RemoteStopTransaction correlation.
    // RemoteStopTransaction carries a transactionID, not a connectorID.
    // The adapter uses these to map back to the connector.
    GetActiveTransactionID(connectorID int) *int      // Returns nil if no active transaction
    GetConnectorByTransaction(transactionID int) *int // Returns connectorID or nil
    SetActiveTransaction(connectorID, transactionID int)
    ClearActiveTransaction(connectorID int)
}

// Supporting types for OCPPAdapter callbacks and views

type ConfigKeyInfo struct {
    Key      string
    Value    string
    ReadOnly bool
    Type     string  // "string", "int", "bool"
}

type AuthorizationCache interface {
    Get(idTag string) (status string, expiry *time.Time)
    Put(idTag string, status string, expiry *time.Time)
    Remove(idTag string)
    Clear()
    Size() int
}

type LocalAuthListView interface {
    GetVersion() int
    GetEntry(idTag string) *LocalAuthEntry
    GetAllEntries() []LocalAuthEntry
    UpdateList(version int, entries []LocalAuthEntry, updateType string) error
    RemoveEntry(idTag string) error
    Clear()
    GetStats() (version, count, maxEntries int, enabled bool)
}

type LocalAuthEntry struct {
    IDTag       string
    IDTagInfo   *IDTagInfo
    Status      string  // "Accepted", "Blocked", "Expired", "ConcurrentTx"
    Expiry      *time.Time
    ParentIDTag *string
}

type IDTagInfo struct {
    Status     string      // "Accepted", "Blocked", "Expired", "ConcurrentTx", "Invalid"
    ExpiryDate *time.Time
    ParentIDTag *string
}

type ChargingProfileInfo struct {
    ProfileID   int
    ConnectorID int
    Purpose     string  // "TxDefaultProfile", "TxProfile", "ChargePointMaxProfile"
    StackLevel  int
    Kind        string  // "Absolute", "Recurring", "Relative"
}

type ChargingProfile struct {
    ProfileID        int
    ConnectorID      int
    Purpose          string
    StackLevel       int
    Kind             string               // "Absolute", "Recurring", "Relative"
    RecurrencyKind   string               // "Daily", "Weekly" (only for Kind="Recurring")
    ValidFrom        *time.Time
    ValidTo          *time.Time
    StartSchedule    *time.Time
    ChargingSchedule ChargingSchedule
}

type ChargingSchedule struct {
    Duration           int                      // seconds (optional)
    StartSchedule      *time.Time
    ChargingRateUnit   string                   // "A" (Amperes) or "W" (Watts)
    MinChargingRate    float64
    Periods            []ChargingSchedulePeriod
}

type ChargingSchedulePeriod struct {
    StartPeriod int     // seconds from schedule start
    Limit       float64 // current limit in A or kW
    NumberPhases *int   // 1 or 3 (optional, default from connector)
}

type FirmwareStatus struct {
    Status       string  // "Idle", "Downloading", "Downloaded", "Installing", "Installed", "InstallationFailed"
    Location     *string
    RetrieveDate *time.Time
    FileName     *string
    FileHash     *string
}

type DiagnosticsStatus struct {
    Status   string  // "Idle", "Uploading", "Uploaded", "UploadFailed"
    Location *string
}

type OCPPMessageInfo struct {
    MessageID      string
    Action         string
    Timestamp      time.Time
    CorrelationKey *string
}
```

### 12.4 Limit Injection Flow

The charging profile limit is injected via a callback pattern:

```
1. OCPP adapter creates ChargingProfileManager.
2. On connection, bridge sets Engine.GetLimit = func(connectorID, txID) *float64 {
       return profileManager.GetCompositeLimit(connectorID, txID, now, voltage, phases)
   }
3. On each Simulate() tick, engine calls GetLimit to get effective current.
4. If limit == 0 and connector is CHARGING → auto-transition to SUSPENDED_EVSE.
5. If limit > 0 and connector is SUSPENDED_EVSE → auto-resume to CHARGING.
```

### 12.4.1 ChargingProfileManager Algorithm

The `ChargingProfileManager` (at `internal/ocpp/profile_manager.go`) computes
the effective charging limit for a connector at a given point in time. It is
thread-safe via `sync.RWMutex`.

**Profile storage:**
- Profiles are stored in a map keyed by `(connectorID, profileID)`.
- Each profile has a `Purpose` (ChargePointMaxProfile, TxDefaultProfile, TxProfile),
  a `StackLevel` (0–9, higher = higher priority), and a `Kind` (Absolute, Recurring,
  Relative).
- Optional persistence to `~/.chargeghost/charging_profiles.json`.

**GetCompositeLimit algorithm:**

```
func GetCompositeLimit(connectorID, transactionID, now, connectorVoltage, transactionStart, phases) *float64

1. Resolve ChargePointMaxProfile limit:
   a. Get all profiles with Purpose=ChargePointMaxProfile for connectorID (or connector 0 = global).
   b. Filter: profile must be valid (ValidFrom ≤ now ≤ ValidTo, or no bounds).
   c. Group by StackLevel; pick highest StackLevel.
   d. For the winning profile, compute elapsed time since schedule start:
      - Absolute: elapsed = now - StartSchedule
      - Relative: elapsed = now - transactionStart (return nil if no transactionStart)
      - Recurring: elapsed = (now - StartSchedule) % cycleLength
        where cycleLength = 86400s (Daily) or 604800s (Weekly)
   e. Find the charging schedule period where StartPeriod ≤ elapsed < next period's StartPeriod.
   f. If ChargingRateUnit is "W", convert to Amps: limitA = limitW / (voltage × phases).
   g. Return the limit in Amps.

2. Resolve TxProfile/TxDefaultProfile limit:
   a. Try TxProfile first: filter profiles where Purpose=TxProfile and
      connectorID matches AND the profile's TransactionID matches transactionID.
   b. If no matching TxProfile, fall back to TxDefaultProfile for connectorID
      (or connector 0 = global).
   c. Apply same validity/stack-level/elapsed logic as step 1.
   d. Return the limit in Amps.

3. Composite result:
   - If both limits exist: return min(chargePointMaxLimit, txLimit).
   - If only one exists: return that limit.
   - If neither exists: return nil (no limit, use connector's full current).
```

**GetCompositeSchedule algorithm:**

Builds a unified schedule over a given duration by collecting time boundaries
from all applicable profiles and computing the composite limit at each boundary:
1. Collect all period start times from all applicable profiles.
2. At each time boundary, compute the individual profile limits.
3. The composite limit at each boundary is `min(all applicable limits)`.
4. Return the list of `(startPeriod, limit)` tuples.

**Constraints:**
- Max profiles: 20 (configurable)
- Max stack level: 5 (configurable)
- Max schedule periods per profile: 10 (configurable)
```

### 12.5 Transaction ID Assignment

In OCPP 1.6, the CSMS assigns the transaction ID in `StartTransaction.conf`.
The flow:

```
1. API calls Engine.StartSession(connectorID, txID=-1, ...)
2. Engine fires OnSessionStarted
3. OCPP adapter enqueues StartTransaction on command channel
4. Command channel delivers to CSMS
5. CSMS responds with transaction_id=42
6. OCPP adapter calls Engine.SetActiveTransaction(connectorID, 42)
   (updates Session.TransactionID under engine lock)
```

The Go engine must support the CSMS-assigned pattern by allowing
`TransactionID` to be updated after session creation via `SetActiveTransaction`.
Thread safety is critical — the update acquires the engine write lock.

> **Future work (OCPP 2.0.1):** In 2.0.1, the charge point generates the
> transaction ID locally before sending `TransactionEvent(Started)`. The
> `EngineView.SetActiveTransaction` interface accommodates this — the caller
> simply sets the ID immediately rather than waiting for a CSMS response.

### 12.6 Offline Message Queue

When the OCPP WebSocket is disconnected, transaction-critical messages must
be buffered and replayed on reconnection. Two backend implementations are required:

**Backends:**
- **`InMemoryQueue`**: Thread-safe in-memory FIFO queue. Lost on process restart.
- **`JsonFileQueue`**: Persisted to `~/.chargeghost/message_queue.json`. Survives restarts.

**Message types to queue:**
- `StartTransaction` / `TransactionEventStarted`
- `StopTransaction` / `TransactionEventEnded`
- `MeterValues`

**Queue data structure (JSON file):**
```json
{
    "messages": [
        {
            "id": "msg-uuid",
            "type": "StartTransaction",
            "payload": { ... },
            "created_at": "2025-04-05T10:00:00Z",
            "retry_count": 0,
            "max_retries": 3
        }
    ]
}
```

**Queue behavior:**
- FIFO ordering — messages sent in the order they were queued.
- Max retry attempts (default 3) before dropping.
- On reconnection: drain all queued messages sequentially, in order.
- Response callbacks update session state (e.g., transaction ID assignment).
- The `persist_message_queue` config field controls which backend is used:
  - `true` → `JsonFileQueue` (survives restarts)
  - `false` → `InMemoryQueue` (lost on restart)

**Message ID assignment**: Go's `google/uuid` package generates unique IDs for
each queued message.

---

## 13. Configuration

### 13.1 Config File

Location: `~/.chargeghost/config.json`

```json
{
    "connection_url": "wss://localhost:3000/CP_1",
    "ocpp_id": "CP_1",
    "charge_point_model": "ChargeGhostV1",
    "charge_point_vendor": "ChargeGhost",
    "connectors": [
        { "voltage": 230.0, "current": 32.0, "phase": 1 }
    ],
    "skip_tls_verify": false,
    "log_mode": "shallow",
    "multi_evse_mode": false,
    "ev_battery_capacity": 55.0,
    "ocpp_version": "1.6",
    "persist_message_queue": false,
    "rfid_tag": null,
    "ignored_version": null
}
```

**Unit convention:** `ev_battery_capacity` is stored in **kWh** in the config
file (user-facing unit). The engine converts to Wh internally on load
(`Engine.EVBatteryCapacity = config.EVBatteryCapacity * 1000`). All engine-
internal calculations use Wh.

**Password** is stored separately (system keyring in Python; in Go, consider
OS-native keychain via `zalando/go-keyring` or environment variable).

### 13.2 Config Categories

| Category | Fields | Change Effect |
|----------|--------|---------------|
| Bridge | connection_url, ocpp_id, ocpp_password, skip_tls_verify, charge_point_model, charge_point_vendor, ocpp_version | Restart OCPP connection |
| Topology | multi_evse_mode, connectors, ev_battery_capacity | Full runtime rebuild |
| Other | log_mode, persist_message_queue, rfid_tag, ignored_version | Immediate, no restart |

Topology changes are rejected if any session is active.

---

## 14. Concurrency Model

Go's concurrency model differs significantly from the Python version's threading
approach. The recommended pattern:

### 14.1 Engine Mutex

The engine uses `sync.RWMutex` to protect all mutable state. Write operations
(session start/stop, plug/unplug, simulate) acquire a write lock. Read-only
operations (status queries, connector lookups) acquire a read lock, allowing
concurrent reads from multiple API handlers.

```go
func (e *Engine) PlugIn(connectorID int) {
    e.mu.Lock()
    defer e.mu.Unlock()
    // ... state mutations ...
}

func (e *Engine) GetConnector(connectorID int) *Connector {
    e.mu.RLock()
    defer e.mu.RUnlock()
    return e.connectors[connectorID]
}
```

### 14.2 Simulation Goroutine

A dedicated goroutine runs the fixed-timestep simulation loop. It holds the
write lock only during `Simulate()`.

### 14.3 API Handlers

HTTP handlers are naturally concurrent in Go. Each handler should:

1. Call engine methods (which acquire the lock internally).
2. Return immediately — do not hold the lock during I/O.

For the async command pattern used in the Python version (to run commands on
the simulation thread), the Go version can simply use the mutex since Go
goroutines are lightweight and the engine operations are fast.

### 14.4 Event Callbacks

Engine events (`OnSessionStarted`, etc.) are called **while the engine write
lock is held**. This means:

- OCPP sends must be non-blocking — enqueue to the OCPP command channel.
- WebSocket broadcasts must be non-blocking — use `Hub.BroadcastAsync()`.
- **Never** call back into the engine from an event handler — deadlock.

### 14.5 OCPP Command Channel (Ordered Message Delivery)

Spawning a goroutine per OCPP send (`go adapter.SendStartTransaction(...)`)
risks out-of-order delivery — a `StopTransaction` could arrive at the CSMS
before the corresponding `StartTransaction`. The Python version avoids this
with a `command_queue` (FIFO). The Go version uses a dedicated command channel:

```go
// internal/ocpp/command.go

type OCPPCommand struct {
    Execute func() error
    Description string  // For logging/debugging
}

type CommandDispatcher struct {
    commands chan OCPPCommand
    adapter  OCPPAdapter
}

func NewCommandDispatcher(adapter OCPPAdapter) *CommandDispatcher {
    return &CommandDispatcher{
        commands: make(chan OCPPCommand, 256),
        adapter:  adapter,
    }
}

// Run drains the command channel sequentially, guaranteeing FIFO order.
// Must be called in a dedicated goroutine.
func (d *CommandDispatcher) Run(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case cmd := <-d.commands:
            if err := cmd.Execute(); err != nil {
                slog.Error("OCPP command failed", "description", cmd.Description, "error", err)
            }
        }
    }
}

// Enqueue adds a command to the channel. Non-blocking — safe to call
// from engine event callbacks (which hold the engine lock).
func (d *CommandDispatcher) Enqueue(cmd OCPPCommand) {
    select {
    case d.commands <- cmd:
    default:
        slog.Warn("OCPP command channel full, dropping", "description", cmd.Description)
    }
}
```

**Wiring example:**

```go
dispatcher := NewCommandDispatcher(adapter)
go dispatcher.Run(ctx)

eng.OnSessionStarted = func(connectorID int) {
    session := eng.GetSession(connectorID)
    dispatcher.Enqueue(OCPPCommand{
        Description: "StartTransaction",
        Execute: func() error {
            _, err := adapter.SendStartTransaction(
                connectorID, *session.IDTag, meterStart, time.Now(), session.ReservationID,
            )
            return err
        },
    })
    hub.BroadcastAsync(...)
}
```

### 14.6 OCPP Connection Thread

The OCPP adapter runs in its own goroutine(s) with its own WebSocket connection.
Communication with the engine is via:
- Engine method calls (mutex-protected).
- Callback injection (`GetLimit`, `EngineView`).
- Command channel (for ordered outbound OCPP messages).

---

## 15. Implementation Order

### Phase 1: Domain Core (no I/O)

1. `ConnectorState` enum and transition table (15 valid transitions)
2. `Connector` with state machine, plug/unplug, parameter validation
3. `EnergyMeter` with update calculation
4. `Session` with energy tracking and SoC
5. `Reservation` with expiry
6. `Engine` wiring all components together (RWMutex, error sentinels, transaction ID tracking)
7. Unit tests for all domain logic

### Phase 2: Simulation Loop

1. Fixed-timestep loop with accumulator
2. `Simulate()` step function
3. Charging limit injection (`GetLimit` callback)
4. EVSE auto-suspend/resume based on limit

### Phase 3: REST API

1. HTTP server setup (net/http + chi or echo)
2. Status endpoint (`GET /status`)
3. Connector CRUD + actions
4. Session start/stop/info
5. Configuration read/write
6. CORS middleware for 3rd-party GUIs
7. Timeline endpoints (`/api/v1/timeline`) — requires `internal/timeline/store.go`
8. Local auth list endpoints (`/api/v1/local-auth-list`)
9. Firmware/diagnostics endpoints (`/api/v1/firmware`, `/api/v1/diagnostics`)
10. About endpoint (`/api/v1/about`)

### Phase 4: WebSocket Events

1. WebSocket hub (single-goroutine, no mutex) with broadcast
2. Engine event subscriptions
3. State snapshot on connect
4. Periodic tick broadcast
5. `connector_params_changed` event type

### Phase 5: OCPP Integration

1. Define `OCPPAdapter` + `EngineView` interfaces (with transaction ID tracking)
2. `CommandDispatcher` for ordered outbound OCPP messages
3. Implement bridge that subscribes to engine events
4. Wire OCPP → engine command flow (all 1.6 inbound handlers)
5. `ChargingProfileManager` with full composite limit algorithm (Section 12.4.1)
6. Offline message queue (in-memory + JSON file backends)
7. Configuration key management (`GetConfiguration`, `ChangeConfiguration`)
8. Authorization cache
9. Local auth list (inbound `GetLocalListVersion`, `SendLocalList`)
10. Firmware + diagnostics managers (with simulation timing per Section 10.11)
11. Vendor data transfer registry
12. All outbound OCPP sends (including `DataTransfer`, `DiagnosticsStatusNotification`, `FirmwareStatusNotification`)

### Phase 6: Polish

1. Configuration persistence (JSON file with keyring integration)
2. Graceful shutdown
3. Health check endpoint (`GET /health`)
4. OpenAPI spec generation
5. Dockerfile
6. OCPP config key REST endpoints (`GET /api/v1/ocpp/config-keys`, `PATCH /api/v1/ocpp/config-keys`)
7. Raw OCPP send endpoints (boot notification, status notification, meter values, data transfer, diagnostics status, firmware status)

---

## 16. Reference: Python Entity Crosswalk

| Python Source | Go Target | Notes |
|---|---|---|
| `engine/engine.py` → `Engine` | `internal/engine/engine.go` → `Engine` | Core coordinator |
| `engine/connector.py` → `Connector` | `internal/engine/connector.go` → `Connector` | State machine entity |
| `engine/connector.py` → `ConnectorState` | `internal/engine/state.go` → `ConnectorState` | String enum |
| `engine/connector.py` → `VALID_TRANSITIONS` | `internal/engine/state.go` → transition table | Map of (state, action) → state |
| `engine/session.py` → `Session` | `internal/engine/session.go` → `Session` | Charging session |
| `engine/energy_meter.py` → `EnergyMeter` | `internal/engine/energy_meter.go` → `EnergyMeter` | Odometer accumulator |
| `engine/reservation.py` → `Reservation` | `internal/engine/reservation.go` → `Reservation` | Time-bounded reservation |
| `util/event.py` → `Event` | `internal/event/bus.go` → callbacks/channels | Replace with Go channels or func fields |
| `util/subscriber.py` → `Subscriber` | Not needed | Go GC handles cleanup; use callbacks |
| `util/config.py` → `SimulationConfig` | `internal/config/config.go` → `Config` | JSON config file |
| `util/config.py` → `ConnectorConfig` | `internal/config/config.go` → `ConnectorConfig` | Per-connector settings |
| `bridge/bridge.py` → `Bridge` | OCPP integration layer | Subscribes to engine events |
| `bridge/bridge.py` → `AsyncRunner` | OCPP integration layer | WebSocket connection management |
| `bridge/message_queue.py` → `MessageQueue` | OCPP integration layer | Offline message buffering |
| `api/routes/connectors.py` | `internal/api/handlers/connectors.go` | REST handlers |
| `api/routes/sessions.py` | `internal/api/handlers/sessions.go` | REST handlers |
| `api/routes/status.py` | `internal/api/handlers/status.go` | REST handlers |
| `api/routes/config.py` | `internal/api/handlers/config.go` | REST handlers |
| `api/routes/ocpp.py` | `internal/api/handlers/ocpp.go` | REST handlers |
| `api/routes/reservations.py` | `internal/api/handlers/reservations.go` | REST handlers |
| `api/routes/charging_profiles.py` | `internal/api/handlers/charging_profiles.go` | REST handlers |
| `api/routes/timeline.py` | `internal/api/handlers/timeline.go` | REST handlers (new in Go) |
| `api/routes/local_auth.py` | `internal/api/handlers/local_auth.go` | REST handlers (new in Go) |
| `api/routes/firmware.py` | `internal/api/handlers/firmware.go` | REST handlers (new in Go) |
| `api/routes/about.py` | `internal/api/handlers/about.go` | REST handlers (new in Go) |
| `api/ws_manager.py` → `WebSocketManager` | `internal/api/ws/hub.go` → `Hub` | Event streaming |
| `api/runtime.py` → `SimulationRuntime` | `cmd/chargeghost/main.go` wiring | Runtime lifecycle |
| `api/schemas.py` | `internal/api/dto.go` | JSON DTOs |
| `ocpp_adapter/charging_profile_manager.py` | `internal/ocpp/profile_manager.go` | Smart charging |
| `ocpp_adapter/config_keys.py` | `internal/ocpp/config_keys.go` | OCPP config key manager |
| `ocpp_adapter/firmware_manager.py` | `internal/ocpp/firmware_manager.go` | Firmware + diagnostics |
| `ocpp_adapter/local_auth_list.py` | `internal/ocpp/local_auth_list.go` | Local auth list |
| `ocpp_adapter/auth_cache.py` | `internal/ocpp/auth_cache.go` | Authorization cache |
| `ocpp_adapter/data_transfer.py` | `internal/ocpp/data_transfer.go` | Vendor data transfer |
| `bridge/message_queue.py` | `internal/ocpp/queue/` | Offline queue (in-memory + JSON) |
| `devtools/timeline_store.py` | `internal/timeline/store.go` | OCPP event timeline (re-implemented for API access) |
| `devtools/simulator_controller.py` | Not needed for Go | API handlers call engine directly |

### What to Drop

| Python Concept | Reason |
|---|---|
| `Subscriber` mixin | Go has no need for weak-reference subscription patterns; use callbacks/channels |
| `Event` class with weak refs | Replace with simple func fields or a channel-based bus |
| `FaultManager` | Dev/testing tool; can be added later if needed |
| `TimelineStore` | Re-implemented in Go as `internal/timeline/store.go` for API access; the Python devtools version is a reference only |
| `ScenarioRunner` | Dev/testing tool; can be added later if needed |
| `UpdateManager` / `HandoverManager` | App update mechanism; irrelevant for a Go binary/service |
| `QtSignalBridge` / all of `ui/` | GUI layer; explicitly excluded |
| `log_setup.py` / `markup.py` | Rich console formatting; use Go `log/slog` |
| `keyring` dependency | Use environment variable or `zalando/go-keyring` |
| OCPP 2.0.1 adapter (`v201_adapter.py`, `*_v201.py`) | Out of scope for initial Go release; see Section 17 |

### What to Preserve

The following Python components have direct equivalents in the Go plan and must be fully implemented:

| Python Component | Go Equivalent | Notes |
|---|---|---|
| `ocpp_adapter/adapter.py` | `internal/ocpp/adapter.go` | Full OCPP 1.6 adapter |
| `ocpp_adapter/charging_profile_manager.py` | `internal/ocpp/profile_manager.go` | Smart charging for OCPP 1.6 |
| `ocpp_adapter/config_keys.py` | `internal/ocpp/config_keys.go` | OCPP configuration key management |
| `ocpp_adapter/firmware_manager.py` | `internal/ocpp/firmware_manager.go` | Firmware + diagnostics simulation |
| `ocpp_adapter/local_auth_list.py` | `internal/ocpp/local_auth_list.go` | Local authorization list |
| `ocpp_adapter/auth_cache.py` | `internal/ocpp/auth_cache.go` | Authorization cache |
| `ocpp_adapter/data_transfer.py` | `internal/ocpp/data_transfer.go` | Vendor-specific data transfer |
| `bridge/message_queue.py` | `internal/ocpp/queue/` | In-memory + JSON file offline queue |
| `devtools/timeline_store.py` | `internal/timeline/store.go` | OCPP event timeline (re-implemented) |
| `api/routes/timeline.py` | `internal/api/handlers/timeline.go` | Event timeline API |
| `api/routes/local_auth.py` | `internal/api/handlers/local_auth.go` | Local auth list API |
| `api/routes/firmware.py` | `internal/api/handlers/firmware.go` | Firmware/diagnostics API |
| `api/routes/about.py` | `internal/api/handlers/about.go` | App info endpoint |

---

## Appendix A: Complete State Transition Diagram

```
                    ┌─────────────┐
         plug_in    │             │  unplug
     ┌──────────────►  Available  ◄──────────────┐
     │              │             │               │
     │              └──────┬──────┘               │
     │                     │ start_charging       │ unplug
     │              ┌──────▼──────┐               │
     │              │  Preparing  ├──unplug───────►│
     │              └──────┬──────┘               │
     │                     │ start_charging       │
     │              ┌──────▼──────┐               │
     │     ┌────────│  Charging   │──────────┐    │
     │     │        └──┬───┬───┬──┘          │    │
     │     │           │   │   │             │    │
     │  suspend_ev    │   │   │  suspend_evse│   │
     │     │           │   │   │             │    │
     │ ┌───▼──────┐   │   │   │  ┌──────────▼──┐ │
     │ │Suspended │   │   │   │  │Suspended    │ │ │
     │ │   EV     │   │   │   │  │   EVSE      │ │ │
     │ └──┬───────┘   │   │   │  └──┬──────────┘ │ │
     │    │ resume    │   │   │     │ resume     │ │
     │    └───────────┘   │   │     └────────────┘ │
     │                    │   │                    │
     │        stop_charging   │         unplug     │
     │              ┌──────▼──────┐                │
     │              │  Finishing  ├────────────────┘
     │              └─────────────┘
     │
     │  (also: Reserved ──plug_in──► Preparing)
     │  (also: SetUnavailable ──► Unavailable)
     │  (also: SetReserved ──► Reserved)
     │  (also: SetOperative ──► Available/Preparing)
```

## Appendix B: Suggested Go Dependencies

| Purpose | Library | Why |
|---|---|---|
| HTTP Router | `go-chi/chi v5` | Lightweight, stdlib-compatible |
| WebSocket | `gorilla/websocket` or `nhooyr.io/websocket` | Mature, well-tested |
| UUID | `google/uuid` | For message IDs |
| Keyring | `zalando/go-keyring` | Cross-platform secret storage |
| Logging | `log/slog` (stdlib) | Structured logging since Go 1.21 |
| OCPP | `lorenzodonini/ocpp-go` | Go OCPP 1.6/2.0.1 library |
| Testing | stdlib `testing` + `stretchr/testify` | Assertions, mocks |

## Appendix C: Minimal main.go Skeleton

```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "chargeghost-engine/internal/api"
    "chargeghost-engine/internal/api/ws"
    "chargeghost-engine/internal/config"
    "chargeghost-engine/internal/engine"
)

func main() {
    cfg, err := config.Load()
    if err != nil {
        slog.Error("failed to load config", "error", err)
        os.Exit(1)
    }

    eng := engine.New(cfg.MultiEVSEMode)
    eng.SetBatteryCapacity(cfg.EVBatteryCapacity * 1000) // kWh → Wh
    for _, cc := range cfg.Connectors {
        eng.AddConnector(cc.Voltage, cc.Current, cc.Phase)
    }

    hub := ws.NewHub()
    go hub.Run()

    // Wire engine events to WebSocket broadcasts + OCPP command channel
    // (In production, the OCPP adapter and dispatcher are created by the bridge)
    eng.OnConnectorStatusChanged = func(connectorID int, status engine.ConnectorState) {
        hub.BroadcastMessage(ws.Message{
            Type: "connector_status_changed",
            Data: map[string]any{"connector_id": connectorID, "status": string(status)},
        })
    }
    eng.OnConnectorParamsChanged = func(connectorID int, voltage, current float64, phase int) {
        hub.BroadcastMessage(ws.Message{
            Type: "connector_params_changed",
            Data: map[string]any{"connector_id": connectorID, "voltage": voltage, "current": current, "phase": phase},
        })
    }
    eng.OnSessionStarted = func(connectorID int) {
        hub.BroadcastMessage(ws.Message{
            Type: "session_started",
            Data: map[string]any{"connector_id": connectorID},
        })
    }
    eng.OnSessionStopped = func(connectorID int) {
        hub.BroadcastMessage(ws.Message{
            Type: "session_stopped",
            Data: map[string]any{"connector_id": connectorID},
        })
    }

    // Start simulation loop
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go runSimulationLoop(ctx, eng)

    // Setup HTTP server
    router := api.NewRouter(eng, hub, cfg)
    srv := &http.Server{
        Addr:    fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
        Handler: router,
    }

    // Graceful shutdown
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        slog.Info("server starting", "addr", srv.Addr)
        if err := srv.ListenAndServe(); err != http.ErrServerClosed {
            slog.Error("server error", "error", err)
        }
    }()

    <-sigCh
    slog.Info("shutting down...")
    cancel()
    shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer shutdownCancel()
    srv.Shutdown(shutdownCtx)
}

func runSimulationLoop(ctx context.Context, eng *engine.Engine) {
    ticker := time.NewTicker(50 * time.Millisecond)
    defer ticker.Stop()

    stepInterval := 100 * time.Millisecond
    accumulator := 0.0
    lastTick := time.Now()

    for {
        select {
        case <-ctx.Done():
            return
        case now := <-ticker.C:
            delta := now.Sub(lastTick).Seconds()
            lastTick = now
            accumulator += delta

            steps := 0
            for accumulator >= stepInterval.Seconds() && steps < 5 {
                eng.Simulate(stepInterval)
                accumulator -= stepInterval.Seconds()
                steps++
            }
        }
    }
}

---

## 17. OCPP 2.0.1 Decision

### Decision: Out of Scope for Initial Go Release

OCPP 2.0.1 support is **not included** in the Go implementation plan. This section
documents the rationale and the implications for future work.

### Rationale

1. **Scope control**: The Python codebase already has a full OCPP 2.0.1 implementation
   plan (`docs/ocpp201-implementation-plan.md`). If Go were to include V2.0.1 from the
   start, the initial implementation would double in complexity (~5,200 new lines of
   OCPP-specific code alone).

2. **Python continues V2.0.1 development**: The Python implementation remains the
   primary vehicle for OCPP 2.0.1 features. The Go service is explicitly a V1.6-only
   product for its initial release.

3. **OCPP 1.6J is still dominant**: V1.6J is the most widely deployed version in
   production. A working V1.6 implementation is immediately useful.

4. **Library support**: The `lorenzodonini/ocpp-go` library supports both V1.6 and
   V2.0.1. Adding V2.0.1 later is architecturally straightforward — it does not
   require structural changes, only additional adapter code.

### What This Means

| Item | Status |
|---|---|
| OCPP 1.6J adapter | Full implementation in scope |
| OCPP 2.0.1 adapter | Out of scope for v1.0 |
| `TransactionEvent` (V201) | Not implemented |
| Device model (`component/variable` tree) | Not implemented |
| Variable monitoring | Not implemented |
| Display messages | Not implemented |
| V201 charging profile manager | Not implemented |
| V201 firmware/diagnostics managers | Not implemented |
| V201 local auth list | Not implemented |

### Adding OCPP 2.0.1 Later

When OCPP 2.0.1 support is needed, the following approach is recommended:

1. **New package**: `internal/ocpp/v201/` with its own adapter, profile manager, etc.
2. **Shared engine**: The `Engine` and domain models are version-agnostic — no changes needed.
3. **Version routing**: The bridge already knows which version is active (`cfg.OcppVersion`).
   Route to the appropriate adapter at the bridge layer.
4. **Shared components**: `AuthorizationCache`, `LocalAuthList`, `FirmwareManager`,
   `DataTransferRegistry` can be shared across versions with V201-specific wrappers.

### Feature Detection

Clients that need to know whether V2.0.1 is available can query `GET /api/v1/about`:

```json
{
    "ocpp_versions": ["1.6J"],
    "features": [...]
}
```

An `ocpp_versions` array with `"2.0.1"` added indicates V2.0.1 support.
```
