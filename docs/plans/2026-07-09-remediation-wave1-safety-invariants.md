# Remediation Wave 1: Physical Safety Invariants Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Immediately prevent sessions and energy transfer in physically impossible states without waiting for the larger persistence and protocol refactors.

**Architecture:** Route immediate and pending session starts through one validation and commit path. Make energy transfer depend on cable, connector state, session state, and a non-negative allocation; keep a transaction open after disconnect when configured, but force actual current and power to zero.

**Tech Stack:** Go engine package, REST handlers, `testify`, table-driven and property-style tests.

## Task 1: Add regression tests for pending-start invariant bypasses

**Files:**
- Modify: `internal/engine/engine_test.go`
- Modify: `internal/engine/engine.go:448-570`

**Step 1: Write failing tests**

Add table-driven tests covering pending remote start followed by plug-in when the connector is:

```go
func TestEngine_PendingRemoteStart_RevalidatesAtConsumption(t *testing.T) {
    cases := []struct {
        name  string
        setup func(*engine.Engine)
    }{
        {"inoperative", func(e *engine.Engine) { e.SetConnectorAvailability(1, "Inoperative") }},
        {"faulted", func(e *engine.Engine) { _ = e.FaultConnector(1, "HighTemperature") }},
        {"locked", func(e *engine.Engine) { _ = e.LockConnector(1) }},
    }
    // For each case: queue remote start, attempt PlugIn, assert no session,
    // no charging meter, no consumed reservation, and no SessionStarted callback.
}
```

Add separate tests proving:

- a second pending start on the same connector returns `ErrRemoteStartAlreadyPending`;
- single-EVSE mode cannot acquire a second session by consuming two pending starts;
- a failed state transition leaves no session, no charging meter, and no consumed reservation.

**Step 2: Run tests to verify failure**

Run:

```bash
go test ./internal/engine -run 'PendingRemoteStart|StartSession.*Atomic' -count=1 -v
```

Expected: FAIL because pending consumption calls `startSessionLocked` without the normal validation and ignores its error.

**Step 3: Introduce one session-start request type and validator**

Add to `internal/engine/engine.go`:

```go
type sessionStartRequest struct {
    ConnectorID    int
    TransactionID  int
    IDTag          *string
    Profile        *ChargingProfile
    RemoteStartID  *int
    ExpectedSource string
}

func (e *Engine) validateSessionStartLocked(req sessionStartRequest) error
func (e *Engine) commitSessionStartLocked(req sessionStartRequest) (*Session, []func(), error)
```

Validation must happen before reservation deletion, session insertion, meter creation, or charging-state mutation. It must check connector existence, cable presence, operability, fault, lock, reservation compatibility, connector state, per-connector uniqueness, and single-EVSE station uniqueness.

Make `StartSession` and pending-start consumption call the same functions. Propagate pending-consumption errors through a new read-only outcome callback or structured log; never ignore them.

**Step 4: Make duplicate pending requests explicit**

Add:

```go
var ErrRemoteStartAlreadyPending = errors.New("remote start already pending on connector")
```

Do not overwrite the first request. Keep its expiry and remote-start correlation intact.

**Step 5: Run focused tests**

