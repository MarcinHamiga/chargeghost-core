## Goal

Add an OCPP-compliant enqueue mechanism for all transaction messages on both protocol versions, so transaction delivery survives disconnects and restarts, preserves order and occurrence timestamps, and reacts to `CALLERROR` responses according to the spec (retry what can succeed, stop retrying what cannot).

## Success Criteria

- Every transaction message (`v16`: StartTransaction, StopTransaction, MeterValues; `v201`: TransactionEvent Started/Updated/Ended, including meter-driven Updated) is created via one version-specific enqueue path. No engine callback, meter ticker, or drain path sends a transaction message around the queue.
- Offline-created transaction messages are persisted, replayed in chronological order after reconnect with original timestamps, and (v201) carry `offline=true` with gap-free per-transaction `seqNo`.
- A `CALLERROR` (or transport failure) never silently drops a transaction message and never retries forever: retryable errors back off per configured policy up to the configured attempt count, then dead-letter; deterministic rejections dead-letter immediately with operator-visible diagnostics.
- `go build ./...`, `go test ./...`, `go test -race` on touched packages, and the v201 integration suite all pass.

## Context And Current Facts

- Outbound delivery has two lanes: `internal/ocpp/command.go` (`CommandDispatcher`, in-memory 256-slot channel, drops on full, counts `failed` and moves on) and `internal/ocpp/queue/` (`MessageQueue`, optional JSON-file persistence, used only when `IsConnected()==false` at send time).
- Engine callbacks in `cmd/chargeghost/callbacks.go` enqueue dispatcher closures that call `bridge.Send*` directly. `internal/ocpp/meter_ticker.go` does the same for periodic MeterValues. Online send failures therefore hit the dispatcher `failed` counter and the message is lost; offline sends only reach the durable queue if the link happens to be down at that instant.
- `v16` senders (`internal/ocpp/v16/senders.go`) return transport/protocol errors synchronously; `drainQueue` (`internal/ocpp/v16/bridge.go`) stops the whole pass on the first failure and does not classify `CALLERROR` codes. Retry backoff is a flat interval, not the `interval x attempts` schedule of OCPP 1.6 section 3.7.1. Exhausted messages are only flagged in place, not dead-lettered.
- `v201` senders (`internal/ocpp/v201/senders.go`) use `SendRequestAsync` with fire-and-forget callbacks that only `slog` on error: online `CALLERROR`s and transport failures are swallowed with no retry. Queued events never get `offline=true` set (violates E11.FR.02/E12.FR.02), and `txBuilders`/`txIntToEVSE`/`nextTxInt` are memory-only, so a restart splits a transaction (new UUID, reset `seqNo`).
- `v201` retry uses a hardcoded 60s flat interval (`queue_drain.go`); the attempt count honors `RetryBackOffRepeatTimes` via `applyReplayPolicy`.
- ocpp-go v0.19.0 surfaces wire `CALLERROR`s as `*ocpp.Error` (`Code ocpp.ErrorCode`, `Description`, `MessageId`) through both sync `SendRequest` and async callbacks (`ocpp/ocpp.go`, `ocpp1.6/charge_point.go`, `ocppj/ocppj.go` constants).
- Prior art: `docs/plans/2026-07-09-remediation-wave2-durable-transaction-delivery.md` proposes a larger redesign (engine domain events + new outbox package + session-ID mapping). This plan is a narrower slice that reuses the existing `queue.MessageQueue` instead.

## Constraints And Non-goals

- Engine stays protocol-agnostic: no OCPP imports in `internal/engine`; callbacks remain the only coupling.
- Non-transactional messages (BootNotification, Heartbeat, StatusNotification, Authorize, DataTransfer, NotifyEvent, ReservationStatusUpdate, firmware/diagnostics) stay on the `CommandDispatcher` as today.
- No CSMS-side changes; no new persistence backend (reuse JSON-file queue).
- No change to authorization-cache semantics (`StopTransactionOnInvalidId`, `StopTxOnInvalidId` handling stays as-is).

## Key Decisions

