# ChargeGhost Remediation Wave 5: Smart Charging and Site Allocation

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Do not begin this wave until Waves 1-4 pass their release gates.

**Goal:** Replace the current per-connector current cap with one deterministic charging-allocation engine used by runtime simulation, OCPP 1.6J, OCPP 2.0.1, and composite-schedule reporting.

**Architecture:** Charging profiles remain protocol-owned inputs, but both protocol managers normalize them into a version-agnostic schedule representation. The engine resolves schedules at a supplied instant and allocates station capacity across active EVSEs. Runtime consumption and reported composite schedules must use the same resolver so the station never promises a limit different from the one it simulates.

**Tech Stack:** Go, existing OCPP libraries, `testing`, `testify`, table-driven and property-oriented tests.

---

## Scope and required invariants

- A connector cannot receive negative current or power.
- The aggregate station allocation cannot exceed the station/site limit.
- Connector, transaction, station-max, and external-constraint profiles are all applied at their proper scope.
- Schedule duration, validity, recurrence, period start, rate unit, and phase count affect the resolved allocation.
- The simulation and `GetCompositeSchedule` share the same calculation path.
- Invalid profiles are rejected before they can mutate stored state.
- Equal inputs produce equal allocations; tie-breaking is stable by EVSE/connector ID.

## Task 1: Introduce normalized schedule and allocation types

**Files:**
- Create: `internal/engine/charging_allocation.go`
- Create: `internal/engine/charging_allocation_test.go`

### Step 1: Write failing normalization and validation tests

Cover:

- amperes and watts as explicit rate units;
- strictly increasing period start offsets beginning at zero;
- duration and validity bounds;
- absolute, recurring, and transaction-relative schedule anchoring;
- stack-level replacement and precedence at the same purpose/scope;
- phase count of 1-3 for AC and phase-independent DC;
- rejection of negative, NaN, and infinite limits;
- recurrence anchored to the profile start;
- explicit profile sources and scope.

Define the intended core types in the test:

```go
type ChargingLimit struct {
    CurrentA float64
    PowerW   float64
    Phases   int
}

type ChargingAllocation struct {
    Limit   ChargingLimit
    Sources []ChargingLimitSource
}
```

### Step 2: Run the focused test and confirm failure

Run: `go test ./internal/engine -run 'Test(NormalizedSchedule|ValidateChargingSchedule)'`

Expected: FAIL because the normalized representation and validator do not exist.

### Step 3: Implement the minimum normalized model

Add version-agnostic profile purpose, scope, kind, stack level, recurrence, rate-unit, schedule-period, minimum-rate, and provenance types. Keep protocol enum conversion outside the engine package.

The validator must return typed errors suitable for mapping to OCPP and REST rejection responses.

### Step 4: Run tests

Run: `go test ./internal/engine -run 'Test(NormalizedSchedule|ValidateChargingSchedule)'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/engine/charging_allocation.go internal/engine/charging_allocation_test.go
git commit -m "refactor: add normalized charging allocation model"
```

## Task 2: Resolve schedules completely and deterministically

**Files:**
- Modify: `internal/engine/charging_allocation.go`
- Modify: `internal/engine/charging_allocation_test.go`
- Modify: `internal/ocpp/v16/profile_manager.go`
- Modify: `internal/ocpp/v16/profile_manager_test.go`
- Modify: `internal/ocpp/v201/profile_manager.go`
- Modify: `internal/ocpp/v201/profile_manager_test.go`

### Step 1: Write failing time-boundary fixtures

Use an injected instant and cover:

- period changes exactly before, at, and after a boundary;
- `Duration` expiry;
- `ValidFrom` and `ValidTo`;
- daily and weekly recurrence;
- transaction-relative schedules anchored to the actual transaction start;
- highest-stack-level selection before intersecting different purposes;
- `MinChargingRate` handling in projections without violating harder limits;
- local-time-independent UTC behavior;
- `NumberPhases` changes by period;
- OCPP 2.0.1 schedules 1-3 rather than only the first schedule;
- external-constraint, station-max, transaction-default, EVSE-default, and transaction-specific precedence/intersection;
- expired and future profiles having no effect.

### Step 2: Run focused tests and confirm failure

Run: `go test ./internal/engine ./internal/ocpp/v16 ./internal/ocpp/v201 -run 'Test.*(Resolve|Schedule|Duration|Recurr|Phase|ExternalConstraint)'`

Expected: FAIL on currently ignored fields and scopes.

### Step 3: Implement protocol normalization and the resolver

Protocol managers convert wire models to normalized schedules. The engine resolver intersects all applicable limits at the requested instant. Do not duplicate schedule evaluation in the protocol packages.

When both amperes and watts constrain an AC EVSE, convert using the Wave 4 electrical snapshot. Preserve the winning sources for diagnostics and WebSocket output.

### Step 4: Run focused and package tests

Run:

```bash
go test ./internal/engine ./internal/ocpp/v16 ./internal/ocpp/v201
go test -race ./internal/engine ./internal/ocpp/v16 ./internal/ocpp/v201
```

Expected: PASS.

### Step 5: Commit

```bash
git add internal/engine/charging_allocation.go internal/engine/charging_allocation_test.go internal/ocpp/v16/profile_manager.go internal/ocpp/v16/profile_manager_test.go internal/ocpp/v201/profile_manager.go internal/ocpp/v201/profile_manager_test.go
git commit -m "fix: resolve complete smart charging schedules"
```

## Task 3: Bind transaction profiles to stable transaction identity

