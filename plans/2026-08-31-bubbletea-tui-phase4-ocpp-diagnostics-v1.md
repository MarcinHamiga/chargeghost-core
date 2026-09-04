# Plan: TUI Phase 4 — OCPP Diagnostics Tab

**Date:** 2026-08-31
**Version:** v1
**Parent plan:** `plans/2026-08-31-bubbletea-tui-v1.md`
**Depends on:** Phase 2
**Unblocks:** nothing (parallel with 3 and 5)

## Objective

Answer "is OCPP healthy?" from a terminal, and exercise the OCPP surface
manually: link status, reconnect, message queue + dead-letter management,
config keys, and raw message sends (the simulator's debugging tools).

## Design

Sub-tabbed container (`internal/tui/station/ocpp.go`) — the OCPP tab is too
dense for one screen. Sub-tabs: `Status | Queue | Config Keys | Send`.
Left/right or 1–4 switch; the container shares one station client.

### 1. Status sub-tab (`status.go`)

`GET /api/v1/stations/{id}/ocpp/status` (from the
`ocpp-status-endpoint` plan: version, connected, connectedAt/disconnectedAt,
lastMessageAt, lastError(+At), reconnectCount, upSince, csmsUrl, ocppId;
v201: queueDepth/queueExhausted/drainInProgress; v16: lastHeartbeatAt).

Layout: formatted key/value block + derived health line:

```
OCPP 2.0.1  ● Connected   (up 4h12m, 3 reconnects)
CSMS ws://…  CP_001       last msg 2s ago
⚠ last error: … 12m ago
```

Live updates: `ocpp_connected`/`ocpp_disconnected`/`ocpp_reconnected`/
`connection_state_changed` WS events → refetch; also a 10s `tea.Tick`
refetch while the sub-tab is active (v1.6 heartbeat staleness otherwise
invisible). `last msg Xs ago` / `heartbeat Xs ago` relative times re-render
on the same tick without refetching.

| Key | Action | REST call |
|---|---|---|
| k | reconnect (confirm; note: full station restart) | `POST …/stations/{id}/ocpp/reconnect` |
| h | send heartbeat | `POST …/ocpp/heartbeat` |
| A | authorize (form: idTag) | `POST …/ocpp/authorize` |

### 2. Queue sub-tab (`queue.go`)

`GET …/stations/{id}/queue/status` → depth/cap/persistence mode/in-flight
state; `GET …/queue/dead-letter` → failed messages.

```
Depth 17/1000  persist=disk  drain: no
Dead letter (2): [v] view  [D] clear
  1. StopTransaction  2026-08-31T12:04:11Z  err: send timeout
  2. MeterValues      2026-08-31T12:04:12Z  err: …
```

| Key | Action | REST call |
|---|---|---|
| d | drain (confirm) | `POST …/queue/drain` |
| c | clear (type-to-confirm) | `POST …/queue/clear` |
| enter | dead-letter detail (payload viewport) | — |
| D | clear dead-letter (confirm) | `DELETE …/queue/dead-letter` |

`ocpp_queue_overflow` events toast immediately + bump a session drop
counter shown in the header line.

### 3. Config Keys sub-tab (`configkeys.go`)

`GET …/ocpp/config-keys` → table `Key | Value | Read-only? | Reboot?`
(filterable — `/` opens a filter input on `bubbles/table`'s filter). Edit
via `form.Modal` (value input constrained by the key's type where the DTO
carries it) → `PATCH …/ocpp/config-keys`. `ocpp_config_key_changed` /
`ocpp_variable_changed` events refetch the row. Keys requiring reboot
render a `*` and post-edit toast reminds the operator.

### 4. Send sub-tab (`send.go`)

Forms for the raw send endpoints (debug tooling; validation mirrors the
handlers):

| Form | Endpoint | Fields |
|---|---|---|
| StatusNotification | `POST …/ocpp/raw/status-notification` | connector, status (select), errorCode (select, optional) |
| MeterValues | `POST …/ocpp/raw/meter-values` | connector, sampledValue(s), measurand select |
| DataTransfer | `POST …/ocpp/raw/data-transfer` | vendorId, messageId, data (textarea) |
| StartTransaction | `POST …/ocpp/raw/start-transaction` | connector, idTag, meterStart |
| StopTransaction | `POST …/ocpp/raw/stop-transaction` | transaction id, meterStop, reason select |

Every send's request AND response render into a per-station OCPP message
log (rolling viewport, last ~50) — this doubles as the manual sanity check
before looking at the CSMS. Multi-line payloads (data-transfer, meter
values array) reuse the textarea field added to `form/field.go` here.

### 5. Client additions

`OCPPStatus`, `QueueStatus`, `DeadLetter`, `ConfigKeys` (GET/PATCH),
`Authorize`, `Heartbeat`, `Reconnect`, `DrainQueue`, `ClearQueue`,
`DeadLetterClear`, and the five raw-send methods. Types: reuse
`handlers.OCPPStatus`-shaped structs if exported; otherwise client-local
mirroring the handler DTOs (check `internal/api/handlers/ocpp_status.go`
during implementation — prefer importing exported types).

## Tests

- Each sub-tab with fake client: render from canned payloads, key → call
  mapping, confirm-gating on destructive ops.
- Config-key PATCH body shape test (key/value/readonly semantics).
- Raw send forms: one happy-path per form against `httptest` + real
  handlers (payload validation parity).
- Relative-time rendering helper unit tests.

## Files Touched

- **New:** `internal/tui/station/ocpp.go` (container), `status.go`,
  `queue.go`, `configkeys.go`, `send.go` (+ `_test.go` for each)
- **Edit:** `internal/client/client.go` (+ tests): OCPP group
- **Edit:** `internal/tui/form/field.go`: textarea field, select-with-
  optional
- **Edit:** `internal/tui/station/station.go`: activate OCPP tab

## Acceptance Criteria

- Status sub-tab reflects a CSMS drop within one event (≤1s) without
  refetching; reconnect via `k` round-trips (op tracking → lifecycle →
  connected state).
- Queue drain/clear/dead-letter all operable with correct confirmation;
  overflow event toasts and the counter increments.
- Config key edit round-trips: change → row updates (event) → reboot-
  required marker honored in UI.
- Each raw send form produces a request the handler accepts (verified by
  httptest happy-paths) and logs request/response to the send log.
- Tests green; build/vet/test clean; server mode untouched.

## Tasks

- [ ] Client: OCPP method group + types + tests
- [ ] Sub-tab container + routing
- [ ] Status sub-tab (health block, relative times, 10s refetch tick, k/h/A actions)
- [ ] Queue sub-tab (+ dead-letter viewport + overflow counter)
- [ ] Config Keys sub-tab (filter, edit form, event refresh)
- [ ] Send sub-tab (5 forms, textarea field, send log viewport)
- [ ] Manual pass against a CSMS (or unroutable URL for failure paths)
- [ ] Full verification suite