1. **One enqueue choke point per version, dispatcher excluded from transaction delivery.** Add `internal/ocpp/v16/transaction_queue.go` and `internal/ocpp/v201/transaction_queue.go` exposing `EnqueueStart/Stop/Meter` (v16) and `EnqueueStarted/Updated/Ended` (v201). Callbacks, meter ticker, and drain paths call these; the `Send*` methods become delivery workers invoked by the queue, not by dispatcher closures. Rejected alternative: keep dispatcher closures and add retry inside `Execute` — closures are not durable, drop silently when the channel is full, and cannot order across restarts.
2. **Reuse `queue.MessageQueue`, do not build the Wave2 outbox in this slice.** The JSON-file backend already persists payloads across restarts and both bridges already drain on reconnect. Rejected alternative: full Wave2 outbox with engine domain events — correct long-term direction but a much larger migration; recorded as follow-up.
3. **Classify `CALLERROR` by code; retryable vs fatal is spec-derived, not configurable.**
   - Fatal (acknowledge + dead-letter immediately, log code/description, timeline + status update): `NotImplemented`, `NotSupported`, `FormationViolation`/`FormatViolation`, `PropertyConstraintViolation`, `OccurrenceConstraintViolation`, `TypeConstraintViolation`, `ProtocolError`, `SecurityError`, `MessageTypeNotSupported`, `RpcFrameworkError`. Rationale: the CSMS deterministically rejects these; retrying burns attempts and head-of-line-blocks every message behind them.
   - Retryable (requeue with backoff, bounded by policy): `InternalError`, `GenericError`, timeouts, transport/websocket errors, missing-response errors. Rationale: transient by definition.
   - Shared classifier in `internal/ocpp/callerror.go` operating on `*ocpp.Error` via `errors.As`, so both bridges behave identically; unknown error shapes default to retryable.
4. **Per-connector FIFO with start-before-rest gating (v16).** Queued `StopTransaction`/`MeterValues` for a session whose `StartTransaction` has no CSMS-assigned ID yet wait behind it; on `StartTransaction` confirmation the assigned integer is persisted to the session mapping before later records release. MeterValues keep their occurrence timestamps (already in payloads).
5. **v201 spec gaps closed in the same slice:** set `offline=true` on any event created while disconnected (E11.FR.02/E12.FR.02, including flipping still-queued messages on link loss per E11.FR.07); persist per-UUID next-`seqNo` so restarts continue the sequence; reuse the existing UUID across restart instead of minting a new builder.
6. **Backoff follows the configured policy:** v16 linear `TransactionMessageRetryInterval x preceding attempts` up to `TransactionMessageAttempts` (section 3.7.1); v201 attempts bounded by `RetryBackOffRepeatTimes` with the existing 60s base kept unless the device model exposes a better value. Exhaustion moves to dead-letter on both versions (extend v16 to use `DeadLetterQueue` like v201 already does).

## Recommended Approach

Route every transaction message through the durable queue first, deliver strictly in order via the existing drain loops, and feed every send result (success, `CALLERROR`, transport error) back into per-message retry state. Concretely: callbacks and the meter ticker stop building `OCPPCommand` closures for transaction work and instead append typed payloads to the version queue; the bridges' drain passes become the sole senders, classifying each result with the shared `CALLERROR` policy, updating `RetryCount`/`LastAttemptAt`/`LastError`, setting v201 `offline` flags, resolving the v16 CSMS transaction-ID mapping, and dead-lettering on fatal codes or exhaustion. `CommandDispatcher` keeps only point-in-time traffic; its drop/failed counters become the alert signal for non-transactional overflow, never the fate of a transaction.

## Work Plan

1. **Shared `CALLERROR` classifier + delivery result type** — new `internal/ocpp/callerror.go` (`Classify(err) (fatal bool, code string)`, `DeliveryResult` enum: ack/retry/dead-letter) with unit tests driving every OCPP-J code from both spec tables through `*ocpp.Error`. No behavior change yet.
2. **v16 enqueue choke point** — new `internal/ocpp/v16/transaction_queue.go`; rewire `newSessionStartedCallback`, `newSessionStoppedCallback`, meter ticker path, and `drainQueue` sends through it; `SendStart/Stop/MeterValues` become delivery-only. Tests: offline start→meters→stop persists three records with original timestamps; reconnect replays in order with the CSMS-assigned ID applied to Stop/MeterValues.
3. **v16 `CALLERROR`-aware drain** — classify per-message results in `drainQueue`; fatal codes dead-letter immediately, retryable follows linear backoff/attempts; later records of a session wait behind its unresolved Start. Tests with stub CSMS returning `NotSupported` (dead-letters, drain continues to next message) vs `InternalError`/timeout (retries, backs off, exhausts to dead-letter).
4. **v201 enqueue choke point + offline/seqNo durability** — new `internal/ocpp/v201/transaction_queue.go`; persist UUID→next-`seqNo` (extend `persist.go`); set `offline=true` at enqueue-while-disconnected and on link-loss for still-queued messages; rewire callbacks, meter path, and `sendQueuedTransactionEvent` through it; convert `SendRequestAsync` callbacks from log-only to classify-and-update. Tests: restart mid-transaction continues UUID/`seqNo`; offline events replay with `offline=true`; `Updated` storm while low-memory still drops intermediate-first per E11.FR.05 (document current drop policy if memory-bounding is out of scope).
5. **Observability + status surface** — timeline `LogError` with `CALLERROR` code/description on fatal and exhaustion; `StatusTracker.OnOutboundError` on retryable; queue depth/oldest-age/dead-letter counts visible via existing status endpoint. Tests assert timeline entries and counters.
6. **Conformance gates** — `go fmt ./...`, `go vet ./...`, `go test ./...`, `go test -race ./internal/ocpp/... ./cmd/chargeghost`, `go test -tags integration ./internal/ocpp/v201 -v -timeout 90s`. Add a stub-CSMS matrix test (both versions x fatal/retryable/success) as the regression net.

