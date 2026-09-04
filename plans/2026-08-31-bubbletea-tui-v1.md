# Plan: Bubble Tea TUI Mode for ChargeGhost

**Date:** 2026-08-31
**Version:** v1
**Priority:** P1 — headless operability
**Status:** approved (architecture + scope + logging + port decisions confirmed)

## Objective

Add a fully-fledged terminal UI (Bubble Tea) as a second way to run and
manage the simulator:

```
./chargeghost        # HTTP/WS server mode — unchanged default
./chargeghost tui    # TUI mode — terminal management, no browser needed
```

The TUI covers the entire REST surface: fleet/station lifecycle admin,
connector/session/reservation operations, OCPP diagnostics (link status,
queue, dead-letter, config keys, raw sends), charging profiles, local auth
list, firmware, diagnostics uploads, config view/patch, plus live event and
log viewers.

## Confirmed decisions

| Decision | Choice |
|---|---|
| TUI ↔ engine coupling | HTTP/WS client of an embedded server (same composition root, loopback-bound) |
| Binary layout | subcommand of `chargeghost`; default server path untouched |
| v1 scope | everything, incl. rarely-used surfaces (firmware, diagnostics, profiles, local auth) |
| TUI logging | `slog` → `~/.chargeghost/logs/tui.log` **plus** in-app Logs viewer tab |
| Embedded port | ephemeral `127.0.0.1:0` by default; `--listen 127.0.0.1:8080` pins it |

## Architecture

The TUI is just another API client — exactly like the JS frontend, with zero
engine coupling. This honors design decision #4 in AGENTS.md ("REST API is
the sole control surface") and reuses all handler validation, admission
control, and operation tracking.

```
cmd/chargeghost
  ├── "tui" arg → runTUI()                    (new tui.go, uses boot.go)
  │     ┌────────────────────────────────────────────────────┐
  │     │ Boot (shared with server mode):                    │
  │     │   ws.Hub → FleetManager → fm.Start → 1s tickers    │
  │     │   HTTP server on 127.0.0.1:<ephemeral|‑-listen>    │
  │     │   slog → file + ring buffer (dual handler)         │
  │     └────────────────────────────────────────────────────┘
  │     tea.NewProgram ──► internal/tui/  ──HTTP/WS──► 127.0.0.1
  │                          └── internal/client/ (typed REST + WS subscriber)
  └── default → existing server path (verbatim)
```

### Package layout (all new)

```
cmd/chargeghost/
  tui.go            runTUI(): composition, program lifecycle, shutdown
  boot.go           shared composition root extracted from main.go
internal/client/
  client.go         typed REST client over internal/api DTOs
  ws.go             /ws subscriber: reconnect, backoff, tea.Msg pump
  types.go          typed structs for WS payloads (tick, fleet_tick, events)
internal/logging/
  dual.go           slog handler: file + thread-safe ring buffer
internal/tui/
  app.go            root model, view stack, modal stack, toasts
  theme.go          lipgloss styles, colors, borders
  keys.go           global + per-view keybindings
  msg.go            client events → tea.Msg types
  fleet/            fleet dashboard model
  station/          station container + tab models (one file per tab)
  form/             shared input-form + confirm-dialog builders
```

### Data flow

- **Live state**: subscribe `GET /ws?scope=all` (see `ScopeFromRequest`,
  `internal/api/ws/hub.go:215`). The hub already broadcasts `fleet_tick` +
  per-station `tick` snapshots every second (`cmd/chargeghost/main.go:89-121`)
  and event messages (`connector_status_changed`, `session_started`,
  `station_lifecycle_changed`, `station_operation_started/completed/failed`,
  `ocpp_*`, …). No polling needed except explicit refetch after mutations.
- **Mutations**: typed REST calls; many fleet ops return `202 Accepted` with
  an `operation_id` — completion arrives via `station_operation_*` WS events.
- **Async ops**: event-driven tracking, no polling loop required.

## Existing capabilities the TUI consumes (verified, no API changes)

- Fleet: `GET/POST /api/v1/stations`, `GET /fleet/status|config`,
  `POST /fleet/config/save|reload`, `GET /fleet/operations[/{id}]`
- Station admin: `status|config|start|stop|restart|enable|disable|reload|persist`,
  `DELETE /stations/{id}`, `ocpp/reconnect`, `credentials/*`, `queue/*`
- Station ops (default station at `/api/v1/*`, others at
  `/api/v1/stations/{id}/*`): `connectors/*` (plug in/out, suspend/resume,
  start/stop charging, availability, RFID), `sessions/*`, `reservations/*`,
  `timeline/*`, `local-auth-list/*`, `firmware/*`, `diagnostics/*`,
  `charging-profiles/*`, `ocpp/*` (status, config-keys, authorize, heartbeat,
  raw sends), `about`
