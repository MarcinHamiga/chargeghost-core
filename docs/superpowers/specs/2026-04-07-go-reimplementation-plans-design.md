# ChargeGhost Go Reimplementation — Implementation Plans Design

**Date:** 2026-04-07
**Source:** GO_REIMPLEMENTING_GUIDE.md
**Status:** Approved

---

## Overview

This document defines the structure and scope of the 11 implementation plans for reimplementing the ChargeGhost EVSE simulation engine in Go. Plans are derived from the 6-phase order in Section 15 of the guide, with Phase 3 split into two and Phase 5 split into five sub-plans.

**Constraint:** Every plan ends with a compiling, testable binary. No plan may leave the repo in a non-compiling state.

**OCPP library:** `lorenzodonini/ocpp-go` (committed).

---

## Plan Structure (applied to every plan)

Each plan contains:

- **Goal**: One sentence describing what the binary can do at the end that it couldn't before.
- **Files to create/modify**: Explicit list.
- **Steps**: Ordered, atomic. Final step must compile. Intermediate steps may be mid-implementation but must not break existing tests.
- **Verification**: `go test`, `go build ./...`, and/or curl smoke tests.
- **Dependencies**: Which prior plan must be complete.

---

## The 11 Plans

### Plan 1 — Domain Core

**Goal:** All engine domain structs, state machine, and sentinel errors exist and are fully unit-tested.

**Scope (from Section 15, Phase 1):**
- `internal/engine/state.go` — `ConnectorState` enum + 15-entry valid-transitions table
- `internal/engine/connector.go` — `Connector` struct, state machine, plug/unplug, parameter validation, bypass transitions (SetUnavailable, SetReserved, SetOperative, ClearReservation), persistent status
- `internal/engine/energy_meter.go` — `EnergyMeter` with `Update()` and cumulative value
- `internal/engine/session.go` — `Session` with SoC calculation, MeterHistory
- `internal/engine/reservation.go` — `Reservation` with `IsExpired()`
- `internal/engine/engine.go` — `Engine` struct, all methods, sentinel errors, `StoppedSessionInfo`, `PendingRemoteStart`
- Unit tests for all of the above

**Dependencies:** None (greenfield).

**Verification:** `go test ./internal/engine/...` — all tests pass.

---

### Plan 2 — Simulation Loop

**Goal:** The engine ticks in real time, accumulating energy and auto-suspending/resuming based on the `GetLimit` callback.

**Scope (from Section 15, Phase 2):**
- `cmd/chargeghost/main.go` — minimal entry point that starts the runtime
- Fixed-timestep loop with accumulator (20 Hz wake-up, 100 ms simulation step, max 5 steps/cycle)
- `Engine.Simulate(interval)` wired into the loop
- `GetLimit` callback injection point (stub that returns nil)
- EVSE auto-suspend/resume logic inside `Simulate()`

**Dependencies:** Plan 1.

**Verification:** `go build ./...` succeeds; manual test shows energy meter incrementing over time.

---

### Plan 3a — REST API Core

**Goal:** The binary exposes a working HTTP server with status, connector CRUD, session control, and config endpoints.

**Scope (from Section 15, Phase 3, items 1–6):**
- `internal/api/server.go` — HTTP server setup, CORS middleware
- `internal/api/router.go` — route registration
- `internal/api/dto.go` — all JSON request/response structs
- `internal/api/handlers/status.go` — `GET /api/v1/status`
- `internal/api/handlers/connectors.go` — full connector CRUD + actions (plug_in, unplug, suspend_ev, resume_charging, start-charging, stop-charging, rfid)
- `internal/api/handlers/sessions.go` — start, stop, list, last-stopped, active, info
- `internal/api/handlers/config.go` — GET + PATCH config, POST config/save (in-memory only; persistence deferred to Plan 6)
- `internal/api/handlers/reservations.go` — GET list, POST create, DELETE cancel (calls `Engine.ReserveConnector`/`CancelReservation` directly; no OCPP dependency)
- `internal/config/config.go` — `Config` struct, in-memory load with defaults
- Response envelope (`success`, `message`, `details`) applied consistently

**Dependencies:** Plan 2.

**Verification:** `go build ./...`; curl smoke tests for status, connector create/plug-in/start-charging/stop-charging/unplug, session list.

---

### Plan 3b — REST API Extended

**Goal:** Timeline, local auth list (stub), firmware/diagnostics (stub), and about endpoints all respond with valid (possibly empty/idle) JSON.

