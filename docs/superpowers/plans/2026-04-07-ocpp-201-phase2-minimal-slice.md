# OCPP 2.0.1 — Phase 2: Minimal 2.0.1 Vertical Slice

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Get a minimal OCPP 2.0.1 bridge working end-to-end: connect to CSMS, BootNotification, Heartbeat, StatusNotification, and wire into `main.go`.

**Architecture:** Create `internal/ocpp/v201/` package with `Bridge201` implementing `OCPPBridge`. All non-boot/status methods are stubs returning errors. Wire into `main.go` behind the `ocpp_version: "2.0.1"` config flag.

**Tech Stack:** Go 1.26+, `lorenzodonini/ocpp-go v0.19.0` (`ocpp2.0.1/*` packages), `stretchr/testify`

**Spec:** `docs/superpowers/specs/2026-04-07-ocpp-201-design.md`

**Prerequisite phases:** Phase 1 (refactor) must be complete
**Next phase:** `2026-04-07-ocpp-201-phase3-transactions.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/ocpp/v201/bridge.go` | **Create** | `Bridge201` struct, connection lifecycle |
| `internal/ocpp/v201/senders.go` | **Create** | Boot, Heartbeat, StatusNotification + stubs |
| `internal/ocpp/v201/handlers.go` | **Create** | Provisioning + availability handler stubs |
| `internal/ocpp/v201/bridge_test.go` | **Create** | Construction tests |
| `internal/ocpp/v201/status_test.go` | **Create** | Status mapping tests |
| `internal/config/config.go` | Modify | Add `ConnectorType` field |
| `cmd/chargeghost/main.go` | Modify | Wire Bridge201 in version switch |

---

### Task 5: Bridge201 Scaffold — Boot + Heartbeat

**Files:**
- Create: `internal/ocpp/v201/bridge.go`
- Create: `internal/ocpp/v201/senders.go`
- Create: `internal/ocpp/v201/handlers.go`
- Create: `internal/ocpp/v201/bridge_test.go`

See the full plan file `2026-04-07-ocpp-201.md` Task 5 for complete code.

- [ ] **Step 1: Write failing test for Bridge201 construction** — `bridge_test.go` with `TestNewBridge_Creates`
- [ ] **Step 2: Run test to verify it fails** — `go test -run TestNewBridge_Creates ./internal/ocpp/v201/ -v`
- [ ] **Step 3: Implement Bridge201 struct** — `bridge.go` with full struct, `NewBridge()`, `SetManagers()`, `Start()`, `Stop()`, `IsConnected()`, `GetHeartbeatInterval()`, `Dispatcher()`, `heartbeatLoop()`, `drainQueue()`
- [ ] **Step 4: Implement senders for Boot + Heartbeat** — `senders.go` with `SendBootNotification()`, `SendHeartbeat()`, and stubs for all other `OCPPBridge` methods
- [ ] **Step 5: Add provisioning handler stubs** — `handlers.go` with `OnGetBaseReport`, `OnGetReport`, `OnGetVariables`, `OnReset`, `OnSetNetworkProfile`, `OnSetVariables`
- [ ] **Step 6: Run tests** — `go test ./internal/ocpp/v201/ -v && go build ./...`
- [ ] **Step 7: Commit**

---

### Task 6: StatusNotification (2.0.1 Format)

**Files:**
- Modify: `internal/ocpp/v201/senders.go`
- Modify: `internal/ocpp/v201/handlers.go`
- Create: `internal/ocpp/v201/status_test.go`

- [ ] **Step 1: Write failing test** — `TestMapConnectorStatus` covering Available→Available, Charging→Occupied, Reserved→Reserved, Unavailable→Unavailable, Faulted→Faulted
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement `mapConnectorStatus()` and `SendStatusNotification()`** — maps 1.6 states to 2.0.1 `ConnectorStatus` enum (Available/Occupied/Reserved/Unavailable/Faulted)
- [ ] **Step 4: Add `OnChangeAvailability` handler** — handles station-level (evseId=0) by iterating all connectors, EVSE-level by targeting specific connector
- [ ] **Step 5: Run tests**
- [ ] **Step 6: Commit**

---

### Task 7: Wire Bridge201 into main.go

**Files:**
- Modify: `cmd/chargeghost/main.go`
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add `ConnectorType` to config** — `ConnectorType string` field, default `"cType2"`
- [ ] **Step 2: Wire Bridge201 in main.go** — add `case "2.0.1":` to version switch, construct `v201.NewBridge()`, call `SetManagers()`
- [ ] **Step 3: Build and verify** — `go build ./...`
- [ ] **Step 4: Commit**
