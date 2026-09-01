# Plan: TUI Phase 3 — Core Operations Tabs

**Date:** 2026-08-31
**Version:** v1
**Parent plan:** `plans/2026-08-31-bubbletea-tui-v1.md`
**Depends on:** Phase 2 (app shell, form framework, station container)
**Unblocks:** nothing (parallel with 4–5)

## Objective

The everyday simulation controls: the Connectors, Sessions, and
Reservations tabs for a selected station. After this phase a user can drive
the full charge session lifecycle (plug in → authorize/start → suspend →
resume → stop → unplug) and reservation lifecycle from the terminal.

All calls go to `/api/v1/stations/{id}/…` (explicit form even for the
default station — uniform URL building in the client).

## Design

### 1. Connectors tab (`internal/tui/station/connectors.go`)

Master list + detail pane (split vertical; `tab`/`shift-tab` or up/down
move, `enter` toggles detail focus).

Table columns (live from station `tick`; refetch `GET …/connectors` on
`connector_status_changed`, `connector_plug_changed`,
`connector_id_tag_changed`, `connector_params_changed` events):

`ID | Status | Plugged | IdTag | Volts | Amps | Phase | Meter(Wh) | SoC`

Meter/SoC join `tick.EnergyMeters[id]` and the connector's active session
from `tick.ActiveSessions` — the tick already carries all three.

Keybindings on the focused connector:

| Key | Action | REST call |
|---|---|---|
| p / u | plug in / unplug | `POST …/connectors/{id}/plug_in` `\|` `/unplug` |
| c | start charging (form: idTag) | `POST …/connectors/{id}/start-charging` |
| x | stop charging (confirm) | `POST …/connectors/{id}/stop-charging` |
| v / m | suspend EV / resume | `POST …/suspend_ev` `\|` `/resume_charging` |
| a | set availability (select: Available/Unavailable) | `PUT …/connectors/{id}/availability` |
| r / C-r | set RFID (input) / clear | `PUT`/`DELETE …/connectors/{id}/rfid` |
| n / e / D | create / edit / delete connector (forms) | `POST …/connectors`, `PUT`/`DELETE …/connectors/{id}` |

Forms reuse phase-2 `form.Modal` (create/edit: max current, voltage,
phase, type per `CreateConnectorRequest`/`UpdateConnectorRequest` DTOs).
State-machine awareness: disable keys whose transition is invalid for the
current status (map from `internal/engine/state.go` rules — status enum
values match `ConnectorDTO.Status`), e.g. `p` only in Available/Preparing;
prevents guaranteed-4xx calls and documents the flow. Invalid-anyway calls
surface the handler's error toast verbatim.

Detail pane: full connector JSON + active session block + reservation
occupying the connector (if any) + last stopped session energy
(`GET …/sessions/last-stopped` on focus).

### 2. Sessions tab (`internal/tui/station/sessions.go`)

Table: `Transaction | Connector | IdTag | Energy(Wh) | SoC% | Started |
Charging?` — sourced from `tick.ActiveSessions` (live) with a
`GET …/sessions` refetch on `session_started`/`session_stopped`/
`charging_state_changed`/`transaction_id_changed` events.

| Key | Action | REST call |
|---|---|---|
| s | start session (form: connector, idTag) | `POST …/sessions/start` |
| x | stop all sessions (confirm) | `POST …/sessions/stop` |
| enter | session detail | `GET …/sessions/{connector_id}` |
| l | last stopped session detail | `GET …/sessions/last-stopped` |

Detail shows energy charged, SoC progression (if `evBatteryCapacity` ≠ 0),
timestamps, idTag. Energy values are cumulative odometers — render Wh with
a kW derived figure for the active session (delta over tick interval is
available client-side; keep it simple: instantaneous V×A for kW display).

### 3. Reservations tab (`internal/tui/station/reservations.go`)

Table: `ReservationID | Connector | IdTag | ParentIdTag | Expires` — live
from `tick.Reservations` + refetch on `reservation_changed`.

| Key | Action | REST call |
|---|---|---|
| n | create (form: connector, idTag, expiry, parent) | `POST …/reservations` |
| D | cancel (confirm) | `DELETE …/reservations/{reservation_id}` |

Expiry input accepts relative durations (`90m`, `1h30m`) with absolute
RFC3339 as fallback — a `form.Field` validator extension
(`relativeTimeField`) used again in phase 4/5 forms.

### 4. Client additions (`internal/client/client.go`)

Connector/session/reservation wrappers returning `internal/api` DTOs
(`ConnectorDTO`, `SessionDTO`, `StoppedSessionDTO`, `ReservationDTO`, …).
One method per route in the tables above; no new error semantics.

### 5. Event fan-out refinement (`internal/tui/app.go`)

Route station-scoped events to the open station view only if
`event.StationID == activeStationID`; `connector_*`/`session_*`/
`reservation_*` events on other stations bump a per-station dirty flag so
returning to that station refetches once. Keep in `app.go` — tabs stay
dumb about routing.

## Tests

- Each tab: fake `StationClient` interface; assert table rendering from
  synthetic ticks, key → correct REST call (via recorded fake), invalid
  transitions disable keys, forms produce correct request bodies.
- Suspend/resume/start/stop against the real engine via httptest +
  `AppContext` (reuse `internal/api/handlers_test.go` scaffolding) for at
  least one happy-path per tab — catches DTO drift.
- Connector state-map test: exhaustive status × action matrix matches
  `engine` transition rules (import `internal/engine` in the test to
  source the matrix — no duplication).

## Files Touched

- **New:** `internal/tui/station/connectors.go` (+ `_test.go`),
  `sessions.go` (+ `_test.go`), `reservations.go` (+ `_test.go`)
- **Edit:** `internal/client/client.go` (+ tests): connector/session/
  reservation methods
- **Edit:** `internal/tui/form/field.go`: `relativeTimeField` validator
- **Edit:** `internal/tui/station/station.go`: activate the three tabs
- **Edit:** `internal/tui/msg.go`, `app.go`: station-scoped event routing + dirty flags

## Acceptance Criteria

- Full session lifecycle drivable from the Connectors tab with correct
  enable/disable of actions at each state; statuses turn over live
  (≤1s) as the simulation advances.
- Sessions tab shows live energy/SoC progression during charging; stop and
  last-stopped details match the REST responses.
- Reservation create with `90m` expiry renders the resolved absolute time;
  expiry frees the connector (status change visible) without manual
  refresh.
- Invalid-action keys are visibly disabled; forcing one server-side
  (race) surfaces the handler error as a toast.
- All tests green; build/vet/test clean; server mode untouched.

## Tasks

- [ ] Client: connector/session/reservation method group + fake-friendly interfaces + tests
- [ ] App: station-scoped event routing + per-station dirty flags
- [ ] Connectors tab: table + detail, action keys, transition-aware enabling
- [ ] Connector forms (create/edit/availability/RFID)
- [ ] Sessions tab: live table, start/stop, details, last-stopped
- [ ] Reservations tab + `relativeTimeField`
- [ ] Transition-matrix test sourced from `internal/engine`
- [ ] Manual lifecycle pass; full verification suite
