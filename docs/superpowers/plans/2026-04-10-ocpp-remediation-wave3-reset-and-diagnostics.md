# OCPP Remediation Wave 3: Reset and Diagnostics Truthfulness

> **For agentic workers:** Complete Waves 1 and 2 first. This wave relies on reliable outbound messaging and correct route wiring.

**Goal:** Make OCPP 2.0.1 reset actually perform a reset-like transition in runtime state, and replace the diagnostics status stub with real 2.0.1 protocol signaling.

**Why this wave matters:** These are protocol-visible lifecycle behaviors that currently mislead the CSMS. The simulator acknowledges reset and diagnostics progress without actually producing the expected state transition or status messages.

---

## Problems To Fix

### Problem 1: OCPP 2.0.1 reset only reboots on paper

**Current behavior:**

- `OnReset` enqueues a `BootNotification`.
- It does not stop sessions.
- It does not clear bridge transaction builders.
- It does not clear active transaction mappings.
- It does not perform the same functional reset behavior as the 1.6J implementation.

**Why this is broken:** The CSMS is told reset was accepted, but the charging station runtime is not meaningfully reset.

### Problem 2: OCPP 2.0.1 diagnostics upload never reports log status

**Current behavior:**

- The diagnostics manager simulates local upload state transitions.
- `main.go` wires those transitions to `SendDiagnosticsStatusNotification`.
- In 2.0.1 that sender method is a stub returning `not implemented`.

**Why this is broken:** REST diagnostics simulation and CSMS-facing OCPP diagnostics behavior diverge.

---

## Scope

### In scope

- Add real runtime reset behavior for OCPP 2.0.1.
- Preserve current scheduled reset semantics, but make the eventual reset complete.
- Replace the 2.0.1 diagnostics stub with proper `LogStatusNotification` behavior.
- Update callback wiring so version-specific diagnostics reporting is modeled correctly.
- Add tests for reset and diagnostics behavior.

### Out of scope

- Monitoring.
- Composite schedule support.
- Doc cleanup beyond code comments needed to explain the new behavior.

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/ocpp/v201/handlers.go` | Modify | Make reset behavior real and protocol-correct |
| `internal/ocpp/v201/senders.go` | Modify | Implement 2.0.1 diagnostics/log status sender |
| `cmd/chargeghost/main.go` | Modify | Route diagnostics status through the correct version-aware sender |
| `internal/ocpp/firmware_manager.go` | Review/Modify | Ensure status callbacks map cleanly to protocol messages |
| `internal/ocpp/v201/*_test.go` | Modify/Create | Reset and diagnostics tests |

---

## Design

### Task 1: Define reset semantics for the simulator

- [ ] Document what reset means in ChargeGhost for both immediate and on-idle reset.
- [ ] Minimum reset behavior should include:
- [ ] stopping active charging sessions
- [ ] clearing bridge transaction builders and bridge transaction ID mappings
- [ ] preserving or intentionally resetting durable stores by explicit decision
- [ ] enqueueing a fresh `BootNotification` after reset completes

**Why:** Reset must be more than an acknowledgement and a boot message.

### Task 2: Implement immediate reset for OCPP 2.0.1

- [ ] Add a bridge-local reset helper used by `OnReset`.
- [ ] Stop active sessions through the engine.
- [ ] Clear bridge-local runtime transaction state.
- [ ] Trigger post-reset boot flow.

**Why:** Immediate reset must produce an observable runtime reset.

### Task 3: Implement on-idle reset completion path

- [ ] Keep the existing scheduled behavior if there are active transactions.
- [ ] Once the last active transaction ends, execute the same full reset helper.
- [ ] Ensure pending reset does not leave stale transaction builder state behind.

**Why:** Delayed reset should be delayed, not reduced.

### Task 4: Implement 2.0.1 diagnostics log status reporting

- [ ] Replace `SendDiagnosticsStatusNotification` stub logic with 2.0.1 `LogStatusNotification` sending.
- [ ] Map local diagnostics manager states to 2.0.1 log upload status values.
- [ ] Preserve the existing 1.6J diagnostics status behavior unchanged.

**Why:** 2.0.1 uses different protocol semantics than 1.6J, but the simulator still needs a real outbound status path.

### Task 5: Clean up main callback wiring

- [ ] Verify `diagOnStatus` uses the bridge abstraction cleanly.
- [ ] If the shared bridge method name becomes misleading, refactor the shared interface to represent diagnostics/log status in a version-neutral but honest way.

**Why:** The current shared name hides that one version is implemented and the other is not.

---

## Testing Requirements

- [ ] Add a test proving OCPP 2.0.1 immediate reset stops active charging state and triggers boot.
- [ ] Add a test proving OCPP 2.0.1 on-idle reset schedules and completes after the last transaction ends.
- [ ] Add a test proving no stale `txBuilders` or transaction mappings remain after reset.
- [ ] Add a test proving diagnostics trigger in 2.0.1 emits protocol status notifications.
- [ ] Add a regression test proving 1.6J reset and diagnostics behavior remain unchanged.

---

## Acceptance Criteria

- OCPP 2.0.1 reset now causes a real runtime reset.
- OCPP 2.0.1 diagnostics simulation emits CSMS-visible status notifications.
- No more `not implemented` diagnostics path is exercised from ordinary runtime behavior.

---

## Validation

- `go test ./internal/ocpp/v201/... -v`
- `go test ./cmd/... ./internal/...`

---

## Risks

- Reset semantics must not create deadlocks by stopping sessions while bridge state is being mutated.
- If reset is broadened too far, it may unexpectedly wipe durable state that should survive reboot.
