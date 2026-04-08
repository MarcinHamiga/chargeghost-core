# Plan 05e — OCPP Firmware, Diagnostics & Data Transfer

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Firmware update and diagnostics upload simulate correctly with exact timing from the guide (Idle→Downloading→Downloaded→Installing→Installed, Idle→Uploading→Uploaded), vendor data transfer is dispatched to registered handlers, and all raw OCPP send REST endpoints are live.

**Architecture:** `FirmwareManager` and `DiagnosticsManager` each run their state machine in a goroutine spawned by `TriggerUpdate`/`TriggerUpload`. They fire a callback on each transition (for WebSocket broadcast + OCPP status notification). Both replace the Plan 3b stubs by implementing the same `FirmwareManager` and `DiagnosticsManager` interfaces. `DataTransferRegistry` routes inbound DataTransfer to registered `vendorID/messageID` handlers.

**Tech Stack:** Go 1.22 stdlib + `lorenzodonini/ocpp-go` (firmware feature)

---

## File Map

| File | Responsibility |
|------|----------------|
| `internal/ocpp/firmware_manager.go` | `FirmwareManager` + `DiagnosticsManager` — real implementations replacing Plan 3b stubs |
| `internal/ocpp/data_transfer.go` | `DataTransferRegistry` — vendor-specific handler dispatch |
| `internal/ocpp/bridge.go` | Modified: OnUpdateFirmware, OnGetDiagnostics, OnDataTransfer wired; remaining outbound sends (Authorize, raw sends) completed |
| `internal/api/handlers/ocpp.go` | Modified: raw OCPP send REST endpoints added |
| `cmd/chargeghost/main.go` | Modified: create real FirmwareManager, DiagnosticsManager, inject into app |

---

## Task 1: FirmwareManager and DiagnosticsManager

**Files:**
- Create: `internal/ocpp/firmware_manager.go`
- Create: `internal/ocpp/firmware_manager_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/ocpp/firmware_manager_test.go`:

```go
package ocpp_test

import (
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/chargeghost/engine/internal/ocpp"
)

func TestFirmwareManager_IdleByDefault(t *testing.T) {
    fm := ocpp.NewFirmwareManager(nil)
    assert.Equal(t, "Idle", fm.GetStatus().Status)
}

func TestFirmwareManager_TransitionsToDownloading(t *testing.T) {
    statuses := make([]string, 0)
    fm := ocpp.NewFirmwareManager(func(status string) {
        statuses = append(statuses, status)
    })

    // Retrieve date is now (immediate start).
    require.NoError(t, fm.TriggerUpdate("http://example.com/fw.bin", time.Now()))

    // Wait for Downloading transition (immediate) + Downloaded (3s) + Installing (1s) + Installed (2s) = ~6s
    time.Sleep(7 * time.Second)

    assert.Contains(t, statuses, "Downloading")
    assert.Contains(t, statuses, "Downloaded")
    assert.Contains(t, statuses, "Installing")
    assert.Contains(t, statuses, "Installed")
    assert.Equal(t, "Installed", fm.GetStatus().Status)
}

func TestFirmwareManager_CancelMidUpdate(t *testing.T) {
    fm := ocpp.NewFirmwareManager(nil)
    require.NoError(t, fm.TriggerUpdate("http://example.com/fw.bin", time.Now()))
    time.Sleep(100 * time.Millisecond) // allow Downloading to start
    require.NoError(t, fm.CancelUpdate())
    assert.Equal(t, "Idle", fm.GetStatus().Status)
}

func TestFirmwareManager_CancelWhenIdle(t *testing.T) {
    fm := ocpp.NewFirmwareManager(nil)
    err := fm.CancelUpdate()
    assert.Error(t, err, "cancel when idle should return error")
}

func TestDiagnosticsManager_Transitions(t *testing.T) {
    statuses := make([]string, 0)
    dm := ocpp.NewDiagnosticsManager(func(status string) {
        statuses = append(statuses, status)
    })

    require.NoError(t, dm.TriggerUpload("http://example.com/diag.tgz", 0, 0))
    time.Sleep(3 * time.Second) // Uploading (0s) → Uploaded (2s)

    assert.Contains(t, statuses, "Uploading")
    assert.Contains(t, statuses, "Uploaded")
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/ocpp/... -run TestFirmwareManager -run TestDiagnosticsManager -v
```

