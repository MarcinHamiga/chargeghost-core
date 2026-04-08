# OCPP 2.0.1 — Phase 4: Device Model

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Component/Variable device model with ~25 variables, supporting GetVariables, SetVariables, GetBaseReport, and NotifyReport.

**Architecture:** `DeviceModel` struct stores variables as `map[componentVariableKey]variableEntry`. Static variables populated from config at startup. CSMS SetVariables rejects read-only mutations. GetBaseReport triggers async NotifyReport with all variables.

**Tech Stack:** Go 1.26+, `lorenzodonini/ocpp-go v0.19.0`, `stretchr/testify`

**Spec:** `docs/superpowers/specs/2026-04-07-ocpp-201-design.md` — "Device Model" section

**Prerequisite phases:** Phase 3 (transactions) must be complete
**Next phase:** `2026-04-07-ocpp-201-phase5-smart-charging.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/ocpp/v201/device_model.go` | **Create** | `DeviceModel` struct, Get/Set/Report methods |
| `internal/ocpp/v201/device_model_test.go` | **Create** | Unit tests for variable CRUD |
| `internal/ocpp/v201/bridge.go` | Modify | Add `deviceModel` field, populate defaults |
| `internal/ocpp/v201/handlers.go` | Modify | Wire GetVariables/SetVariables/GetBaseReport |

---

### Task 12: DeviceModel Core

See `2026-04-07-ocpp-201.md` Task 12 for complete code.

- [ ] **Step 1: Write failing tests** — `TestDeviceModel_GetStaticVariable`, `_SetReadOnlyRejected`, `_SetWritableAccepted`, `_GetUnknownVariable`, `_AllVariables`
- [ ] **Step 2: Run tests to verify they fail**
- [ ] **Step 3: Implement DeviceModel** — `device_model.go` with `NewDeviceModel()`, `SetVariable()`, `SetVariableExternal()`, `GetVariable()`, `AllVariables()`, `PopulateDefaults()`, `BuildGetVariablesResponse()`, `BuildSetVariablesResponse()`, `BuildNotifyReportData()`
- [ ] **Step 4: Run tests to verify they pass**
- [ ] **Step 5: Commit**

---

### Task 13: Wire Device Model Into Handlers

See `2026-04-07-ocpp-201.md` Task 13 for complete code.

- [ ] **Step 1: Add DeviceModel to Bridge201** — field + `PopulateDefaults()` in constructor
- [ ] **Step 2: Wire handlers** — update `OnGetVariables` to delegate to `deviceModel.BuildGetVariablesResponse()`, `OnSetVariables` to `BuildSetVariablesResponse()`, `OnGetBaseReport` to async `BuildNotifyReportData()` + `SendRequestAsync(NotifyReport)`
- [ ] **Step 3: Build and test**
- [ ] **Step 4: Commit**