- DTOs are exported (`internal/api/dto.go`, `internal/api/fleet.go`) — the
  client reuses them instead of redefining request/response shapes.

## Key technical points

1. **`api.Server` listener injection** — `Start()` uses `ListenAndServe`,
   which cannot report an ephemeral port. Add `Listen`/`Serve` split; keep
   `Start()` behavior identical for server mode (`internal/api/server.go`).
2. **stdout ownership** — Bubble Tea owns the terminal; every `slog` write
     must be redirected before `FleetManager` creation (it logs during
   construction). Dual handler: append to `~/.chargeghost/logs/tui.log` and
   push into a bounded ring buffer the Logs tab renders.
3. **Shared boot** — extract main.go's wiring verbatim into `boot.go`
   (preserving the deliberate `runCtx` semantics documented at
   `cmd/chargeghost/main.go:70-79`) so server and TUI modes cannot drift.
4. **Non-TTY guard** — fail fast with a clear message when stdin/stdout is
   not a terminal (Docker/CI), before any engine starts.
5. **WS client reconnect** — exponential backoff (1s → 30s cap), connection
   state surfaced as a TUI banner.
6. **Typed WS payloads** — server payloads are `map[string]any`; client
   re-unmarshals `data` per message type into structs mirroring
   `ws/snapshot.go` keys, tolerant of unknown fields.

## Phase breakdown

Each phase has a detailed implementation plan:

| Phase | Plan file | Delivers |
|---|---|---|
| 1 | `2026-08-31-bubbletea-tui-phase1-foundations-v1.md` | deps, server listener injection, shared boot, subcommand dispatch, dual logging, `internal/client` (REST + WS), non-TTY guard |
| 2 | `2026-08-31-bubbletea-tui-phase2-app-shell-fleet-dashboard-v1.md` | app shell (navigation, theme, toasts, modals), fleet dashboard + station lifecycle ops, fleet config, operations view |
| 3 | `2026-08-31-bubbletea-tui-phase3-core-operations-v1.md` | Connectors, Sessions, Reservations tabs |
| 4 | `2026-08-31-bubbletea-tui-phase4-ocpp-diagnostics-v1.md` | OCPP tab: status, reconnect, queue/dead-letter, config keys, authorize/heartbeat, raw sends |
| 5 | `2026-08-31-bubbletea-tui-phase5-profiles-auth-firmware-v1.md` | Charging profiles, local auth, firmware, diagnostics, config tabs |
| 6 | `2026-08-31-bubbletea-tui-phase6-polish-hardening-v1.md` | Event log tab, Logs tab, resize handling, reconnect UX, tests, docs |

**Every phase leaves `go build ./...`, `go vet ./...`, and `go test ./...`
clean, and the server mode byte-for-byte behaviorally unchanged.** Phases 1–2
are strictly sequential; 3–5 are independent of each other (all depend on 2);
6 depends on all.

## Risks & mitigations

- **Form volume** (largest REST surface → many forms): shared
  `form/` builders in phase 2 keep per-tab cost low; phases 3–5 slice work
  into reviewable units.
- **Loosely-typed WS payloads**: single `types.go` with structs mirroring
  `ws/snapshot.go`; unknown fields ignored by encoding/json default.
- **Shutdown ordering**: TUI exit path reuses the server-mode sequence
  (`fm.Shutdown` → `srv.Shutdown` → `wg.Wait`); state persistence relies on
  it (see comments in `main.go`).
- **Terminal variance**: alt-screen rendering, explicit `WindowSizeMsg`
  propagation, min-width guard with degraded layout.

## Acceptance criteria (program-level)

- `./chargeghost` behaves exactly as before (server mode regression-free).
- `./chargeghost tui` in a bare terminal: fleet dashboard renders live
  (≤2s to first paint), full management surface operable without a browser.
- `./chargeghost tui --listen 127.0.0.1:8080` lets the JS frontend attach
  to the same instance the TUI is driving.
- Ctrl+C or `q` exits cleanly: engines stop, state persists
  (same guarantees as server shutdown), terminal restored.
- Non-TTY invocation prints an actionable error, exit code != 0.
- All tests green; `go vet` clean.

## Tasks

- [ ] Phase 1 — foundations (`plans/2026-08-31-bubbletea-tui-phase1-foundations-v1.md`)
- [ ] Phase 2 — app shell + fleet dashboard
- [ ] Phase 3 — core operations tabs
- [ ] Phase 4 — OCPP diagnostics tab
- [ ] Phase 5 — profiles, local auth, firmware, diagnostics, config tabs
- [ ] Phase 6 — polish & hardening
