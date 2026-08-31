# ChargeGhost Remediation Wave 8: Physical Realism and Cross-Protocol Conformance

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task only after Waves 1-7 pass. Introduce higher-fidelity physics behind an explicit station-model version and preserve deterministic tests.

**Goal:** Evolve the simulator from an ideal energy counter into a configurable EV-plus-EVSE model with readiness/contactors, EV acceptance and taper, conversion losses, site allocation, fault response, and equivalent behavior through OCPP 1.6J and 2.0.1.

**Architecture:** The simulation advances a deterministic physical plant. Commands change intentions and discrete state; the plant determines what power can actually flow. Protocol adapters observe the same domain events. `ideal_v1` remains a migration mode, while `station_v2` enables the higher-fidelity model and becomes the target default after soak testing.

**Tech Stack:** Go, fixed-timestep runtime, injected monotonic/wall clocks, seeded deterministic fixtures, property tests, fake OCPP CSMS harnesses.

---

## Compatibility policy

- Safety fixes from Waves 1-7 apply in every model version.
- Existing configurations without `simulationModel` load as `ideal_v1` during the compatibility window.
- New configurations default to `station_v2` after release-gate approval.
- REST and WebSocket payloads add requested/delivered power and physical-state fields without removing existing fields in the first release.
- Seeded scenario fixtures must be deterministic across machines.

## Task 1: Version the simulation model and configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Create: `internal/engine/plant_config.go`
- Create: `internal/engine/plant_config_test.go`
- Modify: `internal/engine/persist.go`
- Modify: `internal/engine/persist_test.go`
- Add fixtures: `internal/engine/testdata/persistence/`
- Modify: `docs/REST_API.md`

### Step 1: Write failing migration tests

Cover:

- legacy config loads as `ideal_v1` with prior nominal behavior;
- a complete `station_v2` config validates AC and DC hardware separately;
- impossible values are rejected: nonpositive voltage/capacity, efficiency outside `(0,1]`, unsorted SoC curve, negative thermal constants, AC phase count outside 1-3;
- persistence records the model version and plant state;
- downgrade/unknown-version behavior fails safely with an actionable error.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/config ./internal/engine -run 'Test.*(SimulationModel|PlantConfig|PersistenceMigration)'`

Expected: FAIL because the model is currently implicit.

### Step 3: Implement versioned configuration

Add explicit supply, EVSE capability, cable, efficiency, EV defaults, timing, thermal/fault, and site-limit sections. Keep defaults centralized and documented.

### Step 4: Run tests

Run: `go test ./internal/config ./internal/engine -run 'Test.*(SimulationModel|PlantConfig|PersistenceMigration)'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/config/config.go internal/config/config_test.go internal/engine/plant_config.go internal/engine/plant_config_test.go internal/engine/persist.go internal/engine/persist_test.go internal/engine/testdata/persistence docs/REST_API.md
git commit -m "feat: version the ChargeGhost physical model"
```

## Task 2: Model EV capability, battery state, acceptance, and losses

**Files:**
- Create: `internal/engine/vehicle.go`
- Create: `internal/engine/vehicle_test.go`
- Create: `internal/engine/battery.go`
- Create: `internal/engine/battery_test.go`
- Modify: `internal/engine/session.go`
- Modify: `internal/engine/simulation.go`

### Step 1: Write failing plant-equation tests

Cover:

- configurable initial SoC rather than every session starting at zero;
- battery capacity and persisted SoC across unplug/replug for the same simulated vehicle;
- AC maximum current/phases and DC maximum power/current;
- piecewise-linear acceptance/taper curve by SoC;
- EV requested power distinct from EVSE allocated power;
- charger/cable efficiency and auxiliary load;
- grid import, vehicle input, and battery-stored energy conservation within tolerance;
- no negative energy, SoC below zero, or SoC above 100%;
- full battery requests zero and cannot resume energy transfer without changed demand/SoC;
- fixed-step convergence across 50 ms, 100 ms, and 200 ms outer tick patterns.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/engine -run 'Test.*(Vehicle|Battery|Acceptance|Efficiency|EnergyConservation)'`

Expected: FAIL because current simulation is an ideal `P=V*I*phases` integrator.

