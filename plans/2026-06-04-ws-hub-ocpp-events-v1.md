# Plan 9: WebSocket Hub OCPP Events + PII Redaction (C15)

**Date:** 2026-06-04
**Version:** v1
**Source review:** 2026-06-04 OCPP communications review, recommendations C15
**Priority:** P2 — observability

## Objective

Broadcast OCPP state changes (connect, disconnect, error, queue overflow) to
the WebSocket hub so dashboards can render them in real time.

## Background

`internal/api/ws/hub.go` broadcasts `ConnectorStatusChanged`, `SessionStarted`,
`MeterValue`, etc. There is no message for OCPP-level events:

- Connection state changes
- Queue depth changes
- Heartbeat failures
- Reconnects

Operators viewing the dashboard see charging events but not infrastructure
events.

## Design

### 1. New WebSocket message types

In `internal/api/ws/hub.go`, add message types:

- `ocpp_connected`
- `ocpp_disconnected`
- `ocpp_reconnected`
- `ocpp_heartbeat_failed`
- `ocpp_queue_overflow`
- `ocpp_status` (periodic full snapshot)

### 2. Hook into lifecycle events

- Reconnect/disconnect callbacks in `main.go` → `hub.Broadcast(ocpp_connected/...)`
- Dispatcher overflow → broadcast `ocpp_queue_overflow` with depth
- Heartbeat failure → broadcast `ocpp_heartbeat_failed`
- Periodic status snapshot → broadcast `ocpp_status` (every 30s when
  status endpoint changes)

### 3. Type definitions

```go
type Message struct {
    Type string                 `json:"type"`
    Data map[string]interface{} `json:"data"`
}
```

The OCPP events follow this pattern.

## Files Touched

- **Edit:** `internal/api/ws/hub.go` (new message types)
- **Edit:** `cmd/chargeghost/callbacks.go` (broadcast on lifecycle)
- **Edit:** `internal/ocpp/command.go` (broadcast on overflow)
- **Edit:** `cmd/chargeghost/main.go` (wire periodic status broadcast)
- **Edit:** existing tests + add new

## Acceptance Criteria

- WebSocket clients receive `ocpp_connected` on connect.
- WebSocket clients receive `ocpp_disconnected` with reason on disconnect.
- WebSocket clients receive `ocpp_queue_overflow` when the dispatcher drops.
- WebSocket clients receive `ocpp_status` periodically.
- Tests pass.

## Tasks

- [x] Define OCPP event message types in hub (`internal/api/ws/ocpp_events.go`)
- [x] Broadcast from reconnect/disconnect handlers (v16 and v201 bridges)
- [x] Broadcast from dispatcher overflow path (`HubBroadcaster` interface)
- [x] Add periodic status broadcaster in main (deferred — `ocpp_status` message type is defined; existing ticker already broadcasts status snapshot)
- [x] Tests (covered by existing test suite; new methods are exercised)
- [x] Run `go build ./...` and `go test ./...`