**Scope (from Section 15, Phase 3, items 7–10):**
- `internal/timeline/models.go` — `TimelineEvent`, `TimelineFilter` types
- `internal/timeline/store.go` — in-memory ring buffer (1000 events), filter/search logic
- `internal/api/handlers/timeline.go` — GET/DELETE timeline, GET timeline/count
- `internal/api/handlers/local_auth.go` — all local auth list endpoints (backed by in-memory stub implementing `LocalAuthListView`)
- `internal/api/handlers/firmware.go` — firmware status/trigger/cancel + diagnostics status/trigger/cancel (backed by stub returning `"Idle"` state)
- `internal/api/handlers/about.go` — `GET /api/v1/about`
- Stub implementations of `LocalAuthListView`, `FirmwareManager`, `DiagnosticsManager` that will be replaced by real implementations in Plans 5d and 5e

**Dependencies:** Plan 3a.

**Verification:** `go build ./...`; curl smoke tests for all new endpoints returning valid JSON.

---

### Plan 4 — WebSocket Events

**Goal:** Any WebSocket client connecting to `/ws` receives a state snapshot immediately and live event broadcasts thereafter.

**Scope (from Section 15, Phase 4):**
- `internal/api/ws/messages.go` — `Message` struct (type, timestamp, data)
- `internal/api/ws/hub.go` — single-goroutine hub (register, unregister, broadcast channels), `BroadcastAsync`, `BroadcastMessage`
- Engine event subscriptions wired at startup: `OnSessionStarted`, `OnSessionStopped`, `OnConnectorStatusChanged`, `OnConnectorParamsChanged`, `OnReservationExpired`
- State snapshot sent on client connect
- Periodic tick broadcast (~1 s) with full status payload
- All event types from Section 11.2: `connector_status_changed`, `connector_params_changed`, `session_started`, `session_stopped`, `connection_state_changed`, `tick`, `firmware_status_changed`, `ocpp_config_key_changed`, `charging_profile_changed`, `reservation_changed`

**Dependencies:** Plan 3b (timeline store is used in tick payload; stubs satisfy firmware/auth event sources).

**Verification:** `go build ./...`; WebSocket client receives snapshot on connect and `tick` events each second; connector plug-in triggers `connector_status_changed` broadcast.

---

### Plan 5a — OCPP Foundation

**Goal:** The binary connects to a CSMS, sends BootNotification, and exchanges Heartbeats — with the full interface boundary in place for subsequent OCPP plans.

**Scope (from Section 15, Phase 5, items 1–3):**
- `internal/ocpp/adapter.go` — all interface definitions: `OCPPAdapter`, `OCPPSender`, `OCPPReceiver`, `OCPPConfigManager`, `OCPPProfileManager`, `OCPPFirmwareManager`, `EngineView`, `AuthorizationCache`, `LocalAuthListView`, `DataTransferHandler`, and all supporting types (`ConfigKeyInfo`, `ChargingProfile`, `ChargingSchedule`, `ChargingSchedulePeriod`, `FirmwareStatus`, `DiagnosticsStatus`, `OCPPMessageInfo`, `LocalAuthEntry`, `IDTagInfo`, `ChargingProfileInfo`)
- `internal/ocpp/types.go` — any remaining shared OCPP types
- `internal/ocpp/command.go` — `OCPPCommand`, `CommandDispatcher` (buffered channel 256, sequential drain, non-blocking `Enqueue`)
- Bridge skeleton using `lorenzodonini/ocpp-go`:
  - WebSocket connection lifecycle (connect, disconnect, reconnect)
  - BootNotification send + response handling
  - Heartbeat send on configurable interval
  - `connection_state_changed` WebSocket event broadcast
- `Engine.GetLimit` wired to return nil (no profiles yet)
- `Engine.OnConnectorStatusChanged` wired to enqueue StatusNotification (sent after BootNotification accepted)

**Dependencies:** Plan 4.

**Verification:** `go build ./...`; binary connects to a running CSMS (e.g., SteVe), BootNotification accepted, Heartbeat exchanges observed in logs.

---

### Plan 5b — OCPP Transaction Flow

**Goal:** Full charging session lifecycle is reflected in OCPP — StartTransaction, StopTransaction, StatusNotification, and MeterValues flow correctly in both directions.

**Scope (from Section 15, Phase 5, items 3–4; Section 12.1–12.2, 12.5–12.6 core):**

Outbound (engine events → OCPP):
- `SendStartTransaction` — triggered by `OnSessionStarted`; handles CSMS-assigned transaction ID via `SetActiveTransaction` (Section 12.5)
- `SendStopTransaction` — triggered by `OnSessionStopped`; includes MeterHistory
- `SendStatusNotification` — triggered by `OnConnectorStatusChanged`
- `SendMeterValues` — periodic, on configurable interval (`MeterValueSampleInterval` OCPP key)