Expected: compile error.

- [ ] **Step 3: Implement firmware_manager.go**

Create `internal/ocpp/firmware_manager.go`:

```go
package ocpp

import (
    "context"
    "errors"
    "sync"
    "time"
)

// FirmwareManager simulates firmware update with exact timing from the guide:
//   Idle → Downloading (0s after retrieve date) → Downloaded (3s) → Installing (1s) → Installed (2s)
// Implements the FirmwareManager interface (defined in interfaces.go).
type RealFirmwareManager struct {
    mu         sync.Mutex
    status     FirmwareStatus
    cancelFunc context.CancelFunc
    onStatus   func(status string) // callback fired on each transition
}

// NewFirmwareManager creates a manager. onStatus is called on every status change
// (for WebSocket broadcast + OCPP FirmwareStatusNotification).
func NewFirmwareManager(onStatus func(status string)) *RealFirmwareManager {
    return &RealFirmwareManager{
        status:   FirmwareStatus{Status: "Idle"},
        onStatus: onStatus,
    }
}

func (m *RealFirmwareManager) GetStatus() FirmwareStatus {
    m.mu.Lock()
    defer m.mu.Unlock()
    return m.status
}

func (m *RealFirmwareManager) TriggerUpdate(location string, retrieveDate time.Time) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.status.Status != "Idle" {
        return errors.New("firmware update already in progress")
    }
    ctx, cancel := context.WithCancel(context.Background())
    m.cancelFunc = cancel
    m.status = FirmwareStatus{Status: "Idle", Location: &location, RetrieveDate: &retrieveDate}
    go m.runUpdate(ctx, location, retrieveDate)
    return nil
}

func (m *RealFirmwareManager) CancelUpdate() error {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.status.Status == "Idle" {
        return errors.New("no firmware update in progress")
    }
    if m.cancelFunc != nil {
        m.cancelFunc()
        m.cancelFunc = nil
    }
    m.status = FirmwareStatus{Status: "Idle"}
    return nil
}

func (m *RealFirmwareManager) runUpdate(ctx context.Context, location string, retrieveDate time.Time) {
    // Wait until retrieve date.
    waitDur := time.Until(retrieveDate)
    if waitDur > 0 {
        select {
        case <-ctx.Done():
            return
        case <-time.After(waitDur):
        }
    }

    transitions := []struct {
        status string
        delay  time.Duration
    }{
        {"Downloading", 0},
        {"Downloaded", 3 * time.Second},
        {"Installing", 1 * time.Second},
        {"Installed", 2 * time.Second},
    }

    for _, t := range transitions {
        if t.delay > 0 {
            select {
            case <-ctx.Done():
                m.mu.Lock()
                m.status = FirmwareStatus{Status: "Idle"}
                m.mu.Unlock()
                return
            case <-time.After(t.delay):
            }
        }
        m.mu.Lock()
        m.status.Status = t.status
        m.mu.Unlock()
        if m.onStatus != nil {
            m.onStatus(t.status)
        }
    }

    // Final: clear cancel func.
    m.mu.Lock()
    m.cancelFunc = nil
    m.mu.Unlock()
}

// DiagnosticsManager simulates diagnostics upload:
//   Idle → Uploading (0s) → Uploaded (2s)
// Implements the DiagnosticsManager interface (defined in interfaces.go).
type RealDiagnosticsManager struct {
    mu         sync.Mutex
    status     DiagnosticsStatus
    cancelFunc context.CancelFunc
    onStatus   func(status string)
}

func NewDiagnosticsManager(onStatus func(status string)) *RealDiagnosticsManager {
    return &RealDiagnosticsManager{
        status:   DiagnosticsStatus{Status: "Idle"},
        onStatus: onStatus,
    }
}

func (m *RealDiagnosticsManager) GetStatus() DiagnosticsStatus {
    m.mu.Lock()
    defer m.mu.Unlock()
    return m.status
}

func (m *RealDiagnosticsManager) TriggerUpload(location string, retries, retryInterval int) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.status.Status != "Idle" {
        return errors.New("diagnostics upload already in progress")
    }
    ctx, cancel := context.WithCancel(context.Background())
    m.cancelFunc = cancel
    m.status = DiagnosticsStatus{Status: "Idle", Location: &location}
    go m.runUpload(ctx)
    return nil
}

func (m *RealDiagnosticsManager) CancelUpload() error {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.status.Status == "Idle" {
        return errors.New("no diagnostics upload in progress")
    }
    if m.cancelFunc != nil {
        m.cancelFunc()
        m.cancelFunc = nil
    }
    m.status = DiagnosticsStatus{Status: "Idle"}
    return nil
}

func (m *RealDiagnosticsManager) runUpload(ctx context.Context) {
    // Uploading: immediate
    m.mu.Lock()
    m.status.Status = "Uploading"
    m.mu.Unlock()
    if m.onStatus != nil {
        m.onStatus("Uploading")
    }

    // Uploaded: after 2s
    select {
    case <-ctx.Done():
        m.mu.Lock()
        m.status = DiagnosticsStatus{Status: "Idle"}
        m.mu.Unlock()
        return
    case <-time.After(2 * time.Second):
    }

    m.mu.Lock()
    m.status.Status = "Uploaded"
    m.cancelFunc = nil
    m.mu.Unlock()
    if m.onStatus != nil {
        m.onStatus("Uploaded")
    }
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/ocpp/... -run TestFirmwareManager -run TestDiagnosticsManager -v -timeout 30s
```

