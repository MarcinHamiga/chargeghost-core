# ChargeGhost Remediation Wave 7: OCPP 2.0.1 Station Semantics

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task after Waves 1-6. Use the official OCPP 2.0.1 schemas, Edition 3 specification, and approved errata as the protocol baseline.

**Goal:** Correct OCPP 2.0.1 transaction events, scoped commands, token/reservation semantics, device model, runtime variables, recovery, and reporting so the simulator behaves as a stateful charging station.

**Architecture:** Stable transaction identities and typed outbox records drive transaction delivery. The engine owns physical facts. A generated device model exposes actual station capabilities and routes writable variables through typed runtime hooks. Protocol handlers translate without inventing state.

**Tech Stack:** Go, current OCPP 2.0.1 package, existing engine/outbox, JSON schema fixtures, table-driven tests, fake CSMS integration tests.

---

## Task 1: Make TransactionEvent a projection of real transaction state

**Files:**
- Modify: `internal/ocpp/v201/senders.go`
- Modify: `internal/ocpp/v201/senders_test.go`
- Modify: `internal/ocpp/v201/bridge.go`
- Modify: `internal/ocpp/v201/bridge_test.go`
- Modify: `internal/ocpp/v201/transaction.go`
- Modify: `internal/ocpp/v201/transaction_test.go`

### Step 1: Write failing transaction-event sequence tests

Cover:

- `Started`, `Updated`, and `Ended` share one persisted UUID;
- sequence numbers are monotonic across reconnect and process restart;
- periodic updates report the actual `chargingState` (`Charging`, suspended EV, suspended EVSE, idle) instead of defaulting to charging;
- remote start uses the remote-start trigger reason and retains `remoteStartId`;
- cable insertion, authorized start, deauthorization, EV disconnect, local stop, remote stop, and fault map to correct trigger/stopped reasons;
- delayed offline events retain event timestamps and set `offline=true` when required;
- event meter values come from one Wave 4 snapshot;
- token type, group token, and additional token information survive translation.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/ocpp/v201 -run 'TestTransactionEvent'`

Expected: FAIL on restart identity, periodic charging state, offline flag, and trigger reason.

### Step 3: Implement event projection and persistence

Build each call from a persisted domain-event record plus current/recorded transaction snapshot. Persist the next sequence number before marking outbox delivery successful. Never regenerate event identity at send time.

### Step 4: Run tests

Run:

```bash
go test ./internal/ocpp/v201 -run 'TestTransactionEvent'
go test -race ./internal/ocpp/v201 -run 'TestTransactionEvent'
```

Expected: PASS.

### Step 5: Commit

```bash
git add internal/ocpp/v201/senders.go internal/ocpp/v201/senders_test.go internal/ocpp/v201/bridge.go internal/ocpp/v201/bridge_test.go internal/ocpp/v201/transaction.go internal/ocpp/v201/transaction_test.go
git commit -m "fix: project accurate OCPP 2.0.1 transaction events"
```

## Task 2: Correct transaction-status and durable recovery queries

**Files:**
- Modify: `internal/ocpp/v201/handlers.go`
- Modify: `internal/ocpp/v201/handlers_test.go`
- Modify: `internal/ocpp/v201/transaction.go`
- Modify: `internal/ocpp/v201/transaction_test.go`
- Modify: `internal/ocpp/outbox/store.go`

### Step 1: Write failing exact-identity tests

Assert:

- `GetTransactionStatus` reports ongoing only for the requested transaction UUID;
- an unknown ID is not treated as ongoing because another session exists;
- omitted transaction ID follows the specification's station-level semantics;
- `messagesInQueue` reflects relevant undelivered transaction events;
- restart reconciliation restores active identity, sequence, EVSE mapping, and queued state;
- ended transactions are distinguishable from unknown transactions for the supported retention period.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/ocpp/v201 ./internal/ocpp/outbox -run 'TestGetTransactionStatus|Test.*RestartRecovery'`

Expected: FAIL on unknown-ID behavior and queue state.

### Step 3: Implement exact lookup and retention

Query the identity store by UUID. Keep a bounded persisted transaction tombstone/index sufficient for status queries and idempotent recovery. Document retention limits.

### Step 4: Run tests

Run: `go test ./internal/ocpp/v201 ./internal/ocpp/outbox -run 'TestGetTransactionStatus|Test.*RestartRecovery'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/ocpp/v201/handlers.go internal/ocpp/v201/handlers_test.go internal/ocpp/v201/transaction.go internal/ocpp/v201/transaction_test.go internal/ocpp/outbox/store.go
git commit -m "fix: report exact OCPP 2.0.1 transaction status"
```

## Task 3: Correct scoped reset and availability behavior

**Files:**
- Modify: `internal/ocpp/v201/handlers.go`
- Modify: `internal/ocpp/v201/handlers_test.go`
- Modify: `internal/engine/state.go`
- Modify: `internal/engine/connector_state_test.go`
- Modify: `internal/engine/engine.go`

