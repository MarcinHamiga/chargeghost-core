# OCPP Remediation Wave 2: Raw API and Local Authorization Correctness

> **For agentic workers:** Complete Wave 1 first. This wave assumes outbound queue behavior is trustworthy.

**Goal:** Fix externally exposed OCPP APIs that are currently miswired or semantically incorrect, specifically raw transaction routes and differential local authorization list updates.

**Why this wave matters:** These are direct correctness bugs visible to API consumers and the CSMS. They currently create false confidence that the simulator supports protocol behaviors it does not actually execute correctly.

---

## Problems To Fix

### Problem 1: Raw transaction REST routes do not work

**Current behavior:**

- `/api/v1/ocpp/raw/start-transaction` and `/api/v1/ocpp/raw/stop-transaction` are routed to connector handlers.
- Those handlers require a URL `{id}` parameter.
- The raw routes do not provide one.
- Calls fail with `invalid connector id` instead of producing OCPP traffic.

**Why this is broken:** These routes are documented as raw OCPP actions and currently do not do what they claim.

### Problem 2: Differential local auth list deletes are not implemented

**Current behavior:**

- In both 1.6J and 2.0.1, list entries without auth info are treated as accepted upserts.
- The shared local auth manager only inserts or overwrites entries.
- A CSMS trying to remove entries through a differential update cannot actually delete them.

**Why this is broken:** This violates expected local auth list semantics and can leave stale credentials authorized.

---

## Scope

### In scope

- Replace broken raw REST transaction route wiring with real OCPP handlers.
- Extend the REST-facing OCPP interface if needed to support raw transaction sends.
- Implement proper differential delete handling in both OCPP versions.
- Extend the shared local auth manager API or payload semantics to represent deletes cleanly.
- Add tests for both route correctness and local auth list diff behavior.

### Out of scope

- Queue durability internals.
- OCPP 2.0.1 reset behavior.
- Diagnostics/log status.

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/api/router.go` | Modify | Replace broken raw route wiring |
| `internal/api/handlers/ocpp.go` | Modify | Add dedicated raw transaction handlers |
| `internal/api/dto.go` | Modify | Add request DTOs if needed |
| `internal/ocpp/bridge.go` | Modify | Expose raw transaction send methods to REST surface if needed |
| `internal/ocpp/v16/handlers.go` | Modify | Correct local auth delete semantics |
| `internal/ocpp/v201/handlers.go` | Modify | Correct local auth delete semantics |
| `internal/ocpp/local_auth_list.go` | Modify | Support true deletes in differential updates |
| `internal/api/*_test.go` | Modify/Create | REST route coverage |
| `internal/ocpp/*_test.go` | Modify/Create | Local auth diff tests |

---

## Design

### Task 1: Create dedicated raw transaction REST handlers

- [ ] Add `SendRawStartTransaction` in `internal/api/handlers/ocpp.go`.
- [ ] Add `SendRawStopTransaction` in `internal/api/handlers/ocpp.go`.
- [ ] Define request bodies that include all protocol inputs needed by both versions.

Suggested request bodies:

- Start:
  - `connector_id`
  - `id_tag`
  - `meter_start` optional
  - `timestamp` optional
  - `reservation_id` optional
- Stop:
  - `transaction_id`
  - `meter_stop`
  - `timestamp` optional
  - `reason`

**Why:** Raw OCPP actions should call the OCPP bridge, not engine convenience endpoints.

### Task 2: Rewire router to use the dedicated handlers

- [ ] Replace `StartCharging(app.Engine)` under `/api/v1/ocpp/raw/start-transaction`.
- [ ] Replace `StopCharging(app.Engine)` under `/api/v1/ocpp/raw/stop-transaction`.

**Why:** The current route definitions are invalid by construction.

### Task 3: Extend the REST-facing OCPP interface if needed

- [ ] If the existing `handlers.OCPPSendAPI` is too small, add raw transaction send methods.
- [ ] Keep the abstraction version-agnostic.
- [ ] Ensure both `Bridge16` and `Bridge201` satisfy the interface.

**Why:** The REST layer should not need to know concrete bridge types.

### Task 4: Represent local auth deletes explicitly

- [ ] Choose an approach for the shared manager:
- [ ] Option A: Add a new method like `ApplyDiff(version, upserts, deletes)`.
- [ ] Option B: Add a delete marker field to `LocalAuthEntry` and teach `UpdateList` to honor it.

**Recommended:** Option A because it keeps delete semantics explicit and avoids overloading entry meaning.

**Why:** Differential deletion is a separate operation from updating token metadata.

### Task 5: Fix 1.6J local auth diff handling

- [ ] Update `OnSendLocalList` in `internal/ocpp/v16/handlers.go`.
- [ ] For differential updates, treat entries without `IdTagInfo` as delete requests.
- [ ] Preserve existing version mismatch behavior.

**Why:** This is the 1.6J protocol-level entry point for list mutation.

### Task 6: Fix 2.0.1 local auth diff handling

- [ ] Update `OnSendLocalList` in `internal/ocpp/v201/handlers.go`.
- [ ] For differential updates, treat entries without `IdTokenInfo` as delete requests.
- [ ] Preserve versioning and parent/group token handling.

**Why:** This is the 2.0.1 protocol-level entry point for list mutation.

---

## Testing Requirements

- [ ] Add REST tests proving raw start-transaction works and returns success instead of `invalid connector id`.
- [ ] Add REST tests proving raw stop-transaction works for both protocol versions.
- [ ] Add 1.6J tests for differential local auth deletes.
- [ ] Add 2.0.1 tests for differential local auth deletes.
- [ ] Add mixed diff tests covering add, update, and delete in a single request.

---

## Acceptance Criteria

- Raw OCPP transaction routes send OCPP traffic directly.
- Differential local auth list updates can delete entries in both 1.6J and 2.0.1.
- Existing full list replacement behavior continues to work.

---

## Validation

- `go test ./internal/api/... -v`
- `go test ./internal/ocpp/... -v`

---

## Risks

- Transaction ID behavior differs between 1.6J and 2.0.1, so raw REST responses may need version-aware shape or wording.
- If existing persisted local auth state assumes upsert-only semantics, tests should cover migration safety.
