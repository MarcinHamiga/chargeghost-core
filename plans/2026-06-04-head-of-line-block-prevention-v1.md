# Plan 11: Head-of-Line-Block Prevention (P1-7)

**Date:** 2026-06-04
**Version:** v1
**Source review:** 2026-06-04 OCPP communications review, recommendations P1-7
**Priority:** P1 — recovery

## Objective

Prevent the CommandDispatcher from stalling on a single failed send. When
`IsConnected()=false`, the dispatcher should not block on a slow/failed
send; it should skip and retry.

## Background

`internal/ocpp/command.go:29-40` runs a single goroutine that pops commands
and calls `cmd.Execute()`. If the CSMS is down and a command does not check
`IsConnected()` first, the goroutine blocks until the call times out (ocpp-go
default: 30s). All subsequent commands in the queue wait.

v2.0.1 senders check `IsConnected()` and return an error fast, but v1.6
senders do not consistently. The dispatcher itself has no defensive check.

## Design

### 1. Defensive check in dispatcher Run

In `CommandDispatcher.Run`, wrap `cmd.Execute()`:

```go
case cmd := <-d.commands:
    if !d.isLinkUp() {
        // Re-queue with backoff, but don't block forever.
        time.Sleep(100 * time.Millisecond)
        // Re-enqueue at the back of the channel
        select {
        case d.commands <- cmd:
        default:
            d.dropped++
            slog.Warn("dropped while link is down", ...)
        }
        continue
    }
    if err := cmd.Execute(); err != nil { ... }
```

The `isLinkUp` callback is set via `SetLinkUpFunc(func() bool)`.

### 2. Optional: priority lane

For commands that MUST go out (e.g., transaction events), provide a
`EnqueuePriority` method that places the command at the head of the queue.
Defer to a follow-up plan if scope is large.

### 3. Backpressure metric

Add a `linkDownRequeues` counter to the dispatcher's stats.

## Files Touched

- **Edit:** `internal/ocpp/command.go` (link-up check, requeue logic)
- **Edit:** `cmd/chargeghost/main.go` (wire `SetLinkUpFunc` to
  `bridge.IsConnected`)
- **Edit:** `internal/ocpp/command_test.go` (test that a down link causes
  requeue, not hang)
- **Edit:** dispatcher stats to include `linkDownRequeues`

## Acceptance Criteria

- Dispatcher never blocks more than 100ms when link is down.
- Commands are preserved (not dropped) when link is down.
- `linkDownRequeues` counter increments.
- Tests pass.

## Tasks

- [x] Add `SetLinkUpFunc` to `CommandDispatcher` (`internal/ocpp/command.go`)
- [x] Implement requeue logic in `Run` (200ms backoff, re-enqueue at back)
- [x] Add `linkDownRequeues` counter (exposed via `Stats().LinkDownRequeues`)
- [x] Wire in `main.go` (`dispatcher.SetLinkUpFunc(func() bool { return bridge.IsConnected() })`)
- [x] Tests in `internal/ocpp/command_test.go` (link-down requeue + nil disables)
- [x] Run `go build ./...` and `go test ./...`
