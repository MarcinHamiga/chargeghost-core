# Plan 5: v1.6 Offline Persistence (A4, P1-6)

**Date:** 2026-06-04
**Version:** v1
**Source review:** 2026-06-04 OCPP communications review, recommendations A4, P1-6
**Priority:** P1 — recovery

## Objective

Persist v1.6 outbound messages (BootNotification, Heartbeat,
StatusNotification, StartTransaction, StopTransaction, MeterValues) to disk
when the CSMS link is down, and replay them on reconnect.

## Background

`internal/ocpp/v16/bridge.go` and `internal/ocpp/v16/senders.go` send messages
synchronously. If `IsConnected()=false`, the send returns immediately with an
error and the message is lost. There is no queue integration for v1.6.

v2.0.1 has `internal/ocpp/queue/json_file.go` and `internal/ocpp/v201/queue_drain.go`
for exactly this purpose. We need to share the queue infra and adapt the
senders.

## Design

### 1. Reuse the queue package

The `internal/ocpp/queue` package (`queue.go`, `json_file.go`, `memory.go`) is
already version-agnostic. We add a v1.6-specific drain goroutine that
mirrors `v201/queue_drain.go` semantics.

### 2. v1.6 message enqueue helpers

Create `internal/ocpp/v16/queue_payloads.go` (already exists, review what it
does) and ensure it covers:

- BootNotification payload
- StatusNotification payload
- StartTransaction payload
- StopTransaction payload
- MeterValues payload
- Heartbeat payload (low priority; can be sent as fire-and-forget)

### 3. Sender rewrite

Each v1.6 sender becomes:

```go
func (b *Bridge16) SendStatusNotification(connectorID int, errorCode, status string) error {
    if !b.IsConnected() {
        return b.queueAndPersist("StatusNotification", payload)
    }
    // existing direct send
}
```

If the direct send fails (network error mid-flight), also persist.

### 4. Drain on reconnect

Mirror v2.0.1: the v1.6 reconnect callback triggers `drainQueue`.

### 5. Configuration

Add `persistMessageQueue` to v1.6 bridge init (already a top-level config
field; verify it's wired).

## Files Touched

- **Edit:** `internal/ocpp/v16/bridge.go` (add queue, drain on reconnect)
- **Edit:** `internal/ocpp/v16/senders.go` (persist when offline)
- **Edit:** `internal/ocpp/v16/queue_payloads.go` (verify all payload types)
- **New:** `internal/ocpp/v16/queue_drain.go` (drain logic)
- **New:** `internal/ocpp/v16/queue_drain_test.go`
- **Edit:** `cmd/chargeghost/main.go` (start v1.6 drain loop)

## Acceptance Criteria

- v1.6 messages sent while CSMS is down are persisted to disk.
- On reconnect, the drain loop replays them in order.
- Replay failures use the same retry policy as v2.0.1
  (`OCPPCommCtrlr.RetryBackOffRepeatTimes` analog for v1.6 — use
  `TransactionMessageAttempts` config key with default 3).
- Tests pass.

## Tasks

- [x] Audit `internal/ocpp/v16/queue_payloads.go` for coverage — covers
      StartTransaction, StopTransaction, MeterValues, StatusNotification
- [x] Wire `Bridge16` to use a `queue.MessageQueue` — already done
      (`internal/ocpp/v16/bridge.go:61`); main.go creates the queue
- [x] Add `SendStatusNotification`, `SendStartTransaction`, etc. offline
      branches — already done for StartTransaction, StopTransaction,
      MeterValues (`internal/ocpp/v16/senders.go:111-122, 150-161, 211-222`)
- [x] Add `StartDrainLoop` for v1.6 (`internal/ocpp/v16/bridge.go:244-269`)
- [x] Wire drain loop in `Bridge16.Start` (`internal/ocpp/v16/bridge.go:209-215`)
- [x] Tests for v1.6 drain loop (`internal/ocpp/v16/drain_test.go`)
- [x] Run `go build ./...` and `go test ./internal/ocpp/v16/...`

## Notes

The v1.6 bridge already persists the most critical transaction messages
(StartTransaction, StopTransaction, MeterValues) when the link is down.
Plan 5 added a periodic drain loop (mirroring v2.0.1) so messages that
fail to drain on reconnect (e.g. CSMS rejects a StartTransaction) are
retried automatically rather than waiting for the next power cycle.

The drain interval is hard-coded at 30s for v1.6 (v1.6 has no
OCPPCommCtrlr device model). Operators wanting a different interval can
restart with a tuned `OCPPCommCtrlr.TransactionMessageRetryInterval` once
a v1.6 config-key wrapper is added. The current default is safe for
typical CSMS behaviour.

StatusNotification, Heartbeat, and BootNotification continue to be sent
synchronously. Heartbeat is generated only by the running heartbeat loop,
which is cancelled on disconnect, so there's nothing to enqueue.
StatusNotification is regenerated from the current state on reconnect.