### Step 3: Implement EV and battery plant models

Represent an attached vehicle independently from a transaction. Integrate battery energy from delivered DC/AC-equivalent energy after losses. Keep register energy as grid-side import. Use explicit AC voltage basis from Wave 4.

### Step 4: Run tests and randomized invariants

Run:

```bash
go test ./internal/engine -run 'Test.*(Vehicle|Battery|Acceptance|Efficiency|EnergyConservation)'
go test ./internal/engine -run 'Fuzz|Property' -count=20
```

Expected: PASS.

### Step 5: Commit

```bash
git add internal/engine/vehicle.go internal/engine/vehicle_test.go internal/engine/battery.go internal/engine/battery_test.go internal/engine/session.go internal/engine/simulation.go
git commit -m "feat: simulate EV acceptance and battery energy"
```

## Task 3: Model cable, lock, readiness, and contactor sequencing

**Files:**
- Create: `internal/engine/evse_plant.go`
- Create: `internal/engine/evse_plant_test.go`
- Modify: `internal/engine/connector.go`
- Modify: `internal/engine/state.go`
- Modify: `internal/engine/simulation.go`
- Modify: `internal/api/handlers.go`
- Modify: `internal/api/ws/snapshot.go`
- Modify: `internal/api/ws/messages.go`

### Step 1: Write failing deterministic sequence tests

Model and test:

- cable inserted does not immediately imply power flow;
- optional cable lock engages before contactor close;
- authorization/session intent, EV readiness, EVSE readiness, allocation, and fault-free state are all required for contactor closure;
- configurable deterministic precharge/contactor delays;
- suspension opens the contactor but can retain the transaction and cable lock;
- stop opens contactor before unlock;
- unplug is impossible while a physically locked fixed cable is modeled, unless a force/fault API is explicitly invoked;
- unlock changes the lock fact only and never removes the cable;
- power is exactly zero whenever the contactor is open.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/engine -run 'TestEVSEPlant|Test.*(Contactor|CableLock|Readiness)'`

Expected: FAIL because protocol status currently stands in for physical plant state.

### Step 3: Implement the discrete EVSE plant

Advance timers inside the fixed-step simulation using injected time/state, never `time.Sleep`. Project OCPP status from plant plus operational facts; do not expose internal transitional states as invalid protocol enums.

### Step 4: Run tests and race detector

Run:

```bash
go test ./internal/engine ./internal/runtime
go test -race ./internal/engine ./internal/runtime
```

Expected: PASS.

### Step 5: Commit

```bash
git add internal/engine/evse_plant.go internal/engine/evse_plant_test.go internal/engine/connector.go internal/engine/state.go internal/engine/simulation.go internal/api/handlers.go internal/api/ws/snapshot.go internal/api/ws/messages.go
git commit -m "feat: simulate EVSE contactor and cable readiness"
```

## Task 4: Model faults and protective reactions

**Files:**
- Create: `internal/engine/protection.go`
- Create: `internal/engine/protection_test.go`
- Create: `internal/engine/fault.go`
- Create: `internal/engine/fault_test.go`
- Modify: `internal/engine/simulation.go`
- Modify: `internal/ocpp/v16/senders.go`
- Modify: `internal/ocpp/v201/senders.go`

### Step 1: Write failing protection scenarios

Cover:

- overcurrent, over/undervoltage, overtemperature, ground/isolation fault, emergency stop, and grid-power loss as configurable injected conditions;
- debounce/trip delay where physically appropriate;
- contactor opening before or atomically with reported loss of charging;
- configurable transaction stop versus suspension policy per fault class;
- latched versus auto-clearing faults;
- correct OCPP 1.6J error/status projection and OCPP 2.0.1 status/event/stop reason;
- recovery requires the modeled condition and latch policy to permit it;
- metering stops at trip time with no post-fault energy growth.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/engine ./internal/ocpp/v16 ./internal/ocpp/v201 -run 'Test.*(Protection|Overcurrent|Voltage|Temperature|GroundFault|PowerLoss)'`

Expected: FAIL because faults are currently mostly externally assigned statuses.

### Step 3: Implement protective logic

