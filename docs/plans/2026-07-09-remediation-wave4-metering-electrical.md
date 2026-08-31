# Remediation Wave 4: Metering and Electrical Correctness Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make EVSE meter registers cumulative and monotonic, calculate AC/DC power unambiguously, and use one electrical snapshot for simulation and OCPP measurands.

**Architecture:** Move meter ownership from sessions to a persistent per-EVSE meter bank. Add an explicit electrical supply model and one power function; sessions retain meter start/stop readings while actual current, power, phase count, and energy delta come from the shared snapshot.

**Tech Stack:** Go math/time, engine persistence, property-style tests, OCPP measurand builders.

## Task 1: Specify the electrical calculation table

**Files:**
- Create: `internal/engine/electrical.go`
- Create: `internal/engine/electrical_test.go`

**Step 1: Write failing table tests**

Include:

| Supply | Voltage | Reference | Current | Phases | PF | Expected power |
|---|---:|---|---:|---:|---:|---:|
| AC | 230 V | line-neutral | 32 A | 1 | 1.0 | 7,360 W |
| AC | 230 V | line-neutral | 32 A | 3 | 1.0 | 22,080 W |
| AC | 400 V | line-line | 32 A | 3 | 1.0 | `sqrt(3)*400*32` W |
| DC | 800 V | DC | 125 A | 1 | 1.0 | 100,000 W |

Also test invalid negative values, AC phase counts, PF outside `[0,1]`, and efficiency outside `(0,1]`.

**Step 2: Run tests**

```bash
go test ./internal/engine -run 'Electrical|Power' -count=1 -v
```

Expected: FAIL because the model does not exist.

**Step 3: Implement explicit supply types**

```go
type SupplyKind string
type VoltageReference string

type ElectricalSupply struct {
    Kind             SupplyKind
    VoltageV         float64
    VoltageReference VoltageReference
    RatedCurrentA    float64
    Phases           int
    PowerFactor      float64
    Efficiency       float64
}

func (s ElectricalSupply) PowerW(currentA float64, activePhases int) (float64, error)
```

Return grid-side imported power. Battery-side power may apply efficiency in Wave 8; clearly name both values.

**Step 4: Verify and commit**

```bash
go test ./internal/engine -run 'Electrical|Power' -count=1
git add internal/engine/electrical.go internal/engine/electrical_test.go
git commit -m "feat(engine): define explicit AC and DC power calculations"
```

