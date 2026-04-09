# ChargeGhost

**EVSE simulation engine — control EV charging stations via REST API and real-time WebSocket events.**

[![License: AGPL-3.0](https://img.shields.io/badge/license-AGPL--3.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)](https://golang.org)
[![OCPP 1.6J](https://img.shields.io/badge/OCPP-1.6J-4CAF50)](https://www.openchargealliance.org)
[![OCPP 2.0.1](https://img.shields.io/badge/OCPP-2.0.1-4CAF50)](https://www.openchargealliance.org)
[![Docker](https://img.shields.io/badge/Docker-supported-2496ED?logo=docker&logoColor=white)](Dockerfile)

---

ChargeGhost is a Go-based Electric Vehicle Supply Equipment (EVSE) simulator. It faithfully models the behavior of one or more EV charging stations and exposes full control via a REST API, with real-time state updates streamed over WebSocket. OCPP communication (1.6J and 2.0.1) is handled transparently in the background — the Engine is the single source of truth for all simulation state.

## Features

- **Multi-connector simulation** — create and manage multiple EVSE connectors independently
- **OCPP 1.6J & 2.0.1** — full protocol support with automatic version selection at startup
- **Smart charging profiles** — install power limit profiles and compute composite schedules
- **Local authorization list** — manage idTag entries with version tracking and CSMS sync
- **Firmware & diagnostics simulation** — state-machine driven update and upload flows
- **Reservation management** — reserve connectors by idTag with configurable expiry
- **Real-time WebSocket events** — subscribe to `/ws` for live state changes and session updates
- **Configurable energy metering** — single-EVSE cumulative meter or per-connector session meters
- **Docker support** — minimal distroless runtime image

## Quick Start

**Prerequisites:** Go 1.26+

```bash
git clone https://github.com/MarcinHamiga/chargeghost-core.git
cd chargeghost-core

go build -o chargeghost ./cmd/chargeghost
./chargeghost
```

By default, the engine starts with:
- HTTP API on `:8080`
- OCPP WebSocket server on `:9000`

Config is read from (and saved to) `~/.chargeghost/config.json` on first run.

## Build

```bash
go build -o chargeghost ./cmd/chargeghost
```

## Docker

```bash
docker build -t chargeghost .
docker run -p 8080:8080 -p 9000:9000 chargeghost
```

## Configuration

Config file: `~/.chargeghost/config.json`  
OCPP password: stored securely in the system keyring (macOS Keychain, Linux Secret Service, Windows Credential Manager).

| Field | Type | Description |
|---|---|---|
| `ocppID` | string | Charge point identity sent in BootNotification |
| `ocppVersion` | string | `"1.6"` (default) or `"2.0.1"` |
| `multiEVSEMode` | bool | Use per-connector energy meters instead of a shared meter |
| `evBatteryCapacity` | float | EV battery capacity in kWh — enables State of Charge tracking |
| `persistMessageQueue` | bool | Persist OCPP message queue to disk across restarts |
| `connectors` | array | Pre-configured connectors (voltage, current, phase) |

Config can be updated at runtime via `PATCH /api/v1/config` and saved with `POST /api/v1/config/save`.

## REST API

All control is via `/api/v1/*`. See [`REST_API.md`](REST_API.md) for the full reference with request/response payloads.

| Category | Paths |
|---|---|
| Health & Status | `GET /health`, `GET /api/v1/status`, `GET /api/v1/about` |
| Connectors | `GET/POST /api/v1/connectors`, `GET/PUT/DELETE /api/v1/connectors/{id}` |
| Connector Actions | `plug_in`, `unplug`, `start-charging`, `stop-charging`, `suspend_ev`, `resume_charging`, `rfid` |
| Sessions | `GET /api/v1/sessions`, `POST /api/v1/sessions/start`, `POST /api/v1/sessions/stop` |
| Configuration | `GET/PATCH /api/v1/config`, `POST /api/v1/config/save` |
| Reservations | `GET/POST /api/v1/reservations`, `DELETE /api/v1/reservations/{id}` |
| Timeline | `GET/DELETE /api/v1/timeline` |
| Local Auth List | `GET/PUT/DELETE /api/v1/local-auth-list` |
| Firmware | `GET /api/v1/firmware/status`, `POST /api/v1/firmware/trigger` |
| Diagnostics | `GET /api/v1/diagnostics/status`, `POST /api/v1/diagnostics/trigger` |
| Charging Profiles | `GET/POST/DELETE /api/v1/charging-profiles` |
| OCPP Control | `GET/PATCH /api/v1/ocpp/config-keys`, `POST /api/v1/ocpp/raw/*` |

## WebSocket Events

Connect to `GET /ws` to receive a state snapshot on connection, followed by a stream of real-time events.

| Event | Description |
|---|---|
| `connector_status_changed` | Connector state transition (Available, Charging, Faulted, etc.) |
| `session_started` | Charging session began |
| `session_stopped` | Charging session ended |
| `connector_params_changed` | Voltage, current, or phase updated |
| `reservation_changed` | Reservation created or cancelled |
| `firmware_status_changed` | Firmware update state changed |
| `diagnostics_status_changed` | Diagnostics upload state changed |

The tick broadcaster also sends a full state snapshot every second to all connected clients.

## Architecture

```
REST API ─────────────┐
                       ▼
                    Engine  ◄──── OCPP inbound handlers
                       │
              callbacks │
                       ▼
            CommandDispatcher ──► OCPP bridge (v1.6 / v2.0.1) ──► CSMS
                       │
                       ▼
              WebSocket Hub ──► connected clients
```

- **Engine** is the single source of truth — all state mutations flow through it
- **REST API** is a thin control surface; it calls engine methods
- **OCPP adapters** call engine methods on inbound CSMS messages; engine callbacks enqueue outbound OCPP commands
- **CommandDispatcher** delivers OCPP messages serially and in order, with optional disk persistence
- **OCPPBridge interface** is version-agnostic — `Bridge16` (OCPP 1.6J) and `Bridge201` (OCPP 2.0.1) are drop-in
- **Simulation loop** runs at 20 Hz (fixed 100 ms timesteps) via the runtime package

## Development

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Integration tests (requires -tags integration)
go test -tags integration ./internal/ocpp/v201/

# Format and lint
go fmt ./...
go vet ./...
```

## License

Copyright (C) 2026 Marcin Hamiga

This program is free software: you can redistribute it and/or modify it under the terms of the GNU Affero General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version.

See [LICENSE](LICENSE) for the full license text.