Run the command from Step 2.

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/engine/engine.go internal/engine/engine_test.go
git commit -m "fix(engine): unify pending and immediate session admission"
```

## Task 2: Stop all energy flow when the cable is disconnected

**Files:**
- Modify: `internal/engine/engine.go:392-445`
- Modify: `internal/engine/engine.go:1216-1349`
- Modify: `internal/engine/engine_test.go`
- Modify: `internal/runtime/runtime_test.go`

**Step 1: Write the failing regression test**

```go
func TestEngine_UnplugWithoutStoppingTransaction_StopsEnergyTransfer(t *testing.T) {
    e := newChargingEngine(t)
    e.GetConfigValue = func(key string) string {
        if key == "StopTransactionOnEVSideDisconnect" { return "false" }
        return ""
    }
    e.Simulate(1)
    before, _ := e.GetMeterSnapshot(1)
    e.Unplug(1)
    e.Simulate(60)
    after, _ := e.GetMeterSnapshot(1)

    require.NotNil(t, e.GetSession(1), "transaction remains open")
    assert.Equal(t, before, after, "open cable cannot deliver energy")
    assert.Zero(t, e.GetEnergyMeter(1).EffectiveCurrent)
}
```

Also assert that plugging in another connector in single-EVSE mode cannot reactivate or double-count the disconnected session.

**Step 2: Run the test to verify failure**

```bash
go test ./internal/engine -run 'UnplugWithoutStopping|SingleEVSE.*Double' -count=1 -v
```

Expected: FAIL because `meter.IsCharging` remains true and simulation does not require `IsPluggedIn`.

**Step 3: Add a single transfer predicate**

Add a locked helper:

```go
func (e *Engine) canTransferEnergyLocked(c *Connector, s *Session, m *EnergyMeter, currentA float64) bool {
    return c != nil && s != nil && m != nil &&
        c.IsPluggedIn && c.Status == StateCharging &&
        m.IsCharging && currentA > 0
}
```

On any false predicate, set `EffectiveCurrent=0` and skip the meter update. On unplug with a retained transaction, set `meter.IsCharging=false`, `EffectiveCurrent=0`, and move the connector to a non-energy-transfer state without deleting the session.

**Step 4: Define replug behavior**

Replugging a connector with an open transaction must not close the power path automatically. It returns to an EV-connected/preparing state and requires `ResumeCharging` or the later EV-ready model. Add a test for this decision.

**Step 5: Run focused engine/runtime tests**

```bash
go test ./internal/engine ./internal/runtime -run 'Unplug|Energy|Replug' -count=1 -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/engine/engine.go internal/engine/engine_test.go internal/runtime/runtime_test.go
git commit -m "fix(engine): require a connected power path for energy transfer"
```

## Task 3: Make full-charge suspension terminal until acceptance changes

**Files:**
- Modify: `internal/engine/engine.go:711-747`
- Modify: `internal/engine/engine.go:1311-1342`
- Modify: `internal/engine/engine_test.go`
- Modify: `internal/api/handlers.go:295-318`

**Step 1: Write a failing test**

Start a session with a small battery, simulate to 100%, call `ResumeCharging`, simulate again, and assert the connector remains SuspendedEV with zero effective current and unchanged energy.

**Step 2: Run it**

```bash
go test ./internal/engine -run 'FullCharge.*Resume' -count=1 -v
```

Expected: FAIL because `ResumeCharging` re-enables the meter while `MaxChargeReached` suppresses another suspension.

**Step 3: Add a typed error and guard**

```go
var ErrBatteryFull = errors.New("EV cannot accept additional energy")
```

`ResumeCharging` must return this error while the session is at its configured maximum. Later waves may permit resume if an EV model lowers SoC or changes its acceptance limit; this wave keeps the safe behavior.

**Step 4: Make the API expose the conflict**

Keep the existing HTTP conflict shape, but ensure the error message identifies the full-battery reason.

**Step 5: Verify**

```bash
go test ./internal/engine ./internal/api -run 'FullCharge|ResumeCharging' -count=1 -v
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/engine/engine.go internal/engine/engine_test.go internal/api/handlers.go internal/api/handlers_test.go
git commit -m "fix(engine): prevent energy flow after full-charge suspension"
```

## Task 4: Clamp unsafe allocations and make updates atomic

**Files:**
- Modify: `internal/engine/engine.go:236-279`
- Modify: `internal/engine/engine.go:1266-1324`
- Modify: `internal/engine/energy_meter.go`
- Modify: `internal/engine/engine_test.go`
- Modify: `internal/api/handlers/charging_profiles.go`

**Step 1: Write failing tests**

Cover:

- negative `GetLimit` cannot decrease meter value, session energy, or SoC;
- negative/zero simulation intervals cannot mutate energy;
- `UpdateConnector` validates the entire proposed connector before changing any field;
- REST rejects a profile period with negative limit, negative start, zero phases, or unsupported rate unit.

**Step 2: Run tests**

```bash
go test ./internal/engine ./internal/api/handlers -run 'Negative|Atomic|ChargingProfile.*Valid' -count=1 -v
```

Expected: FAIL.

**Step 3: Implement boundary validation**

- Clamp protocol-provided current limits to `[0, connector.Current]` as defense in depth.
- Reject invalid engine/REST profile input before it reaches profile managers.
- Return early for non-positive simulation intervals.
- Build proposed connector values in locals, validate once, then commit all fields and fire one callback.

**Step 4: Add monotonicity assertions**

In tests, generate a table of negative, zero, and valid limits and assert:

```go
assert.GreaterOrEqual(t, afterMeter, beforeMeter)
assert.GreaterOrEqual(t, session.EnergyCharged, 0.0)
assert.GreaterOrEqual(t, session.StateOfCharge, 0.0)
```

**Step 5: Verify Wave 1**

```bash
go fmt ./...
go test ./internal/engine ./internal/runtime ./internal/api/... -count=1
go test ./...
go vet ./...
```

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/engine internal/api/handlers/charging_profiles.go internal/api/handlers
git commit -m "fix(engine): reject nonphysical inputs atomically"
```
