# Plan: TUI Phase 2 — App Shell + Fleet Dashboard

**Date:** 2026-08-31
**Version:** v1
**Parent plan:** `plans/2026-08-31-bubbletea-tui-v1.md`
**Depends on:** Phase 1 (client, boot, logging)
**Unblocks:** Phases 3–5

## Objective

Build the navigable skeleton every tab plugs into, and the first real
screen: the fleet dashboard. After this phase the TUI is genuinely usable
for station lifecycle management — create/start/stop/restart/reload/
enable/disable/delete stations, watch lifecycle and OCPP state live, edit
fleet config, and track async operations — entirely from a terminal.

## Design

### 1. Root application model (`internal/tui/app.go`)

```go
type App struct {
    cli    *client.Client
    events *client.Events
    ring   *logging.Ring            // phase 6 renders it; wiring lands here

    width, height int
    view    View                    // ViewFleet | ViewStation(stationID)
    fleet   fleet.Model             // dashboard
    station *station.Model          // created lazily on entry; nil at fleet level

    toasts  []toast                 // bounded queue, auto-expire
    modal   Modal                   // nil-able: confirm dialog / form overlay
    connState  string               // "", "reconnecting" → banner
}
```

- `Init`: issue `cli.ListStations` + subscribe pump commands; request
  `tea.WindowSizeMsg` (bubbletea's initial sizing) before first render.
- `Update`: route (in order) window-size → resize everything; client event
  messages → `tick`/`fleet_tick` refresh + toast triggers; modal-first key
  routing (modal swallows keys until closed); global keys; then active view.
- `View`: header (mode, station breadcrumb, conn banner) + active view +
  toast area + help bar. Alt screen, full-bleed.

### 2. Client events → tea.Msg (`internal/tui/msg.go`)

```go
type clientEventMsg client.Event      // raw envelope
type tickMsg client.Tick              // per-station snapshot, decoded
type fleetTickMsg map[string]client.Tick
type opEventMsg client.Event          // station_operation_started/completed/failed
type lifecycleMsg client.Event        // station_lifecycle_changed
type toastMsg toast; type toastExpireMsg int
```

A single pump `tea.Cmd` reads `events.Chan()` and returns messages; the
app re-issues the pump after each event (standard bubbletea pattern).
`client.Tick` (from phase 1 decoders) holds typed snapshot fields mirroring
`ws/snapshot.go`: `OcppConnected`, `Connectors []ConnectorTick`,
`ActiveSessions []SessionTick`, `EnergyMeters map[int]MeterTick`,
`Reservations`, `PendingRemoteStarts`, `UptimeSeconds`.

### 3. Theme + shared chrome (`internal/tui/theme.go`, `help.go`, `toast.go`)

- `theme.go`: lipgloss style set — status colors (Available green,
  Charging yellow-green, Suspended amber, Faulted red, Finishing gray,
  Reserved cyan, Unavailable dim), border styles, table header/footer,
  breadcrumb, banner, toast (success/error/info).
- `help.go`: full-width bottom bar from per-view `helpEntries`; `?` toggles
  an expanded overlay.
- `toast.go`: success/error/info, 5s auto-expire via `tea.Tick`, max 3
  visible, newest at bottom. Errors from `*client.APIError` render
  `message` + status.

### 4. Modal stack — confirm + form framework (`internal/tui/form/`)

Every mutation in phases 2–5 routes through two reusable components:

- `confirm.Modal` — title, body, confirm/cancel labels; returns
  `confirmResultMsg{Action string, OK bool}`.
- `form.Modal` — ordered `form.Field` list (text input, number, select,
  toggle, read-only), tab/shift-tab navigation, inline validation
  (required, numeric, enum), Enter submits → `formResultMsg{Action string,
  Values map[string]string}`. Wraps `bubbles/textinput` + custom select.

App owns one `modal` slot; views open modals by returning a
`openModalMsg`, results are dispatched back to the opening view by
`Action`. This is the single most reused code in the TUI — keep it small
and well-tested.

### 5. Fleet dashboard (`internal/tui/fleet/fleet.go`)

`bubbles/table`, columns:

| Station | Lifecycle | OCPP | Connectors | Sessions | Uptime |
(bold default station, `*` marker)

Data: merge `cli.ListStations()` (lifecycle, config-level fields) with the
latest `fleetTickMsg` (per-station `tick`: OCPP connected, connector
statuses, active sessions, uptime). `fleet_tick` arrives 1/s — repaint is
cheap; throttle re-sorting to preserve user's row focus (keyed by station
ID, not index).

Keybindings:

