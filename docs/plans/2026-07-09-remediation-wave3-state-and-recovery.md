# Remediation Wave 3: Orthogonal State and Recovery Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Separate operability, reservation, fault, cable, lock, power-path, and transaction state, then persist and reconcile all of it safely across restart.

**Architecture:** Store independent connector facts and derive OCPP status projections. Introduce versioned engine snapshots with read-old/write-new migrations and a recovery reconciler that aligns engine sessions, meter state, transaction mappings, and outbox records.

**Tech Stack:** Go engine, JSON snapshots, golden fixtures, OCPP status projections.

## Task 1: Specify state-axis invariants with failing tests

**Files:**
- Create: `internal/engine/connector_state_test.go`
- Create: `internal/engine/invariants.go`

**Step 1: Write permutation tests**

Cover at minimum:

- reserved → inoperative → reservation expires remains inoperative;
- inoperative → faulted → clear fault remains inoperative;
- reserved → faulted → clear fault remains reserved;
- plugged + inoperative projects unavailable, not preparing;
- locked cable cannot be physically removed until unlock succeeds;
- no session implies contactor open and zero actual current;
- session stopped while cable remains projects Finishing/Occupied according to protocol.

**Step 2: Run tests**

```bash
go test ./internal/engine -run 'StateAxes|Invariant|Projection' -count=1 -v
```

Expected: FAIL against the current `PersistentStatus` design.

**Step 3: Add an invariant checker**

```go
func validateConnectorRuntime(c *Connector, session *Session, meter *EnergyMeter) error
```

Use it in tests immediately. Production debug logging may call it after mutations, but it must not panic in release builds.

**Step 4: Commit tests and checker**

```bash
git add internal/engine/connector_state_test.go internal/engine/invariants.go
git commit -m "test(engine): specify orthogonal connector invariants"
```

## Task 2: Replace PersistentStatus with independent runtime facts

**Files:**
- Modify: `internal/engine/connector.go`
- Modify: `internal/engine/state.go`
- Modify: `internal/engine/engine.go`
- Modify: `internal/engine/engine_test.go`

**Step 1: Add compatibility fields and projection methods**

Add `Operative`, `Fault`, `ReservationID`, `Cable`, `Lock`, and `Contactor` facts. Keep `Status` as a derived cached projection during migration; remove mutation of `PersistentStatus`.

```go
func (c *Connector) ProjectOCPP16Status(hasSession bool) ConnectorState
func (c *Connector) CanAcceptSession() error
func (c *Connector) CanTransferEnergy(hasSession bool) bool
```

**Step 2: Convert one mutation path at a time**

Order:

1. availability;
2. reservation create/cancel/expiry;
3. fault/clear;
4. plug/unplug;
5. lock/unlock;
6. start/suspend/resume/stop.

After each conversion, run `go test ./internal/engine -count=1`.

**Step 3: Fire status callbacks only on projection change**

Capture the old projected status, mutate facts, derive the new projection, then append one callback if it changed. This prevents duplicate or contradictory notifications.

**Step 4: Remove `PersistentStatus` writes**

Keep decode-only migration support in persistence, but no runtime code may use the field.

**Step 5: Verify and commit**

```bash
go test ./internal/engine -count=1
git add internal/engine
git commit -m "refactor(engine): derive connector status from orthogonal state"
```

## Task 3: Define physically meaningful lock behavior

**Files:**
- Modify: `internal/engine/connector.go`
- Modify: `internal/engine/engine.go`
- Modify: `internal/ocpp/v16/handlers.go`
- Modify: `internal/ocpp/v201/handlers.go`
- Test: `internal/engine/connector_state_test.go`
- Test: `internal/ocpp/v16/handlers_test.go`
- Test: `internal/ocpp/v201/handlers_test.go`

**Step 1: Write failing lock scenarios**

- Plugging is allowed when the lock is idle.
- A detachable cable becomes locked after plug/authorization according to station capability.
- A locked cable cannot be unplugged through the physical control API.
- OCPP unlock on an active authorized transaction returns the version-appropriate non-success result.
- Unlock without an active transaction is idempotent and permits unplug.