Inbound (CSMS → engine):
- `OnRemoteStart` → `Engine.StartSession()` with 30s timeout for pending remote start
- `OnRemoteStop` → `Engine.StopSession()` via `GetConnectorByTransaction`
- `OnReset` (Soft/Hard) → stop all sessions + optional reconnect
- `OnChangeAvailability` → `Engine.SetConnectorAvailability()`
- `OnUnlockConnector` → return "Unlocked"/"UnlockFailed"
- `OnTriggerMessage` → dispatch to appropriate send method
- `OnClearCache` → clear auth cache, return "Accepted"

**Dependencies:** Plan 5a.

**Verification:** Full session lifecycle test against a CSMS: plug-in → RemoteStart → Charging → MeterValues → RemoteStop → StopTransaction all observed.

---

### Plan 5c — OCPP Smart Charging

**Goal:** Charging profile limits are applied per-tick, and `GetCompositeSchedule` returns correct results for all profile types.

**Scope (from Section 15, Phase 5, item 5; Section 12.4–12.4.1):**
- `internal/ocpp/profile_manager.go` — `ChargingProfileManager`:
  - Storage: map keyed by `(connectorID, profileID)`
  - `SetChargingProfile`, `ClearChargingProfile` (with filter: profileID, connectorID, purpose, stackLevel)
  - `GetCompositeLimit`: ChargePointMaxProfile + TxProfile/TxDefaultProfile resolution, stack-level priority, Absolute/Relative/Recurring timing, Watts→Amps conversion, `min()` composition
  - `GetCompositeSchedule`: boundary collection algorithm
  - Constraints: max 20 profiles, max stack level 5, max 10 periods per profile
- `Engine.GetLimit` replaced with real `profileManager.GetCompositeLimit` call
- Inbound handlers wired:
  - `OnSetChargingProfile` → `profileManager.SetChargingProfile`
  - `OnClearChargingProfile` → `profileManager.ClearChargingProfile`
  - `OnGetCompositeSchedule` → `profileManager.GetCompositeSchedule`
- Charging profile REST endpoints (`internal/api/handlers/charging_profiles.go`) wired to profile manager:
  - GET list, GET by ID, POST install, DELETE clear, POST composite-schedule
- `charging_profile_changed` WebSocket event broadcast on set/clear

**Dependencies:** Plan 5a (5b not required — charging profiles work independently of transaction flow).

**Verification:** Install a TxDefaultProfile with limit 16A; start session on 32A connector; confirm effective current is 16A in energy accumulation; install ChargePointMaxProfile at 8A and confirm composite is 8A.

---

### Plan 5d — OCPP Config, Auth & Queue

**Goal:** OCPP configuration keys are readable/writable, the auth cache works, the local auth list is fully functional (replacing the Plan 3b stub), and offline messages survive reconnection.

**Scope (from Section 15, Phase 5, items 6–9):**
- `internal/ocpp/config_keys.go` — `ConfigKeyManager`: all standard OCPP 1.6 keys with types, read-only flags, defaults; `GetConfigValue`, `SetConfigValue`, `GetConfigKeyInfo`; inbound `OnGetConfiguration`, `OnChangeConfiguration` wired
- `internal/ocpp/auth_cache.go` — `AuthorizationCache` implementation (thread-safe map); `Get`, `Put`, `Remove`, `Clear`, `Size`; populated on Authorize responses
- `internal/ocpp/local_auth_list.go` — full `LocalAuthListView` implementation replacing Plan 3b stub: versioned list, full/differential update, expiry checking, max 1000 entries; inbound `OnGetLocalListVersion`, `OnSendLocalList` wired
- `internal/ocpp/queue/queue.go` — `MessageQueue` interface + factory
- `internal/ocpp/queue/memory.go` — `InMemoryQueue`: thread-safe FIFO, max retries, drain-on-reconnect
- `internal/ocpp/queue/json_file.go` — `JsonFileQueue`: persisted to `~/.chargeghost/message_queue.json`, survives restarts; uses `google/uuid` for message IDs
- Queue wired into StartTransaction/StopTransaction/MeterValues sends; drain triggered on OCPP reconnection
- OCPP config key REST endpoints wired: `GET /api/v1/ocpp/config-keys`, `PATCH /api/v1/ocpp/config-keys`

**Dependencies:** Plan 5a.

**Verification:** `go build ./...`; send Authorize request, verify cache populated; update local auth list via REST, verify inbound `SendLocalList` accepted; disconnect CSMS, trigger session stop, reconnect, verify StopTransaction delivered from queue.

---

### Plan 5e — OCPP Firmware, Diagnostics & Data Transfer

**Goal:** Firmware update and diagnostics upload simulate correctly with exact timing from the guide, and vendor data transfer is dispatched to registered handlers — replacing Plan 3b stubs.

