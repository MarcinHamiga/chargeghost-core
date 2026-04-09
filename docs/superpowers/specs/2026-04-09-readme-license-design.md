# README + LICENSE Design — 2026-04-09

## Goal

Add a comprehensive GitHub-style `README.md` and a `LICENSE` file (GNU AGPL v3.0) to the repository root, then commit and push directly to `origin/release`.

## README Structure (Option C — Overview + Deep Dive)

### Section 1 — Header
- Title: `ChargeGhost` (H1)
- Tagline: *"EVSE simulation engine — control EV charging stations via REST API and real-time WebSocket events"*
- Badges: `AGPL-3.0 license` · `Go 1.26` · `OCPP 1.6J` · `OCPP 2.0.1` · `Docker`

### Section 2 — Overview
2–3 sentences covering: Go reimplementation of an EVSE simulator, REST + WebSocket control surface, Engine-as-single-source-of-truth design principle.

### Section 3 — Features
Bullet list:
- Multi-connector EVSE simulation
- OCPP 1.6J and 2.0.1 support
- Smart charging profiles + composite schedule calculation
- Local authorization list management
- Firmware update and diagnostics upload simulation
- Reservation management
- Real-time WebSocket event streaming
- Configurable energy metering (single-EVSE and multi-EVSE modes)
- Docker support (distroless runtime image)

### Section 4 — Quick Start
- Prerequisites: Go 1.26+, optional Docker
- `go build -o chargeghost ./cmd/chargeghost`
- `./chargeghost` — defaults: HTTP `:8080`, OCPP `:9000`

### Section 5 — Docker
- `docker build -t chargeghost .`
- `docker run -p 8080:8080 -p 9000:9000 chargeghost`

### Section 6 — Configuration
- Config file: `~/.chargeghost/config.json`
- OCPP password: system keyring (macOS Keychain, Linux Secret Service, Windows Credential Manager)
- Key fields table:

| Field | Type | Description |
|---|---|---|
| `ocppID` | string | Charge point identity |
| `ocppVersion` | string | `"1.6"` or `"2.0.1"` |
| `multiEVSEMode` | bool | Per-connector energy meters |
| `evBatteryCapacity` | float | EV battery capacity (kWh) for SoC calculation |
| `persistMessageQueue` | bool | Persist OCPP message queue to disk |
| `connectors` | array | Pre-configured connectors |

### Section 7 — REST API
Category summary table linking to full `docs/REST_API.md`:

| Category | Base Path |
|---|---|
| Health & Status | `/health`, `/api/v1/status`, `/api/v1/about` |
| Connectors | `/api/v1/connectors` |
| Sessions | `/api/v1/sessions` |
| Configuration | `/api/v1/config` |
| Reservations | `/api/v1/reservations` |
| Timeline | `/api/v1/timeline` |
| Local Auth List | `/api/v1/local-auth-list` |
| Firmware & Diagnostics | `/api/v1/firmware`, `/api/v1/diagnostics` |
| Charging Profiles | `/api/v1/charging-profiles` |
| OCPP Control | `/api/v1/ocpp` |

### Section 8 — WebSocket Events
- Endpoint: `GET /ws`
- Initial state snapshot on connect
- Event types: `connector_status_changed`, `session_started`, `session_stopped`, `connector_params_changed`, `reservation_changed`, `firmware_status_changed`, `diagnostics_status_changed`

### Section 9 — Architecture
- Engine is the single source of truth for all simulation state
- REST API is a thin control surface; OCPP adapters call engine methods (not vice versa)
- Engine callbacks → CommandDispatcher → OCPP bridge (ordered async delivery)
- Version-agnostic `OCPPBridge` interface supports OCPP 1.6J (`Bridge16`) and 2.0.1 (`Bridge201`)
- Fixed-timestep simulation loop at 20 Hz (100 ms steps)

### Section 10 — Development
- `go test ./...` — all tests
- `go test -tags integration ./internal/ocpp/v201/` — integration tests
- `go fmt ./...` — format
- `go vet ./...` — static analysis

### Section 11 — License
Licensed under the GNU Affero General Public License v3.0. See [LICENSE](LICENSE).

---

## LICENSE File

Full text of GNU Affero General Public License version 3.0.

---

## Git Workflow

1. Checkout `release` branch (tracking `origin/release`)
2. Create `README.md` and `LICENSE` in repo root
3. Commit with message: `docs: add README and AGPL-3.0 LICENSE`
4. Push to `origin/release`