## Validation Plan

- Unit: `go test ./internal/ocpp -run 'CallError|Classify' -count=1 -v` — every wire code maps to retry/dead-letter per Decision 3.
- v16: `go test ./internal/ocpp/v16 -run 'Replay|CallError|Backoff|Exhaust' -count=1 -v` — order, original timestamps, CSMS-ID resolution, fatal-skips-rest, linear backoff timing, dead-letter.
- v201: `go test ./internal/ocpp/v201 -run 'Restart|Offline|Sequence|CallError' -count=1 -v` — UUID/`seqNo` continuity across reconstructed bridge, `offline=true` on replay, async `CALLERROR` classification.
- Wiring: `go test ./cmd/chargeghost -run 'Session|Transaction|Disconnected' -count=1 -v` — with link down, start/sample/stop produce durable records and zero dispatcher transaction closures.
- Full: `go test ./...` then `go test -tags integration ./internal/ocpp/v201 -v -timeout 90s`. Highest-risk step is the v201 async-callback rewire (phase 4): a missed error path silently drops transactions exactly as today, so the matrix test must cover success, fatal `CALLERROR`, retryable `CALLERROR`, transport error, and timeout per event type.

## Risks / Rollback

- **Duplicate delivery after reconnect** (messages the CSMS processed but never acked will resend): acceptable and spec-expected; v201 idempotency keys already correlate replays, v16 resends carry original timestamps so the CSMS can deduplicate. Call out in release notes.
- **Fatal-code misclassification blocks billing evidence**: mitigated by dead-letter retention (never silent drop) plus timeline/status visibility; classifier defaults unknown errors to retryable.
- **Head-of-line blocking behind a poisoned session**: mitigated by fatal-codes skipping immediately and per-session gating (only that session's later records wait, other connectors drain).
- **Rollback**: branch-local, no migration of persisted queue format in this slice (payload types unchanged, only send path and metadata handling change); revert is a clean branch delete. If queue schema must change during implementation, add version-tolerant decoders like the existing `queued*Payload` legacy paths rather than breaking old files.

## Open Questions

None. One assumption for approval: reusing `queue.MessageQueue` rather than building the Wave2 domain-event outbox now; Wave2 remains the recommended follow-up for engine-level durability (stable session IDs, meter streams, migration of legacy payloads).

## Sources

- https://raw.githubusercontent.com/sepehr-safari/ocpp-handbook/main/modules/07-the-transaction-lifecycle.md — OCPP 1.6 sections 3.5/3.7/3.7.1: transaction message set, offline queueing, chronological replay, linear backoff `TransactionMessageRetryInterval x attempts` up to `TransactionMessageAttempts`.
- https://raw.githubusercontent.com/sap/e-mobility-charging-stations-simulator/main/docs/ocpp2/OCPP-2.0.1_edition3_part2_specification.md — E04/E11/E12 (offline queue MUST, `offline=true` on replay, `seqNo` continuity, drop-intermediate-Updated-first) and J02 meter-value rules.
- https://raw.githubusercontent.com/sap/e-mobility-charging-stations-simulator/main/docs/ocpp16/ocpp-j-1.6-specification.md — OCPP-J 1.6 Table 7 `CALLERROR` codes.
- https://raw.githubusercontent.com/sap/e-mobility-charging-stations-simulator/main/docs/ocpp2/OCPP-2.0.1_edition3_part4_ocpp-j-specification.md — OCPP 2.0.1 Table 8 `CALLERROR` codes.
- Local: `github.com/lorenzodonini/ocpp-go@v0.19.0/ocpp/ocpp.go` (`Error` struct), `ocppj/ocppj.go` (wire code constants), `ocpp1.6/charge_point.go` (`SendRequest`/`SendRequestAsync` error surfacing) — inspected in module cache.
