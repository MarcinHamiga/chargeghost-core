# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## Project Overview

**ChargeGhost** — A Go EVSE (Electric Vehicle Supply Equipment) simulation engine controlled via REST API + WebSocket event streaming, with OCPP 1.6J and 2.0.1 protocol support.

## Build & Test Commands

```bash
go build -o chargeghost ./cmd/chargeghost    # Build
./chargeghost                                 # Run (HTTP :8080, OCPP :9000)
go test ./...                                 # All tests
go test -run TestName ./internal/engine       # Single test
go test -tags integration ./internal/ocpp/v201/  # Integration tests (requires build tag)
go fmt ./...                                  # Format
go vet ./...                                  # Static analysis
```

## Architecture

```
REST API ─────────────┐
                      v
                   Engine  <──── OCPP inbound handlers
                      │
             callbacks │
                      v
           CommandDispatcher ──> OCPP bridge (v1.6 / v2.0.1) ──> CSMS
                      │
                      v
             WebSocket Hub ──> connected clients
```

### Core Principle: Engine is the Single Source of Truth

All state mutations flow through `internal/engine/engine.go`. The REST API and OCPP adapters are thin layers that call engine methods. The engine never calls OCPP directly — it fires callbacks that `main.go` wires to OCPP commands and WebSocket broadcasts.

### Data Flow

1. **Inbound**: REST API or OCPP handler calls an engine method (e.g., `PlugIn()`, `StartSession()`)
2. **State mutation**: Engine updates connector/session/meter state
3. **Callbacks**: Engine fires function pointers (`OnConnectorStatusChanged`, `OnSessionStarted`, etc.)
4. **Outbound**: Callbacks (wired in `main.go`) enqueue `OCPPCommand`s to the `CommandDispatcher` and broadcast WebSocket messages
5. **Delivery**: `CommandDispatcher` executes OCPP commands serially in a dedicated goroutine

### OCPP Version-Agnostic Bridge

`internal/ocpp/bridge.go` defines the `OCPPBridge` interface. Two implementations exist:
- `internal/ocpp/v16/` — OCPP 1.6J (via `lorenzodonini/ocpp-go`)
- `internal/ocpp/v201/` — OCPP 2.0.1

Each version package follows the same structure: `bridge.go` (setup + lifecycle), `handlers.go` (inbound CSMS messages), `senders.go` (outbound messages). The engine and API layer are version-agnostic — `config.OCPPVersion` selects which bridge `main.go` instantiates.

### Connector State Machine

Defined in `internal/engine/state.go`. States follow OCPP 1.6:
```
Available → Preparing → Charging → Finishing → Available
         ↘ Reserved    ↘ Suspended*   ↗ (Unplug)
           Faulted / Unavailable (persistent)
```

State transition validation governs what operations are allowed. Check `state.go` when debugging stuck connectors.

### Simulation Loop

`internal/runtime/runtime.go` runs a fixed-timestep loop: 20 Hz wake-up (50ms), advances simulation in 100ms steps. Calls `engine.Simulate(deltaSeconds)` which updates energy meters, checks reservation expiry, and advances session state.

### Main Wiring

`cmd/chargeghost/main.go` is the composition root. It creates the engine, managers (charging profiles, config keys, auth cache, local auth list, firmware, diagnostics), message queue, bridge, and wires engine callbacks to WebSocket + OCPP. Launches 8+ goroutines in a WaitGroup with graceful shutdown on SIGTERM/SIGINT.

## Key Patterns

- **Test pattern**: `httptest.NewRequest()` + `httptest.NewRecorder()` with a test `AppContext` passed to the router. Testify assert/require for assertions.
- **API handlers**: Main handlers in `internal/api/handlers.go`, specialized handlers in `internal/api/handlers/` subdirectory. Routes registered in `internal/api/router.go` (chi).
- **REST API reference**: Full endpoint docs in `REST_API.md`. All endpoints under `/api/v1/*`.
- **Energy metering**: `energy_wh = (voltage * current * phase * interval_seconds) / 3600`, cumulative like an odometer. Single-EVSE mode: meter persists across sessions. Multi-EVSE: per-connector session meters.
- **CommandDispatcher** (`internal/ocpp/command.go`): Serial OCPP message delivery with optional disk persistence via `internal/ocpp/queue/`.

## Configuration

Config at `~/.chargeghost/config.json`. OCPP password in system keyring. Key fields: `ocppID`, `ocppVersion` ("1.6" or "2.0.1"), `multiEVSEMode`, `evBatteryCapacity`, `persistMessageQueue`, `connectors`.

## Debugging

- **Connector state stuck?** Check transition rules in `internal/engine/state.go`
- **OCPP messages not reaching CSMS?** Check `bridge.IsConnected()`, then dispatcher queue, then `slog` output for errors
- **MeterValues not sending?** Check `MeterValueSampleInterval` OCPP config key
- **StateOfCharge not updating?** `evBatteryCapacity` must be non-zero in config
- **Simulation not advancing?** Check runtime ticker in `internal/runtime/runtime.go` (spiral-of-death guard via `maxSteps`)
- **WebSocket event missing?** Verify engine callback fires, then check `internal/api/ws/hub.go` broadcast

## Design Decisions

1. Engine never imports OCPP, API, or WebSocket packages — callbacks are the only coupling
2. OCPP commands delivered serially via CommandDispatcher (ordered delivery guarantee)
3. Fixed-timestep simulation decouples simulation speed from wall clock
4. REST API is the sole control surface (no GUI coupling)
5. `OCPPBridge` interface + shared `ConfigKeyAPI`/`ChargingProfileManagerAPI` interfaces keep the API layer version-agnostic
