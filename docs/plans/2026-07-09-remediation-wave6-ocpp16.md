# ChargeGhost Remediation Wave 6: OCPP 1.6J Station Semantics

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task after Waves 1-5. Use OCPP 1.6 Edition 2 plus approved errata and the project's pinned library behavior as the protocol baseline.

**Goal:** Make every implemented OCPP 1.6J operation change or report the same state an actual charge point would, including configuration-driven behavior, offline transaction recovery, reservations, authorization, metering, and smart charging.

**Architecture:** Handlers validate and translate protocol requests, the engine owns physical and transaction state, typed outbox events own delivery, and a descriptor registry owns configuration metadata plus runtime hooks. Handlers must not directly manufacture physical state.

**Tech Stack:** Go, `github.com/lorenzodonini/ocpp-go`, existing engine/outbox packages, table-driven protocol tests and fake CSMS integration tests.

---

## Task 1: Build an executable 1.6J configuration registry

**Files:**
- Modify: `internal/ocpp/v16/config_keys.go`
- Modify: `internal/ocpp/v16/config_keys_persist.go`
- Create: `internal/ocpp/v16/config_registry.go`
- Create: `internal/ocpp/v16/config_registry_test.go`
- Modify: `internal/ocpp/v16/bridge.go`
- Modify: `internal/ocpp/v16/handlers.go`

### Step 1: Write failing descriptor tests

Every exposed key must declare:

- type and parser;
- read-only status;
- default and current value source;
- validator;
- runtime apply hook;
- persistence behavior.

Specifically assert:

- `NumberOfConnectors` reflects configured connectors;
- invalid boolean/integer/list values are rejected;
- `MeterValueSampleInterval=0` disables periodic samples rather than becoming 30 seconds;
- `ClockAlignedDataInterval=0` disables aligned samples;
- `SupportedFeatureProfiles` and measurand lists are derived from executable capabilities;
- accepted runtime changes affect newly scheduled work without restart;
- read-only keys cannot be changed.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/ocpp/v16 -run 'TestConfigRegistry'`

Expected: FAIL because current keys are partly static strings without behavioral hooks.

### Step 3: Implement the registry and runtime hooks

Replace switch-like metadata with descriptors. Keep persisted values backward compatible and validate stored values on load. Invalid legacy values fall back to defaults with a structured warning.

### Step 4: Run tests

Run: `go test ./internal/ocpp/v16 -run 'TestConfigRegistry'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/ocpp/v16/config_keys.go internal/ocpp/v16/config_keys_persist.go internal/ocpp/v16/config_registry.go internal/ocpp/v16/config_registry_test.go internal/ocpp/v16/bridge.go internal/ocpp/v16/handlers.go
git commit -m "fix: make OCPP 1.6 configuration executable"
```

## Task 2: Implement sampling and metering configuration faithfully

**Files:**
- Modify: `internal/ocpp/v16/bridge.go`
- Modify: `internal/ocpp/v16/senders.go`
- Modify: `internal/ocpp/v16/handlers.go`
- Modify: `internal/ocpp/v16/bridge_test.go`
- Modify: `internal/engine/energy_meter.go`
- Modify: `internal/engine/electrical_snapshot.go`

### Step 1: Write failing sampling tests with a fake clock

Cover:

- periodic sampling disabled at interval zero;
- clock-aligned sampling at UTC boundaries;
- separate sampled and aligned measurand lists;
- only configured and supported measurands emitted;
- measurand context, phase, unit, location, and format consistent with Wave 4 snapshots;
- transaction and non-transaction meter values;
- stop-transaction sampled data according to configuration;
- no substitution of `Energy.Active.Import.Register` for an unsupported requested measurand.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/ocpp/v16 -run 'Test.*(Meter|Sample|Aligned)'`

Expected: FAIL on zero interval, aligned sampling, and measurand parity.

### Step 3: Implement scheduling from configuration snapshots

Use an injectable clock/ticker abstraction; do not use sleeps in tests. On configuration change, cancel and replace the affected schedule. Read all sampled values from one electrical snapshot timestamp.

### Step 4: Run tests

