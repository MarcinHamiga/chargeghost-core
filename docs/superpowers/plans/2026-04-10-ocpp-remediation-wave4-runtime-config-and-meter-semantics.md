# OCPP Remediation Wave 4: Runtime Config and Meter Semantics

> **For agentic workers:** Complete Waves 1 through 3 first. This wave assumes the simulator can already send reliably and report reset and diagnostics truthfully.

**Goal:** Make runtime OCPP config changes actually affect live behavior and ensure meter context or trigger information is preserved instead of silently normalized away.

**Why this wave matters:** The current system exposes config mutation APIs and meter send APIs that look flexible but often only update stored values or ignore the caller's intended message semantics.

---

## Problems To Fix

### Problem 1: REST config changes often only mutate storage

**Current behavior:**

- REST config changes call `SetConfigValue` directly.
- Heartbeat runtime application exists in inbound OCPP handlers, but not in the REST path.
- The meter ticker interval is only read at startup.
- OCPP 2.0.1 writable device model values can be changed through REST without changing live runtime behavior.

**Why this is broken:** It creates fake configurability.

### Problem 2: Meter context is ignored

**Current behavior:**

- The send API accepts context values such as `Sample.Clock` and `Trigger`.
- OCPP 1.6J always emits `SamplePeriodic`.
- OCPP 2.0.1 always emits a periodic trigger reason.

**Why this is broken:** Triggered or raw meter sends cannot preserve their intended protocol meaning.

---

## Scope

### In scope

- Add runtime application logic for important config keys and device model variables.
- Make heartbeat updates live from REST as well as inbound protocol requests.
- Make meter interval updates live without restarting the process.
- Preserve meter context and trigger semantics where the protocol supports them.
- Add tests for runtime config application and meter context mapping.

### Out of scope

- Broader capability advertisement and docs alignment.
- Monitoring implementation.

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/api/handlers/ocpp.go` | Modify | Route REST config updates through runtime-aware application |
| `cmd/chargeghost/main.go` | Modify | Own runtime reconfiguration plumbing |
| `internal/ocpp/meter_ticker.go` | Modify | Support dynamic interval updates |
| `internal/ocpp/v16/config_keys.go` | Modify | Expose runtime-significant keys cleanly |
| `internal/ocpp/v16/senders.go` | Modify | Map supplied meter context to OCPP 1.6J reading context |
| `internal/ocpp/v201/device_model.go` | Modify | Support runtime application of selected variables |
| `internal/ocpp/v201/senders.go` | Modify | Map meter context to correct OCPP 2.0.1 trigger reason |
| `internal/ocpp/v201/transaction.go` | Modify | Allow trigger-aware meter update construction |
| `internal/api/*_test.go` | Modify/Create | REST config runtime tests |
| `internal/ocpp/*_test.go` | Modify/Create | Meter semantics tests |

---

## Design

### Task 1: Introduce a runtime config application layer

- [ ] Stop having the REST handler directly mutate stored config only.
- [ ] Add a runtime-aware application path that performs:
- [ ] storage update
- [ ] runtime side effect
- [ ] error reporting if runtime application fails

**Why:** Persistence and live behavior must be coordinated.

### Task 2: Apply heartbeat changes immediately from REST

- [ ] For 1.6J `HeartbeatInterval`, update `Bridge16.heartbeatInt` and restart the heartbeat loop.
- [ ] For 2.0.1 `OCPPCommCtrlr.HeartbeatInterval`, update `Bridge201.heartbeatInt` and restart the heartbeat loop.

**Why:** The same setting should behave the same whether changed by CSMS or REST.

### Task 3: Make meter sample interval live-updateable

- [ ] Replace the current startup-only ticker interval model.
- [ ] Add a runtime-owned interval source or restartable ticker controller.
- [ ] Support at least:
- [ ] 1.6J `MeterValueSampleInterval`
- [ ] 2.0.1 `SampledDataCtrlr.TxUpdatedInterval`

**Why:** Sample cadence is part of the observable charger behavior.

### Task 4: Preserve meter context in 1.6J

- [ ] Add a mapping from API strings to `types.ReadingContext`.
- [ ] Use the provided context when building sampled values.
- [ ] Normalize unsupported inputs explicitly, not silently.

**Why:** Raw and triggered meter sends should not always masquerade as periodic data.

### Task 5: Preserve trigger semantics in 2.0.1

- [ ] Add trigger-aware meter event construction.
- [ ] Map caller-supplied context to a sensible 2.0.1 `TriggerReason`.
- [ ] Keep a safe default for periodic sends.

**Why:** OCPP 2.0.1 shifted meter reporting into transaction events, but trigger meaning still matters.

---

## Testing Requirements

- [ ] Add a REST test proving heartbeat changes apply live in 1.6J.
- [ ] Add a REST test proving heartbeat changes apply live in 2.0.1.
- [ ] Add a test proving meter interval can change without restart.
- [ ] Add 1.6J tests for `Sample.Clock`, `Sample.Periodic`, and trigger-style contexts.
- [ ] Add 2.0.1 tests for context-to-trigger mapping.

---

## Acceptance Criteria

- REST config changes affect live runtime behavior for the keys that claim to matter at runtime.
- Meter reporting preserves requested semantics instead of always sending periodic values.
- Behavior is consistent across REST-originated changes and inbound OCPP changes.

---

## Validation

- `go test ./internal/api/... -v`
- `go test ./internal/ocpp/... -v`

---

## Risks

- Live ticker reconfiguration can create goroutine leaks if the old ticker is not stopped cleanly.
- Context mapping must be conservative so unsupported values do not produce invalid library constants.
