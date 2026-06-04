# Plan 1: OCPP Status Endpoint (P0-1)

**Date:** 2026-06-04
**Version:** v1
**Source review:** 2026-06-04 OCPP communications review, recommendation P0-1
**Priority:** P0 — operational critical

## Objective

Expose OCPP link health and queue state through a new REST endpoint
(`/api/v1/ocpp/status`) so operators can answer "is OCPP healthy?" without
scraping logs. The endpoint reports connection state, message timing, queue
depth, recent errors, reconnect count, and uptime for both v1.6 and v2.0.1.

## Background / Rationale

The current `/api/v1/*` surface has connectors, sessions, transactions, and
metering, but no operational view of the OCPP layer. When the CSMS link is
silently degraded, the only signal is the absence of meter values appearing on
the CSMS — operators find out from the CSMS, not from the charger. This plan
adds the missing observability surface.

## Design

### 1. New types in `internal/ocpp/status.go` (new file)

```go
type Status struct {
    Version          string    `json:"version"`           // "1.6" or "2.0.1"
    Connected        bool      `json:"connected"`
    ConnectedAt      time.Time `json:"connectedAt,omitempty"`
    DisconnectedAt   time.Time `json:"disconnectedAt,omitempty"`
    LastMessageAt    time.Time `json:"lastMessageAt,omitempty"`
    LastError        string    `json:"lastError,omitempty"`
    LastErrorAt      time.Time `json:"lastErrorAt,omitempty"`
    ReconnectCount   int       `json:"reconnectCount"`
    UpSince          time.Time `json:"upSince"`            // process start
    CSMSURL          string    `json:"csmsUrl"`
    OCPPID           string    `json:"ocppId"`

    // v2.0.1 only
    QueueDepth       int       `json:"queueDepth,omitempty"`
    QueueExhausted   int       `json:"queueExhausted,omitempty"`
    DrainInProgress  bool      `json:"drainInProgress,omitempty"`

    // v1.6 only
    LastHeartbeatAt  time.Time `json:"lastHeartbeatAt,omitempty"`
}
```

### 2. Track state changes

Add a thread-safe `StatusTracker` owned by each bridge, updated by:

- **Reconnect callback** in `cmd/chargeghost/main.go` → set `Connected=true`,
  `ConnectedAt=now`, increment `ReconnectCount`.
- **Disconnect callback** in `cmd/chargeghost/main.go` → set `Connected=false`,
  `DisconnectedAt=now`.
- **Outbound success** (in `senders.go` wrappers) → set `LastMessageAt=now`.
- **Outbound error** (in `senders.go` wrappers and `command.go` Run loop) →
  set `LastError`, `LastErrorAt`.

### 3. Bridge interface extension

Add to `OCPPBridge` in `internal/ocpp/bridge.go`:

```go
Status() Status
```

Implement in both `v16.Bridge16` and `v201.Bridge201`.

### 4. REST handler

Add `internal/api/handlers/ocpp_status.go` with handler returning JSON of
`Status`. Register route `GET /api/v1/ocpp/status` in
`internal/api/router.go`.

### 5. Tests

- Unit test for `StatusTracker` transitions.
- HTTP test for the handler (using the project's `httptest` + `AppContext`
  pattern from `internal/api/handlers_test.go`).
- Test that reconnection increments the counter.

## Files Touched

- **New:** `internal/ocpp/status.go`
- **New:** `internal/ocpp/status_test.go`
- **New:** `internal/api/handlers/ocpp_status.go`
- **New:** `internal/api/handlers/ocpp_status_test.go`
- **Edit:** `internal/ocpp/bridge.go` (interface)
- **Edit:** `internal/ocpp/v16/bridge.go` (StatusTracker field, implement Status())
- **Edit:** `internal/ocpp/v16/senders.go` (update tracker on success/error)
- **Edit:** `internal/ocpp/v201/bridge.go` (StatusTracker field, implement Status())
- **Edit:** `internal/ocpp/v201/senders.go` (update tracker on success/error)
- **Edit:** `internal/ocpp/command.go` (update tracker on Run error)
- **Edit:** `cmd/chargeghost/main.go` (wire reconnect/disconnect to tracker)
- **Edit:** `internal/api/router.go` (register route)

## Acceptance Criteria

- `GET /api/v1/ocpp/status` returns 200 with a populated `Status` JSON.
- `Connected` flips on reconnect/disconnect.
- `ReconnectCount` increments on each reconnect.
- `LastMessageAt` updates on any successful send.
- `LastError`/`LastErrorAt` updates on any send failure (with truncated message).
- Tests pass; build is clean (`go build ./...`, `go vet ./...`).

## Tasks

- [ ] Define `Status` struct and `StatusTracker` in `internal/ocpp/status.go`
- [ ] Add unit tests for `StatusTracker` in `internal/ocpp/status_test.go`
- [ ] Add `Status() Status` to `OCPPBridge` interface
- [ ] Implement on `Bridge16` with field updates from callbacks/senders
- [ ] Implement on `Bridge201` with field updates from callbacks/senders
- [ ] Update `CommandDispatcher` to surface errors to the tracker
- [ ] Wire reconnect/disconnect handlers in `cmd/chargeghost/main.go`
- [ ] Create `internal/api/handlers/ocpp_status.go`
- [ ] Register route in `internal/api/router.go`
- [ ] Write HTTP handler test
- [ ] Run `go build ./...` and `go vet ./...` and `go test ./...`
- [ ] Update `REST_API.md` with the new endpoint