### Step 1: Write failing scope matrices

For station scope and each EVSE, test:

- immediate reset affects only the requested scope;
- on-idle reset waits only for transactions in the requested scope;
- unknown EVSE is rejected;
- reset completion/status is reported after the simulated action, not before;
- operational status target and current state are distinct while a transaction is active;
- station-wide availability fans out deterministically;
- a fault or reservation is not erased by availability changes;
- status and transaction events remain causally ordered.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/engine ./internal/ocpp/v201 -run 'Test.*(Reset|Availability).*Scope'`

Expected: FAIL because `evseId` is currently ignored or incompletely modeled.

### Step 3: Implement scoped engine operations

Add scope-aware reset requests with explicit completion callbacks. Keep hard-reset process behavior configurable and accurately documented. Project connector/EVSE status from independent facts.

### Step 4: Run tests

Run: `go test ./internal/engine ./internal/ocpp/v201 -run 'Test.*(Reset|Availability|Status)'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/ocpp/v201/handlers.go internal/ocpp/v201/handlers_test.go internal/engine/state.go internal/engine/connector_state_test.go internal/engine/engine.go
git commit -m "fix: scope OCPP 2.0.1 reset and availability"
```

## Task 4: Correct reservations, tokens, and connector addressing

**Files:**
- Modify: `internal/ocpp/v201/handlers.go`
- Modify: `internal/ocpp/v201/handlers_test.go`
- Create: `internal/ocpp/v201/token.go`
- Create: `internal/ocpp/v201/token_test.go`
- Modify: `internal/ocpp/local_session_admission.go`
- Modify: `internal/ocpp/authorization_decision.go`
- Modify: `internal/engine/reservation.go`
- Modify: `internal/engine/reservation_test.go`
- Modify: `internal/engine/connector.go`

### Step 1: Write failing behavior tests

Cover:

- omitted `evseId` selects an eligible EVSE based on requested connector type;
- explicit EVSE is validated for existence, connector availability, reservation conflicts, and physical compatibility;
- idToken type, groupIdToken, additional info, and certificate status are preserved as far as supported;
- group-token authorization can satisfy a reservation without equating unrelated tokens;
- reservation expiry and cancellation use exact IDs;
- reservation-status updates are durable when offline;
- an EVSE with one modeled connector rejects invalid connector IDs;
- unlock reports active transaction, unlock failure, and success without changing plug state.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/engine ./internal/ocpp/v201 -run 'Test.*(Reserve|Token|Unlock|Connector)'`

Expected: FAIL on omitted EVSE and discarded token metadata.

### Step 3: Implement rich token and selection models

Extend version-agnostic authorization input without reducing tokens to a bare string. Selection must be deterministic and must use current engine eligibility at execution time.

### Step 4: Run tests

Run: `go test ./internal/engine ./internal/ocpp/v201 -run 'Test.*(Reserve|Token|Unlock|Connector)'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/ocpp/v201/handlers.go internal/ocpp/v201/handlers_test.go internal/ocpp/v201/token.go internal/ocpp/v201/token_test.go internal/ocpp/local_session_admission.go internal/ocpp/authorization_decision.go internal/engine/reservation.go internal/engine/reservation_test.go internal/engine/connector.go
git commit -m "fix: preserve OCPP 2.0.1 reservation semantics"
```

## Task 5: Generate the device model from actual station capabilities

**Files:**
- Modify: `internal/ocpp/v201/device_model.go`
- Create: `internal/ocpp/v201/device_model_registry.go`
- Create: `internal/ocpp/v201/device_model_registry_test.go`
- Modify: `internal/ocpp/v201/handlers.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

### Step 1: Write failing model-consistency tests

Assert that generated variables report actual:

- EVSE and connector counts;
- AC/DC supply type, nominal voltage, maximum current, phases, and power;
- serial/model/vendor and firmware version;
- supported measurands and feature profiles;
- smart-charging availability;
- transaction start/stop points and EV connection timeout;
- writable/read-only status and data type.

Reject hardcoded 230 V, 32 A, three-phase AC values when configuration differs.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/config ./internal/ocpp/v201 -run 'TestDeviceModel'`

Expected: FAIL on static hardware and capability claims.

### Step 3: Implement generated components and variables

Build the device model from validated configuration and engine capability descriptors at startup. Preserve standard component/variable names and characteristics. Unknown variables return the correct attribute status.

### Step 4: Run tests

Run: `go test ./internal/config ./internal/ocpp/v201 -run 'TestDeviceModel'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/ocpp/v201/device_model.go internal/ocpp/v201/device_model_registry.go internal/ocpp/v201/device_model_registry_test.go internal/ocpp/v201/handlers.go internal/config/config.go internal/config/config_test.go
git commit -m "fix: generate OCPP 2.0.1 device model from hardware"
```

## Task 6: Make writable variables affect runtime behavior