## Task 2: Add backward-compatible connector electrical configuration

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/engine/connector.go`
- Modify: `cmd/chargeghost/station_runtime.go`
- Modify: `internal/api/dto.go`
- Modify: `docs/REST_API.md`

**Step 1: Write config migration/default tests**

Legacy `{voltage,current,phase}` must map to AC line-neutral, PF 1, efficiency 1. New configs may specify `supply_kind`, `voltage_reference`, `power_factor`, and `efficiency`. Reject DC with three phases and line-line single phase.

**Step 2: Add optional JSON fields**

Keep existing fields and defaults so current configs load unchanged. Expose normalized effective supply in status DTOs without removing old fields.

**Step 3: Update connector construction**

Make `NewConnector` accept/derive `ElectricalSupply`. Keep a compatibility wrapper until all tests and call sites migrate.

**Step 4: Verify and commit**

```bash
go test ./internal/config ./internal/api ./cmd/chargeghost -run 'Connector|Electrical|Config' -count=1
git add internal/config internal/engine/connector.go cmd/chargeghost/station_runtime.go internal/api docs/REST_API.md
git commit -m "feat(config): describe connector electrical supply explicitly"
```

## Task 3: Replace session-scoped meters with a persistent EVSE meter bank

**Files:**
- Create: `internal/engine/meter_bank.go`
- Create: `internal/engine/meter_bank_test.go`
- Modify: `internal/engine/energy_meter.go`
- Modify: `internal/engine/engine.go:517-669`
- Modify: `internal/engine/persist.go`
- Modify: `internal/engine/persist_test.go`

**Step 1: Write failing multi-session tests**

For one EVSE, run two sessions and assert:

```go
assert.Equal(t, first.MeterStop, second.MeterStart)
assert.Greater(t, second.MeterStop, first.MeterStop)
assert.Equal(t, second.MeterStop-second.MeterStart, second.EnergyCharged)
```

Repeat independently for two EVSEs. Assert a stopped EVSE still exposes its cumulative register.

**Step 2: Add MeterBank**

```go
type MeterBank struct { meters map[int]*EnergyMeter }
func (b *MeterBank) Reading(evseID int) float64
func (b *MeterBank) Apply(evseID int, deltaWh float64, actualCurrentA, powerW float64) error
```

Never delete a meter at transaction stop. Session energy derives from captured start and current readings.

**Step 3: Migrate existing state**

Single-mode global meter becomes EVSE 1 by default or the documented shared-meter identity. Multi-EVSE active meter values migrate directly; stopped EVSEs without saved meters start from zero with a migration notice because the old implementation discarded them.

**Step 4: Verify and commit**

```bash
go test ./internal/engine -run 'MeterBank|MultiEVSE|Cumulative|SaveLoadState' -count=1 -v
git add internal/engine
git commit -m "refactor(engine): persist cumulative EVSE meter registers"
```

## Task 4: Produce one immutable electrical snapshot per simulation step

**Files:**
- Create: `internal/engine/electrical_snapshot.go`
- Modify: `internal/engine/engine.go:1216-1349`
- Modify: `internal/engine/session.go`
- Modify: `internal/runtime/runtime_test.go`
- Modify: `internal/engine/engine_test.go`

**Step 1: Write failing snapshot/invariant tests**

Assert energy delta equals `GridPowerW * interval / 3600`, actual current is zero while suspended/disconnected, active phases affect both power and energy, and the meter register never decreases across random valid steps.

**Step 2: Add snapshot type**

```go
type ElectricalSnapshot struct {
    Timestamp       time.Time
    EVSEID          int
    VoltageV        float64
    OfferedCurrentA float64
    ActualCurrentA  float64
    ActivePhases    int
    GridPowerW      float64
    BatteryPowerW   float64
    RegisterWh      float64
    SessionEnergyWh float64
    StateOfCharge   float64
}
```

Compute once under the engine lock, update the meter from it, then expose a copy to OCPP and API readers.

**Step 3: Remove duplicated power formulas**

Delete direct `voltage * current * phases` calculations from engine paths after snapshot tests pass.

**Step 4: Verify and commit**

```bash
go test ./internal/engine ./internal/runtime -run 'ElectricalSnapshot|Energy|Power|Phase' -count=1
git add internal/engine internal/runtime/runtime_test.go
git commit -m "refactor(engine): drive simulation from electrical snapshots"
```

## Task 5: Make OCPP measurands consume the engine snapshot

**Files:**
- Modify: `internal/ocpp/v16/senders.go`
- Modify: `internal/ocpp/v16/senders_test.go`
- Modify: `internal/ocpp/v201/senders.go`
- Modify: `internal/ocpp/v201/transaction.go`
- Modify: `internal/ocpp/v201/senders_test.go`
- Modify: `internal/ocpp/v201/transaction_test.go`

**Step 1: Write cross-version parity tests**

Given one `ElectricalSnapshot`, assert both versions report equal energy, voltage, offered current, actual current, offered power, and actual power with version-appropriate encoding.

**Step 2: Change sender inputs**

Replace independent connector/meter lookups with `Engine.GetElectricalSnapshot(evseID)`. Preserve the event occurrence timestamp from Wave 2.

**Step 3: Add SoC only when valid**

If the engine has a configured EV and a known SoC, render it when requested. Otherwise reject unsupported measurand configuration or omit it according to protocol rules; never substitute an unrelated energy measurand.

**Step 4: Verify Wave 4**

```bash
go fmt ./...
go test ./internal/engine ./internal/runtime ./internal/ocpp/v16 ./internal/ocpp/v201 -count=1
go test ./...
go vet ./...
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/ocpp/v16 internal/ocpp/v201
git commit -m "fix(ocpp): report engine-derived electrical measurements"
```
