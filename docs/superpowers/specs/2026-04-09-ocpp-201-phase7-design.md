# OCPP 2.0.1 Phase 7 — Remaining Operations Design

**Date:** 2026-04-09
**Status:** Approved

## Overview

Implement the remaining OCPP 2.0.1 operations: Reset with OnIdle, UpdateFirmware, PublishFirmware/UnpublishFirmware stubs, LocalAuthList with GroupIdToken, DataTransfer passthrough, and firmware status notification. Also wire up missing handler registrations (firmware, localauth, data).

## Already Implemented (no changes)

- `ChangeAvailability` — full implementation in handlers.go
- `UnlockConnector` — full implementation in handlers.go
- `TriggerMessage` — BootNotification, Heartbeat, StatusNotification covered

## Changes Required

### 1. Reset with OnIdle (`handlers.go` + `bridge.go`)

Add `pendingReset atomic.Bool` to `Bridge201`.

Handler logic:
- `ResetTypeImmediate`: return `Accepted`, enqueue BootNotification via dispatcher
- `ResetTypeOnIdle` with active transactions: return `Scheduled`, set `pendingReset = true`
- `ResetTypeOnIdle` with no active transactions: return `Accepted`, enqueue BootNotification immediately

In `SendTransactionStop`: after deleting the last txBuilder entry, check `pendingReset`; if set, clear it and enqueue BootNotification.

Simulated reset = re-send BootNotification (no process restart in simulator).

### 2. UpdateFirmware (`handlers.go` + `senders.go`)

**`OnUpdateFirmware`**:
- Call `b.fwManager.TriggerUpdate(location, retrieveDate)` — existing FirmwareManager interface
- Return `UpdateFirmwareStatusAccepted`
- Wire: add `b.cs.SetFirmwareHandler(b)` in `bridge.go`

**`SendFirmwareStatusNotification`** (fix stub in `senders.go`):
- Map shared FirmwareManager status strings to `firmware.FirmwareStatus` enum:
  - `"Idle"` → `FirmwareStatusIdle`
  - `"Downloading"` → `FirmwareStatusDownloading`
  - `"Downloaded"` → `FirmwareStatusDownloaded`
  - `"Installing"` → `FirmwareStatusInstalling`
  - `"Installed"` → `FirmwareStatusInstalled`
  - `"InstallationFailed"` → `FirmwareStatusInstallationFailed`
  - default → `FirmwareStatusIdle`
- Call `b.cs.FirmwareStatusNotification(mappedStatus)`

### 3. PublishFirmware / UnpublishFirmware (`handlers.go`)

Both are stub handlers:
- `OnPublishFirmware`: log + return `PublishFirmwareStatusAccepted`
- `OnUnpublishFirmware`: log + return `UnpublishFirmwareStatusUnpublished`

### 4. LocalAuthList (`handlers.go` + `bridge.go`)

**`OnGetLocalListVersion`**: return `b.localAuth.GetVersion()`

**`OnSendLocalList`**: 
- Map each `localauth.AuthorizationData` entry to shared `LocalAuthEntry`:
  - `IdToken.IdToken` (string) → `IDTag`
  - `IdTokenInfo.Status` → `Status` (string cast)
  - `IdTokenInfo.CacheExpiryDateTime` → `Expiry` (if present)
  - `IdTokenInfo.GroupIdToken.IdToken` → `ParentIDTag` (if GroupIdToken present)
- Call `localAuth.UpdateList(versionNumber, entries, string(updateType))`
- Return `Accepted` on success, `Failed` on error, `VersionMismatch` if version <= current for differential

Wire: add `b.cs.SetLocalAuthListHandler(b)` in `bridge.go`.

### 5. DataTransfer (`handlers.go` + `senders.go` + `bridge.go`)

**`OnDataTransfer`**:
- Dispatch through `b.dataTransfer` registry using `VendorID + MessageID` key
- If handler found and returns data: return `DataTransferStatusAccepted` with response data
- If no handler: return `DataTransferStatusUnknownVendorId`

**`SendDataTransfer`** (fix stub in `senders.go`):
- Call `b.cs.DataTransfer(vendorID, func(r) { r.MessageID = messageID; r.Data = data })`
- Return status string, response data, error

Wire: add `b.cs.SetDataHandler(b)` in `bridge.go`.

### 6. TriggerMessage additions (`handlers.go`)

Add explicit cases to the existing switch:
- `MessageTriggerLogStatusNotification` → return `NotImplemented`
- `MessageTriggerTransactionEvent` → already returns `NotImplemented` (explicit case for clarity)

## Files Changed

| File | Changes |
|------|---------|
| `internal/ocpp/v201/bridge.go` | Add `pendingReset atomic.Bool`; add `SetFirmwareHandler`, `SetLocalAuthListHandler`, `SetDataHandler` calls |
| `internal/ocpp/v201/handlers.go` | Update `OnReset`; add `OnUpdateFirmware`, `OnPublishFirmware`, `OnUnpublishFirmware`, `OnGetLocalListVersion`, `OnSendLocalList`, `OnDataTransfer`; extend `OnTriggerMessage` |
| `internal/ocpp/v201/senders.go` | Implement `SendFirmwareStatusNotification`, `SendDataTransfer` |

## Tests

New test cases in `internal/ocpp/v201/`:

- **bridge_test.go**: handler registrations are set (SetFirmwareHandler, SetLocalAuthListHandler, SetDataHandler)
- **handlers_test.go** (new or extend):
  - `OnReset` Immediate path
  - `OnReset` OnIdle with active transactions → Scheduled
  - `OnReset` OnIdle with no active transactions → Accepted
  - Pending reset fires BootNotification after last tx ends
  - `OnUpdateFirmware` delegates to fwManager
  - `OnGetLocalListVersion` returns current version
  - `OnSendLocalList` Full update, Differential update, error path
  - `OnDataTransfer` known vendor → Accepted, unknown vendor → UnknownVendorId
- **senders_test.go** (new or extend):
  - `SendFirmwareStatusNotification` maps all status strings correctly
  - `SendDataTransfer` returns correct status

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Reset simulation | Re-send BootNotification | Most realistic behavior for simulator |
| GroupIdToken mapping | Map to ParentIDTag string | Matches existing LocalAuthEntry structure |
| SendDataTransfer | Blocking call | Consistent with other sync senders |