**Step 2: Add lock capability and state**

Separate `LockSupported` from `LockState`. Do not use “currently locked” as the capability signal.

**Step 3: Route protocol handlers through engine unlock**

Return protocol status based on the typed engine result rather than checking fields independently in each handler.

**Step 4: Verify and commit**

```bash
go test ./internal/engine ./internal/ocpp/v16 ./internal/ocpp/v201 -run 'Lock|Unlock' -count=1
git add internal/engine internal/ocpp/v16 internal/ocpp/v201
git commit -m "fix(engine): model connector lock capability and state"
```

## Task 4: Version and migrate engine persistence

**Files:**
- Modify: `internal/engine/persist.go`
- Create: `internal/engine/migrate.go`
- Create: `internal/engine/testdata/engine_v1_active_session.json`
- Create: `internal/engine/testdata/engine_v1_reserved.json`
- Create: `internal/engine/testdata/engine_v1_faulted.json`
- Create: `internal/engine/testdata/engine_v1_multi_evse.json`
- Modify: `internal/engine/persist_test.go`

**Step 1: Add failing golden tests**

Load each current-schema fixture and assert the equivalent new facts, including fault code, pre-fault operability, lock, reservation, pending remote start correlation, last stop time, and effective current defaults.

**Step 2: Add schema version**

```go
type engineSnapshot struct {
    SchemaVersion int `json:"schema_version"`
    // existing/new fields
}
```

Treat missing version as v1. Write only the latest version. Preserve unknown future versions as an explicit load error.

**Step 3: Migrate ambiguous legacy states conservatively**

- Legacy `Unavailable` becomes `Operative=false`.
- Legacy `Reserved` sets reservation only when an unexpired reservation record exists; otherwise clear the stale projection.
- Legacy `Faulted` without a fault code becomes `OtherError` with an audit warning.
- Legacy active sessions force cable-connected only if the saved connector says so; otherwise recover as open transaction with no energy transfer.

**Step 4: Round-trip all new facts**

Add tests for fault, lock, last stop, pending RemoteStartID, current session UUID/sequence, meter actual current, and reservation/availability independence.

**Step 5: Verify and commit**

```bash
go test ./internal/engine -run 'Migrate|SaveLoadState' -count=1 -v
git add internal/engine
git commit -m "feat(engine): migrate versioned orthogonal station state"
```

## Task 5: Reconcile engine, transaction mapping, and outbox on startup

**Files:**
- Create: `internal/ocpp/recovery.go`
- Create: `internal/ocpp/recovery_test.go`
- Modify: `cmd/chargeghost/station_runtime.go`
- Modify: `internal/ocpp/v16/transaction_store.go`
- Modify: `internal/ocpp/v201/transaction_store.go`

**Step 1: Write failing reconciliation scenarios**

Cover engine-active/outbox-present, engine-active/mapping-missing, mapping-present/engine-missing, ended-event-pending, start-event-unacknowledged, and corrupt mapping.

**Step 2: Define reconciliation outcomes**

```go
type RecoveryAction string
const (
    RecoveryResume       RecoveryAction = "resume"
    RecoveryAwaitStart   RecoveryAction = "await_start_ack"
    RecoveryQueueEnd     RecoveryAction = "queue_recovery_end"
    RecoveryDeadLetter   RecoveryAction = "dead_letter"
)
```

Every action is logged and testable. Never silently invent a CSMS transaction integer for 1.6J.

**Step 3: Run reconciliation before simulation starts**

Build/load engine, stores, and outbox; reconcile; only then launch runtime, dispatcher, bridge, and meter ticker.

**Step 4: Verify Wave 3**

```bash
go fmt ./...
go test ./...
go test -race ./internal/engine ./internal/ocpp/... ./cmd/chargeghost
go vet ./...
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/ocpp/recovery.go internal/ocpp/recovery_test.go cmd/chargeghost/station_runtime.go internal/ocpp/v16 internal/ocpp/v201
git commit -m "feat(runtime): reconcile active transactions during station recovery"
```
