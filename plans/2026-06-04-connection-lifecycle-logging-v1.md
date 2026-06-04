# Plan 3: Connection Lifecycle Logging (P0-4)

**Date:** 2026-06-04
**Version:** v1
**Source review:** 2026-06-04 OCPP communications review, recommendations P0-4, A8, A9
**Priority:** P0 — operational critical

## Objective

Capture the WebSocket close code/reason on disconnect and surface ping/pong
statistics. Update the disconnect log to be diagnostic-grade, not just
"OCPP disconnected".

## Background

When the CSMS closes the WebSocket, ocpp-go's reconnect callback fires with no
close code or reason. We log `"OCPP disconnected"` with no context. Operators
cannot distinguish:

- CSMS graceful shutdown (1000)
- Server endpoint gone (1001)
- Protocol error (1002)
- Auth challenge (ocpp custom 4001+)
- Network idle timeout (CSMS-side)

The `WebSocketPingInterval=30` in `device_model.go:148` sends pings but we log
no RTT, no missed pongs, and no `lastPingAt`.

## Design

### 1. Reconnect/Disconnect callback signatures

The ocpp-go library's `SetDisconnectedHandler` takes no error in v0.19.0. We
need an alternative — read the close frame before the connection is fully
torn down. Strategy:

- Hook into the WebSocket client via a `OnDisconnected(reason string)` callback
  we wrap around `ocpp-go`'s `SetDisconnectedHandler`.
- The wrapper polls `ocpp-go`'s client state or uses a separate
  `gorilla/websocket` interceptor.

Simpler approach: in the v2.0.1 and v1.6 bridges, register an additional
`OnDisconnected` callback that records the timestamp and a placeholder reason.
Augment with periodic ping RTT logs from the heartbeat goroutine.

### 2. Heartbeat goroutine reports RTT

In `v201/senders.go:SendHeartbeat` and v1.6 equivalent, wrap the call in:

```go
start := time.Now()
_, err := b.cs.Heartbeat()
rtt := time.Since(start)
b.statusTracker.RecordHeartbeat(rtt, err)
```

The tracker exposes:
- `LastHeartbeatAt`
- `LastHeartbeatRTTMs`
- `HeartbeatFailures uint64`
- `HeartbeatSuccesses uint64`

### 3. Periodic health log

A 60s ticker in the runtime package (or in the bridge Start) emits a
`slog.Info` with the current `Status` snapshot. Configurable via
`-ocpp-health-log-interval` flag (default 60s, 0 to disable).

## Files Touched

- **Edit:** `internal/ocpp/v201/senders.go` (RTT around Heartbeat)
- **Edit:** `internal/ocpp/v16/senders.go` (RTT around Heartbeat)
- **Edit:** `internal/ocpp/status.go` (add heartbeat fields from Plan 1)
- **Edit:** `cmd/chargeghost/main.go` (health log ticker)
- **Edit:** `cmd/chargeghost/callbacks.go` (disconnect reason capture)

## Acceptance Criteria

- Disconnect log includes reason when available.
- Heartbeat RTT appears in `Status.LastHeartbeatRTTMs`.
- Heartbeat failures increment `HeartbeatFailures` counter.
- Periodic health log emits every N seconds with full status snapshot.

## Tasks

- [x] Add heartbeat RTT tracking in v2.0.1 and v1.6 senders (already done in Plan 1)
- [x] Extend `Status` struct with heartbeat fields (already done in Plan 1)
- [x] Add periodic health log ticker in `main.go` — `internal/ocpp/health_ticker.go`
- [x] Capture WebSocket close code & reason — `internal/ocpp/close_info.go`
  + `isGracefulClose` helper in v16/v201
- [x] Tests for RTT recording (in Plan 1 status_test.go)
- [x] Tests for close-code formatter — `internal/ocpp/close_info_test.go`
- [x] Tests for health ticker — `internal/ocpp/health_ticker_test.go`
- [x] Run `go build ./...` and `go test ./...`

## Implementation Notes

The ocpp-go v0.19.0 library's `SetDisconnectedHandler` **does** receive an
`error` — `ws/websocket.go:761` defines it as
`SetDisconnectedHandler(handler func(err error))` and the test suite at
`websocket_test.go:331-336` confirms the err is a `*websocket.CloseError`
when the peer initiated the close. We can type-assert to extract the code
and reason text directly, no interceptor needed.

- `internal/ocpp/close_info.go:13` — `FormatDisconnectReason` is the shared
  helper used by both bridges.
- `internal/ocpp/v16/bridge.go:75-87` and
  `internal/ocpp/v201/bridge.go:90-102` — disconnect handler now logs the
  reason and demotes graceful closures (1000, 1001) to `Info` so operators
  aren't alerted on benign shutdowns.
- `internal/ocpp/health_ticker.go:13` — `StartHealthTicker` emits a
  structured `slog.LogAttrs` line every N seconds (default 60s) with
  connected/reconnectCount/uptime/lastMessageAt/lastError/heartbeat stats
  and v2.0.1 queue depth. Log level is WARN when disconnected.
- `cmd/chargeghost/main.go:282-289` — wires the ticker.
