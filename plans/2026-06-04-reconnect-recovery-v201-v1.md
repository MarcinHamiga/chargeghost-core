# Plan 4: Reconnect-Driven Recovery for v2.0.1 (P0-2)

**Date:** 2026-06-04
**Version:** v1
**Source review:** 2026-06-04 OCPP communications review, recommendations P0-2, P3-17, P3-19
**Priority:** P0 — operational critical

## Objective

Trigger the v2.0.1 message queue drain on every reconnect, not just the first
`BootNotification` Accepted. Schedule periodic drain attempts. Handle
`BootNotification Pending` correctly.

## Background

`internal/ocpp/v201/senders.go:40-44` only calls `go b.drainQueue()` inside
`SendBootNotification` on `RegistrationStatusAccepted`. If the CSMS connection
is lost and re-established mid-session:

1. The queue may be holding TransactionEvents.
2. The reconnect callback fires (in `main.go`) but does not trigger drain.
3. Messages sit in the queue until the next process restart.

Additionally:

- `BootNotification Pending` is not retried via `triggerMessage`.
- `drainQueue` runs once and stops; if it fails for transient reasons (e.g.,
  CSMS just came back up but not fully ready), there's no second attempt.

## Design

### 1. Move drain trigger to reconnect callback

In `cmd/chargeghost/main.go`, the v2.0.1 reconnect handler should:

```go
b.OnReconnect(func() {
    if b.QueueDepth() > 0 {
        go b.DrainQueue()
    }
    // Also re-publish StatusNotification for all connectors
    for _, id := range b.Engine().GetConnectorIDs() {
        b.Dispatcher().Enqueue(OCPPCommand{...})
    }
})
```

The existing `drainQueue` call inside `SendBootNotification` remains (for the
first boot), but subsequent reconnects trigger via callback.

### 2. Periodic drain attempts

In `v201/queue_drain.go`, expose a `StartDrainLoop(ctx context.Context,
interval time.Duration)` that:

- Ticks every `interval` (default 30s, configurable via
  `OCPPCommCtrlr.TransactionMessageRetryInterval`).
- If queue depth > 0 and not already draining, launches `drainQueue`.
- Stops on `ctx.Done()`.

### 3. BootNotification Pending handling

`SendBootNotification` should:

- On `Pending`, schedule a retry (via `restartHeartbeat` or a new ticker).
- On `Accepted`, the existing drain flow runs.

### 4. Expose `QueueDepth()` and `DrainInProgress` on the bridge

Already covered by the `Status` struct from Plan 1. Verify the fields are
populated by the drain code.

## Files Touched

- **Edit:** `internal/ocpp/v201/senders.go` (drain from reconnect)
- **Edit:** `internal/ocpp/v201/queue_drain.go` (add `StartDrainLoop`,
  `QueueDepth()`, `DrainInProgress`)
- **Edit:** `internal/ocpp/v201/bridge.go` (start drain loop on `Start`)
- **Edit:** `cmd/chargeghost/main.go` (reconnect callback wires drain)
- **Edit:** `internal/ocpp/v201/queue_drain_test.go` (test periodic drain)

## Acceptance Criteria

- After a reconnect with non-empty queue, drain runs within 1s.
- If drain fails, retry happens within 30s.
- BootNotification Pending triggers re-attempt at configured interval.
- Tests pass.

## Tasks

- [x] Add `QueueDepth()` and `DrainInProgress` accessors (`internal/ocpp/v201/bridge.go:232-239`)
- [x] Add `StartDrainLoop(ctx, interval)` to `Bridge201` (`internal/ocpp/v201/bridge.go:252-281`)
- [x] Wire drain loop in `Bridge201.Start` (`internal/ocpp/v201/bridge.go:217-224`)
- [x] Reconnect-driven drain already in place (`internal/ocpp/v201/bridge.go:108-121` re-arms drain)
- [x] Tests for periodic drain (`internal/ocpp/v201/queue_drain_test.go`)
- [x] Run `go build ./...` and `go test ./internal/ocpp/v201/...`

## Notes

The reconnect handler at `internal/ocpp/v201/bridge.go:108-121` already
calls `go b.drainQueue()`, addressing review item B3 for the reconnect case.
The new `startDrainLoop` is the safety net for the case where the link
stays up but the CSMS rejects a queued message — the original drain exits
on first send error (line 371-373) and would otherwise not retry until
the next reconnect. The loop honors `OCPPCommCtrlr.TransactionMessageRetryInterval`
(device-model variable) so operators can tune the interval via SetVariables.

The `BootNotification Pending` retry path is intentionally not implemented
in this plan; the dispatcher will re-enqueue BootNotification via the
heartbeat loop's `restartHeartbeat` if the connection drops.