**Files:**
- Modify: `internal/ocpp/v201/device_model_registry.go`
- Modify: `internal/ocpp/v201/device_model_registry_test.go`
- Modify: `internal/ocpp/v201/handlers.go`
- Modify: `internal/ocpp/v201/bridge.go`
- Modify: `internal/engine/session.go`
- Modify: `internal/engine/simulation.go`

### Step 1: Write failing set-variable tests

For each writable variable, verify type/range validation, persistence, and live effect. At minimum cover:

- sampled and aligned data intervals;
- transaction event sample interval;
- EV connection timeout;
- `TxStartPoint` and `TxStopPoint` policies;
- stop-on-EV-disconnect behavior;
- smart-charging enablement and station limits where exposed.

Also prove unsupported writable claims are removed or made read-only.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/engine ./internal/ocpp/v201 -run 'TestSetVariables.*Runtime'`

Expected: FAIL because several accepted values currently do not change behavior.

### Step 3: Implement typed apply hooks

Use the same descriptor pattern as Wave 6. Apply changes atomically; if a runtime hook fails, do not persist the new value. Reschedule timers using the injected clock.

### Step 4: Run tests

Run: `go test ./internal/engine ./internal/ocpp/v201 -run 'TestSetVariables.*Runtime|Test.*Tx(Start|Stop)Point'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/ocpp/v201/device_model_registry.go internal/ocpp/v201/device_model_registry_test.go internal/ocpp/v201/handlers.go internal/ocpp/v201/bridge.go internal/engine/session.go internal/engine/simulation.go
git commit -m "fix: apply OCPP 2.0.1 variables at runtime"
```

## Task 7: Correct composite schedules and event reporting

**Files:**
- Modify: `internal/ocpp/v201/handlers.go`
- Modify: `internal/ocpp/v201/handlers_test.go`
- Modify: `internal/ocpp/v201/senders.go`
- Modify: `internal/ocpp/v201/senders_test.go`
- Modify: `internal/ocpp/v201/monitoring.go`
- Modify: `internal/ocpp/v201/monitoring_test.go`

### Step 1: Write failing reporting tests

Assert:

- EVSE `0` returns a station composite schedule;
- specific EVSEs return their allocated schedule;
- requested amperes or watts are honored;
- unknown EVSEs and unsupported conversions are rejected;
- `NotifyEvent` is emitted only for modeled variables/monitors and valid event types;
- availability changes use `StatusNotification`, not duplicate ad-hoc raw availability events;
- trigger messages produce the correct current report without mutating state.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/ocpp/v201 -run 'Test.*(CompositeSchedule|NotifyEvent|TriggerMessage)'`

Expected: FAIL on EVSE zero, unit handling, and duplicate availability events.

### Step 3: Implement protocol projections

Serialize the Wave 5 schedule projection and Wave 3 state projection. Monitoring looks up the generated device-model registry before emitting an event.

### Step 4: Run tests

Run: `go test ./internal/ocpp/v201 -run 'Test.*(CompositeSchedule|NotifyEvent|TriggerMessage|StatusNotification)'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/ocpp/v201/handlers.go internal/ocpp/v201/handlers_test.go internal/ocpp/v201/senders.go internal/ocpp/v201/senders_test.go internal/ocpp/v201/monitoring.go internal/ocpp/v201/monitoring_test.go
git commit -m "fix: align OCPP 2.0.1 reporting with station state"
```

## Task 8: Add a 2.0.1 behavior/conformance matrix

**Files:**
- Create: `internal/ocpp/v201/scenario_test.go`
- Create: `docs/ocpp201-capabilities.md`
- Modify: `docs/REST_API.md`

### Step 1: Add end-to-end station scenarios

Exercise boot, security/token authorization as supported, cable state, transaction start/update/end, remote start, reservations, availability, scoped reset, device variables, monitoring, offline recovery, and smart charging against a fake CSMS. Assert wire calls, ordering, and engine state.

### Step 2: Document exact support

List each implemented functional block, command, component/variable, and limitation. Mark security features or ISO 15118 behavior that are not simulated; do not imply conformance merely because a schema type exists.

### Step 3: Run final Wave 7 verification

Run:

```bash
go test ./internal/ocpp/v201 -count=10
go test -tags integration ./internal/ocpp/v201
go test -race ./internal/ocpp/v201
go test ./...
go vet ./...
```

Expected: PASS with stable sequence/timestamp assertions.

### Step 4: Commit

```bash
git add internal/ocpp/v201/scenario_test.go docs/ocpp201-capabilities.md docs/REST_API.md
git commit -m "test: codify OCPP 2.0.1 station behavior"
```

## Wave 7 release gate

- Transaction UUID, sequence, timestamps, trigger reasons, charging states, and offline flags survive restart correctly.
- Scoped reset, availability, reservation, unlock, and status-query matrices pass.
- The device model reports real station hardware/capabilities and writable variables have tested runtime hooks.
- Smart charging and composite schedules use the Wave 5 resolver.
- The capability document and implementation agree.
- Full, integration-tagged, race, repeated scenario, and vet checks pass.
