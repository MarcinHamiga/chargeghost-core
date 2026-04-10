# OCPP Remediation Wave 5: Control Path Corrections and Honest Responses

> **For agentic workers:** Complete Waves 1 through 4 first. This wave is about reducing misleading protocol success responses.

**Goal:** Fix control handlers that currently report success without producing an effect, and make unsupported 2.0.1 operations explicitly unsupported instead of accepted.

**Why this wave matters:** Even when core transport is fixed, protocol-level honesty still matters. A simulator that says "Accepted" or "Unlocked" without changing anything is harder to integrate with than one that rejects unsupported operations clearly.

---

## Problems To Fix

### Problem 1: 1.6J `UnlockConnector` always reports success

**Current behavior:** Returns `Unlocked` unconditionally and does not alter engine state.

**Why this is broken:** This is a pure accept-without-effect path.

### Problem 2: 1.6J `ClearChargingProfile` ignores `stackLevel`

**Current behavior:** The filter is parsed but ignored.

**Why this is broken:** It claims to honor request criteria that it does not actually apply.

### Problem 3: Several 2.0.1 handlers acknowledge unsupported functionality

**Current behavior:** Many handlers log and return success-like responses with no state change or follow-up message.

**Why this is broken:** This creates false interoperability and makes the simulator look more complete than it is.

---

## Scope

### In scope

- Fix 1.6J unlock behavior.
- Fix 1.6J charging profile clear filter behavior.
- Review 2.0.1 accept-without-effect handlers and convert them to explicit unsupported or rejected responses where appropriate.
- Add tests to pin the new honest behavior.

### Out of scope

- Full implementation of 2.0.1 monitoring or customer information workflows.
- Full 2.0.1 publish firmware support.

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/ocpp/v16/handlers.go` | Modify | Make unlock behavior real |
| `internal/ocpp/v16/profile_manager.go` | Modify | Honor `stackLevel` filter in profile clearing |
| `internal/ocpp/v201/handlers.go` | Modify | Replace fake success responses with explicit unsupported semantics |
| `internal/ocpp/v16/*_test.go` | Modify/Create | 1.6J control path tests |
| `internal/ocpp/v201/*_test.go` | Modify/Create | 2.0.1 unsupported response tests |

---

## Design

### Task 1: Make 1.6J unlock do something real

- [ ] Decide the simulator meaning of unlock.
- [ ] Minimum acceptable behavior: mirror 2.0.1 by calling `engine.Unplug(connectorId)` if that is the chosen lock simulation.
- [ ] If unlock cannot be honored, return a failure status instead of `Unlocked`.

**Why:** The current path is a fake success.

### Task 2: Honor `stackLevel` in profile clear logic

- [ ] Update `ClearChargingProfile` filtering in `internal/ocpp/v16/profile_manager.go`.
- [ ] Ensure combined filtering works across connector, profile ID, purpose, and stack level.

**Why:** The request criteria should match actual deletion behavior.

### Task 3: Audit and narrow unsupported 2.0.1 handlers

Target handlers:

- [ ] `OnNotifyEVChargingSchedule`
- [ ] `OnNotifyEVChargingNeeds`
- [ ] `OnSetMonitoringBase`
- [ ] `OnSetMonitoringLevel`
- [ ] `OnCustomerInformation`
- [ ] `OnGetLog`
- [ ] `OnPublishFirmware`
- [ ] `OnUnpublishFirmware`

- [ ] For each handler, decide whether to:
- [ ] implement minimal real behavior, or
- [ ] return protocol-appropriate unsupported or rejected status.

**Recommended:** Reject or mark unsupported unless the behavior is already implemented elsewhere.

**Why:** Honest non-support is better than accepted no-op behavior.

---

## Testing Requirements

- [ ] Add a 1.6J unlock test proving state changes or explicit failure.
- [ ] Add 1.6J charging profile clear tests with stack-level filtering.
- [ ] Add 2.0.1 tests proving formerly fake-success handlers now return explicit non-success statuses where applicable.

---

## Acceptance Criteria

- No known pure accept-without-effect path remains in the targeted 1.6J control handlers.
- Targeted 2.0.1 unsupported handlers no longer imply behavior the simulator does not implement.

---

## Validation

- `go test ./internal/ocpp/v16/... -v`
- `go test ./internal/ocpp/v201/... -v`

---

## Risks

- Some OCPP library enums may not have a perfect "unsupported" analogue for every action, so the exact returned non-success status must be chosen carefully.