**Files:**
- Modify: `internal/engine/charging_allocation.go`
- Modify: `internal/engine/charging_allocation_test.go`
- Modify: `internal/ocpp/v16/handlers.go`
- Modify: `internal/ocpp/v16/profile_manager.go`
- Modify: `internal/ocpp/v16/bridge_test.go`
- Modify: `internal/ocpp/v201/handlers.go`
- Modify: `internal/ocpp/v201/profile_manager.go`
- Modify: `internal/ocpp/v201/bridge_test.go`

### Step 1: Write failing remote-start profile tests

For OCPP 1.6J, prove a `TxProfile` received in `RemoteStartTransaction` is initially bound to the stable local session ID and is updated when the CSMS assigns its integer transaction ID. Test both online and offline starts.

For OCPP 2.0.1, prove a transaction profile binds to the requested remote-start ID and the created stable transaction UUID without losing its scope after restart.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/ocpp/v16 ./internal/ocpp/v201 -run 'TestRemoteStart.*Profile'`

Expected: FAIL because the 1.6J profile currently cannot match before the CSMS transaction ID exists.

### Step 3: Implement stable binding and remapping hooks

Use the Wave 2 transaction identity store. Profile lookup must accept stable session ID and, where present, protocol transaction ID. Persist the association needed after restart.

### Step 4: Run tests

Run: `go test ./internal/ocpp/v16 ./internal/ocpp/v201 -run 'TestRemoteStart.*Profile'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/engine/charging_allocation.go internal/engine/charging_allocation_test.go internal/ocpp/v16/handlers.go internal/ocpp/v16/profile_manager.go internal/ocpp/v16/bridge_test.go internal/ocpp/v201/handlers.go internal/ocpp/v201/profile_manager.go internal/ocpp/v201/bridge_test.go
git commit -m "fix: bind transaction profiles to stable sessions"
```

## Task 4: Enforce a station-level power budget

**Files:**
- Create: `internal/engine/site_allocator.go`
- Create: `internal/engine/site_allocator_test.go`
- Create: `internal/engine/simulation.go`
- Modify: `internal/engine/engine.go`
- Modify: `internal/api/ws/snapshot.go`
- Modify: `internal/api/ws/messages.go`

### Step 1: Write failing multi-EVSE allocation tests

Cover:

- two and three EVSEs contending for an amperes limit;
- mixed single-phase and three-phase EVSEs under a watts limit;
- one EVSE below its share and redistribution of unused capacity;
- a suspended, unavailable, or full EV receiving zero;
- stable results independent of map iteration order;
- no aggregate overshoot within a documented floating-point tolerance;
- connector-local limits remaining enforced.

Use proportional allocation among eligible demand with stable EVSE ID tie-breaking for rounding. Do not add priority policy until it is represented explicitly in configuration.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/engine -run 'TestSiteAllocator'`

Expected: FAIL because station limits are currently applied to every connector independently.

### Step 3: Implement the allocator

Calculate demand first, allocate station capacity second, and integrate energy third. Publish requested and allocated values separately so callers can distinguish EV demand from EVSE delivery.

### Step 4: Run engine tests and race detector

Run:

```bash
go test ./internal/engine
go test -race ./internal/engine
```

Expected: PASS.

### Step 5: Commit

```bash
git add internal/engine/site_allocator.go internal/engine/site_allocator_test.go internal/engine/simulation.go internal/engine/engine.go internal/api/ws/snapshot.go internal/api/ws/messages.go
git commit -m "feat: allocate station capacity across EVSEs"
```

## Task 5: Make composite schedules use the runtime resolver

**Files:**
- Modify: `internal/engine/charging_allocation.go`
- Modify: `internal/engine/charging_allocation_test.go`
- Modify: `internal/ocpp/v16/handlers.go`
- Modify: `internal/ocpp/v16/handlers_test.go`
- Modify: `internal/ocpp/v201/handlers.go`
- Modify: `internal/ocpp/v201/handlers_test.go`

### Step 1: Write failing parity tests

For the same profile fixtures and clock instant, assert:

- the first composite-schedule period equals runtime allocation;
- requested A and W units are honored;
- OCPP 1.6J connector `0` and OCPP 2.0.1 EVSE `0` return station totals;
- per-EVSE queries return that EVSE only;
- schedule boundaries include all changes within the requested duration;
- unsupported unit conversion or unknown EVSE is rejected, not fabricated.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/engine ./internal/ocpp/v16 ./internal/ocpp/v201 -run 'Test.*CompositeSchedule.*Parity'`

Expected: FAIL because composite schedule construction currently diverges from runtime behavior.

### Step 3: Implement one projection API

Expose an engine method that projects allocations over a duration by walking normalized schedule boundaries. Both protocol handlers serialize this projection.

### Step 4: Run verification

Run:

```bash
go test ./internal/engine ./internal/ocpp/v16 ./internal/ocpp/v201
go test ./...
go vet ./...
```

Expected: PASS.

### Step 5: Commit

```bash
git add internal/engine/charging_allocation.go internal/engine/charging_allocation_test.go internal/ocpp/v16/handlers.go internal/ocpp/v16/handlers_test.go internal/ocpp/v201/handlers.go internal/ocpp/v201/handlers_test.go
git commit -m "fix: unify composite and runtime charging schedules"
```

## Wave 5 release gate

- Aggregate station power never exceeds the configured limit in deterministic simulation tests.
- Every supported schedule field has boundary tests.
- Invalid profiles are rejected at REST, OCPP 1.6J, and OCPP 2.0.1 ingress.
- Remote-start transaction profiles affect their intended session.
- Composite-schedule and runtime fixtures are byte-for-byte equivalent after unit normalization.
- `go test ./...`, focused race tests, and `go vet ./...` pass.
