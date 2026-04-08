# OCPP 2.0.1 — Phase 6: Monitoring, Display, Cost

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement variable monitoring (Set/Clear/GetMonitoringReport), display message management (Set/Get/Clear), and CostUpdated handling with WebSocket broadcast.

**Architecture:** `MonitoringManager` registers monitors on device model variables. `DisplayMessageStore` is in-memory CRUD. `CostStore` tracks per-transaction costs. All broadcast state changes via WebSocket hub.

**Tech Stack:** Go 1.26+, `lorenzodonini/ocpp-go v0.19.0`, `stretchr/testify`

**Spec:** `docs/superpowers/specs/2026-04-07-ocpp-201-design.md` — "Variable Monitoring", "Display Messages", "Cost Updates" sections

**Prerequisite phases:** Phase 5 (smart charging) must be complete
**Next phase:** `2026-04-07-ocpp-201-phase7-remaining-ops.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/ocpp/v201/monitoring.go` | **Create** | `MonitoringManager` |
| `internal/ocpp/v201/monitoring_test.go` | **Create** | Monitor add/clear/list tests |
| `internal/ocpp/v201/display.go` | **Create** | `DisplayMessageStore` |
| `internal/ocpp/v201/display_test.go` | **Create** | Display CRUD tests |
| `internal/ocpp/v201/cost.go` | **Create** | `CostStore` |
| `internal/ocpp/v201/cost_test.go` | **Create** | Cost update/get/clear tests |
| `internal/ocpp/v201/handlers.go` | Modify | Diagnostics, display, tariff handlers |
| `internal/ocpp/v201/bridge.go` | Modify | Add manager fields, register handlers |

---

### Task 15: MonitoringManager

See `2026-04-07-ocpp-201.md` Task 15 for complete code.

- [ ] **Step 1: Write failing tests** — `TestMonitoringManager_AddMonitor`, `_ClearMonitor`, `_GetAllMonitors`, `_UnknownVariable`
- [ ] **Step 2: Run tests to verify they fail**
- [ ] **Step 3: Implement MonitoringManager** — `monitoring.go` with `NewMonitoringManager()`, `AddMonitor()`, `ClearMonitor()`, `GetAllMonitors()`
- [ ] **Step 4: Run tests to verify they pass**
- [ ] **Step 5: Add diagnostics handlers** — `OnSetVariableMonitoring`, `OnClearVariableMonitoring`, `OnGetMonitoringReport`, `OnSetMonitoringBase`, `OnSetMonitoringLevel`, `OnCustomerInformation`, `OnGetLog`
- [ ] **Step 6: Add monitoringManager field and register handler** — `SetDiagnosticsHandler(b)`
- [ ] **Step 7: Build and test**
- [ ] **Step 8: Commit**

---

### Task 16: DisplayMessageStore

See `2026-04-07-ocpp-201.md` Task 16 for complete code.

- [ ] **Step 1: Write failing tests** — `TestDisplayMessageStore_SetAndGet`, `_Clear`, `_GetAll`
- [ ] **Step 2: Run tests to verify they fail**
- [ ] **Step 3: Implement DisplayMessageStore** — `display.go` with `NewDisplayMessageStore()`, `Set()`, `Get()`, `Clear()`, `GetAll()`
- [ ] **Step 4: Run tests to verify they pass**
- [ ] **Step 5: Add display handlers** — `OnSetDisplayMessage` (with WebSocket broadcast), `OnClearDisplay`, `OnGetDisplayMessages`
- [ ] **Step 6: Add displayStore to Bridge201** — `SetDisplayHandler(b)`
- [ ] **Step 7: Build and test**
- [ ] **Step 8: Commit**

---

### Task 17: CostUpdated Handler

See `2026-04-07-ocpp-201.md` Task 17 for complete code.

- [ ] **Step 1: Write failing test** — `TestCostStore_UpdateAndGet`, `_GetUnknown`, `_Clear`
- [ ] **Step 2: Run test to verify it fails**
- [ ] **Step 3: Implement CostStore** — `cost.go` with `NewCostStore()`, `Update()`, `Get()`, `Clear()`
- [ ] **Step 4: Run tests**
- [ ] **Step 5: Add CostUpdated handler** — stores cost, broadcasts via WebSocket
- [ ] **Step 6: Add costStore to Bridge201** — `SetTariffCostHandler(b)`
- [ ] **Step 7: Build and test**
- [ ] **Step 8: Commit**