| Key | Action | REST call |
|---|---|---|
| enter | open station view | — |
| s / x | start / stop (confirm) | `POST /stations/{id}/start` `\|` `stop` |
| r | restart (confirm) | `POST /stations/{id}/restart` |
| R | reload config (confirm) | `POST /stations/{id}/reload` |
| e / d | enable / disable | `POST /stations/{id}/enable` `\|` `disable` |
| D | delete station (type-to-confirm) | `DELETE /stations/{id}` |
| n | new station (form: id, ocpp_id, version, url, connectors…) | `POST /stations` |
| f | fleet config view | `GET /fleet/config` |
| O | operations view | `GET /fleet/operations` |
| v | persist now | `POST /stations/{id}/persist` |

Toast on 202: "start requested… (op xxxx)"; completion/failure arrives via
`station_operation_completed/failed` → toast + row refresh +
`station_lifecycle_changed` → immediate `cli.ListStations()` refetch
(defensive; the next `fleet_tick` also self-heals).

### 6. Fleet config view + operations view (`internal/tui/fleet/config.go`, `operations.go`)

- Config: read-only pretty rendering of `GET /fleet/config` JSON with a
  viewport; `s` opens a save confirm (`POST /fleet/config/save`). Editing
  fleet config JSON in a textarea ships here only if trivial; otherwise
  read-only in v1 (station-level editing lives in phase 5) — decide during
  implementation, default read-only.
- Operations: table of `cli.Operations()` (id, type, station, state, age);
  auto-refresh on every `station_operation_*` event + `r` manual. Enter →
  detail (message, error, timestamps) via `cli.Operation(id)`.

### 7. Station container (`internal/tui/station/station.go`)

Tab bar across the top; tab set is the phases' delivery vehicle:

| Tab | Phase |
|---|---|
| Connectors / Sessions / Reservations | 3 |
| OCPP | 4 |
| Profiles / Auth / Firmware+Diag / Config | 5 |
| Events / Logs | 6 |

Empty tabs render "coming in phase N" placeholders (removed as they land).
Station-scoped REST calls use `/api/v1/stations/{id}/…` (the default
station is reachable both ways; always use the explicit form from the TUI —
simpler, uniform). `esc` pops back to fleet; `[`/`]` or tab-number keys
switch tabs; per-tab data fetch issues on first activation.

## Tests

- `form`/`confirm` model tests: navigation, validation, submit/cancel
  semantics (pure `Update`-driven; no teatest dependency yet).
- Fleet model test: inject synthetic `fleetTickMsg` + fake client
  interface — assert row set, default-station marker, lifecycle refresh
  after op-completion event.
- App routing test: modal swallows keys; `q` with dirty modal asks confirm.
- Client interface seam: `fleet.Model` takes a `FleetClient` interface
  (subset of `*client.Client`) so tests inject fakes — same pattern for
  every later tab.

## Files Touched

- **New:** `internal/tui/app.go`, `theme.go`, `keys.go`, `msg.go`,
  `toast.go`, `help.go`
- **New:** `internal/tui/fleet/fleet.go`, `config.go`, `operations.go` (+ `_test.go`)
- **New:** `internal/tui/station/station.go` (container + placeholders)
- **New:** `internal/tui/form/modal.go`, `confirm.go`, `field.go` (+ `_test.go`)
- **Edit:** `cmd/chargeghost/tui.go` (placeholder app → real `tui.NewApp`)
- **Edit:** `internal/client` (add operation/config methods if not already present)

## Acceptance Criteria

- TUI opens on the fleet dashboard with live-updating rows (≤1s lag from
  engine state change to row change).
- Full lifecycle loop from the terminal: create station → start → open →
  back → stop → delete, each with correct confirm/toast/async-op feedback.
- Station created via TUI appears in the JS frontend connected to the same
  port (`--listen` mode) — proves client/server parity.
- Fleet config and operations views render; op completion updates state
  without manual refresh.
- All model tests green; build/vet/test clean.

## Tasks

- [ ] Theme + help + toast chrome
- [ ] msg types + event pump wiring in `app.go`
- [ ] `form` framework (fields, validation, modal nav) + tests
- [ ] `confirm` modal + type-to-confirm variant for delete
- [ ] Fleet table model + merge logic (`ListStations` × `fleet_tick`) + tests
- [ ] Lifecycle keybindings → client calls → toast/op tracking
- [ ] New-station form
- [ ] Fleet config view + save confirm
- [ ] Operations view (event-driven refresh + detail)
- [ ] Station container with tab bar + placeholders; `tui.go` swap
- [ ] Manual pass on both modes; full verification suite