Keep physical trip facts distinct from protocol error-code projections. Store structured fault type, source, severity, active/latched state, onset, clear time, and stop/suspend policy.

### Step 4: Run tests

Run: `go test ./internal/engine ./internal/ocpp/v16 ./internal/ocpp/v201 -run 'Test.*(Protection|Fault|PowerLoss)'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/engine/protection.go internal/engine/protection_test.go internal/engine/fault.go internal/engine/fault_test.go internal/engine/simulation.go internal/ocpp/v16/senders.go internal/ocpp/v201/senders.go
git commit -m "feat: simulate EVSE protective reactions"
```

## Task 5: Implement configurable transaction start/stop points

**Files:**
- Create: `internal/engine/transaction_policy.go`
- Create: `internal/engine/transaction_policy_test.go`
- Modify: `internal/engine/session.go`
- Modify: `internal/engine/connector.go`
- Modify: `internal/ocpp/v16/handlers.go`
- Modify: `internal/ocpp/v201/handlers.go`

### Step 1: Write failing policy state-machine tests

For each supported policy, test whether the transaction starts/stops at:

- authorized;
- cable/EV connected;
- power path ready/energy transfer;
- deauthorized;
- EV disconnected;
- energy transfer ended.

Prove OCPP 2.0.1 `TxStartPoint`/`TxStopPoint` variables affect behavior. Keep OCPP 1.6J behavior explicit and compatible with its transaction model. Test ordering when several points occur in one simulation step.

### Step 2: Run tests and confirm failure

Run: `go test ./internal/engine ./internal/ocpp/v16 ./internal/ocpp/v201 -run 'TestTransactionPolicy|Test.*Tx(Start|Stop)Point'`

Expected: FAIL because transaction lifecycle is currently coupled directly to commands.

### Step 3: Implement policy evaluation over domain facts

Commands change authorization, cable, or stop intent. A policy evaluator emits start/end domain events exactly once when facts cross configured points. Use idempotency keys for same-step transitions.

### Step 4: Run tests

Run: `go test ./internal/engine ./internal/ocpp/v16 ./internal/ocpp/v201 -run 'TestTransactionPolicy|Test.*Tx(Start|Stop)Point'`

Expected: PASS.

### Step 5: Commit

```bash
git add internal/engine/transaction_policy.go internal/engine/transaction_policy_test.go internal/engine/session.go internal/engine/connector.go internal/ocpp/v16/handlers.go internal/ocpp/v201/handlers.go
git commit -m "feat: evaluate configurable transaction points"
```

## Task 6: Create a protocol-neutral scenario harness

**Files:**
- Create: `internal/scenario/harness.go`
- Create: `internal/scenario/harness_test.go`
- Create: `internal/scenario/fixtures/basic_charge.json`
- Create: `internal/scenario/fixtures/offline_restart.json`
- Create: `internal/scenario/fixtures/smart_charging.json`
- Create: `internal/scenario/fixtures/fault_trip.json`
- Modify: `internal/ocpp/v16/scenario_test.go`
- Modify: `internal/ocpp/v201/scenario_test.go`

### Step 1: Define physical scenario fixtures

Each fixture contains initial configuration, clock, actions, expected physical snapshots, expected domain events, and protocol-specific projections. Include at least:

- ordinary AC charge with taper;
- local and remote starts;
- EV-side suspension and reconnect;
- offline start/meter/stop/restart/reconnect;
- reservation then authorized plug-in;
- station power sharing;
- protective trip and recovery;
- full battery.

### Step 2: Write failing cross-protocol equivalence tests

Run one physical scenario through 1.6J and 2.0.1 adapters. Assert identical engine snapshots and semantically equivalent lifecycle/meter events, allowing only protocol-required representation differences.

### Step 3: Run tests and confirm failure

Run: `go test ./internal/scenario ./internal/ocpp/v16 ./internal/ocpp/v201 -run 'TestScenario'`

Expected: FAIL until both adapters satisfy the shared contract.

### Step 4: Implement the deterministic harness and close gaps

Use a fake clock, fake transport, seeded IDs, temp persistence directory, and explicit reconnect/restart controls. Do not special-case expected values in production code.

### Step 5: Run tests repeatedly

Run:

