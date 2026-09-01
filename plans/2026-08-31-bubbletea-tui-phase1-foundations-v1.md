# Plan: TUI Phase 1 — Foundations

**Date:** 2026-08-31
**Version:** v1
**Parent plan:** `plans/2026-08-31-bubbletea-tui-v1.md`
**Depends on:** nothing
**Unblocks:** all later phases

## Objective

Lay the groundwork without any visible TUI yet: Charm dependencies, HTTP
server listener injection, a shared composition root for server and TUI
modes, `tui` subcommand dispatch with a non-TTY guard, dual slog handler
(file + ring buffer), and the complete `internal/client` package (typed REST
client + resilient WebSocket subscriber) with tests.

End state: `go run ./cmd/chargeghost tui` boots the full fleet, serves the
API on an ephemeral loopback port, prints the bound address, runs a trivial
placeholder Bubble Tea program (single line of text, `q` to quit), and shuts
down cleanly with persisted state.

## Design

### 1. Dependencies

```
go get github.com/charmbracelet/bubbletea
go get github.com/charmbracelet/bubbles
go get github.com/charmbracelet/lipgloss
```

(`golang.org/x/term` arrives transitively via bubbletea — use it for the
TTY check; do not add it explicitly unless `go mod tidy` requires it.)

### 2. `api.Server` listener injection (`internal/api/server.go`)

Split bind from serve so callers can discover an ephemeral port. Existing
`Start()` semantics preserved bit-for-bit for server mode.

```go
// Listen binds addr without serving. Use with Serve. An addr of
// "127.0.0.1:0" yields an OS-assigned port via ln.Addr().
func (s *Server) Listen(addr string) (net.Listener, error)

// Serve serves on the supplied listener; blocks until the server stops.
func (s *Server) Serve(ln net.Listener) error

// Start remains: Listen(addr) + Serve(ln) with the existing log line.
func (s *Server) Start() error
```

`Shutdown` unchanged. Add `server_test.go` covering: ephemeral port is
discoverable via `ln.Addr()`, `Serve` + `Shutdown` terminate promptly.

### 3. Shared composition root (`cmd/chargeghost/boot.go`)

Move the wiring from `main()` verbatim — same ordering, same goroutines,
same comments (notably the `runCtx`-is-deliberately-NOT-ctx invariant
documented at `main.go:70-79`; the comment must survive the move). Shape:

```go
// Boot owns every process-level goroutine for one running instance:
// hub loop, fleet start, snapshot tickers, HTTP server.
type Boot struct {
    Hub    *ws.Hub
    Fleet  *FleetManager
    Server *api.Server
    Addr   string // actual bound address (ephemeral-safe)
    // ...unexported: cancel, wg, cfgPath, baseDir
}

// StartBoot composes and starts everything. listen is the requested HTTP
// address (":8080" for server mode, "127.0.0.1:0" for TUI mode).
func StartBoot(cfgPath, baseDir, listen string) (*Boot, error)

// Shutdown performs the server-mode shutdown sequence verbatim:
// cancel() → fm.Shutdown(15s ctx) → srv.Shutdown → wg.Wait.
func (b *Boot) Shutdown() error
```

`main()` becomes: logger setup, flag scan, `StartBoot(cfgPath, baseDir, ":8080")`,
signal wait, `boot.Shutdown()`. Behavior identical; no route, handler, or
config changes.

### 4. Subcommand dispatch (`cmd/chargeghost/main.go`)

At the very top of `main`, before anything writes to stdout:

```go
if len(os.Args) > 1 && os.Args[1] == "tui" {
    runTUI(os.Args[2:])
    return
}
```

Server-mode flag parsing (`-log-level` scan) unchanged — it never sees the
`tui` arg since dispatch happens first. Unknown first args keep current
behavior (ignored), matching today's loose flag handling.

### 5. TUI boot (`cmd/chargeghost/tui.go`)

