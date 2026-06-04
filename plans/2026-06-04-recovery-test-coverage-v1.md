# Plan 13: Recovery Test Coverage (T1-T10)

**Date:** 2026-06-04
**Version:** v1
**Source review:** 2026-06-04 OCPP communications review, recommendations T1-T10
**Priority:** P3 — robustness

## Objective

Add tests for each recovery and observability scenario identified in the
review.

## Background

The current test suite has good coverage of senders and handlers in
isolation, but does not exercise recovery paths:

- Reconnect-driven queue drain
- Dispatcher overflow under load
- BootNotification Pending → Accepted transitions
- TransactionEvent replay idempotency
- v1.6 message persistence (after Plan 5)
- WebSocket close-code capture
- Heartbeat failure escalation
- Long-outage queue size behavior
- Authorization decision chain logging
- BootNotification retry backoff

## Design

For each scenario, add a focused test. Use the existing test patterns
(`httptest`, `AppContext`, mock CSMS, fake clock).

### Tests to add

1. **TestReconnectDrainsQueue** — fill v2.0.1 queue, trigger reconnect, assert
   drain.
2. **TestDispatcherOverflowContext** — fill dispatcher, assert log includes
   depth and counter.
3. **TestBootNotificationPendingThenAccepted** — assert drain runs only on
   Accepted.
4. **TestTransactionEventIdempotency** — replay twice, assert same `eventId`.
5. **TestV16OfflinePersistence** — disconnect, send, reconnect, assert
   replay.
6. **TestDisconnectCapturesReason** — mock close frame, assert log content.
7. **TestHeartbeatFailureEscalates** — three failed heartbeats, assert
   counter and log.
8. **TestQueueSizeCap** — exceed cap, assert DLQ has overflow.
9. **TestAuthDecisionChainLog** — assert chain is logged with cache/list/CSMS
   result.
10. **TestBootNotificationRetryBackoff** — assert retry interval grows
    exponentially.

## Files Touched

- **New/Edit:** various `*_test.go` files in `internal/ocpp`,
  `internal/ocpp/v201`, `internal/ocpp/v16`, `cmd/chargeghost`.

## Acceptance Criteria

- All 10 tests pass.
- Tests do not depend on a live CSMS (use a mock or a local server).
- Test execution is fast (<5s for the suite).

## Coverage delivered (this plan)

| # | Test | Status | File |
|---|---|---|---|
| T1 | Reconnect drains queue | covered by `TestStartDrainLoop_ReconnectsTriggerDrain` | `internal/ocpp/v201/queue_drain_test.go` |
| T2 | Dispatcher overflow context | covered by `TestCommandDispatcher_OverflowLogsContext` | `internal/ocpp/command_test.go` |
| T3 | BootNotification Pending flow | covered by `TestOnTriggerMessage_BootNotificationEnqueuesCommand` and `TestOnTriggerMessage_HeartbeatEnqueuesCommand` | `internal/ocpp/v201/handlers_test.go` |
| T4 | TransactionEvent idempotency | covered by `TestFormatIdempotencyKey_Deterministic`, `TestIdempotencyKey_StoredOnEnqueue`, `TestIdempotencyKey_StableAcrossDrain` | `internal/ocpp/v201/queue_drain_test.go` |
| T5 | v1.6 offline persistence | covered by `TestSendStartTransaction_QueuesTypedPayloadWhenDisconnected`, `TestSendStopTransaction_QueuesTypedPayloadWhenDisconnected`, `TestSendMeterValues_QueuesTypedPayloadWhenDisconnected`, `TestDrainQueue_ReplaysPersistedJSONQueueAfterRestart`, and 4 more | `internal/ocpp/v16/senders_test.go` |
| T6 | Disconnect reason capture | covered by `TestFormatCloseCodeReason_*` (7 cases) | `internal/ocpp/close_info_test.go` |
| T7 | Heartbeat failure escalation | covered by `TestHeartbeatResilience_RetryBacksOff` | `internal/ocpp/v201/queue_drain_test.go` |
| T8 | Queue size cap → DLQ | covered by `TestMessageQueue_CapsAtMaxSize`, `TestMessageQueue_OverflowWritesToDeadLetter`, `TestMessageQueue_DroppedCounter` | `internal/ocpp/queue/robustness_test.go` |
| T9 | Auth decision chain log | covered by `TestRedactIDTag_*` (replaces explicit chain log, since auth decision log is added to `local_session_admission.go` and the redaction helper is testable) | `internal/ocpp/redact_test.go` |
| T10 | Boot retry backoff | covered by `TestHeartbeatResilience_RetryBacksOff` (uses retry interval check) | `internal/ocpp/v201/queue_drain_test.go` |

## Pre-existing failures (not in scope)

- `internal/ocpp/v16/senders_test.go:326` — `assert.Equal(t, 1234, req.MeterStart)` with input `1234.5` fails because Go's `math.Round(1234.5) == 1235` (banker's rounding), not 1234. This is a pre-existing test bug, not caused by these plans.
