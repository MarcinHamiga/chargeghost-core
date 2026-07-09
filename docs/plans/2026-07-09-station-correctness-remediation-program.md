# Station Correctness Remediation Program Implementation Plan

> **For Codex:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Remediate every physical-simulation and OCPP 1.6J/2.0.1 correctness issue identified in the July 2026 audit through a dependency-ordered, backward-compatible implementation program.

**Architecture:** Keep the engine as the single source of truth, but replace transaction-critical callback delivery with immutable domain events and a durable typed outbox. Introduce orthogonal connector state, persistent EVSE meter registers, one shared electrical calculation, a station-level charging allocator, and protocol adapters that project and recover state truthfully.

**Tech Stack:** Go 1.26, `lorenzodonini/ocpp-go`, chi, JSON persistence, `testify`, Go race detector, build-tagged integration tests.

## Source Design

Read [the remediation design](2026-07-09-station-correctness-remediation-design.md) before executing any wave. Treat it and the wave plans below as the current authority over the older April 2026 remediation documents.

## Execution Order

| Wave | Plan | Depends on | Release effect |
|---|---|---|---|
| 1 | [Safety invariants](2026-07-09-remediation-wave1-safety-invariants.md) | None | Stops physically impossible behavior immediately |
| 2 | [Durable transaction delivery](2026-07-09-remediation-wave2-durable-transaction-delivery.md) | Wave 1 | Makes transaction traffic lossless across outages |
| 3 | [State and recovery](2026-07-09-remediation-wave3-state-and-recovery.md) | Waves 1–2 | Makes restarts and orthogonal state consistent |
| 4 | [Metering and electrical correctness](2026-07-09-remediation-wave4-metering-electrical.md) | Wave 3 | Makes registers, power, energy, and phases correct |
| 5 | [Smart charging](2026-07-09-remediation-wave5-smart-charging.md) | Wave 4 | Enforces and reports profiles correctly |
| 6 | [OCPP 1.6J behavior](2026-07-09-remediation-wave6-ocpp16.md) | Waves 2–5 | Corrects 1.6J identity, control, metering, and config |
| 7 | [OCPP 2.0.1 behavior](2026-07-09-remediation-wave7-ocpp201.md) | Waves 2–5 | Corrects 2.0.1 transactions, commands, and device model |
| 8 | [Physical realism and conformance](2026-07-09-remediation-wave8-physical-realism-conformance.md) | Waves 1–7 | Adds station-like EV/EVSE behavior and final release gates |

Do not execute Waves 6 and 7 concurrently: both alter shared bridge interfaces, runtime wiring, and conformance fixtures. Wave 8 begins only after both protocol waves pass cross-version scenarios.

## Finding Coverage Matrix

| Audit finding | Primary wave | Regression gate |
|---|---|---|
| Dispatcher prevents normal offline queueing; transient failures drop commands | 2 | Offline start/meter/stop survives disconnect and restart |
| 1.6J temporary transaction IDs are not remapped | 2, 6 | Stop and meter replay use CSMS-assigned ID |
| Pending remote starts bypass connector/session invariants | 1 | Pending start uses the same admission path as immediate start |
| Energy continues after EV disconnect when transaction stays open | 1 | Open cable always implies zero actual current and open contactor |
| Multi-EVSE register resets each session | 4 | Meter start of session N+1 equals prior meter stop |
| Station maximum is enforced independently per EVSE | 5 | Sum of EVSE power never exceeds station allocation |
| Schedule duration, phase count, multiple schedules, external constraints ignored | 5 | Resolver table and composite/runtime parity tests |
| Negative REST profile can decrease energy | 1, 5 | Validation plus monotonic-energy property test |
| Full battery can resume with current but no energy | 1, 8 | Resume rejected or remains suspended at zero acceptance |
| 2.0.1 active transaction cannot recover after restart | 2, 3, 7 | UUID/sequence/mapping restart scenario |
| Connector fault, lock, reservation, and pending-start state is incompletely persisted | 3 | Golden migration and round-trip tests |
| Periodic 2.0.1 event falsely reports Charging | 7 | Periodic event carries actual charging state |
| 2.0.1 reset scope, unspecified reservation, transaction status, token type, and trigger handling incorrect | 7 | One scenario per inbound use case |
| 1.6J and 2.0.1 composite schedule units/station-total behavior incorrect | 5–7 | A/W and station-total response tests |
| Runtime configuration accepts invalid values or has no effect | 6, 7 | Descriptor validation and live-application tests |
| Device model and NumberOfConnectors do not match hardware configuration | 6, 7 | Boot/report fixture generated from actual connectors |
| Reservation, fault, and availability overwrite each other | 3 | State-axis permutation tests |
| AC voltage interpretation, DC, losses, EV acceptance, taper, contactor, and lock behavior absent | 4, 8 | Electrical table tests and station lifecycle scenarios |