Expected: PASS. (Tests involve real time.Sleep — 7s total for firmware, 3s for diagnostics.)

- [ ] **Step 5: Commit**

```bash
git add internal/ocpp/firmware_manager.go internal/ocpp/firmware_manager_test.go
git commit -m "feat(ocpp): RealFirmwareManager and RealDiagnosticsManager with timed state machines"
```

---

## Task 2: Data Transfer Registry

**Files:**
- Create: `internal/ocpp/data_transfer.go`

- [ ] **Step 1: Write the failing test**

Create `internal/ocpp/data_transfer_test.go`:

```go
package ocpp_test

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/chargeghost/engine/internal/ocpp"
)

func TestDataTransferRegistry_Dispatch(t *testing.T) {
    r := ocpp.NewDataTransferRegistry()
    r.Register("MyVendor", "GetFoo", func(messageID, data string) (string, string) {
        return "Accepted", "response-data"
    })

    status, resp := r.Dispatch("MyVendor", "GetFoo", "GetFoo", "input")
    assert.Equal(t, "Accepted", status)
    assert.Equal(t, "response-data", resp)
}

func TestDataTransferRegistry_UnknownVendor(t *testing.T) {
    r := ocpp.NewDataTransferRegistry()
    status, _ := r.Dispatch("UnknownVendor", "Msg", "Msg", "")
    assert.Equal(t, "UnknownVendorId", status)
}
```

- [ ] **Step 2: Implement data_transfer.go**

Create `internal/ocpp/data_transfer.go`:

