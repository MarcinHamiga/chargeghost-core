# Plan: TUI Phase 6 — Polish & Hardening

**Date:** 2026-08-31
**Version:** v1
**Parent plan:** `plans/2026-08-31-bubbletea-tui-v1.md`
**Depends on:** Phases 2–5

## Objective

Close the gaps between "all features present" and "daily-drivable": the
two live-viewer tabs (Events, Logs), connection-loss UX, resize/narrow-
terminal handling, model test coverage, and documentation. Also the
program-level regression pass against the acceptance criteria of the
parent plan.

## Design

### 1. Events tab (`internal/tui/station/eventlog.go`)

Live scrolling log of every WS event for the station (`bubbles/viewport`,
follow-tail on by default, auto-pause on user scroll-up):

```
12:04:11 session_started        conn 1  txn 42  idTag TAG_7
12:04:12 connector_status_changed conn 1  Charging
12:04:13 ocpp_queue_overflow    depth 1000/1000 dropped 3
```

- Line rendering per known event type (structured extraction from
  `data`); unknown types fall back to compact JSON.
- Filters: `/` free-text, `t` cycle type-class filter (connector/session/
  ocpp/lifecycle/other), `c` clear. Bounded ring (last 2000) so long
  sessions don't grow memory.
- Fleet-level variant on the fleet dashboard (`e` key): same component,
  all stations, station column added — reuses the component with a
  station-column toggle.

### 2. Logs tab (`internal/tui/station/logs.go` + fleet view)

Renders the phase-1 `logging.Ring`:

- On activation: backfill from `ring.Snapshot()`, then stream via
  `ring.Subscribe(ch)` pumped through `tea.Cmd` like the WS events.
- `bubbles/viewport` follow-tail, `l` cycle level filter (all/debug/info/
  warn+), `c` clear view (ring itself persists to `tui.log`).
- Station tab and fleet dashboard (`L` key) share the component — logs
  are process-wide.

### 3. Connection-loss UX (`internal/tui/app.go`)

The WS subscriber's synthetic `__disconnected`/`__reconnected` events
(phase 1) drive a persistent top banner ("engine connection lost —
retrying in 4s") + disable mutation keys fleet-wide (render-only
degradation; actions that would certainly fail shouldn't be invocable).
HTTP-call failures during disconnection already toast. On reconnect:
full refetch (`ListStations` + active view data) — the subscriber
resubscribes and the next `fleet_tick` self-heals rows.

### 4. Resize & narrow terminals

- `WindowSizeMsg` already propagates (phase 2); audit every tab:
  tables must recompute column widths from `width`, detail panes wrap,
  forms center correctly at ≥60 cols.
- Below 80×24: hide the help bar to one hint line, collapse detail panes
  to summary rows; below 60 cols: table switches to a minimal column set
  (defined per tab in theme.go as `MinCols()`).
- Degradation is render-only; no data is dropped.

### 5. Crash-safety & exit hygiene

- `tea.WithFilter`/recovered `Update` wrapper: a panic in any tab model is
  caught, rendered as an error toast + tab reset, never kills the program
  (the engine keeps running regardless — TUI is only a client).
- Ctrl+C during modal: close modal first, second press quits; `q` at fleet
  view with running stations offers a confirm that shows what will be
  stopped (station list + "state will be persisted").
- Exit path: existing `events.Stop()` → `boot.Shutdown()` sequence;
  after shutdown restore a stderr logger and print the goodbye line so
  operators see clean-stop confirmation after alt-screen restore.

### 6. Test coverage pass

- Every tab model: at minimum render-from-canned-state + one key→call
  assertion (phases 3–5 delivered these; fill gaps).
- `teatest` end-to-end for one golden path: boot fleet (httptest fake) →
  open station → start charging → observe row → quit. Runs in CI without
  a TTY (teatest uses a pty — verify CI container support; if flaky, mark
  `//go:build !ci` style guard per repo conventions, else keep local-only
  with a Makefile/script target).
- Golangci-style pass with `go vet`; `gofmt` check.

### 7. Documentation

- `README.md`: new "Terminal UI" section — invocation, flags (`--listen`,
  `-log-level`), keymap cheatsheet, non-TTY caveat.
- `AGENTS.md`: one line under Build & Test (`./chargeghost tui`) + a note
  in Key Patterns that the TUI is an HTTP/WS client (no engine coupling),
  pointing at the master plan.
- `REST_API.md`: unchanged (no API changes anywhere in the program).
- Master plan task list: tick phases, record deviations.

## Files Touched

- **New:** `internal/tui/station/eventlog.go`, `internal/tui/station/logs.go`
  (+ tests), `internal/tui/eventlog/` shared component if extracted
- **Edit:** `internal/tui/app.go` (banner, panic recovery, exit confirm,
  refetch-on-reconnect)
- **Edit:** `internal/tui/theme.go` (`MinCols` sets), all tabs (resize audit)
- **Edit:** `cmd/chargeghost/tui.go` (exit hygiene, post-exit logger)
- **Edit:** `README.md`, `AGENTS.md`, master plan checklist

## Acceptance Criteria

- Events and Logs tabs stream live with working filters and follow-tail;
  30-minute soak shows flat memory (ring bounds).
- Killing the embedded HTTP server externally (or forcing WS failure)
  shows the banner, disables mutations, and fully self-heals on recovery.
- Usable at 60 cols and 200 cols; no truncation artifacts, no panic on
  rapid resize.
- A deliberately-injected tab panic shows an error toast and the app keeps
  running.
- Quit flow: confirm shows running stations; exit persists state; goodbye
  line visible after terminal restore.
- Docs updated; full suite green (`go build`, `go vet`, `go test ./...`);
  server mode regression-free final pass.

## Tasks

- [ ] Events tab (per-station + fleet variant, filters, ring bound)
- [ ] Logs tab (backfill + stream, level filter)
- [ ] Disconnect banner + mutation disable + reconnect refetch
- [ ] Resize audit across all tabs + MinCols degradation
- [ ] Panic recovery wrapper + tab reset
- [ ] Exit hygiene (modal-aware Ctrl+C, quit confirm, post-exit logger)
- [ ] Test coverage gaps + teatest golden path (with CI decision)
- [ ] README / AGENTS.md / master-plan updates
- [ ] Final full verification: build, vet, test, both modes manual pass