Run: `go test ./internal/engine ./internal/ocpp/v16 -run 'Test.*(Meter|Sample|Aligned)'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/ocpp/v16/bridge.go internal/ocpp/v16/senders.go internal/ocpp/v16/handlers.go internal/ocpp/v16/bridge_test.go internal/engine/energy_meter.go internal/engine/electrical_snapshot.go
git commit -m "fix: honor OCPP 1.6 metering configuration"
```

## Task 3: Correct authorization and remote-start behavior

**Files:**
- Modify: `internal/ocpp/v16/handlers.go`
- Modify: `internal/ocpp/v16/handlers_test.go`
- Modify: `internal/ocpp/local_session_admission.go`
- Modify: `internal/ocpp/authorization_decision.go`
- Modify: `internal/ocpp/auth_cache.go`
- Modify: `internal/ocpp/auth_cache_test.go`
- Modify: `internal/ocpp/local_auth_list.go`
- Modify: `internal/ocpp/local_auth_list_test.go`
- Modify: `cmd/chargeghost/admission.go`
- Modify: `internal/engine/session.go`
- Create: `internal/engine/session_test.go`

### Step 1: Write failing scenario tests

Cover:

- local authorization list and authorization cache consulted only when enabled;
- expired entries rejected and parent ID constraints preserved;
- remote start with connector omitted/zero chooses one eligible connector deterministically;
- explicit connector is revalidated at execution time;
- pending remote start expires according to `ConnectionTimeOut`;
- a second pending start does not silently overwrite the first;
- a rejected `StartTransaction.conf` is correlated with the assigned transaction ID before local stop/cleanup;
- an offline accepted transaction receives its stable local identity and later CSMS mapping;
- remote stop addresses only the mapped transaction.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/engine ./internal/ocpp/v16 -run 'Test.*(Authoriz|RemoteStart|ConnectionTimeOut|StartTransaction)'`

Expected: FAIL on disabled-cache behavior, pending expiry, and rejected-start ID propagation.

### Step 3: Implement behavior through engine commands

Use the unified atomic start path from Wave 1 and identity store from Wave 2. Selection criteria must exclude faulted, unavailable, reserved-for-another-token, occupied, or locked-incompatible connectors.

### Step 4: Run focused and race tests

Run:

```bash
go test ./internal/engine ./internal/ocpp/v16
go test -race ./internal/engine ./internal/ocpp/v16
```

Expected: PASS.

### Step 5: Commit

```bash
git add internal/ocpp/v16/handlers.go internal/ocpp/v16/handlers_test.go internal/ocpp/local_session_admission.go internal/ocpp/authorization_decision.go internal/ocpp/auth_cache.go internal/ocpp/auth_cache_test.go internal/ocpp/local_auth_list.go internal/ocpp/local_auth_list_test.go cmd/chargeghost/admission.go internal/engine/session.go internal/engine/session_test.go
git commit -m "fix: align OCPP 1.6 authorization and remote starts"
```

## Task 4: Correct reservation, availability, unlock, and reset semantics

**Files:**
- Modify: `internal/ocpp/v16/handlers.go`
- Modify: `internal/ocpp/v16/handlers_test.go`
- Modify: `internal/engine/reservation.go`
- Create: `internal/engine/reservation_test.go`
- Modify: `internal/engine/state.go`
- Modify: `internal/engine/connector_state_test.go`

### Step 1: Write failing command-matrix tests

Build a table over connector facts: plugged, active transaction, reserved token, unavailable target, fault, and lock state. Verify:

- `ReserveNow` connector `0` creates an unbound station-level reservation, advertises support, and binds to the first compatible eligible connector used by the reserved token rather than arbitrarily blocking one connector;
- cancellation requires the correct reservation ID;
- expiry uses the injected clock and restores projected status;
- `ChangeAvailability` schedules `Inoperative` after an active transaction and applies immediately when idle;
- connector `0` applies station-wide without erasing reservations or faults;
- `UnlockConnector` rejects invalid connector IDs, returns the appropriate active-transaction outcome, and never implies cable removal;
- soft reset waits for transaction completion; hard reset follows the documented configurable simulation policy;
- status notifications reflect projection changes in causal order.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/engine ./internal/ocpp/v16 -run 'Test.*(Reserve|Availability|Unlock|Reset)'`