```go
package ocpp

import "sync"

// DataTransferHandler is called when a DataTransfer request arrives for a registered vendorID/messageID pair.
type DataTransferHandler func(messageID, data string) (status, responseData string)

type vendorKey struct{ vendorID, messageID string }

// DataTransferRegistry routes inbound DataTransfer messages to registered handlers.
type DataTransferRegistry struct {
    mu       sync.RWMutex
    handlers map[vendorKey]DataTransferHandler
}

func NewDataTransferRegistry() *DataTransferRegistry {
    return &DataTransferRegistry{handlers: make(map[vendorKey]DataTransferHandler)}
}

// Register maps a vendorID/messageID pair to a handler.
func (r *DataTransferRegistry) Register(vendorID, messageID string, handler DataTransferHandler) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.handlers[vendorKey{vendorID, messageID}] = handler
}

// Dispatch calls the handler for the given vendor/message pair.
// Returns "UnknownVendorId" if no handler is registered.
func (r *DataTransferRegistry) Dispatch(vendorID, messageID, requestMessageID, data string) (status, responseData string) {
    r.mu.RLock()
    handler, ok := r.handlers[vendorKey{vendorID, messageID}]
    r.mu.RUnlock()
    if !ok {
        return "UnknownVendorId", ""
    }
    return handler(requestMessageID, data)
}
```

- [ ] **Step 3: Run tests and commit**

```bash
go test ./internal/ocpp/... -run TestDataTransferRegistry -v
git add internal/ocpp/data_transfer.go internal/ocpp/data_transfer_test.go
git commit -m "feat(ocpp): DataTransferRegistry for vendor-specific handler dispatch"
```

---

## Task 3: Wire Firmware, Diagnostics, and Data Transfer into Bridge

**Files:**
- Modify: `internal/ocpp/bridge.go`
- Modify: `cmd/chargeghost/main.go`

- [ ] **Step 1: Add fields to Bridge**

```go
type Bridge struct {
    // ... existing fields ...
    firmware    FirmwareManager
    diagnostics DiagnosticsManager
    dataTransfer *DataTransferRegistry
}
```

Update `NewBridge` signature to accept these. Register firmware handler:

```go
import "github.com/lorenzodonini/ocpp-go/ocpp1.6/firmware"

// In NewBridge:
b.cp.SetFirmwareManagementHandler(b)
```

- [ ] **Step 2: Implement OnUpdateFirmware**

```go
func (b *Bridge) OnUpdateFirmware(request *firmware.UpdateFirmwareRequest) (*firmware.UpdateFirmwareResponse, error) {
    retrieveDate := request.RetrieveDate.Time
    err := b.firmware.TriggerUpdate(request.Location, retrieveDate)
    if err != nil {
        slog.Warn("firmware update trigger failed", "error", err)
    }
    return firmware.NewUpdateFirmwareResponse(), nil
}
```

- [ ] **Step 3: Implement OnGetDiagnostics**

```go
func (b *Bridge) OnGetDiagnostics(request *firmware.GetDiagnosticsRequest) (*firmware.GetDiagnosticsResponse, error) {
    retries := 0
    retryInterval := 30
    if request.Retries != nil {
        retries = *request.Retries
    }
    if request.RetryInterval != nil {
        retryInterval = *request.RetryInterval
    }
    fileName := "diagnostics.tgz"
    err := b.diagnostics.TriggerUpload(request.Location, retries, retryInterval)
    if err != nil {
        slog.Warn("diagnostics upload trigger failed", "error", err)
        return firmware.NewGetDiagnosticsResponse(), nil
    }
    resp := firmware.NewGetDiagnosticsResponse()
    resp.FileName = &fileName
    return resp, nil
}
```

- [ ] **Step 4: Implement OnDataTransfer**

Replace the stub:

```go
func (b *Bridge) OnDataTransfer(request *core.DataTransferRequest) (*core.DataTransferResponse, error) {
    messageID := ""
    if request.MessageId != nil {
        messageID = *request.MessageId
    }
    data := ""
    if request.Data != nil {
        data = *request.Data
    }
    status, responseData := b.dataTransfer.Dispatch(request.VendorId, messageID, messageID, data)
    resp := core.NewDataTransferResponse(core.DataTransferStatus(status))
    if responseData != "" {
        resp.Data = &responseData
    }
    return resp, nil
}
```

