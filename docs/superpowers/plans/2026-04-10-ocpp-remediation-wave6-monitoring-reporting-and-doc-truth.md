# OCPP Remediation Wave 6: Monitoring, Reporting, Composite Schedule, and Documentation Truth

> **For agentic workers:** Complete Waves 1 through 5 first. This wave should only start once the core communication and control paths are already trustworthy.

**Goal:** Resolve the remaining 2.0.1 faux-features around monitoring, reporting, and composite schedule support, then align documentation and runtime feature advertisement with what the simulator truly supports.

**Why this wave matters:** After the major breaking issues are fixed, the remaining risk is feature overstatement. Monitoring, report scoping, and REST composite schedule behavior currently appear more complete than they are.

---

## Problems To Fix

### Problem 1: Monitoring is storage-only

**Current behavior:**

- Monitors can be added and listed.
- No monitor is ever evaluated against variable changes.
- No `NotifyEvent` path exists.
- `SetMonitoringBase` and `SetMonitoringLevel` do not affect behavior.

**Why this is broken:** The simulator acknowledges monitoring configuration without actually monitoring anything.

### Problem 2: Report requests ignore scope and requested base

**Current behavior:**

- `GetReport` and `GetBaseReport` dump the full device model.
- Request filtering and report base semantics are not honored.

**Why this is broken:** The CSMS cannot rely on scoped or base-specific reports.

### Problem 3: REST composite schedule in 2.0.1 is effectively empty

**Current behavior:**

- The REST endpoint exists for the version-agnostic profile manager API.
- The 2.0.1 implementation returns an empty schedule.
- The inbound OCPP `GetCompositeSchedule` correctly rejects as unimplemented.

**Why this is broken:** The REST surface implies support that the implementation does not provide.

### Problem 4: Docs and `/about` do not reliably match runtime capability

**Current behavior:**

- `/about` still advertises only `1.6J`.
- Feature lists overstate queue durability and other OCPP capabilities.
- REST docs include routes and feature claims that outpace implementation reality.

**Why this is broken:** Documentation and runtime metadata are part of the public interface.

---

## Scope

### In scope

- Decide whether to implement or explicitly narrow 2.0.1 monitoring.
- Respect request scoping for `GetReport` and `GetBaseReport`, or explicitly reject unsupported combinations.
- Either implement 2.0.1 composite schedule generation for REST or disable/hide the endpoint for that version.
- Add a version-aware and capability-aware `/about` response.
- Update all OCPP docs to match the actual supported surface.

### Out of scope

- Large new product areas unrelated to the identified faux-features.

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/ocpp/v201/monitoring.go` | Modify | Implement monitor evaluation or narrow support |
| `internal/ocpp/v201/handlers.go` | Modify | Respect report scope, implement or reject monitoring-related commands honestly |
| `internal/ocpp/v201/device_model.go` | Modify | Provide variable-change hooks or filtered report building |
| `internal/ocpp/v201/profile_manager.go` | Modify | Implement composite schedule or keep version-specific non-support |
| `internal/api/handlers/charging_profiles.go` | Modify | Handle version-specific composite schedule truthfully |
| `internal/api/handlers/about.go` | Modify | Make feature advertisement version-aware |
| `REST_API.md` | Modify | Match actual OCPP behavior |
| `docs/REST_API.md` | Modify | Match actual OCPP behavior |
| `README.md` | Modify | Align public claims with real support |

---

## Design

### Task 1: Decide whether monitoring is implemented or unsupported

- [ ] Choose one path:
- [ ] Path A: implement real monitor evaluation and outbound notification behavior.
- [ ] Path B: narrow support by rejecting monitor setup until real evaluation exists.

**Recommended:** If implementation is small enough, support threshold and periodic monitors only. Otherwise reject setup and document non-support.

**Why:** Storage-only monitoring is not a meaningful feature.

### Task 2: If implementing monitoring, connect it to variable changes

- [ ] Add a variable-change hook in the device model or bridge layer.
- [ ] Evaluate threshold, delta, and periodic monitor rules.
- [ ] Add outbound event notification support if required by the chosen scope.
- [ ] Make `SetMonitoringBase` and `SetMonitoringLevel` affect actual behavior.

**Why:** Monitoring configuration must affect emitted behavior.

### Task 3: Respect report request scope

- [ ] Add filtered report builders for `GetReport`.
- [ ] Respect requested base in `GetBaseReport`, or reject unsupported bases explicitly.
- [ ] Keep the asynchronous report delivery shape intact.

**Why:** Reports should contain what was asked for, not always everything.

### Task 4: Resolve 2.0.1 REST composite schedule ambiguity

- [ ] Decide whether to implement `ChargingProfileManager201.GetCompositeSchedule`.
- [ ] If implemented, reuse existing effective-limit logic where possible.
- [ ] If not implemented, make the REST endpoint version-aware and return an explicit unsupported result for 2.0.1.

**Why:** Empty success responses are another faux-feature.

### Task 5: Make `/about` and docs capability-aware

- [ ] Stop hardcoding `1.6J` as the only OCPP version in `/about`.
- [ ] Expose supported features based on configured bridge and actual capability.
- [ ] Update `REST_API.md`, `docs/REST_API.md`, and `README.md` to distinguish:
- [ ] supported
- [ ] partially supported
- [ ] unsupported

**Why:** Runtime metadata and docs must not overpromise.

---

## Testing Requirements

- [ ] Add tests for monitoring behavior if implemented, or tests for explicit rejection if narrowed.
- [ ] Add tests for filtered `GetReport` behavior.
- [ ] Add tests for requested base handling in `GetBaseReport`.
- [ ] Add tests for 2.0.1 REST composite schedule behavior.
- [ ] Add tests for `/about` output reflecting configured version and capability.

---

## Acceptance Criteria

- No storage-only or empty-success feature remains in the targeted monitoring, reporting, or composite schedule paths.
- `/about`, REST docs, and README match the true implemented support surface.

---

## Validation

- `go test ./internal/ocpp/v201/... -v`
- `go test ./internal/api/... -v`
- Manual review of all OCPP-related docs for truthfulness.

---

## Risks

- Full monitoring implementation may grow larger than the rest of the wave. If so, narrow support explicitly instead of shipping a partial fake feature.
- Docs drift can recur; capability-aware generation or shared constants may be worth considering if this surface keeps changing.
