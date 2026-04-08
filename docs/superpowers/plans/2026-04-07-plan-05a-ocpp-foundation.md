# Plan 05a — OCPP Foundation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Connect to a CSMS, send BootNotification, exchange Heartbeats, and broadcast `connection_state_changed` WebSocket events — with the full OCPP interface boundary defined for all subsequent OCPP plans.

**Architecture:** The OCPP adapter wraps `lorenzodonini/ocpp-go`. The `CommandDispatcher` owns a buffered channel that is drained by a single goroutine, guaranteeing FIFO delivery. Engine callbacks enqueue to the dispatcher (non-blocking). The `Bridge` struct wires the OCPP library to the engine. A `BootNotification`-accepted handler fires `StatusNotification` for each connector.

**Tech Stack:** Go 1.22, `github.com/lorenzodonini/ocpp-go`

---

## File Map

| File | Responsibility |
|------|----------------|
| `internal/ocpp/adapter.go` | All OCPP-related interfaces (OCPPAdapter, EngineView, AuthorizationCache, LocalAuthListView, etc.) |
| `internal/ocpp/command.go` | `OCPPCommand`, `CommandDispatcher` — FIFO OCPP message delivery |
| `internal/ocpp/bridge.go` | `Bridge` — wires lorenzodonini/ocpp-go to the engine; BootNotification, Heartbeat |
| `internal/ocpp/interfaces.go` | Already exists (Plan 3b) — no changes needed |
| `cmd/chargeghost/main.go` | Modified: create and start the bridge |

---

## Task 1: Add OCPP Dependency

- [ ] **Step 1: Add lorenzodonini/ocpp-go**

```bash
go get github.com/lorenzodonini/ocpp-go@latest
go mod tidy
```

Expected: `go.sum` updated.

---

## Task 2: OCPP Interfaces

**Files:**
- Create: `internal/ocpp/adapter.go`

Note: `internal/ocpp/interfaces.go` already defines `LocalAuthManager`, `FirmwareManager`, `DiagnosticsManager`. `adapter.go` adds the OCPP-protocol-specific interfaces.

- [ ] **Step 1: Implement adapter.go**

Create `internal/ocpp/adapter.go`:

```go
package ocpp

import (
    "time"

    engine "github.com/chargeghost/engine/internal/engine"
)

// MeterRecord mirrors engine.MeterRecord for OCPP-layer use.
// Re-exported here so the OCPP layer doesn't import engine types directly
// where it would create confusion; in practice it imports engine directly.
// (The engine package defines MeterRecord; use engine.MeterRecord in bridge code.)

// OCPPAdapter is the combined interface the external OCPP library must satisfy.
// Implemented by Bridge in bridge.go.
type OCPPAdapter interface {
    OCPPSender
    IsConnected() bool
    GetHeartbeatInterval() int
}

// OCPPSender covers all outbound OCPP 1.6 messages the engine can trigger.
type OCPPSender interface {
    SendBootNotification() error
    SendHeartbeat() error
    SendStartTransaction(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error)
    SendStopTransaction(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error
    SendStatusNotification(connectorID int, errorCode, status string) error
    SendMeterValues(connectorID int, value float64, transactionID int, context string) error
    SendAuthorize(idTag string) error
    SendDataTransfer(vendorID, messageID, data string) (string, string, error)
    SendDiagnosticsStatusNotification(status string) error
    SendFirmwareStatusNotification(status string) error
}

// EngineView is the read-only interface the OCPP layer uses to query engine state.
// Engine implements all these methods — no separate wrapper needed.
type EngineView interface {
    GetConnector(connectorID int) *engine.Connector
    GetSession(connectorID int) *engine.Session
    GetEnergyMeter(connectorID int) *engine.EnergyMeter
    GetConnectorIDs() []int
    GetLastStoppedSession() *engine.StoppedSessionInfo
    GetConnectorStatus(connectorID int) string
    GetMeterSnapshot(connectorID int) (float64, int)
    GetActiveTransactionID(connectorID int) *int
    GetConnectorByTransaction(transactionID int) *int
    SetActiveTransaction(connectorID, transactionID int)
    ClearActiveTransaction(connectorID int)
}

// AuthorizationCacheStore manages per-tag authorization status caching.
// Plan 5a uses a no-op implementation; Plan 5d replaces it.
type AuthorizationCacheStore interface {
    Get(idTag string) (status string, expiry *time.Time, found bool)
    Put(idTag string, status string, expiry *time.Time)
    Remove(idTag string)
    Clear()
    Size() int
}

// NoopAuthCache is an empty auth cache used before Plan 5d.
type NoopAuthCache struct{}

func (NoopAuthCache) Get(idTag string) (string, *time.Time, bool) { return "", nil, false }
func (NoopAuthCache) Put(idTag string, status string, expiry *time.Time) {}
func (NoopAuthCache) Remove(idTag string)                                 {}
func (NoopAuthCache) Clear()                                              {}
func (NoopAuthCache) Size() int                                           { return 0 }
```