- [ ] **Step 5: Implement SendFirmwareStatusNotification and SendDiagnosticsStatusNotification**

Replace the stubs:

```go
func (b *Bridge) SendFirmwareStatusNotification(status string) error {
    req := firmware.NewFirmwareStatusNotificationRequest(firmware.FirmwareStatus(status))
    _, err := b.cp.SendRequest(req)
    return err
}

func (b *Bridge) SendDiagnosticsStatusNotification(status string) error {
    req := firmware.NewDiagnosticsStatusNotificationRequest(firmware.DiagnosticsStatus(status))
    _, err := b.cp.SendRequest(req)
    return err
}
```

- [ ] **Step 6: Wire callbacks in main.go**

```go
fwOnStatus := func(status string) {
    hub.BroadcastMessage(ws.Message{
        Type: "firmware_status_changed",
        Data: map[string]string{"status": status},
    })
    if bridge.IsConnected() {
        dispatcher.Enqueue(ocpp.OCPPCommand{
            Description: "FirmwareStatusNotification",
            Execute: func() error {
                return bridge.SendFirmwareStatusNotification(status)
            },
        })
    }
}

diagOnStatus := func(status string) {
    hub.BroadcastMessage(ws.Message{
        Type: "diagnostics_status_changed",
        Data: map[string]string{"status": status},
    })
    if bridge.IsConnected() {
        dispatcher.Enqueue(ocpp.OCPPCommand{
            Description: "DiagnosticsStatusNotification",
            Execute: func() error {
                return bridge.SendDiagnosticsStatusNotification(status)
            },
        })
    }
}

firmwareManager := ocpp.NewFirmwareManager(fwOnStatus)
diagnosticsManager := ocpp.NewDiagnosticsManager(diagOnStatus)
dataTransferReg := ocpp.NewDataTransferRegistry()

bridge := ocpp.NewBridge(e, hub, cfg, dispatcher, profileManager, configKeys, authCache, localAuthReal, messageQueue, firmwareManager, diagnosticsManager, dataTransferReg)

// Replace stubs in AppContext.
app := &api.AppContext{
    // ...
    Firmware:    firmwareManager,
    Diagnostics: diagnosticsManager,
}
```

- [ ] **Step 7: Build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 8: Commit**

```bash
git add internal/ocpp/bridge.go cmd/chargeghost/main.go
git commit -m "feat(ocpp): firmware, diagnostics, data transfer wired into Bridge"
```

---

## Task 4: Raw OCPP Send REST Endpoints

