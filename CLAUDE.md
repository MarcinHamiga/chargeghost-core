# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**ChargeGhost EVSE Simulation Engine** — A Go reimplementation of an EV charging station simulator.

The engine simulates the behavior of electric vehicle supply equipment (EVSE) and is controlled exclusively via a REST API + WebSocket event streaming. OCPP (Open Charge Point Protocol) 1.6J support is delegated to the `lorenzodonini/ocpp-go` external library.

**Key design principle**: The Engine is the single source of truth for all simulation state. The REST API is a thin control surface; OCPP adapters call engine methods (not vice versa). All state mutations flow through the Engine.

## Project Structure

```
chargeghost-core/
├── cmd/chargeghost/
│   └── main.go                      # Entry point; wires engine, API, OCPP v1.6/v2.0.1
├── internal/
│   ├── engine/                      # Domain logic (Connectors, Sessions, Meters, Reservations)
│   │   ├── engine.go                # Core simulation + state mutations + callbacks
│   │   ├── connector.go             # Connector entity + validation
│   │   ├── session.go               # Charging session lifecycle
│   │   ├── energy_meter.go          # Cumulative energy tracking
│   │   ├── reservation.go           # Connector reservations
│   │   ├── state.go                 # ConnectorState enum + transitions
│   │   ├── engine_test.go           # Engine integration tests
│   │   └── *_test.go                # Domain tests
│   ├── api/                         # REST API (chi-based router)
│   │   ├── server.go                # HTTP server setup (wraps chi router)
│   │   ├── router.go                # Route registration & middleware
│   │   ├── handlers.go              # Connector, session, config, reservation handlers
│   │   ├── handlers/                # Specialized handlers
│   │   │   ├── status.go            # GET /api/v1/status
│   │   │   ├── timeline.go          # Timeline CRUD
│   │   │   ├── local_auth.go        # Local auth list CRUD
│   │   │   ├── firmware.go          # Firmware update control
│   │   │   ├── charging_profiles.go # Charging profile CRUD
│   │   │   ├── ocpp.go              # OCPP config keys + raw message sending
│   │   │   ├── about.go             # GET /api/v1/about
│   │   │   └── helpers.go           # Shared handler utilities
│   │   ├── dto.go                   # JSON request/response structures
│   │   ├── handlers_test.go         # HTTP handler tests
│   │   └── ws/                      # WebSocket hub for real-time events
│   │       ├── hub.go               # Connection manager + broadcasting
│   │       ├── messages.go          # Message types (connector_status_changed, etc.)
│   │       ├── tick.go              # Periodic state snapshot broadcaster (1s interval)
│   │       └── hub_test.go          # Hub tests
│   ├── ocpp/                        # OCPP version-agnostic adapter layer
│   │   ├── bridge.go                # OCPPBridge interface contract
│   │   ├── adapter.go               # EngineView interface contract
│   │   ├── command.go               # CommandDispatcher for ordered message delivery
│   │   ├── meter_ticker.go          # Periodic MeterValues sender (configurable interval)
│   │   ├── auth_cache.go            # Authorization caching layer
│   │   ├── local_auth_list.go       # Local auth list manager (real implementation)
│   │   ├── firmware_manager.go      # Firmware update simulation
│   │   ├── data_transfer.go         # Data transfer message registry
│   │   ├── interfaces.go            # LocalAuthManager, FirmwareManager, DiagnosticsManager, ConfigKeyAPI interfaces
│   │   ├── queue/                   # Message queue backends
│   │   │   ├── queue.go             # Queue interface + factory
│   │   │   ├── memory.go            # In-memory queue
│   │   │   └── json_file.go         # File-based persistence
│   │   ├── v16/                     # OCPP 1.6J implementation
│   │   │   ├── bridge.go            # Bridge16: concrete OCPP 1.6J bridge
│   │   │   ├── handlers.go          # OCPP 1.6J message handlers (Authorize, StartTx, etc.)
│   │   │   ├── senders.go           # Outbound message methods (SendBootNotification, etc.)
│   │   │   ├── profile_manager.go   # Smart charging profile algorithm
│   │   │   ├── config_keys.go       # OCPP configuration key management (MeterValueSampleInterval, etc.)
│   │   │   ├── *_test.go            # v1.6 specific tests
│   │   ├── v201/                    # OCPP 2.0.1 implementation
│   │   │   ├── bridge.go            # Bridge201: concrete OCPP 2.0.1 bridge
│   │   │   ├── handlers.go          # OCPP 2.0.1 inbound CSMS message handlers
│   │   │   ├── senders.go           # Outbound message methods (BootNotification, TransactionEvent, etc.)
│   │   │   ├── transaction.go       # TransactionEventBuilder (Started/Updated/Ended)
│   │   │   ├── device_model.go      # Device model variable store (replaces v1.6 config keys)
│   │   │   ├── profile_manager.go   # Smart charging profile manager (2.0.1 types)
│   │   │   ├── monitoring.go        # Variable monitoring manager
│   │   │   ├── display.go           # Display message store
│   │   │   ├── cost.go              # CostUpdated tracking
│   │   │   ├── integration_test.go  # Integration tests with mock CSMS (build tag: integration)
│   │   │   ├── *_test.go            # v2.0.1 unit tests
│   │   └── *_test.go                # OCPP layer tests
│   ├── config/                      # Configuration persistence
│   │   ├── config.go                # Load/save ~/.chargeghost/config.json + keyring password
│   │   └── config_test.go           # Config tests
│   ├── timeline/                    # OCPP event timeline (ring buffer storage)
│   │   ├── models.go                # TimelineEvent model
│   │   ├── store.go                 # Ring buffer store (fixed size)
│   │   └── store_test.go            # Store tests
│   ├── runtime/                     # Simulation loop + timing
│   │   ├── runtime.go               # Fixed-timestep simulation runner (20 Hz, 100 ms steps)
│   │   └── runtime_test.go          # Runtime tests
│   └── docs/                        # API documentation (if present)
└── GO_REIMPLEMENTING_GUIDE.md       # Detailed architecture & domain model spec
```

