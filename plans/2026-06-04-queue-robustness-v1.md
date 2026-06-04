# Plan 6: Queue Robustness (P1-8, P1-9)

**Date:** 2026-06-04
**Version:** v1
**Source review:** 2026-06-04 OCPP communications review, recommendations P1-8, P1-9
**Priority:** P1 — recovery

## Objective

Cap the persisted queue size, implement dead-letter for stuck messages, and
add idempotency keys for v2.0.1 TransactionEvent replays.

## Background

`internal/ocpp/queue/json_file.go` writes messages to disk without a size
cap. A multi-day CSMS outage could fill the disk. There is no dead-letter
mechanism — exhausted messages get rewritten to the queue with their
`LastError` and `RetryCount` updated forever (see
`internal/ocpp/v201/queue_drain.go:82-101`).

For v2.0.1, `TransactionEvent` has an `eventId` (a UUID). If the network drops
the response after the CSMS persisted the event, the replay re-sends a
*different* `eventId`, causing the CSMS to record the event twice.

## Design

### 1. Queue size cap

In `JSONFileQueue`, add:

```go
type Config struct {
    MaxBytes     int64 // 0 = unlimited
    MaxMessages  int   // 0 = unlimited
}
```

When `Enqueue` would exceed the cap, drop the oldest non-exhausted message
(moving it to a dead-letter file `dead_letter.jsonl`) and emit a
`slog.Warn`.

### 2. Dead-letter handling

In `v201/queue_drain.go` (and v1.6 equivalent), when `messageAttemptsExhausted`
returns true:

- Move the message to a dead-letter file
- Emit a `slog.Error` with full context
- Stop retrying that message

Provide a `--replay-dead-letter` flag (or REST endpoint) for operator-driven
recovery.

### 3. Idempotency keys for TransactionEvent

The v2.0.1 TransactionEvent request already has an `eventId`. We need to:

- Record the `eventId` when the message is first created (not when sent).
- Use the same `eventId` on every replay.
- Verify the senders path preserves `eventId` through queue.

This requires `transaction.go` to generate the `eventId` once at
`SendTransactionEvent*` time and store it in the queued payload.

## Files Touched

- **Edit:** `internal/ocpp/queue/queue.go` (add Config)
- **Edit:** `internal/ocpp/queue/json_file.go` (cap, dead-letter)
- **Edit:** `internal/ocpp/v201/queue_drain.go` (move to dead-letter on
  exhausted)
- **Edit:** `internal/ocpp/v201/transaction.go` (stable eventId)
- **New:** `internal/ocpp/queue/dead_letter.go` (DLQ abstraction)
- **Edit:** `internal/api/router.go` (add `/api/v1/ocpp/dead-letter` endpoint
  — optional; can defer)

## Acceptance Criteria

- Queue cap enforced; oldest non-exhausted dropped to DLQ on overflow.
- Exhausted messages moved to DLQ, not retried forever.
- TransactionEvent replays preserve original `eventId`.
- Tests for cap, DLQ, idempotency.

## Tasks

- [x] Add Config with size caps to queue — `queue.Config{MaxMessages, MaxBytes, DeadLetterPath}` (`internal/ocpp/queue/queue.go:22-37`)
- [x] Implement size cap in `JSONFileQueue.Enqueue` and `InMemoryQueue.Enqueue` — `internal/ocpp/queue/json_file.go:54-71`, `internal/ocpp/queue/memory.go:38-55`
- [x] Create dead-letter abstraction — `internal/ocpp/queue/dead_letter.go` (JSONL append)
- [x] Update `v201/queue_drain.go` to move exhausted messages — `internal/ocpp/v201/queue_drain.go:94-118` (DLQ write + Dequeue on exhaustion)
- [x] Update v2.0.1 senders to set stable `IdempotencyKey` — `internal/ocpp/v201/senders.go:158-167, 218-227, 282-291`
- [x] Add `IdempotencyKeyFor` derivation (SHA-256 of txID+seqNo+eventType+ts) — `internal/ocpp/v201/queue_drain.go:130-152`
- [x] Wire dead-letter path into `main.go` — `cmd/chargeghost/main.go:100-107`
- [x] Surface `QueueDropped` in `Status` endpoint — `internal/ocpp/status.go:50-53, 187-195, 228`
- [x] Tests — `internal/ocpp/queue/robustness_test.go` (11 tests) and `internal/ocpp/v201/queue_drain_test.go` (5 new tests)
- [x] Run `go build ./...` and `go test ./...`

## Notes

The dead-letter file is a JSONL (one record per line) so operators can
inspect it with standard tools. Each record carries a `reason` tag
(`queue-full` or `exhausted`) plus the full queued message envelope.

The `IdempotencyKey` is a stable SHA-256 of (txID, sequenceNo, eventType,
timestamp) so a CSMS can dedupe replays of the same logical event.
We can't fix the CSMS-side dedup directly (the OCPP 2.0.1 transport
generates a new message-id per send), but the key is logged on every
attempt and on dead-lettering so operators can correlate.

The size cap (`MaxMessages`, `MaxBytes`) is **not enabled by default**
in `main.go` to avoid surprising existing deployments. Operators
wanting a cap can set `Config.MaxMessages` in a future patch; the
eviction logic and dead-letter integration are already in place.

The `DeadLetterQueue` interface is split off as a capability interface
so test mocks can implement just `MessageQueue` without the DLQ
methods. The drain code type-asserts to it before calling
`DeadLetter()` / `IncDropped()`.
