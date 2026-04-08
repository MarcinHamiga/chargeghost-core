# OCPP 2.0.1 — Phase 7: Remaining Operations

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Reset with OnIdle, firmware management, DataTransfer, LocalAuth with GroupIdToken, and remaining handler stubs (reservation, security).

**Architecture:** Reset OnIdle defers execution via `pendingReset` flag until last transaction ends. Firmware reuses shared `FirmwareManager` interface with 2.0.1 message wrappers. DataTransfer and LocalAuth reuse shared registries/managers.

**Tech Stack:** Go 1.26+, `lorenzodonini/ocpp-go v0.19.0`, `stretchr/testify`

**Spec:** `docs/superpowers/specs/2026-04-07-ocpp-201-design.md` — "Firmware", "Remote Operations", "Excluded" sections

**Prerequisite phases:** Phase 6 (monitoring/display/cost) must be complete
**Next phase:** `2026-04-07-ocpp-201-phase8-integration-polish.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/ocpp/v201/handlers.go` | Modify | Reset OnIdle, firmware, data transfer, local auth, reservation, security handlers |
| `internal/ocpp/v201/senders.go` | Modify | FirmwareStatusNotification, LogStatusNotification, DataTransfer senders |
| `internal/ocpp/v201/bridge.go` | Modify | Add `pendingReset` field, register remaining handlers |

---

### Task 18: Reset with OnIdle, Firmware, DataTransfer, LocalAuth

See `2026-04-07-ocpp-201.md` Task 18 for complete code.

- [ ] **Step 1: Implement Reset with OnIdle** — if `ResetTypeOnIdle` and transactions active, set `pendingReset = true`, return `Scheduled`. Add pending reset check to `SendTransactionStop`: when last builder removed and `pendingReset` is true, execute deferred reset.
- [ ] **Step 2: Implement firmware handlers** — `OnUpdateFirmware` (reuses `FirmwareManager.TriggerUpdate`), `OnPublishFirmware` (stub Accepted), `OnUnpublishFirmware` (stub Unpublished). Register `SetFirmwareHandler(b)`.
- [ ] **Step 3: Implement SendFirmwareStatusNotification and SendDiagnosticsStatusNotification** — firmware uses `FirmwareStatusNotificationRequest`, diagnostics uses `LogStatusNotificationRequest` (2.0.1 equivalent).
- [ ] **Step 4: Implement DataTransfer** — `OnDataTransfer` dispatches to shared `DataTransferRegistry`. `SendDataTransfer` sends via `data.NewDataTransferRequest`. Register `SetDataHandler(b)`.
- [ ] **Step 5: Implement LocalAuth handlers** — `OnSendLocalList` maps 2.0.1 `AuthorizationData` to shared `LocalAuthEntry`, supports Full/Differential. `OnGetLocalListVersion` delegates to `LocalAuthManager`. Register `SetLocalAuthListHandler(b)`.
- [ ] **Step 6: Build and test**
- [ ] **Step 7: Commit**

---

### Task 19: Remaining Handler Stubs (Reservation, Security)

See `2026-04-07-ocpp-201.md` Task 19 for complete code.

- [ ] **Step 1: Add remaining handler stubs** — `OnReserveNow` (Accepted), `OnCancelReservation` (Accepted), `OnCertificateSigned` (Rejected), `OnInstallCertificate` (Rejected), `OnGetInstalledCertificateIds` (NotFound), `OnDeleteCertificate` (NotFound)
- [ ] **Step 2: Register handlers** — `SetReservationHandler(b)`, `SetSecurityHandler(b)`. Add `SetMeterHandler`/`SetISO15118Handler` only if the build requires them.
- [ ] **Step 3: Build and test**
- [ ] **Step 4: Commit**