```go
func runTUI(args []string) {
    // 1. Parse --listen (default "127.0.0.1:0") and -log-level.
    // 2. Non-TTY guard: if stdin OR stdout is not a terminal
    //    (golang.org/x/term.IsTerminal), print
    //    "chargeghost tui requires an interactive terminal" to stderr,
    //    os.Exit(1). Before any engine starts.
    // 3. Logging FIRST (FleetManager logs during construction):
    //    internal/logging.NewDualHandler(dir ~/.chargeghost/logs,
    //    file "tui.log", ring capacity 1000); slog.SetDefault.
    // 4. boot, err := StartBoot(cfgPath, baseDir, listen) — on error the
    //    dual handler already captures it; also fmt.Fprintln(os.Stderr).
    // 5. cli := client.New(boot.Addr)  // http://127.0.0.1:<port>
    //    events := cli.Subscribe("scope=all") // starts WS goroutine
    // 6. p := tea.NewProgram(tui.NewPlaceholderApp(cli, events),
    //       tea.WithAltScreen())
    //    _, runErr := p.Run()
    // 7. events.Stop(); boot.Shutdown(); report runErr; restore logger
    //    to stderr for the final "goodbye" line.
}
```

Placeholder app (phase 2 replaces it): one line showing `boot.Addr` and
station count (via `cli.ListStations`), `q`/ctrl+c quits. Enough to prove
the whole loop end-to-end.

### 6. Dual slog handler (`internal/logging/dual.go`)

```go
type DualHandler struct {
    file  slog.Handler // JSON, append, to <dir>/tui.log
    ring  *Ring        // bounded, thread-safe
    level slog.LevelVar
}

type Ring struct { /* mutex-guarded []string, cap N, Subscribe() chan string */ }
```

- `Enabled`/`WithAttrs`/`WithGroup` fan out to both; ring stores
  pre-formatted lines (`2006-01-02 15:04:05 LEVEL msg attrs...`).
- `Ring.Subscribe(ch)` delivers new lines (non-blocking send, drop on slow
  consumer) — the phase-6 Logs tab consumes this; the ring snapshot is the
  backfill on tab open.
- Unit tests: fan-out to both sinks, ring eviction at cap, concurrent
  Handle calls race-free (`-race`).

### 7. `internal/client` — REST (`client.go`, `types.go`)

`Client{baseURL string, hc *http.Client}` (2s timeout; mutations 10s).
Reuse exported `internal/api` DTOs (`StationSnapshot`, `Operation`,
`OperationResponse`, `CreateConnectorRequest`, `StartSessionRequest`, …)
wherever shapes match — no redefinition. Only WS payloads and a few loose
responses (e.g. `about`) get client-local structs.

Method groups (one file section each; only what later phases consume):

- `ListStations() ([]api.StationSnapshot, error)` → `GET /api/v1/stations`
- `FleetStatus|FleetConfig|SaveFleetConfig|ReloadFleet|Operations|Operation(id)`
- `StartStation|StopStation|RestartStation|EnableStation|DisableStation|
  ReloadStation|PersistStation|DeleteStation|CreateStation|PatchStationConfig`
  (all `/api/v1/stations/{id}/...` + `POST /api/v1/stations`)
- `StationStatus(id)`; connector/session/reservation, OCPP, profile,
  local-auth, firmware, diagnostics, config-key, queue, raw-send wrappers
  added incrementally in phases 3–5 (leave TODO stubs out — add with the
  tab that needs them; only fleet + status methods land in phase 1)

Error model: non-2xx → `*APIError{Status int, Message string}` parsing the
`{success, message}` envelope from `internal/api.Response`; 202 responses
return `OperationResponse` so callers can track `operation_id`.

### 8. `internal/client` — WebSocket subscriber (`ws.go`)

```go
type Events struct { /* internal chans, ctx */ }
func (c *Client) Subscribe(query string) *Events   // "scope=all"
func (e *Events) Chan() <-chan Event               // Event{Type, StationID,
                                                    //     OperationID, Ts, Raw json.RawMessage}
func (e *Events) Stop()
func (e *Client) DecodeTick(raw json.RawMessage) (Tick, error)          // phase 2
func (c *Client) DecodeFleetTick(raw json.RawMessage) (map[string]Tick, error)
```

- Dial `GET {base}/ws?scope=all` (query format verified:
  `ScopeFromRequest`, `internal/api/ws/hub.go:215`). gorilla/websocket is
  already a dependency.