- [ ] **Step 2: Commit**

```bash
git add internal/ocpp/adapter.go
git commit -m "feat(ocpp): OCPPAdapter, OCPPSender, EngineView interface definitions"
```

---

## Task 3: CommandDispatcher

**Files:**
- Create: `internal/ocpp/command.go`
- Create: `internal/ocpp/command_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/ocpp/command_test.go`:

```go
package ocpp_test

import (
    "context"
    "sync/atomic"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/chargeghost/engine/internal/ocpp"
)

func TestCommandDispatcher_ExecutesInOrder(t *testing.T) {
    d := ocpp.NewCommandDispatcher()
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go d.Run(ctx)

    var results []int
    var mu sync.Mutex

    for i := 0; i < 5; i++ {
        n := i
        d.Enqueue(ocpp.OCPPCommand{
            Description: fmt.Sprintf("cmd %d", n),
            Execute: func() error {
                mu.Lock()
                results = append(results, n)
                mu.Unlock()
                return nil
            },
        })
    }

    time.Sleep(100 * time.Millisecond)
    mu.Lock()
    assert.Equal(t, []int{0, 1, 2, 3, 4}, results)
    mu.Unlock()
}

func TestCommandDispatcher_NonBlockingEnqueue(t *testing.T) {
    d := ocpp.NewCommandDispatcher()
    // Don't start Run — channel fills up.
    // Enqueue should not block.
    done := make(chan struct{})
    go func() {
        for i := 0; i < 300; i++ {
            d.Enqueue(ocpp.OCPPCommand{
                Description: "overflow",
                Execute:     func() error { return nil },
            })
        }
        close(done)
    }()

    select {
    case <-done:
        // good — did not block
    case <-time.After(500 * time.Millisecond):
        t.Fatal("Enqueue blocked when channel was full")
    }
}
```

Add `"fmt"` and `"sync"` imports.

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/ocpp/... -run TestCommandDispatcher -v
```

Expected: compile error.

- [ ] **Step 3: Implement command.go**

Create `internal/ocpp/command.go`:

```go
package ocpp

import (
    "context"
    "log/slog"
)

// OCPPCommand is a single OCPP send operation to be executed sequentially.
type OCPPCommand struct {
    Description string
    Execute     func() error
}

// CommandDispatcher guarantees FIFO execution of OCPP commands.
// Engine callbacks enqueue via Enqueue (non-blocking); a single goroutine
// running Run drains the channel sequentially.
type CommandDispatcher struct {
    commands chan OCPPCommand
}

// NewCommandDispatcher creates a dispatcher with a 256-command buffer.
func NewCommandDispatcher() *CommandDispatcher {
    return &CommandDispatcher{
        commands: make(chan OCPPCommand, 256),
    }
}

// Run drains commands sequentially. Call in a dedicated goroutine.
func (d *CommandDispatcher) Run(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case cmd := <-d.commands:
            if err := cmd.Execute(); err != nil {
                slog.Error("OCPP command failed", "description", cmd.Description, "error", err)
            }
        }
    }
}

