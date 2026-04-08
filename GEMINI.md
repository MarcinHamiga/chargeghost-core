# Gemini CLI Context: ChargeGhost EVSE Engine

This project is a high-performance Go reimplementation of the ChargeGhost EVSE simulation engine. It simulates the behavior of Electric Vehicle charging stations and integrates with Central Systems via OCPP 1.6J.

## Project Overview

*   **Purpose:** A headless EVSE simulator for testing CSMS (Central System Management Software) implementations and simulating complex charging scenarios.
*   **Core Architecture:** The **Engine** (`internal/engine`) is the authoritative single source of truth. All state mutations (plugging in, starting sessions, etc.) must flow through the Engine.
*   **Control Surface:** Controlled exclusively via a REST API (`internal/api`) and real-time event streaming via WebSockets (`internal/api/ws`).
*   **OCPP Integration:** Delegated to the `lorenzodonini/ocpp-go` library. The engine is decoupled from OCPP via a version-agnostic bridge pattern (`internal/ocpp`).
*   **Simulation Model:** Uses a fixed-timestep simulation loop (20 Hz) to ensure deterministic behavior regardless of system load.

## Key Technologies

*   **Language:** Go 1.26.1
*   **Routing:** `go-chi/chi/v5`
*   **WebSockets:** `gorilla/websocket`
*   **OCPP:** `lorenzodonini/ocpp-go` (v1.6J)
*   **Testing:** `stretchr/testify`
*   **Credentials:** `zalando/go-keyring` (for secure OCPP password storage)

## Project Structure

*   `cmd/chargeghost/`: Entry point and component wiring.
*   `internal/engine/`: Domain logic (Connector state machine, Session lifecycle, Energy meters, Reservations).
*   `internal/api/`: REST API handlers, DTOs, and WebSocket hub.
*   `internal/ocpp/`: OCPP adapter layer, command dispatcher (ordered delivery), and version-specific bridges (e.g., `v16`).
*   `internal/runtime/`: Fixed-timestep simulation runner.
*   `internal/config/`: Configuration persistence (`~/.chargeghost/config.json`).
*   `internal/timeline/`: Ring-buffer for OCPP event history.

## Building and Running

### Commands
*   **Build:** `go build -o chargeghost ./cmd/chargeghost`
*   **Run:** `./chargeghost` (Default: API on `:8080`, OCPP on `:9000`)
*   **Test:** `go test ./...`
*   **Docker:** `docker build -t chargeghost .`
*   **Lint:** `go vet ./...`

### Configuration
*   Config is stored in `~/.chargeghost/config.json`.
*   OCPP passwords are saved in the system keyring (macOS Keychain, Linux Secret Service, etc.).

## Development Guidelines

*   **State Mutations:** Never modify connector or session state directly. Always use `Engine` methods to ensure state machine integrity and trigger necessary callbacks.
*   **OCPP Communication:** Use the `CommandDispatcher` to enqueue outbound OCPP messages. This ensures FIFO ordering and prevents race conditions with the CSMS.
*   **Testing:** New features MUST include tests in adjacent `*_test.go` files. Use `testify/assert` and `testify/mock`.
*   **Concurrency:** The Engine uses a `sync.RWMutex`. API handlers should call engine methods and return quickly; do not hold the engine lock during I/O.
*   **Callbacks:** Engine callbacks (e.g., `OnConnectorStatusChanged`) are executed while holding the engine lock. Actions inside callbacks MUST be non-blocking (e.g., async WebSocket broadcast, enqueuing OCPP commands).

## Documentation References
*   `GO_REIMPLEMENTING_GUIDE.md`: Deep dive into domain models and implementation logic.
*   `CLAUDE.md`: Quick reference for commands and project structure.