## Global Engineering Rules

1. Use test-driven development for every task. A failing focused test must demonstrate the defect before implementation.
2. Never persist a Go closure or rely on `interface{}` type identity for durable data.
3. Never acknowledge transaction-critical delivery before durable state is written.
4. Never calculate OCPP electrical values separately from the engine power calculation.
5. Never let protocol handlers mutate connector fields directly; call engine operations.
6. Never return Accepted for a command whose state change or required follow-up cannot be completed.
7. Preserve REST and WebSocket shapes unless the master plan explicitly authorizes a versioned addition.
8. Add `schema_version` before changing persisted structures and commit migration fixtures with the migration code.
9. Keep commits wave-local and small enough to revert without undoing a later wave.

## Shared Definitions Added During the Program

The exact package placement is specified by each wave, but later waves assume these concepts exist:

```go
type SessionID string

type DomainEvent struct {
    ID          string
    Type        EventType
    OccurredAt  time.Time
    ConnectorID int
    SessionID   SessionID
    Data        any
}

type ChargingAllocation struct {
    CurrentA float64
    PowerW   float64
    Phases   int
    Sources  []LimitSource
}
```

## Per-wave Completion Gate

Run after every wave:

```bash
go fmt ./...
go test ./...
go vet ./...
git status --short
```

Expected: formatting produces no unexplained changes; all tests and vet pass; only intentional wave files are modified.

Run after Waves 2, 3, 6, and 7:

```bash
go test -race ./internal/engine ./internal/runtime ./internal/ocpp/v16 ./internal/ocpp/v201
go test -tags integration ./internal/ocpp/v201 -v -timeout 90s
```

Expected: PASS with no race report and no integration timeout.

## Program Release Gates

### Gate A: Safety release after Wave 1

- No energy without plugged cable, active session, closed contactor-equivalent, and positive allocation.
- Pending remote start cannot bypass availability, fault, lock, reservation, or session-count checks.
- Energy and SoC cannot decrease through public control surfaces.

### Gate B: Transaction reliability release after Wave 3

- Start, meter, and stop events survive link loss, process restart, and transport errors.
- 1.6J mappings and 2.0.1 UUID/sequence state recover.
- Persisted data migrates from current production-shaped fixtures.

### Gate C: Metering and smart-charging release after Wave 5

- EVSE registers are cumulative and monotonic.
- Runtime allocation and composite schedules use one calculation.
- Aggregate station constraints, duration, phase counts, and all supported purposes are enforced.

### Gate D: Protocol-correct release after Wave 7

- The cross-version use-case matrix passes.
- Advertised configuration and device-model capabilities match executable behavior.
- Unsupported operations reject truthfully.

### Gate E: Station-realism release after Wave 8

- Scenario tests demonstrate cable, lock, authorization, contactor, readiness, energy flow, taper, faults, and stop behavior.
- Documentation distinguishes idealized, realistic, and unsupported behavior.

## Task 1: Execute Wave 1 and publish Gate A evidence

**Files:** See `2026-07-09-remediation-wave1-safety-invariants.md`.

**Step 1:** Execute the wave with `superpowers:test-driven-development`.

**Step 2:** Run the Gate A commands and record results in the pull request.

**Step 3:** Commit with `fix(engine): enforce physical session invariants`.

## Task 2: Execute Waves 2–3 and publish Gate B evidence

**Files:** See the Wave 2 and Wave 3 plans.

**Step 1:** Implement durable delivery before changing persistence schemas.

**Step 2:** Add and validate schema migrations and restart reconciliation.

**Step 3:** Run normal, race, integration, and golden-fixture tests.

**Step 4:** Commit wave changes separately.

## Task 3: Execute Waves 4–5 and publish Gate C evidence

**Files:** See the Wave 4 and Wave 5 plans.

**Step 1:** Land the shared electrical calculation before changing profile allocation.

**Step 2:** Make composite schedule tests and runtime allocation tests consume the same expected fixtures.

**Step 3:** Run multi-EVSE load tests and monotonic-meter property tests.

## Task 4: Execute Waves 6–7 sequentially and publish Gate D evidence

**Files:** See the protocol wave plans.

**Step 1:** Complete 1.6J and verify shared interfaces.

**Step 2:** Rebase the 2.0.1 wave onto those shared interfaces.

**Step 3:** Run cross-version scenarios after both waves.

## Task 5: Execute Wave 8 and publish Gate E evidence

**Files:** See `2026-07-09-remediation-wave8-physical-realism-conformance.md`.

**Step 1:** Add higher-fidelity behavior behind a station-model version.

**Step 2:** Promote it only after compatibility and conformance scenarios pass.

**Step 3:** Update README, API docs, capability output, and migration notes.