## Build & Test Commands

**Build:**
```bash
go build -o chargeghost ./cmd/chargeghost
```

**Run:**
```bash
./chargeghost
# Default: HTTP on :8080, OCPP on :9000
# Config stored in ~/.chargeghost/config.json
# OCPP password stored securely in system keyring
```

**Run Tests:**
```bash
go test ./...                    # All tests
go test -v ./...                # Verbose output
go test -run TestName ./path    # Single test (e.g., go test -run TestConnector ./internal/engine)
```

**Docker:**
```bash
docker build -t chargeghost .
docker run -p 8080:8080 -p 9000:9000 chargeghost
```

**Code Quality:**
```bash
go fmt ./...       # Format code
go vet ./...       # Static analysis
```

## Architecture & Key Concepts

### Engine: Single Source of Truth

The `Engine` struct (in `internal/engine/engine.go`) is the authoritative state holder:
- Manages all connectors and their state transitions
- Tracks active charging sessions
- Maintains cumulative energy meters
- Handles reservation and authorization logic
- Runs a simulation loop that advances connector/session states via `Simulate(deltaSeconds float64)`

**State mutations ONLY happen through engine methods.** The REST API calls engine methods; the OCPP adapter calls engine methods.

**Engine Callbacks:** The engine exposes these function pointers for external event handling:
- `OnConnectorStatusChanged(connectorID int, status ConnectorState)` — Connector state changed → sends StatusNotification via OCPP
- `OnConnectorParamsChanged(connectorID, voltage, current, phase)` — Voltage/current/phase updated → WebSocket broadcast
- `OnSessionStarted(connectorID int)` — Session began → sends StartTransaction via OCPP
- `OnSessionStopped(connectorID int)` — Session ended → sends StopTransaction via OCPP
- `OnReservationExpired(reservationID, connectorID int)` — Reservation expired → WebSocket broadcast

In `main.go`, these callbacks are wired to:
1. Broadcast WebSocket messages to connected clients
2. Enqueue OCPP commands to the `CommandDispatcher` for ordered delivery to the CSMS

### Connector State Machine

Connectors flow through well-defined states (from OCPP 1.6):
```
Available → Preparing → Charging → Finishing → Available
        ↓                  ↓                        ↑
     Reserved        Suspended*                    │
        │            Faulted                      (Unplug)
        └─────────────────────────────────────────┘
Unavailable (persistent across plug/unplug)
```

States govern what operations are allowed (plugging, starting sessions, etc.).

### Charging Sessions