**Files:**
- Modify: `internal/api/handlers/ocpp.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add Bridge reference to AppContext**

In `router.go`, add `Bridge interface{ ... }` or a concrete type reference. For simplicity, add the specific send methods to an interface in `router.go`:

```go
// OCPPSendAPI defines the outbound OCPP operations exposed via REST.
type OCPPSendAPI interface {
    SendAuthorize(idTag string) error
    SendHeartbeat() error
    SendBootNotification() error
    SendStatusNotification(connectorID int, errorCode, status string) error
    SendMeterValues(connectorID int, value float64, transactionID int, context string) error
    SendDataTransfer(vendorID, messageID, data string) (string, string, error)
    IsConnected() bool
}
```

Add `OCPP OCPPSendAPI` to `AppContext`.

- [ ] **Step 2: Implement raw send handlers in ocpp.go**

Add to `internal/api/handlers/ocpp.go`:

```go
func SendAuthorize(ocppAPI api.OCPPSendAPI) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            IDTag string `json:"id_tag"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid body"})
            return
        }
        if err := ocppAPI.SendAuthorize(req.IDTag); err != nil {
            writeJSON(w, http.StatusServiceUnavailable, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Authorize sent"})
    }
}

func SendHeartbeat(ocppAPI api.OCPPSendAPI) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if err := ocppAPI.SendHeartbeat(); err != nil {
            writeJSON(w, http.StatusServiceUnavailable, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Heartbeat sent"})
    }
}

func SendRawDataTransfer(ocppAPI api.OCPPSendAPI) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            VendorID  string `json:"vendor_id"`
            MessageID string `json:"message_id"`
            Data      string `json:"data"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid body"})
            return
        }
        status, data, err := ocppAPI.SendDataTransfer(req.VendorID, req.MessageID, req.Data)
        if err != nil {
            writeJSON(w, http.StatusServiceUnavailable, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, map[string]string{"status": status, "data": data})
    }
}
```

For raw StartTransaction, StopTransaction, StatusNotification, MeterValues — these call the corresponding engine methods to ensure state consistency, then let the normal OCPP flow send them:

```go
func SendRawStatusNotification(e *engine.Engine, ocppAPI api.OCPPSendAPI) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            ConnectorID int    `json:"connector_id"`
            ErrorCode   string `json:"error_code"`
            Status      string `json:"status"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid body"})
            return
        }
        if err := ocppAPI.SendStatusNotification(req.ConnectorID, req.ErrorCode, req.Status); err != nil {
            writeJSON(w, http.StatusServiceUnavailable, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "StatusNotification sent"})
    }
}

func SendRawMeterValues(e *engine.Engine, ocppAPI api.OCPPSendAPI) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            ConnectorID   int `json:"connector_id"`
            TransactionID int `json:"transaction_id"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid body"})
            return
        }
        reading, txID := e.GetMeterSnapshot(req.ConnectorID)
        if req.TransactionID != 0 {
            txID = req.TransactionID
        }
        if err := ocppAPI.SendMeterValues(req.ConnectorID, reading, txID, "Sample.Clock"); err != nil {
            writeJSON(w, http.StatusServiceUnavailable, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "MeterValues sent"})
    }
}
```

- [ ] **Step 3: Register routes**

In `router.go`:

```go
        r.Route("/ocpp", func(r chi.Router) {
            r.Get("/config-keys", handlers.GetOCPPConfigKeys(app.ConfigKeys))
            r.Patch("/config-keys", handlers.PatchOCPPConfigKey(app.ConfigKeys))
            r.Post("/authorize", handlers.SendAuthorize(app.OCPP))
            r.Post("/heartbeat", handlers.SendHeartbeat(app.OCPP))
            r.Route("/raw", func(r chi.Router) {
                r.Post("/status-notification", handlers.SendRawStatusNotification(app.Engine, app.OCPP))
                r.Post("/meter-values", handlers.SendRawMeterValues(app.Engine, app.OCPP))
                r.Post("/data-transfer", handlers.SendRawDataTransfer(app.OCPP))
                // start-transaction and stop-transaction use engine endpoints; these are aliases.
                r.Post("/start-transaction", handlers.StartCharging(app.Engine))
                r.Post("/stop-transaction", handlers.StopCharging(app.Engine))
            })
        })
```

- [ ] **Step 4: Wire in main.go**

```go
app.OCPP = bridge
```

- [ ] **Step 5: Build and run all tests**

```bash
go build ./...
go test ./... -count=1 -timeout 60s
```

Expected: all pass.

- [ ] **Step 6: Integration test**

```bash
./chargeghost &
sleep 1

# Trigger firmware update.
curl -s -X POST http://localhost:8080/api/v1/firmware/trigger \
  -H "Content-Type: application/json" \
  -d '{"location":"http://example.com/fw.bin","retrieve_date":"2020-01-01T00:00:00Z"}' | jq .

# Verify status progression via WebSocket or polling.
for i in 1 2 3 4 5 6 7 8; do
    sleep 1
    curl -s http://localhost:8080/api/v1/firmware/status | jq .status
done

# Expected output: "Downloading", "Downloaded", "Installing", "Installed"
kill %1
```

- [ ] **Step 7: Commit**

```bash
git add internal/api/handlers/ocpp.go internal/api/router.go cmd/chargeghost/main.go
git commit -m "feat(api): raw OCPP send REST endpoints wired to Bridge"
```