- Read pump goroutine: first message is `state_snapshot`; then `tick`
  (1 Hz/station), `fleet_tick` (1 Hz), and event messages — envelope parse
  only (`Type`, `StationID`, `OperationID`, `Timestamp`); `data` stays
  `json.RawMessage` for the TUI to decode per type.
- Reconnect: on read error, close, exponential backoff 1s→2s→…→30s (cap),
  re-dial, banner event (`__disconnected`/`__reconnected` synthetic types
  delivered on the channel). Resubscribe is implicit (server pushes
  snapshots to new connections).
- Backpressure: channel cap 256; if full, drop the oldest queued *tick*
  (state is refreshed 1/s anyway) and keep events; document the policy.
- `Stop()` cancels context, waits for pump exit.

### 9. Tests

- `internal/api/server_test.go` — ephemeral port discovery, Serve/Shutdown.
- `internal/logging/dual_test.go` — fan-out, ring eviction, `-race`.
- `internal/client/client_test.go` — against `httptest.Server` +
  `api.NewFleetRouter`-mounted fake fleet (reuse handler-test scaffolding
  from `internal/api/handlers_fleet_test.go`): success, 4xx → `APIError`,
  202 → `OperationResponse`.
- `internal/client/ws_test.go` — against a real `ws.Hub` +
  `httptest.Server`: receive initial `state_snapshot`, a broadcast
  `fleet_tick`, synthetic disconnect on server close, reconnect.
- `cmd/chargeghost/boot_test.go` — `StartBoot` on `127.0.0.1:0` serves
  `/health` 200; `Shutdown` returns within 5s and all goroutines drain.
- Placeholder app: trivial model test (Update on `q` returns `tea.Quit`).

## Files Touched

- **Edit:** `go.mod` / `go.sum` (charm deps)
- **Edit:** `internal/api/server.go` (Listen/Serve split)
- **Edit:** `cmd/chargeghost/main.go` (dispatch + rewrite to use Boot; behavior identical)
- **New:** `cmd/chargeghost/boot.go`, `cmd/chargeghost/boot_test.go`
- **New:** `cmd/chargeghost/tui.go` (runTUI + placeholder app model)
- **New:** `internal/logging/dual.go`, `internal/logging/dual_test.go`
- **New:** `internal/client/client.go`, `internal/client/types.go`,
  `internal/client/ws.go`, `internal/client/client_test.go`,
  `internal/client/ws_test.go`
- **New:** `internal/api/server_test.go`

## Acceptance Criteria

- `go build ./... && go vet ./... && go test ./...` clean (incl. `-race` on new packages).
- `./chargeghost` (server mode) behavior identical: same startup logs, same
  `:8080` bind, `/health` 200, clean SIGINT shutdown.
- `./chargeghost tui` in a real terminal: placeholder renders the loopback
  address and station count within ~2s; `~/.chargeghost/logs/tui.log`
  receives JSON entries; `q` exits cleanly (fleet persisted, goroutines
  drained, terminal restored).
- `./chargeghost tui` under `script -c`-less non-TTY stdin (e.g.
  `echo | ./chargeghost tui`) exits 1 with the terminal message.
- `./chargeghost tui --listen 127.0.0.1:18080` serves the full API on the
  pinned port while the TUI runs.
- WS subscriber test demonstrates reconnect after server-side close.

## Tasks

- [ ] `go get` bubbletea / bubbles / lipgloss; `go mod tidy`
- [ ] `api.Server`: `Listen`/`Serve` split + `server_test.go`; keep `Start()` semantics
- [ ] Extract `boot.go` from `main.go` verbatim (preserve `runCtx` invariant + comments); `main()` rewritten on top; `boot_test.go`
- [ ] Subcommand dispatch in `main()`; verify server flag parsing unaffected
- [ ] `internal/logging` dual handler + ring + tests
- [ ] `runTUI`: flags (`--listen`, `-log-level`), non-TTY guard, logging-first ordering, boot, placeholder app, shutdown path
- [ ] `internal/client` REST core (fleet + status groups) + `APIError`/`OperationResponse` handling + tests
- [ ] `internal/client` WS subscriber (envelope parse, backoff, synthetic disconnect events, tick decoders) + tests
- [ ] Placeholder app model + test
- [ ] Full verification: build, vet, test, both modes smoke-tested manually