A `Session` ties a connector to energy delivery:
- `TransactionID`: OCPP identifier
- `EnergyCharged`: Cumulative Wh delivered
- `StateOfCharge`: Battery % (0–100, disabled if `MaxEnergy == 0`)
- `MeterHistory`: Timestamped meter snapshots
- Charging profiles (power limits) applied during the session

### Energy Metering

`EnergyMeter` tracks cumulative Wh consumption:
```
energy_wh = (voltage × current × phase × interval_seconds) / 3600
meter_value += energy_wh  // Cumulative (like an odometer)
```

In single-EVSE mode, the meter persists across sessions. In multi-EVSE, each connector gets a per-session meter.

### OCPP Adapter Pattern

The engine does NOT call OCPP directly. Instead:
1. The `OCPPBridge` interface (in `internal/ocpp/bridge.go`) defines the contract for OCPP communication.
2. Concrete implementation: `Bridge16` (in `internal/ocpp/v16/bridge.go`) connects to a CSMS via `lorenzodonini/ocpp-go` 1.6J.
3. The bridge's `handlers.go` implements inbound message handlers called by the OCPP library (Authorize, StartTransaction, StopTransaction, etc.).
4. The bridge's `senders.go` implements outbound message methods (SendBootNotification, SendStatusNotification, SendMeterValues, etc.).
5. Engine methods (via API or OCPP library) trigger state changes and callbacks.
6. Callbacks enqueue `OCPPCommand`s to the `CommandDispatcher` for ordered, async delivery.

**CommandDispatcher** (in `internal/ocpp/command.go`):
- Runs in a dedicated goroutine
- Dequeues commands and executes them serially
- Ensures OCPP messages are delivered in order

**MessageQueue** (in `internal/ocpp/queue/`):
- Optional persistence to disk (`queue.NewQueue(persistMessageQueue, filePath, flushInterval)`)
- In-memory fallback if not enabled
- Commands can be durable across restarts

**Key separation:**
- Engine state mutations don't depend on OCPP
- OCPP inbound messages trigger engine methods
- Engine callbacks trigger OCPP outbound commands
- This allows engine to work offline; OCPP is optional

### REST API Surface

All control is via `/api/v1/*`. Full endpoint list:

**Health & Status:**
- `GET /health` — Liveness probe
- `GET /api/v1/status` — Engine status (uptime, connector count, active sessions)
- `GET /api/v1/about` — Engine version/build info

**Connectors:**
- `GET /api/v1/connectors` — List all connectors
- `POST /api/v1/connectors` — Create connector (voltage, current, phase)
- `GET /api/v1/connectors/{id}` — Get connector details
- `PUT /api/v1/connectors/{id}` — Update connector params (voltage, current, phase)
- `DELETE /api/v1/connectors/{id}` — Delete connector
- `POST /api/v1/connectors/{id}/plug_in` — Simulate EV plug-in
- `POST /api/v1/connectors/{id}/unplug` — Simulate EV unplug
- `POST /api/v1/connectors/{id}/suspend_ev` — Suspend charging (EV side)
- `POST /api/v1/connectors/{id}/resume_charging` — Resume from EV suspend
- `POST /api/v1/connectors/{id}/start-charging` — Begin charging session
- `POST /api/v1/connectors/{id}/stop-charging` — Stop charging session
- `PUT /api/v1/connectors/{id}/rfid` — Set RFID tag for connector
- `DELETE /api/v1/connectors/{id}/rfid` — Clear RFID tag

**Sessions:**
- `GET /api/v1/sessions` — List active sessions
- `POST /api/v1/sessions/start` — Start session (body: `{connectorID, idTag}`)
- `POST /api/v1/sessions/stop` — Stop all sessions
- `GET /api/v1/sessions/last-stopped` — Get last stopped session info
- `GET /api/v1/sessions/active` — Get active sessions
- `GET /api/v1/sessions/info` — Get session stats
- `GET /api/v1/sessions/{connector_id}` — Get session by connector

**Configuration:**
- `GET /api/v1/config` — Get current config
- `PATCH /api/v1/config` — Patch config (multiEVSEMode, evBatteryCapacity, etc.)
- `POST /api/v1/config/save` — Persist config to disk

**Reservations:**
- `GET /api/v1/reservations` — List reservations
- `POST /api/v1/reservations` — Create reservation (body: `{connectorID, expiryMinutes}`)
- `DELETE /api/v1/reservations/{reservation_id}` — Cancel reservation