Expected: FAIL on connector-zero and conflated persistent-state behavior.

### Step 3: Implement with independent state axes

Handlers call engine operations only. Emit OCPP status from the Wave 3 projection. Document simulated hard-reset behavior explicitly; do not pretend to restart the process if only internal state is reset.

### Step 4: Run tests

Run: `go test ./internal/engine ./internal/ocpp/v16 -run 'Test.*(Reserve|Availability|Unlock|Reset|Status)'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/ocpp/v16/handlers.go internal/ocpp/v16/handlers_test.go internal/engine/reservation.go internal/engine/reservation_test.go internal/engine/state.go internal/engine/connector_state_test.go
git commit -m "fix: align OCPP 1.6 connector commands"
```

## Task 5: Complete transaction delivery and recovery scenarios

**Files:**
- Modify: `internal/ocpp/v16/senders.go`
- Modify: `internal/ocpp/v16/senders_test.go`
- Modify: `internal/ocpp/v16/bridge_test.go`
- Create: `internal/ocpp/outbox/integration_test.go`
- Modify: `internal/engine/persist_test.go`

### Step 1: Write failing fake-CSMS scenarios

Cover:

- start while offline, multiple meter events, stop while offline, restart, reconnect;
- a temporary send failure while connected;
- CSMS-assigned integer ID remapped into queued meter and stop calls;
- original event timestamps retained after delayed delivery;
- FIFO ordering within one transaction;
- independent transactions do not corrupt each other's mapping;
- authorization rejection after `StartTransaction.conf` sends a stop using the assigned ID;
- duplicate confirmation/retry is idempotent locally.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/ocpp/v16 ./internal/ocpp/outbox -run 'Test.*(Offline|Reconnect|Remap|Retry|Rejected)'`

Expected: FAIL until the Wave 2 primitives are fully wired into the 1.6J bridge.

### Step 3: Finish sender integration

No transaction-lifecycle callback may bypass the typed outbox. Delivery failures remain queued with bounded backoff. Persist mapping before acknowledging local delivery success.

### Step 4: Run verification

Run:

```bash
go test ./internal/engine ./internal/ocpp/outbox ./internal/ocpp/v16
go test -race ./internal/engine ./internal/ocpp/outbox ./internal/ocpp/v16
go test ./...
go vet ./...
```

Expected: PASS.

### Step 5: Commit

```bash
git add internal/ocpp/v16/senders.go internal/ocpp/v16/senders_test.go internal/ocpp/v16/bridge_test.go internal/ocpp/outbox/integration_test.go internal/engine/persist_test.go
git commit -m "fix: complete durable OCPP 1.6 transactions"
```

## Task 6: Add a 1.6J behavior/conformance matrix

**Files:**
- Create: `internal/ocpp/v16/scenario_test.go`
- Create: `docs/ocpp16-capabilities.md`
- Modify: `docs/REST_API.md`

### Step 1: Add end-to-end station scenarios

Exercise boot, authorize, plug, start, meter, suspend/resume, stop, unplug, reservation, availability, reset, configuration, offline recovery, and smart charging against a fake CSMS. Assert both calls and engine state.

### Step 2: Document exact support

For every 1.6J feature profile/key/action, mark supported, partially simulated, or unsupported, and link to its scenario test. Remove claims that are not executable.

### Step 3: Run final Wave 6 verification

Run:

```bash
go test ./internal/ocpp/v16 -count=10
go test -race ./internal/ocpp/v16
go test ./...
go vet ./...
```

Expected: PASS with no flaky clock-based cases.

### Step 4: Commit

```bash
git add internal/ocpp/v16/scenario_test.go docs/ocpp16-capabilities.md docs/REST_API.md
git commit -m "test: codify OCPP 1.6 station behavior"
```

## Wave 6 release gate

- Every exposed configuration key has a tested behavior or is honestly read-only/unsupported.
- Offline transaction sequences survive process restart with correct timestamps and transaction IDs.
- Reservation, remote-start, availability, unlock, and reset matrices pass.
- Meter and composite schedule output matches the engine snapshot/resolver.
- The capability document and implementation agree.
- Full suite, race detector, repeated scenario tests, and vet pass.
