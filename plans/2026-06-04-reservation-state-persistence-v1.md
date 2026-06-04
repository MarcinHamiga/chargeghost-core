# Plan 12: Reservation State Persistence (B11)

**Date:** 2026-06-04
**Version:** v1
**Source review:** 2026-06-04 OCPP communications review, recommendations B11
**Priority:** P3 — robustness

## Objective

Persist the engine's reservation state across restarts. On restart, restore
active reservations and inform the CSMS (via StatusNotification or
ReservationStatusUpdate for 2.0.1).

## Background

`internal/engine/state.go` defines `StateReserved` for connectors. When a
reservation is placed (OCPP `ReserveNow` or v2.0.1 `RequestStartTransaction`
with reservation), the engine transitions a connector to Reserved. The
reservation expiry is checked in `Simulate()`.

If the charger reboots:

- The reservation is lost from the engine's in-memory state.
- The CSMS still believes the connector is reserved.
- A new EV plugging in sees `Available` (wrong) and starts charging without
  authorization for the reservation.

`internal/persistence/coordinator.go` handles persistence of various state
but it's not clear whether reservations are covered.

## Design

### 1. Survey existing persistence

Read `internal/persistence/coordinator.go` and `internal/persistence/persistence.go`
to determine which engine state is already persisted. Identify gap for
reservations.

### 2. Add reservation to persistence

- New `Reservation` struct in `internal/engine` with `ID, ConnectorID,
  IdTag, Expiry`.
- New `Reservations` slice in `Engine`.
- Persist via `persistence.Coordinator`.
- On startup, load and re-apply to engine state.

### 3. Inform CSMS of restored reservations

On startup, for each restored reservation, broadcast a status notification
(`Reserved` for v1.6) so the CSMS state converges.

## Files Touched

- **Edit:** `internal/engine/engine.go` (add Reservations field)
- **Edit:** `internal/engine/state.go` (if reservation logic is here)
- **Edit:** `internal/persistence/persistence.go` (add Reservation
  serialization)
- **Edit:** `internal/persistence/coordinator.go` (include in snapshot)
- **Edit:** `cmd/chargeghost/main.go` (load on startup, re-broadcast status)
- **New:** tests

## Acceptance Criteria

- Reservations survive a process restart.
- After restart, CSMS receives a `StatusNotification`/`ReservationStatusUpdate`
  for restored reservations.
- Expired reservations are not restored.
- Tests pass.

## Tasks

- [x] Audit `persistence/coordinator.go` for engine state coverage (reservations are persisted by engine.SaveState/LoadState)
- [x] Add `Reservation` type to engine (exists in `internal/engine/reservation.go`)
- [x] Add Reservations to persistence (`internal/engine/persist.go:23,213-226` — expired ones discarded on load)
- [x] Load on startup (`cmd/chargeghost/main.go:66` calls `e.LoadState(engineDir)`)
- [x] Re-broadcast status to CSMS (post-boot `StatusNotification` flow in v16/v201 `SendBootNotification` re-broadcasts restored connector state)
- [x] Tests (existing `TestEngine_SaveLoadState_Reservations` + new `TestEngine_SaveLoadState_ReservationRestoresConnectorStatus`)
- [x] Run `go build ./...` and `go test ./...`