**Timeline (OCPP Event Log):**
- `GET /api/v1/timeline` — List events (paginated)
- `GET /api/v1/timeline/count` — Event count
- `DELETE /api/v1/timeline` — Clear all events

**Local Authentication List:**
- `GET /api/v1/local-auth-list` — Get all auth entries
- `GET /api/v1/local-auth-list/{id_tag}` — Get specific entry
- `PUT /api/v1/local-auth-list` — Update/replace entries
- `DELETE /api/v1/local-auth-list/{id_tag}` — Delete specific entry
- `DELETE /api/v1/local-auth-list` — Clear all entries

**Firmware Updates:**
- `GET /api/v1/firmware/status` — Get firmware state
- `POST /api/v1/firmware/trigger` — Trigger update (body: `{location, retrieveDate}`)
- `POST /api/v1/firmware/cancel` — Cancel ongoing update

**Diagnostics:**
- `GET /api/v1/diagnostics/status` — Get diagnostics state
- `POST /api/v1/diagnostics/trigger` — Trigger upload (body: `{location, retries, retryInterval}`)
- `POST /api/v1/diagnostics/cancel` — Cancel ongoing upload

**Charging Profiles:**
- `GET /api/v1/charging-profiles` — List installed profiles
- `POST /api/v1/charging-profiles` — Install profile
- `DELETE /api/v1/charging-profiles` — Clear all profiles
- `GET /api/v1/charging-profiles/{profile_id}` — Get specific profile
- `POST /api/v1/charging-profiles/composite-schedule` — Calculate composite schedule

**OCPP (v1.6J) Control:**
- `GET /api/v1/ocpp/config-keys` — Get OCPP config keys
- `PATCH /api/v1/ocpp/config-keys` — Update config key
- `POST /api/v1/ocpp/authorize` — Send Authorize message
- `POST /api/v1/ocpp/heartbeat` — Send Heartbeat
- `POST /api/v1/ocpp/raw/status-notification` — Send raw StatusNotification
- `POST /api/v1/ocpp/raw/meter-values` — Send raw MeterValues
- `POST /api/v1/ocpp/raw/data-transfer` — Send raw DataTransfer
- `POST /api/v1/ocpp/raw/start-transaction` — Send raw StartTransaction
- `POST /api/v1/ocpp/raw/stop-transaction` — Send raw StopTransaction

**WebSocket:**
- `GET /ws` — Connect to event stream (sends initial state snapshot, then streams events)

Event types: `connector_status_changed`, `session_started`, `session_stopped`, `connector_params_changed`, `reservation_changed`, `firmware_status_changed`, `diagnostics_status_changed`, etc.

## Key Files & Patterns

| File | Purpose |
|------|---------|
| `cmd/chargeghost/main.go` | Entry point: wires engine, API server, OCPP bridge, runtime, WebSocket hub, callbacks |
| `internal/engine/engine.go` | Core engine: state holder, connector/session/meter/reservation management, callbacks (OnConnectorStatusChanged, OnSessionStarted, etc.) |
| `internal/engine/state.go` | ConnectorState enum + state transition validation |
| `internal/api/router.go` | Route registration (chi), middleware, CORS setup |
| `internal/api/handlers.go` | Main connector/session/config/reservation handlers |
| `internal/api/handlers/*.go` | Specialized handlers (status, timeline, firmware, local_auth, charging_profiles, ocpp) |
| `internal/api/ws/hub.go` | WebSocket hub: connection mgmt + broadcasting |
| `internal/api/ws/tick.go` | Periodic state snapshot broadcaster (1 second interval) |
| `internal/ocpp/bridge.go` | OCPPBridge interface (version-agnostic contract) |
| `internal/ocpp/v16/bridge.go` | Bridge16: concrete OCPP 1.6J implementation |
| `internal/ocpp/v16/handlers.go` | OCPP 1.6J inbound message handlers (Authorize, StartTransaction, etc.) |
| `internal/ocpp/v16/senders.go` | OCPP 1.6J outbound message methods |
| `internal/ocpp/command.go` | CommandDispatcher: ordered async message delivery |
| `internal/ocpp/meter_ticker.go` | Periodic MeterValues sender (hooks into CommandDispatcher) |
| `internal/ocpp/auth_cache.go` | Authorization caching store |
| `internal/ocpp/local_auth_list.go` | Local auth list manager (full implementation) |
| `internal/ocpp/firmware_manager.go` | Firmware update simulation + status callbacks |
| `internal/ocpp/v16/profile_manager.go` | Smart charging profile algorithm + composite schedule calculation |
| `internal/ocpp/v16/config_keys.go` | OCPP configuration key storage (MeterValueSampleInterval, etc.) |
| `internal/ocpp/v201/bridge.go` | Bridge201: concrete OCPP 2.0.1 implementation |
| `internal/ocpp/v201/handlers.go` | OCPP 2.0.1 inbound CSMS message handlers |
| `internal/ocpp/v201/senders.go` | OCPP 2.0.1 outbound message methods |
| `internal/ocpp/v201/transaction.go` | TransactionEventBuilder (Started/Updated/Ended) |
| `internal/ocpp/v201/device_model.go` | Device model variable store + ConfigKeyAPI |
| `internal/ocpp/v201/monitoring.go` | Variable monitoring manager |
| `internal/ocpp/v201/display.go` | Display message store |
| `internal/ocpp/v201/cost.go` | CostUpdated transaction tracking |
| `internal/config/config.go` | Load/save config (~/.chargeghost/config.json) + keyring password mgmt |
| `internal/timeline/store.go` | Ring buffer for OCPP event history |
| `internal/runtime/runtime.go` | Fixed-timestep simulation loop (20 Hz wake-up, 100 ms steps) |