```bash
go test ./internal/scenario ./internal/ocpp/v16 ./internal/ocpp/v201 -run 'TestScenario' -count=20
go test -race ./internal/scenario ./internal/ocpp/v16 ./internal/ocpp/v201
```

Expected: PASS without flakiness.

### Step 6: Commit

```bash
git add internal/scenario internal/ocpp/v16/scenario_test.go internal/ocpp/v201/scenario_test.go
git commit -m "test: compare physical behavior across OCPP versions"
```

## Task 7: Add failure injection, properties, and load verification

**Files:**
- Create: `internal/engine/invariants_test.go`
- Create: `internal/ocpp/outbox/failure_injection_test.go`
- Create: `internal/runtime/load_test.go`
- Modify: `internal/scenario/harness_test.go`

### Step 1: Add invariant/property tests

Generate bounded command and time-step sequences and assert:

- energy registers never decrease;
- no power flows with open contactor, no EV, full battery, or zero allocation;
- at most one active transaction per connector and station rule;
- aggregate delivery never exceeds site capacity;
- SoC and temperature remain within modeled bounds or produce a fault;
- domain-event sequence is causal and duplicate-free;
- every delivered outbox event was either acknowledged once or remains durably pending;
- restart at any persistence boundary converges to the same state.

### Step 2: Add transport/storage failure injection

Fail before/after outbox append, identity-store write, transport send, confirmation handling, and delete/ack. Verify retry and idempotency.

### Step 3: Add deterministic load cases

Run many EVSEs and long simulated durations without wall-clock sleeps. Measure that the spiral-of-death guard does not silently discard energy/state transitions; expose dropped simulation time as a metric/error.

### Step 4: Run verification

Run:

```bash
go test ./internal/engine ./internal/ocpp/outbox ./internal/runtime ./internal/scenario -count=20
go test -race ./...
go test ./... -cover
go vet ./...
```

Expected: PASS; record coverage and load baselines in the release notes.

### Step 5: Commit

```bash
git add internal/engine/invariants_test.go internal/ocpp/outbox/failure_injection_test.go internal/runtime/load_test.go internal/scenario/harness_test.go
git commit -m "test: harden simulation invariants and recovery"
```

## Task 8: Publish capability, migration, and validation documentation

**Files:**
- Create: `docs/physical-model.md`
- Create: `docs/conformance-matrix.md`
- Create: `docs/migrations/station-v2.md`
- Modify: `docs/ocpp16-capabilities.md`
- Modify: `docs/ocpp201-capabilities.md`
- Modify: `README.md`
- Modify: `docs/REST_API.md`

### Step 1: Document the mathematical and state model

Include units, voltage basis, phase handling, efficiency boundaries, energy-register location, acceptance interpolation, timing, contactor/lock behavior, fault policies, and known non-goals.

### Step 2: Publish an evidence-linked conformance matrix

Map each claimed behavior to a scenario/integration test. Clearly distinguish protocol schema compatibility, implemented behavior, and formal certification.

### Step 3: Document migration and rollout

Explain `ideal_v1` versus `station_v2`, persisted-state migration, changed defaults, API additions, feature flags, rollback constraints, and how to compare a recorded scenario before switching.

### Step 4: Run final program verification

Run:

```bash
go fmt ./...
go test ./...
go test -tags integration ./internal/ocpp/v201
go test -race ./...
go vet ./...
git diff --check
```

Expected: PASS.

### Step 5: Commit

```bash
git add docs/physical-model.md docs/conformance-matrix.md docs/migrations/station-v2.md docs/ocpp16-capabilities.md docs/ocpp201-capabilities.md README.md docs/REST_API.md
git commit -m "docs: publish station model and conformance evidence"
```

## Wave 8 release gate

- Every physical invariant is tested with deterministic time and property sequences.
- Grid energy, delivered energy, battery energy, and losses balance within documented tolerance.
- Contactors, cable locks, EV readiness, taper, full-battery behavior, and faults react physically.
- Equivalent physical scenarios produce equivalent outcomes through both OCPP adapters.
- Restart/failure injection demonstrates durable convergence.
- Capability and conformance claims link to passing evidence.
- The complete test, integration, race, vet, formatting, and diff checks pass before making `station_v2` the default.
