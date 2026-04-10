# OCPP Remediation Wave 1: Outbound Reliability and Queue Durability

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers` or `feature-dev`. Implement this wave before any later remediation work.

**Goal:** Make outbound OCPP traffic reliably reach the sender layer even while disconnected, and make the offline queue durable and replayable, especially for OCPP 2.0.1 `TransactionEvent` traffic.

**Why this wave matters:** The project currently claims an offline OCPP queue with JSON persistence, but normal engine events are often dropped before they reach the queue. In OCPP 2.0.1, persisted transaction events also cannot be replayed after restart because the payload type is lost when marshaled to JSON.

---

## Problems To Fix

### Problem 1: Main callback layer drops queueable outbound traffic

**Current behavior:**

- `OnSessionStarted` returns immediately when disconnected.
- `OnSessionStopped` returns immediately when disconnected.
- The periodic meter ticker skips work while disconnected.
- Queue fallback exists in the senders, but those code paths are unreachable during ordinary disconnected operation.

**Why this is broken:** The system advertises durable outbound queueing, but its main event path prevents queueable messages from ever being offered to the bridge.

### Problem 2: OCPP 2.0.1 JSON-persisted transaction events are not replayable

**Current behavior:**

- `SendTransactionStart`, `SendTransactionStop`, and `SendMeterValues` enqueue raw `*transactions.TransactionEventRequest` objects.
- JSON persistence round-trips these as generic decoded payloads.
- `drainQueue` requires the original typed request pointer.
- On replay, the type assertion fails and the queued message is dequeued anyway.

**Why this is broken:** This defeats the main value of durable queue persistence for OCPP 2.0.1.

### Problem 3: Queue semantics are inconsistent across message types

**Current behavior:**

- Session start/stop and meter data were intended to be queue-backed.
- Status notifications are still dropped during disconnect from the main callback layer.
- There is no explicit capability distinction between queue-backed and non-queue-backed outbound messages.

**Why this is risky:** The system's behavior is neither fully durable nor clearly scoped.

---

## Scope

### In scope

- Remove disconnected short-circuiting from queueable engine event callbacks.
- Remove disconnected short-circuiting from the periodic meter ticker.
- Introduce a serializable OCPP 2.0.1 queue payload envelope.
- Teach replay logic to rebuild real OCPP 2.0.1 transaction requests from stored envelopes.
- Decide and document queue behavior for status notifications.
- Add tests for disconnected queueing and restart replay.

### Out of scope

- Raw REST transaction route fixes.
- Reset behavior.
- Diagnostics/log status behavior.
- Monitoring and reporting features.

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `cmd/chargeghost/main.go` | Modify | Stop dropping queueable events before sender layer |
| `internal/ocpp/meter_ticker.go` | Modify | Allow ticker traffic to reach sender queue fallback |
| `internal/ocpp/queue/queue.go` | Modify | Support typed envelope conventions if needed |
| `internal/ocpp/queue/json_file.go` | Modify | Preserve queue payload compatibility expectations |
| `internal/ocpp/v16/senders.go` | Review/Modify | Confirm 1.6 queue semantics remain correct |
| `internal/ocpp/v201/senders.go` | Modify | Enqueue serializable transaction envelopes |
| `internal/ocpp/v201/bridge.go` | Modify | Rebuild queued transaction requests during drain |
| `internal/ocpp/v201/transaction.go` | Modify | Add request reconstruction helpers if needed |
| `internal/ocpp/v201/*_test.go` | Modify/Create | Replay and persistence tests |
| `internal/ocpp/*_test.go` | Modify/Create | Shared queueing behavior tests |

---

## Design

### Task 1: Make queueable engine callbacks always reach the bridge

- [ ] Remove `if !bridge.IsConnected() { return }` from `OnSessionStarted` in `cmd/chargeghost/main.go`.
- [ ] Remove `if !bridge.IsConnected() { return }` from `OnSessionStopped` in `cmd/chargeghost/main.go`.
- [ ] Keep dispatching the command so the bridge sender decides whether to send immediately or enqueue.
- [ ] Verify the engine's active transaction bookkeeping still works when `SendTransactionStart` returns a synthetic or queued transaction ID.

**Why:** Queueability belongs to the sender layer, not the callback wiring layer.

### Task 2: Make the meter ticker queue-aware instead of connection-gated

- [ ] Remove the disconnected skip in `internal/ocpp/meter_ticker.go`.
- [ ] Continue to skip connectors with no active transaction.
- [ ] Continue to go through the dispatcher for ordering.

**Why:** Periodic metering is part of the same durable outbound story as session lifecycle traffic.

### Task 3: Decide status-notification durability explicitly

- [ ] Pick one of two approaches and document it in code comments and docs:
- [ ] Option A: Add queue fallback for `SendStatusNotification` in both 1.6J and 2.0.1.
- [ ] Option B: Keep status notifications non-durable, but explicitly scope the queue to transaction lifecycle and meter traffic only.

**Recommended:** Option A if small enough. Otherwise Option B with honest documentation.

**Why:** Current behavior is half-implemented and misleading.

### Task 4: Introduce a serializable OCPP 2.0.1 queue envelope

- [ ] Add a new typed payload struct for queued OCPP 2.0.1 transaction events.
- [ ] Store only serializable primitives and slices in the queue payload.
- [ ] Capture at minimum:
- [ ] event type
- [ ] transaction ID string
- [ ] sequence number
- [ ] EVSE ID and connector ID
- [ ] timestamp
- [ ] trigger reason
- [ ] stop reason if ended
- [ ] reservation ID if present
- [ ] ID token if present
- [ ] meter values if present

**Why:** The queue must survive process restart without depending on Go interface type identity.

### Task 5: Reconstruct requests during OCPP 2.0.1 queue drain

- [ ] Update `drainQueue` in `internal/ocpp/v201/bridge.go` to recognize the new envelope.
- [ ] Rebuild a real `*transactions.TransactionEventRequest` before sending.
- [ ] If decode or reconstruction fails, do not silently dequeue the message.
- [ ] Log enough context to diagnose the bad queue entry.

**Why:** Silent data loss is worse than retained bad queue state.

### Task 6: Preserve sequence and transaction identity across replay

- [ ] Ensure queued `Started`, `Updated`, and `Ended` events preserve transaction identity.
- [ ] Ensure replay does not generate a new transaction UUID for already-queued events.
- [ ] Ensure sequence numbers continue in a valid order after reconnect or restart.

**Why:** OCPP 2.0.1 transaction event ordering is part of the protocol contract.

---

## Testing Requirements

- [ ] Add a test proving disconnected session start in 1.6J queues a start message.
- [ ] Add a test proving disconnected session stop in 1.6J queues a stop message.
- [ ] Add a test proving disconnected periodic meter reporting is queued.
- [ ] Add a test proving OCPP 2.0.1 queued transaction events replay from an in-memory queue.
- [ ] Add a test proving OCPP 2.0.1 queued transaction events survive JSON persistence and replay after restart.
- [ ] Add a regression test proving malformed persisted payloads are not silently discarded.

---

## Acceptance Criteria

- Queueable outbound traffic is no longer dropped simply because the bridge is disconnected.
- OCPP 2.0.1 transaction events survive restart when queue persistence is enabled.
- Queue drain preserves ordering and transaction identity.
- Status notification durability is either implemented or explicitly scoped out.

---

## Validation

- `go test ./internal/ocpp/... -v`
- `go test ./cmd/... ./internal/...`
- `go test -tags integration ./internal/ocpp/v201/ -v -timeout 60s`

---

## Risks

- Transaction bookkeeping in `main.go` and `Bridge201` can drift if queued synthetic transaction IDs are mishandled.
- Queue payload migrations must be backward-aware if there are already persisted queue files in the field.
