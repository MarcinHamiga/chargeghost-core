# OCPP 2.0.1 — Phase 3: Transaction Lifecycle

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the TransactionEvent model (replaces StartTransaction/StopTransaction/MeterValues), Authorize with IdToken, remote start/stop handlers, and offline queue integration.

**Architecture:** `TransactionEventBuilder` creates Started/Updated/Ended events with incrementing seqNo. One builder per active EVSE, keyed in `Bridge201.txBuilders`. Station generates UUID transaction IDs. Meter values are embedded in TransactionEvent(Updated).

**Tech Stack:** Go 1.26+, `lorenzodonini/ocpp-go v0.19.0`, `google/uuid`, `stretchr/testify`

**Spec:** `docs/superpowers/specs/2026-04-07-ocpp-201-design.md` — "Transactions" section

**Prerequisite phases:** Phase 2 (minimal slice) must be complete
**Next phase:** `2026-04-07-ocpp-201-phase4-device-model.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/ocpp/v201/transaction.go` | **Create** | `TransactionEventBuilder`, `makeMeterValue()` |
| `internal/ocpp/v201/transaction_test.go` | **Create** | Builder tests: Started/Updated/Ended, seqNo |
| `internal/ocpp/v201/senders.go` | Modify | Replace stubs with real TransactionEvent senders |
| `internal/ocpp/v201/handlers.go` | Modify | Add remote control + auth + transaction handlers |
| `internal/ocpp/v201/bridge.go` | Modify | Register new handlers, enhance drainQueue |

---

### Task 8: TransactionEventBuilder

See `2026-04-07-ocpp-201.md` Task 8 for complete code.

- [ ] **Step 1: Write failing tests** — `TestTransactionEventBuilder_Started`, `_Updated`, `_Ended`, `_SeqNoIncrements`
- [ ] **Step 2: Run tests to verify they fail**
- [ ] **Step 3: Implement TransactionEventBuilder** — `transaction.go` with `NewTransactionEventBuilder()`, `Started()`, `Updated()`, `Ended()`, `TransactionID()`, `makeMeterValue()`
- [ ] **Step 4: Run tests to verify they pass**
- [ ] **Step 5: Commit**

---

### Task 9: Wire TransactionEvent Into Senders

See `2026-04-07-ocpp-201.md` Task 9 for complete code.

- [ ] **Step 1: Implement SendTransactionStart** — creates builder, stores in txBuilders, sends TransactionEvent(Started)
- [ ] **Step 2: Implement SendTransactionStop** — finds builder by EVSE, sends TransactionEvent(Ended), removes builder
- [ ] **Step 3: Implement SendMeterValues** — sends TransactionEvent(Updated, MeterValuePeriodic) with embedded meter value
- [ ] **Step 4: Implement SendAuthorize** — uses 2.0.1 IdToken model with ISO14443 type
- [ ] **Step 5: Register transaction and authorization handlers** — `SetTransactionsHandler(b)`, `SetAuthorizationHandler(b)`, add `OnClearCache`, `OnGetTransactionStatus` stubs
- [ ] **Step 6: Build and test**
- [ ] **Step 7: Commit**

---

### Task 10: Remote Start/Stop Handlers

See `2026-04-07-ocpp-201.md` Task 10 for complete code.

- [ ] **Step 1: Add remote control handlers** — `OnRequestStartTransaction` (maps to `engine.StartSession`), `OnRequestStopTransaction` (finds EVSE by transaction ID string), `OnTriggerMessage` (dispatches Boot/Heartbeat/Status/TransactionEvent/FirmwareStatus), `OnUnlockConnector` (maps to `engine.Unplug`)
- [ ] **Step 2: Register handler** — `SetRemoteControlHandler(b)`
- [ ] **Step 3: Build and test**
- [ ] **Step 4: Commit**

---

### Task 11: Offline Queue Integration

See `2026-04-07-ocpp-201.md` Task 11 for complete code.

- [ ] **Step 1: Add queue fallback to SendTransactionStart** — if `!IsConnected()`, enqueue TransactionEvent as `QueuedMessage{Type: "TransactionEvent"}`. Apply same pattern to SendTransactionStop and SendMeterValues.
- [ ] **Step 2: Enhance drainQueue** — switch on `msg.Type`, cast payload to `*TransactionEventRequest`, resend via `SendRequestAsync`
- [ ] **Step 3: Build and test**
- [ ] **Step 4: Commit**