**Scope (from Section 15, Phase 5, items 10–12):**
- `internal/ocpp/firmware_manager.go` — `FirmwareManager` replacing Plan 3b stub:
  - State machine: Idle → Downloading (0s after retrieve date gate) → Downloaded (3s) → Installing (1s) → Installed (2s)
  - Cancellation at any state
  - `OnFirmwareStatusChanged` callback → WebSocket `firmware_status_changed` broadcast
  - `SendFirmwareStatusNotification` enqueued at each transition
  - Inbound `OnUpdateFirmware` wired
- `internal/ocpp/firmware_manager.go` also contains `DiagnosticsManager`:
  - State machine: Idle → Uploading (0s) → Uploaded (2s)
  - `SendDiagnosticsStatusNotification` at each transition
  - Inbound `OnGetDiagnostics` wired
- `internal/ocpp/data_transfer.go` — `DataTransferRegistry`: `RegisterDataTransferHandler(vendorID, messageID, handler)`; inbound `OnDataTransfer` dispatched to registered handlers; `SendDataTransfer` outbound
- Remaining outbound sends not yet implemented: `SendAuthorize`, `SendBootNotification` (reconnect path), `SendHeartbeat` (already done in 5a — verify complete)
- Raw OCPP send REST endpoints wired to real implementations:
  - `POST /api/v1/ocpp/authorize`
  - `POST /api/v1/ocpp/heartbeat`
  - `POST /api/v1/ocpp/raw/start-transaction`
  - `POST /api/v1/ocpp/raw/stop-transaction`
  - `POST /api/v1/ocpp/raw/status-notification`
  - `POST /api/v1/ocpp/raw/meter-values`
  - `POST /api/v1/ocpp/raw/data-transfer`

**Dependencies:** Plan 5a (5b not required; firmware/diagnostics are independent of transaction flow).

**Verification:** `go build ./...`; trigger firmware update via REST, observe Idle→Downloading→Downloaded→Installing→Installed transitions at correct intervals in WebSocket events and CSMS logs.

---

### Plan 6 — Polish

**Goal:** The binary is production-deployable: config persists to disk, shutdown is graceful, a health endpoint exists, and a Dockerfile is provided.

**Scope (from Section 15, Phase 6):**
- `internal/config/config.go` — JSON persistence to `~/.chargeghost/config.json`; keyring integration via `zalando/go-keyring` for password storage; load on startup, save on `POST /api/v1/config/save`
- Graceful shutdown: `context.WithCancel` propagated to all goroutines (simulation loop, command dispatcher, WebSocket hub, OCPP connection); `os.Signal` handler for SIGTERM/SIGINT
- `GET /health` endpoint — returns `{"status":"ok"}` (no auth, no engine lock)
- OpenAPI spec — generated or handwritten `openapi.yaml` at repo root covering all `/api/v1` endpoints
- `Dockerfile` — multi-stage build (Go builder + minimal runtime image)

**Dependencies:** Plans 5b, 5c, 5d, 5e all complete (all functionality in place before polish).

**Verification:** `docker build` succeeds; binary starts, handles SIGTERM cleanly (no goroutine leaks), config survives restart, `GET /health` returns 200.

---

## Dependency Graph

```
1 (Domain Core)
└── 2 (Simulation Loop)
    └── 3a (REST API Core)
        └── 3b (REST API Extended)
            └── 4 (WebSocket Events)
                └── 5a (OCPP Foundation)
                    ├── 5b (Transaction Flow)
                    ├── 5c (Smart Charging)
                    ├── 5d (Config, Auth & Queue)
                    └── 5e (Firmware, Diagnostics & DataTransfer)
                        └── 6 (Polish)  ← after all 5x complete
```

Plans 5b, 5c, 5d, 5e are independent of each other and can be executed in any order after 5a.

---

## Cross-Plan Notes

- **Plan 3b stubs → replaced later**: `LocalAuthListView` stub replaced in 5d; `FirmwareManager`/`DiagnosticsManager` stubs replaced in 5e. The stub interface must match the real interface exactly so replacement is a drop-in swap.
- **Timeline store**: Created in 3b as an in-memory ring buffer. It is populated by the OCPP bridge in 5a+ by calling `store.Append(event)` at each inbound/outbound OCPP message. No stub swap needed — the store is real from 3b onward.
- **`cmd/chargeghost/main.go`**: Grows incrementally across plans. Each plan adds wiring without removing prior wiring.
- **Config persistence**: `PATCH /api/v1/config` works in-memory from Plan 3a. Disk persistence and the `POST /api/v1/config/save` action that applies pending actions (bridge restart, runtime rebuild) are fully implemented in Plan 6.