## Testing

Tests use **testify** (assert/require) and are colocated with source (adjacent `*_test.go` files):
- `internal/engine/*_test.go` — Domain logic tests (engine, connector, session, meter, etc.)
- `internal/api/handlers_test.go` — HTTP handler integration tests
- `internal/api/ws/hub_test.go` — WebSocket hub tests
- `internal/config/config_test.go` — Config load/save tests
- `internal/ocpp/auth_cache_test.go`, `local_auth_list_test.go`, `command_test.go`, `firmware_manager_test.go`, `data_transfer_test.go` — OCPP layer tests
- `internal/ocpp/v16/*_test.go` — OCPP 1.6J specific tests (profile_manager, config_keys)
- `internal/ocpp/v201/*_test.go` — OCPP 2.0.1 unit tests (bridge, handlers, transaction, device_model, monitoring, display, cost, profiles)
- `internal/ocpp/v201/integration_test.go` — Integration tests with mock CSMS (`go test -tags integration`)
- `internal/ocpp/queue/queue_test.go` — Message queue tests
- `internal/timeline/store_test.go` — Timeline store tests
- `internal/runtime/runtime_test.go` — Simulation loop tests

**Pattern**: 
- Use `httptest.NewRequest()` and `httptest.NewRecorder()` for HTTP handler tests
- Create a test `AppContext` and pass it to the router
- Mock the Engine and other dependencies as needed with testify mocks

## Dependencies

| Dependency | Use | Version |
|------------|-----|---------|
| `go-chi/chi/v5` | HTTP routing | v5.2.5 |
| `gorilla/websocket` | WebSocket support | v1.5.3 |
| `lorenzodonini/ocpp-go` | OCPP 1.6J + 2.0.1 protocol library | v0.19.0 |
| `stretchr/testify` | Assertions + mocking | v1.11.1 |
| `google/uuid` | ID generation (connector IDs, transaction IDs) | v1.6.0 |
| `zalando/go-keyring` | Secure credential storage (OCPP password) | v0.2.8 |

**Go Version:** 1.26.1

## Configuration & Runtime

**Config location:** `~/.chargeghost/config.json`

