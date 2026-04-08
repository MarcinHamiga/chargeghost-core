# OCPP 2.0.1 — Phase 5: Smart Charging (2.0.1 Format)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement OCPP 2.0.1 smart charging profile management: Set/Clear/Get profiles with EVSE-level targeting, GetCompositeSchedule, ReportChargingProfiles, and EV charging schedule stubs.

**Architecture:** `ChargingProfileManager201` stores profiles in 2.0.1 format (EVSE-level, string transactionId). Same composite limit algorithm concept as v16, new message format.

**Tech Stack:** Go 1.26+, `lorenzodonini/ocpp-go v0.19.0`, `stretchr/testify`

**Spec:** `docs/superpowers/specs/2026-04-07-ocpp-201-design.md` — "Smart Charging" section

**Prerequisite phases:** Phase 4 (device model) must be complete
**Next phase:** `2026-04-07-ocpp-201-phase6-monitoring-display-cost.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/ocpp/v201/profile_manager.go` | **Create** | `ChargingProfileManager201` |
| `internal/ocpp/v201/handlers.go` | Modify | Smart charging handlers |
| `internal/ocpp/v201/bridge.go` | Modify | Add profileManager field, register handler |

---

### Task 14: ChargingProfileManager201

See `2026-04-07-ocpp-201.md` Task 14 for complete code.

- [ ] **Step 1: Implement profile manager** — `profile_manager.go` with `NewChargingProfileManager201()`, `SetProfile()`, `ClearProfile()`, `GetAllProfiles()`
- [ ] **Step 2: Add smart charging handlers** — `OnSetChargingProfile`, `OnClearChargingProfile`, `OnGetChargingProfiles` (with async `ReportChargingProfiles`), `OnGetCompositeSchedule` (stub), `OnNotifyEVChargingSchedule` (stub), `OnNotifyEVChargingNeeds` (stub)
- [ ] **Step 3: Add profileManager field and register handler** — `SetSmartChargingHandler(b)`
- [ ] **Step 4: Build and test**
- [ ] **Step 5: Commit**