// Enqueue adds a command to the channel without blocking.
// If the channel is full, the command is dropped with a warning log.
func (d *CommandDispatcher) Enqueue(cmd OCPPCommand) {
    select {
    case d.commands <- cmd:
    default:
        slog.Warn("OCPP command channel full, dropping", "description", cmd.Description)
    }
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/ocpp/... -run TestCommandDispatcher -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ocpp/command.go internal/ocpp/command_test.go
git commit -m "feat(ocpp): CommandDispatcher for ordered OCPP message delivery"
```

---

## Task 4: Bridge — BootNotification and Heartbeat

**Files:**
- Create: `internal/ocpp/bridge.go`

- [ ] **Step 1: Implement bridge.go**

The `lorenzodonini/ocpp-go` charge-point API:
- `ocpp16.NewChargePoint(id, dispatcher, client)` creates the charge point
- `cp.SetCoreHandler(handler)` registers the CSMS → CP message handler
- `go cp.Start(serverURL, path)` connects to the CSMS WebSocket
- `cp.SendRequest(req)` sends a request and returns a channel for the response

Create `internal/ocpp/bridge.go`:

```go
package ocpp

import (
    "context"
    "fmt"
    "log/slog"
    "sync/atomic"
    "time"

    ocpp16 "github.com/lorenzodonini/ocpp-go/ocpp1.6"
    "github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
    "github.com/lorenzodonini/ocpp-go/ocppj"
    engine "github.com/chargeghost/engine/internal/engine"
    ws "github.com/chargeghost/engine/internal/api/ws"
    "github.com/chargeghost/engine/internal/config"
)

// Bridge connects the engine to a CSMS via the lorenzodonini/ocpp-go library.
type Bridge struct {
    cp           ocpp16.ChargePoint
    dispatcher   *CommandDispatcher
    engine       *engine.Engine
    hub          *ws.Hub
    cfg          *config.Config
    connected    atomic.Bool
    heartbeatInt int // seconds
}

// NewBridge creates a Bridge. Call Start(ctx) to connect.
func NewBridge(e *engine.Engine, hub *ws.Hub, cfg *config.Config, dispatcher *CommandDispatcher) *Bridge {
    b := &Bridge{
        engine:       e,
        hub:          hub,
        cfg:          cfg,
        dispatcher:   dispatcher,
        heartbeatInt: 300, // default; overridden by BootNotification response
    }

    // Create the lorenzodonini charge point with a core handler.
    b.cp = ocpp16.NewChargePoint(cfg.OCPPID, nil, nil)
    b.cp.SetCoreHandler(b)

    return b
}

// IsConnected returns true when the OCPP WebSocket is connected.
func (b *Bridge) IsConnected() bool { return b.connected.Load() }

// GetHeartbeatInterval returns the CSMS-assigned heartbeat interval in seconds.
func (b *Bridge) GetHeartbeatInterval() int { return b.heartbeatInt }

// Start connects to the CSMS and runs until ctx is cancelled.
// Must be called in a goroutine.
func (b *Bridge) Start(ctx context.Context) {
    serverURL := b.cfg.ConnectionURL
    slog.Info("OCPP bridge connecting", "url", serverURL, "id", b.cfg.OCPPID)

    // Set connection handlers on the underlying websocket client.
    b.cp.SetOnConnectedHandler(func() {
        slog.Info("OCPP connected")
        b.connected.Store(true)
        b.hub.BroadcastMessage(ws.Message{
            Type: "connection_state_changed",
            Data: map[string]bool{"connected": true},
        })
        // Send BootNotification on connect.
        b.dispatcher.Enqueue(OCPPCommand{
            Description: "BootNotification",
            Execute:     b.SendBootNotification,
        })
    })

    b.cp.SetOnDisconnectedHandler(func() {
        slog.Warn("OCPP disconnected")
        b.connected.Store(false)
        b.hub.BroadcastMessage(ws.Message{
            Type: "connection_state_changed",
            Data: map[string]bool{"connected": false},
        })
    })

    // Connect — this blocks and auto-reconnects.
    go func() {
        if err := b.cp.Start(serverURL); err != nil {
            slog.Error("OCPP bridge error", "error", err)
        }
    }()

    <-ctx.Done()
    b.cp.Stop()
    slog.Info("OCPP bridge stopped")
}

// SendBootNotification sends a BootNotification to the CSMS.
func (b *Bridge) SendBootNotification() error {
    req := core.NewBootNotificationRequest(b.cfg.ChargePointModel, b.cfg.ChargePointVendor)
    resp, err := b.cp.SendRequest(req)
    if err != nil {
        return fmt.Errorf("BootNotification send: %w", err)
    }
    bootResp, ok := resp.(*core.BootNotificationResponse)
    if !ok {
        return fmt.Errorf("unexpected BootNotification response type")
    }
    slog.Info("BootNotification response", "status", bootResp.Status, "interval", bootResp.Interval)

    if bootResp.Status == core.RegistrationStatusAccepted {
        b.heartbeatInt = bootResp.Interval
        // Send StatusNotification for each connector.
        for _, id := range b.engine.GetConnectorIDs() {
            connID := id
            b.dispatcher.Enqueue(OCPPCommand{
                Description: fmt.Sprintf("StatusNotification connector %d", connID),
                Execute: func() error {
                    return b.SendStatusNotification(connID, "NoError", b.engine.GetConnectorStatus(connID))
                },
            })
        }
        // Start heartbeat loop.
        go b.heartbeatLoop()
    }
    return nil
}

func (b *Bridge) heartbeatLoop() {
    interval := time.Duration(b.heartbeatInt) * time.Second
    ticker := time.NewTicker(interval)
    defer ticker.Stop()
    for range ticker.C {
        if !b.connected.Load() {
            return
        }
        b.dispatcher.Enqueue(OCPPCommand{
            Description: "Heartbeat",
            Execute:     b.SendHeartbeat,
        })
    }
}

// SendHeartbeat sends a Heartbeat to the CSMS.
func (b *Bridge) SendHeartbeat() error {
    _, err := b.cp.SendRequest(core.NewHeartbeatRequest())
    return err
}

// SendStatusNotification sends StatusNotification for a connector.
func (b *Bridge) SendStatusNotification(connectorID int, errorCode, status string) error {
    req := core.NewStatusNotificationRequest(
        connectorID,
        core.ChargePointErrorCode(errorCode),
        core.ChargePointStatus(status),
    )
    req.Timestamp = ocppj.NewDateTime(time.Now())
    _, err := b.cp.SendRequest(req)
    return err
}

// SendStartTransaction sends a StartTransaction request and returns the CSMS-assigned transaction ID.
func (b *Bridge) SendStartTransaction(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
    req := core.NewStartTransactionRequest(connectorID, idTag, int(meterStart), ocppj.NewDateTime(timestamp))
    if reservationID != nil {
        req.ReservationId = *reservationID
    }
    resp, err := b.cp.SendRequest(req)
    if err != nil {
        return 0, err
    }
    startResp := resp.(*core.StartTransactionResponse)
    return startResp.TransactionId, nil
}

// SendStopTransaction sends a StopTransaction request.
func (b *Bridge) SendStopTransaction(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error {
    req := core.NewStopTransactionRequest(int(meterStop), ocppj.NewDateTime(timestamp), transactionID)
    req.Reason = core.Reason(reason)
    // MeterValues from history — omit for now; added in Plan 5b.
    _, err := b.cp.SendRequest(req)
    return err
}

// SendMeterValues sends a MeterValues message.
func (b *Bridge) SendMeterValues(connectorID int, value float64, transactionID int, context string) error {
    // Implemented fully in Plan 5b.
    return nil
}

// SendAuthorize sends an Authorize request.
func (b *Bridge) SendAuthorize(idTag string) error {
    _, err := b.cp.SendRequest(core.NewAuthorizeRequest(idTag))
    return err
}

// SendDataTransfer sends a DataTransfer request.
func (b *Bridge) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
    // Implemented in Plan 5e.
    return "Accepted", "", nil
}

// SendDiagnosticsStatusNotification sends DiagnosticsStatusNotification.
func (b *Bridge) SendDiagnosticsStatusNotification(status string) error {
    // Implemented in Plan 5e.
    return nil
}

// SendFirmwareStatusNotification sends FirmwareStatusNotification.
func (b *Bridge) SendFirmwareStatusNotification(status string) error {
    // Implemented in Plan 5e.
    return nil
}

// --- OCPPReceiver stubs (inbound handlers from CSMS) ---
// All inbound handlers are no-ops until Plan 5b wires the full transaction flow.

func (b *Bridge) OnChangeAvailability(request *core.ChangeAvailabilityRequest) (confirmation *core.ChangeAvailabilityResponse, err error) {
    return core.NewChangeAvailabilityResponse(core.AvailabilityStatusRejected), nil
}

func (b *Bridge) OnChangeConfiguration(request *core.ChangeConfigurationRequest) (confirmation *core.ChangeConfigurationResponse, err error) {
    return core.NewChangeConfigurationResponse(core.ConfigurationStatusNotSupported), nil
}

func (b *Bridge) OnClearCache(request *core.ClearCacheRequest) (confirmation *core.ClearCacheResponse, err error) {
    return core.NewClearCacheResponse(core.ClearCacheStatusAccepted), nil
}

func (b *Bridge) OnDataTransfer(request *core.DataTransferRequest) (confirmation *core.DataTransferResponse, err error) {
    return core.NewDataTransferResponse(core.DataTransferStatusUnknownVendorId), nil
}

func (b *Bridge) OnGetConfiguration(request *core.GetConfigurationRequest) (confirmation *core.GetConfigurationResponse, err error) {
    return core.NewGetConfigurationResponse([]core.ConfigurationKey{}, []string{}), nil
}

func (b *Bridge) OnRemoteStartTransaction(request *core.RemoteStartTransactionRequest) (confirmation *core.RemoteStartTransactionResponse, err error) {
    return core.NewRemoteStartTransactionResponse(core.RemoteStartStopStatusRejected), nil
}

func (b *Bridge) OnRemoteStopTransaction(request *core.RemoteStopTransactionRequest) (confirmation *core.RemoteStopTransactionResponse, err error) {
    return core.NewRemoteStopTransactionResponse(core.RemoteStartStopStatusRejected), nil
}

func (b *Bridge) OnReset(request *core.ResetRequest) (confirmation *core.ResetResponse, err error) {
    return core.NewResetResponse(core.ResetStatusRejected), nil
}

func (b *Bridge) OnUnlockConnector(request *core.UnlockConnectorRequest) (confirmation *core.UnlockConnectorResponse, err error) {
    return core.NewUnlockConnectorResponse(core.UnlockStatusUnlockFailed), nil
}
```

- [ ] **Step 2: Build**

```bash
go build ./...
```

Expected: no errors. (The Bridge implements `core.ChargePointCoreHandler` via the On* methods.)

- [ ] **Step 3: Commit**

```bash
git add internal/ocpp/bridge.go
git commit -m "feat(ocpp): Bridge with BootNotification, Heartbeat, and stub inbound handlers"
```

---

## Task 5: Wire Bridge into main.go

**Files:**
- Modify: `cmd/chargeghost/main.go`

- [ ] **Step 1: Create and start the bridge**

In `cmd/chargeghost/main.go`, after starting the hub:

```go
import (
    // ... existing imports ...
    "github.com/chargeghost/engine/internal/ocpp"
)

// After hub setup:
dispatcher := ocpp.NewCommandDispatcher()
go dispatcher.Run(ctx)

bridge := ocpp.NewBridge(e, hub, cfg, dispatcher)

// Wire engine callbacks to enqueue OCPP sends.
// These replace the nil callbacks set earlier if any were set.
// (Session callbacks for StartTransaction/StopTransaction are wired in Plan 5b.)
e.OnConnectorStatusChanged = func(connectorID int, status engine.ConnectorState) {
    hub.BroadcastMessage(ws.Message{
        Type: "connector_status_changed",
        Data: map[string]interface{}{
            "connector_id": connectorID,
            "status":       string(status),
        },
    })
    if bridge.IsConnected() {
        dispatcher.Enqueue(ocpp.OCPPCommand{
            Description: fmt.Sprintf("StatusNotification connector %d", connectorID),
            Execute: func() error {
                return bridge.SendStatusNotification(connectorID, "NoError", string(status))
            },
        })
    }
}

// Start the bridge (connects to CSMS).
go bridge.Start(ctx)
```

Add `"fmt"` to imports if not already present.

- [ ] **Step 2: Build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Manual verification (requires a running CSMS)**

Start a CSMS (e.g., SteVe at `localhost:8180` or any OCPP 1.6 CSMS). Update `cfg.ConnectionURL` if needed:

```bash
export OCPP_URL="ws://localhost:8180/steve/websocket/CentralSystemService"
# Or edit DefaultConfig() temporarily.
./chargeghost
```

Expected in logs:
```
OCPP bridge connecting url=ws://... id=CP_1
OCPP connected
BootNotification response status=Accepted interval=300
```

If no CSMS is available, verify that the binary starts without errors and the `/ws` endpoint still delivers ticks.

- [ ] **Step 4: Run all tests**

```bash
go test ./... -count=1 -timeout 30s
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/chargeghost/main.go
git commit -m "feat(cmd): wire OCPP Bridge and CommandDispatcher"
```