**Config fields:**
- `multiEVSEMode` (bool) — Enable multi-EVSE mode (one connectors' meter ≠ global meter)
- `evBatteryCapacity` (float) — EV battery capacity in kWh (used for StateOfCharge calculation)
- `ocppID` (string) — Charge point identity
- `ocppVersion` (string) — "1.6" (default) or "2.0.1"
- `ocppPassword` (string, from keyring) — CSMS authentication
- `persistMessageQueue` (bool) — Enable message queue persistence to disk
- `connectors` (array) — Pre-configured connectors with voltage, current, phase

**Environment & Security:**
- OCPP password stored in system keyring (macOS Keychain, Linux Secret Service, Windows Credential Manager)
- Configuration persisted to disk on save via `config.Save()`
- Graceful shutdown on `syscall.SIGTERM` / `syscall.SIGINT`

**Simulation timing:**
- **Runtime loop:** Fixed-timestep at 20 Hz (50 ms wake-up), advances simulation in 100 ms steps
- **WebSocket ticker:** Sends state snapshot every 1 second to all connected clients
- **MeterValues ticker:** Periodic meter value sends at configurable interval (OCPP config key `MeterValueSampleInterval`, default 30s)
- **Engine callbacks:** Triggered synchronously during state mutations, enqueue OCPP commands to dispatcher

## Recent Features (Plan 5d+)

The following features have been implemented beyond the core engine:

**Local Authorization List (Plan 5d):**
- Full management of OCPP local auth entries (idTag, status, expiry, parentIDTag)
- Version tracking for list updates
- REST API for CRUD operations
- Handles OCPP `SendLocalList` messages from CSMS

**Firmware Updates (Plan 5e):**
- Firmware update simulation with state machine (Idle, Downloading, Downloaded, Installing, Installed, InstallationFailed)
- REST API to trigger/cancel updates
- Sends FirmwareStatusNotification to CSMS
- Supports location, retrieveDate, fileName, fileHash

**Diagnostics Upload (Plan 5e):**
- Diagnostics upload simulation (Idle, Uploading, Uploaded, UploadFailed)
- REST API to trigger/cancel uploads
- Sends DiagnosticsStatusNotification to CSMS
- Supports retries and retry intervals

**Charging Profiles (Plan 5d):**
- Install/manage smart charging profiles
- Composite schedule calculation across all profiles
- Profile types: TxProfile, ChargingProfilePeriod
- `GetCompositeLimit()` callback in engine for dynamic power limiting
- REST API for CRUD + composite schedule calculation

**OCPP Configuration Keys (Plan 5d):**
- In-memory storage of OCPP config keys (MeterValueSampleInterval, HeartbeatInterval, etc.)
- REST API to get/patch keys
- Metered value sample interval controls periodic MeterValues sending

**Authorization Cache (Plan 5d):**
- Per-idTag authorization status caching with optional expiry
- Used to avoid repeated Authorize messages to CSMS

**Timeline / Event History (Plan 5d):**
- Ring buffer storage of OCPP events (max 1000 events, configurable)
- Useful for debugging and audit trails
- REST API for querying events

**OCPP 2.0.1 Support (Phases 1–8):**
- Full OCPP 2.0.1 adapter in `internal/ocpp/v201/` implementing `OCPPBridge` interface
- TransactionEvent model (Started/Updated/Ended) replacing 1.6's StartTransaction/StopTransaction
- Device Model replacing 1.6 configuration keys (component/variable hierarchy, GetBaseReport, SetVariables)
- Smart Charging with 2.0.1 profile types (ReportChargingProfiles, GetChargingProfiles)
- Variable Monitoring (SetVariableMonitoring, ClearVariableMonitoring, threshold/delta/periodic)
- Display Messages (SetDisplayMessage, ClearDisplay, GetDisplayMessages)
- CostUpdated tracking per transaction
- Reset with OnIdle scheduling
- Firmware, LocalAuth, DataTransfer handlers
- Integration tests with in-process mock CSMS

## Important Design Decisions

1. **No GUI coupling**: REST API is the sole control surface (allows any UI to be built against it)
2. **Engine is authoritative**: All state lives in Engine, not in API handlers or OCPP adapters
3. **OCPP messages are reactive**: Engine methods are called by external OCPP lib; engine never calls OCPP (except for firmware/diagnostics status callbacks)
4. **Ordered delivery**: OCPP commands are delivered serially via CommandDispatcher, with optional persistence to disk
5. **Version-agnostic bridge**: OCPPBridge interface supports both OCPP 1.6J and 2.0.1 without engine changes
6. **OCPP version switching**: `config.OCPPVersion` selects v1.6 or v2.0.1 at startup; `main.go` instantiates the correct bridge (`v16.Bridge16` or `v201.Bridge201`). Shared interfaces (`OCPPBridge`, `ConfigKeyAPI`, `ChargingProfileManagerAPI`) ensure the API layer is version-agnostic.
7. **Callback-driven WebSocket**: Engine callbacks enqueue commands to trigger WebSocket broadcasts and OCPP messages
8. **Fixed-timestep simulation**: Decouples simulation speed from wall clock (20 Hz, 100 ms steps)

## Debugging Tips

**State & Logic:**
- **Connector state stuck?** Check `internal/engine/state.go` for state transition validation rules.
- **Session not progressing?** Verify energy meter is updated: check `Simulate()` in `internal/engine/engine.go` and meter tick loop in `internal/runtime/runtime.go`.
- **StateOfCharge not updating?** Verify `MaxEnergy` (EV battery capacity) is set to non-zero in config.

**OCPP Communication:**
- **OCPP message not reaching CSMS?** 
  1. Check if bridge is connected: `bridge.IsConnected()`
  2. Verify command is enqueued to dispatcher: check `internal/ocpp/command.go`
  3. Check message queue backend: `internal/ocpp/queue/` (memory vs. file-based)
  4. Monitor `slog` output for "OCPP bridge error" or dispatcher errors
- **Handshake failing?** Check OCPP ID, password, and CSMS endpoint in config.
- **MeterValues not sending?** Check `MeterValueSampleInterval` OCPP config key (via PATCH `/api/v1/ocpp/config-keys`).

**WebSocket:**
- **WebSocket event missing?** 
  1. Verify engine callback is triggered (add logging to callbacks in `main.go`)
  2. Check `internal/api/ws/hub.go` broadcast logic
  3. Verify client is subscribed to `/ws` endpoint
  4. Check WebSocket tick interval (default 1 second, sends state snapshot)
- **Initial state snapshot not received?** Verify `ws.BuildStatusSnapshot(app.Engine)` in router returns valid data.

**Simulation:**
- **Simulation not advancing?** Check runtime ticker: should wake every 50 ms and accumulate to 100 ms steps.
- **Performance issue?** Profile with `pprof` or check `maxSteps` in `internal/runtime/runtime.go` (spiral-of-death guard).

**Configuration:**
- **Config not persisting?** Verify `config.Save()` is called after changes.
- **Keyring access failing?** Check system keyring availability (macOS Keychain, Linux Secret Service, etc.).

**Testing:**
- Run `go test ./...` to verify all tests pass.
- Run `go test -run TestName ./path` for specific tests.
- Check test setup in `internal/api/handlers_test.go` for HTTP test patterns.

## Main Wiring (cmd/chargeghost/main.go)

Understanding `main.go` is key to understanding the system architecture:

1. **Config & Engine:** Load config, create `Engine`, add pre-configured connectors
2. **Goroutines:** Launch 8+ goroutines in WaitGroup:
   - **Runtime loop:** Calls `engine.Simulate()` at fixed timestep (20 Hz, 100 ms steps)
   - **WebSocket hub:** Broadcasts to all connected clients
   - **WebSocket ticker:** Sends state snapshot every 1 second
   - **Command dispatcher:** Executes OCPP commands serially
   - **HTTP server:** Listens on :8080
   - **OCPP bridge:** Connects to CSMS (v1.6 WebSocket)
   - **MeterValues ticker:** Sends MeterValues at configurable interval
3. **Managers:** Create ChargingProfileManager, ConfigKeyManager, AuthCache, LocalAuthListManager, FirmwareManager, DiagnosticsManager
4. **Message Queue:** Create persistent or in-memory queue for OCPP commands
5. **Bridge:** Instantiate v16.Bridge (OCPP 1.6J) with all dependencies
6. **Callbacks:** Wire engine callbacks to:
   - Broadcast WebSocket messages
   - Enqueue OCPP commands to dispatcher
7. **AppContext:** Inject all dependencies into API handlers
8. **Graceful shutdown:** Wait for SIGTERM/SIGINT, cancel context, shutdown HTTP server, wait for goroutines

**Key insight:** The engine doesn't know about OCPP, the API, or WebSocket. It only knows about state and calls callbacks. The wiring in `main.go` connects these layers.

## Further Reading

- **`GO_REIMPLEMENTING_GUIDE.md`** — Complete domain model, session lifecycle, concurrency model, and implementation order
- **OCPP 1.6J Spec** — Referenced in code comments; see `internal/ocpp/bridge.go` and `internal/ocpp/v16/bridge.go`
- **go-chi docs** — HTTP routing library: https://github.com/go-chi/chi
- **lorenzodonini/ocpp-go** — OCPP library: https://github.com/lorenzodonini/ocpp-go
