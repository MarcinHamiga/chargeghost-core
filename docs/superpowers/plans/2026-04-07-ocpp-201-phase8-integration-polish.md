# OCPP 2.0.1 — Phase 8: Integration Tests & Polish

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Write integration tests with a mock CSMS, update REST API version routing, update documentation, and run final verification.

**Architecture:** Integration tests use `ocpp-go`'s `ocpp2.NewCSMS()` as an in-process mock CSMS on localhost. Tests cover boot→charge→stop flow, remote start/stop, device model round-trip, and offline queue drain.

**Tech Stack:** Go 1.26+, `lorenzodonini/ocpp-go v0.19.0`, `stretchr/testify`

**Spec:** `docs/superpowers/specs/2026-04-07-ocpp-201-design.md` — "Testing Strategy" section

**Prerequisite phases:** Phase 7 (remaining ops) must be complete

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/ocpp/v201/integration_test.go` | **Create** | End-to-end tests with mock CSMS |
| `internal/api/handlers/` | Modify | OCPP send handler uses `OCPPBridge` interface |
| `CLAUDE.md` | Modify | Updated project structure and design decisions |

---

### Task 20: Integration Test — Full Charging Flow

See `2026-04-07-ocpp-201.md` Task 20 for complete code.

- [ ] **Step 1: Write the integration test** — `integration_test.go` with build tag `//go:build integration`. Create `mockCSMSHandler` with channels for `bootReceived` and `transactionEvents`. Implement `OnBootNotification` (returns Accepted) and `OnTransactionEvent` (returns empty response). Write `TestIntegration_BootAndHeartbeat`: start mock CSMS on free port, create Bridge201, connect, assert BootNotification received with correct model/vendor.
- [ ] **Step 2: Run integration test** — `go test -run TestIntegration -tags integration ./internal/ocpp/v201/ -v -timeout 30s`. Fix compilation errors by adding no-op implementations for any CSMS handler methods the library requires.
- [ ] **Step 3: Commit**

---

### Task 21: REST API Version Routing

- [ ] **Step 1: Find the OCPP send handler** — locate `POST /api/v1/ocpp/send` handler in `internal/api/handlers/`
- [ ] **Step 2: Update to accept OCPPBridge interface** — if the handler's dependency references concrete `*Bridge`, update to `ocpp.OCPPBridge`
- [ ] **Step 3: Build and test** — `go build ./... && go test ./internal/api/... -v`
- [ ] **Step 4: Commit**

---

### Task 22: Documentation Update

- [ ] **Step 1: Update CLAUDE.md** — add v16/v201 package structure, add design decision #6 (OCPP version switching), update Dependencies table to note 2.0.1 support
- [ ] **Step 2: Commit**

---

### Task 23: Final Verification

- [ ] **Step 1: Full build** — `go build ./...`
- [ ] **Step 2: Full test suite** — `go test ./... -v`
- [ ] **Step 3: Integration tests** — `go test -tags integration ./internal/ocpp/v201/ -v -timeout 60s`
- [ ] **Step 4: Vet and format** — `go vet ./... && go fmt ./...`
- [ ] **Step 5: Final commit** (if any formatting changes)
