# Plan 05b — OCPP Transaction Flow

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Full charging session lifecycle flows through OCPP — StartTransaction, StopTransaction, StatusNotification, and MeterValues outbound; RemoteStart, RemoteStop, Reset, ChangeAvailability, UnlockConnector, TriggerMessage, and ClearCache inbound.

**Architecture:** All outbound messages are enqueued via `CommandDispatcher` (non-blocking, from engine callbacks). Inbound handlers call engine methods directly (they hold the OCPP library's own mutex, not the engine mutex, so this is safe). CSMS-assigned transaction IDs are fed back to the engine via `SetActiveTransaction`. MeterValues are sent on a periodic goroutine keyed by `MeterValueSampleInterval` OCPP config key (default 30s; actual config key management is in Plan 5d — use a hardcoded 30s default here).

**Tech Stack:** `lorenzodonini/ocpp-go`, Go 1.22

---

## File Map

| File | Responsibility |
|------|----------------|
| `internal/ocpp/bridge.go` | Modified: implement all inbound handlers + outbound sends |
| `internal/ocpp/meter_ticker.go` | Periodic MeterValues sender goroutine |

---

## Task 1: Outbound Transaction Messages

**Files:**
- Modify: `internal/ocpp/bridge.go`

- [ ] **Step 1: Wire engine session callbacks in main.go**

In `cmd/chargeghost/main.go`, after creating `bridge` and `dispatcher`, add engine session callbacks:

```go
e.OnSessionStarted = func(connectorID int) {
    hub.BroadcastMessage(ws.Message{
        Type: "session_started",
        Data: map[string]interface{}{"connector_id": connectorID},
    })
    if !bridge.IsConnected() {
        return
    }
    session := e.GetSession(connectorID)
    if session == nil {
        return
    }
    idTag := ""
    if session.IDTag != nil {
        idTag = *session.IDTag
    }
    meter, _ := e.GetMeterSnapshot(connectorID)
    reservationID := session.ReservationID

    dispatcher.Enqueue(ocpp.OCPPCommand{
        Description: fmt.Sprintf("StartTransaction connector %d", connectorID),
        Execute: func() error {
            txID, err := bridge.SendStartTransaction(connectorID, idTag, meter, time.Now(), reservationID)
            if err != nil {
                return err
            }
            // Feed CSMS-assigned transaction ID back to the engine.
            e.SetActiveTransaction(connectorID, txID)
            return nil
        },
    })
}

e.OnSessionStopped = func(connectorID int) {
    info := e.GetLastStoppedSession()
    hub.BroadcastMessage(ws.Message{
        Type: "session_stopped",
        Data: map[string]interface{}{
            "connector_id":      connectorID,
            "transaction_id":    info.TransactionID,
            "energy_charged_wh": info.EnergyCharged,
            "reason":            info.Reason,
        },
    })
    if !bridge.IsConnected() || info == nil {
        return
    }
    snapshot := *info
    dispatcher.Enqueue(ocpp.OCPPCommand{
        Description: fmt.Sprintf("StopTransaction connector %d tx %d", connectorID, snapshot.TransactionID),
        Execute: func() error {
            return bridge.SendStopTransaction(snapshot.MeterStop, time.Now(), snapshot.TransactionID, snapshot.Reason, snapshot.MeterHistory)
        },
    })
}
```

- [ ] **Step 2: Implement SendStopTransaction with MeterValues in bridge.go**

Replace the stub `SendStopTransaction` in `internal/ocpp/bridge.go`:

```go
func (b *Bridge) SendStopTransaction(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error {
    req := core.NewStopTransactionRequest(int(meterStop), ocppj.NewDateTime(timestamp), transactionID)
    req.Reason = core.Reason(reason)

    // Include last meter values from history as TransactionData.
    if len(meterHistory) > 0 {
        var sampledValues []types.SampledValue
        for _, record := range meterHistory {
            t := ocppj.NewDateTime(mustParseTime(record.Timestamp))
            sampledValues = append(sampledValues, types.SampledValue{
                Value:   fmt.Sprintf("%.2f", record.Value),
                Context: types.ReadingContextSamplePeriodic,
                Unit:    types.UnitOfMeasureWh,
                Measurand: types.MeasurandEnergyActiveImportRegister,
            })
            _ = t
        }
        // TransactionData is a slice of MeterValue (each with a timestamp).
        last := meterHistory[len(meterHistory)-1]
        ts, _ := time.Parse(time.RFC3339Nano, last.Timestamp)
        req.TransactionData = []types.MeterValue{
            {
                Timestamp:     ocppj.NewDateTime(ts),
                SampledValue:  sampledValues,
            },
        }
    }
    _, err := b.cp.SendRequest(req)
    return err
}

func mustParseTime(s string) time.Time {
    t, err := time.Parse(time.RFC3339Nano, s)
    if err != nil {
        return time.Now()
    }
    return t
}
```

Add import: `"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"`

- [ ] **Step 3: Implement SendMeterValues**

Replace the stub `SendMeterValues` in `bridge.go`:

```go
func (b *Bridge) SendMeterValues(connectorID int, value float64, transactionID int, meterContext string) error {
    req := core.NewMeterValuesRequest(connectorID, []types.MeterValue{
        {
            Timestamp: ocppj.NewDateTime(time.Now()),
            SampledValue: []types.SampledValue{
                {
                    Value:     fmt.Sprintf("%.2f", value),
                    Context:   types.ReadingContextSamplePeriodic,
                    Format:    types.ValueFormatRaw,
                    Measurand: types.MeasurandEnergyActiveImportRegister,
                    Unit:      types.UnitOfMeasureWh,
                },
            },
        },
    })
    if transactionID != 0 {
        req.TransactionId = &transactionID
    }
    _, err := b.cp.SendRequest(req)
    return err
}
```

- [ ] **Step 4: Commit**

```bash
git add internal/ocpp/bridge.go cmd/chargeghost/main.go
git commit -m "feat(ocpp): outbound StartTransaction, StopTransaction, MeterValues"
```

---

## Task 2: Periodic MeterValues Sender

**Files:**
- Create: `internal/ocpp/meter_ticker.go`

- [ ] **Step 1: Implement meter_ticker.go**

Create `internal/ocpp/meter_ticker.go`:

```go
package ocpp

import (
    "context"
    "time"

    engine "github.com/chargeghost/engine/internal/engine"
)

// StartMeterValueTicker periodically sends MeterValues for all active sessions.
// interval defaults to 30s (overridden by MeterValueSampleInterval config key in Plan 5d).
// Call in a dedicated goroutine.
func StartMeterValueTicker(ctx context.Context, e *engine.Engine, bridge *Bridge, interval time.Duration) {
    if interval <= 0 {
        interval = 30 * time.Second
    }
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if !bridge.IsConnected() {
                continue
            }
            for _, connID := range e.GetConnectorIDs() {
                meterReading, txID := e.GetMeterSnapshot(connID)
                if txID == 0 {
                    continue // no active transaction
                }
                cid := connID
                bridge.dispatcher.Enqueue(OCPPCommand{
                    Description: "MeterValues",
                    Execute: func() error {
                        return bridge.SendMeterValues(cid, meterReading, txID, "Sample.Periodic")
                    },
                })
            }
        }
    }
}
```

- [ ] **Step 2: Wire in main.go**

In `cmd/chargeghost/main.go`, after starting the bridge:

```go
go ocpp.StartMeterValueTicker(ctx, e, bridge, 30*time.Second)
```

- [ ] **Step 3: Commit**

```bash
git add internal/ocpp/meter_ticker.go cmd/chargeghost/main.go
git commit -m "feat(ocpp): periodic MeterValues sender"
```

---

## Task 3: Inbound Handlers

**Files:**
- Modify: `internal/ocpp/bridge.go`

- [ ] **Step 1: Implement OnRemoteStartTransaction**

Replace the stub in `bridge.go`:

```go
func (b *Bridge) OnRemoteStartTransaction(request *core.RemoteStartTransactionRequest) (*core.RemoteStartTransactionResponse, error) {
    connectorID := 1 // default
    if request.ConnectorId != nil {
        connectorID = *request.ConnectorId
    }

    var profile *engine.ChargingProfile
    if request.ChargingProfile != nil {
        profile = convertChargingProfile(request.ChargingProfile, connectorID)
    }

    err := b.engine.StartSession(connectorID, -1, 0.0, &request.IdTag, 30)
    if err != nil {
        return core.NewRemoteStartTransactionResponse(core.RemoteStartStopStatusRejected), nil
    }

    // Store the charging profile for use when session starts.
    if session := b.engine.GetSession(connectorID); session != nil && profile != nil {
        session.RemoteStartChargingProfile = profile
    }

    return core.NewRemoteStartTransactionResponse(core.RemoteStartStopStatusAccepted), nil
}
```

- [ ] **Step 2: Implement OnRemoteStopTransaction**

Replace the stub:

```go
func (b *Bridge) OnRemoteStopTransaction(request *core.RemoteStopTransactionRequest) (*core.RemoteStopTransactionResponse, error) {
    connectorID := b.engine.GetConnectorByTransaction(request.TransactionId)
    if connectorID == nil {
        return core.NewRemoteStopTransactionResponse(core.RemoteStartStopStatusRejected), nil
    }
    b.engine.StopSession(connectorID, "Remote")
    return core.NewRemoteStopTransactionResponse(core.RemoteStartStopStatusAccepted), nil
}
```

- [ ] **Step 3: Implement OnReset**

Replace the stub:

```go
func (b *Bridge) OnReset(request *core.ResetRequest) (*core.ResetResponse, error) {
    reason := "SoftReset"
    if request.Type == core.ResetTypeHard {
        reason = "HardReset"
    }
    // Stop all sessions.
    for _, id := range b.engine.GetConnectorIDs() {
        cid := id
        b.engine.StopSession(&cid, reason)
    }
    return core.NewResetResponse(core.ResetStatusAccepted), nil
}
```

- [ ] **Step 4: Implement OnChangeAvailability**

Replace the stub:

```go
func (b *Bridge) OnChangeAvailability(request *core.ChangeAvailabilityRequest) (*core.ChangeAvailabilityResponse, error) {
    availType := string(request.Type) // "Operative" or "Inoperative"
    result := b.engine.SetConnectorAvailability(request.ConnectorId, availType)
    switch result {
    case "accepted":
        return core.NewChangeAvailabilityResponse(core.AvailabilityStatusAccepted), nil
    case "scheduled":
        return core.NewChangeAvailabilityResponse(core.AvailabilityStatusScheduled), nil
    default:
        return core.NewChangeAvailabilityResponse(core.AvailabilityStatusRejected), nil
    }
}
```

- [ ] **Step 5: Implement OnUnlockConnector**

Replace the stub:

```go
func (b *Bridge) OnUnlockConnector(request *core.UnlockConnectorRequest) (*core.UnlockConnectorResponse, error) {
    // Unlocking is always successful in simulation.
    return core.NewUnlockConnectorResponse(core.UnlockStatusUnlocked), nil
}
```

- [ ] **Step 6: Implement OnClearCache**

Replace the stub:

```go
func (b *Bridge) OnClearCache(request *core.ClearCacheRequest) (*core.ClearCacheResponse, error) {
    // Auth cache cleared in Plan 5d; for now just return Accepted.
    return core.NewClearCacheResponse(core.ClearCacheStatusAccepted), nil
}
```

- [ ] **Step 7: Add convertChargingProfile helper**

Add to `bridge.go`:

```go
// convertChargingProfile maps the lorenzodonini type to the engine type.
func convertChargingProfile(p *types.ChargingProfile, connectorID int) *engine.ChargingProfile {
    if p == nil {
        return nil
    }
    profile := &engine.ChargingProfile{
        ProfileID:   p.ChargingProfileId,
        ConnectorID: connectorID,
        StackLevel:  p.StackLevel,
        Purpose:     string(p.ChargingProfilePurpose),
        Kind:        string(p.ChargingProfileKind),
    }
    if p.RecurrencyKind != "" {
        profile.RecurrencyKind = string(p.RecurrencyKind)
    }
    if p.ValidFrom != nil {
        t := p.ValidFrom.Time
        profile.ValidFrom = &t
    }
    if p.ValidTo != nil {
        t := p.ValidTo.Time
        profile.ValidTo = &t
    }
    if p.ChargingSchedule != nil {
        sched := engine.ChargingSchedule{
            ChargingRateUnit: string(p.ChargingSchedule.ChargingRateUnit),
            Duration:         p.ChargingSchedule.Duration,
        }
        if p.ChargingSchedule.StartSchedule != nil {
            t := p.ChargingSchedule.StartSchedule.Time
            sched.StartSchedule = &t
            profile.StartSchedule = &t
        }
        for _, period := range p.ChargingSchedule.ChargingSchedulePeriod {
            p2 := period
            sp := engine.ChargingSchedulePeriod{
                StartPeriod:  p2.StartPeriod,
                Limit:        p2.Limit,
            }
            if p2.NumberPhases != nil {
                n := *p2.NumberPhases
                sp.NumberPhases = &n
            }
            sched.Periods = append(sched.Periods, sp)
        }
        profile.Schedule = sched
    }
    return profile
}
```

- [ ] **Step 8: Build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 9: Commit**

```bash
git add internal/ocpp/bridge.go
git commit -m "feat(ocpp): inbound handlers RemoteStart/Stop, Reset, ChangeAvailability, UnlockConnector"
```

---

## Task 4: TriggerMessage Handler

**Files:**
- Modify: `internal/ocpp/bridge.go`

The `OnTriggerMessage` handler lives in the `remotetrigger` feature package of lorenzodonini/ocpp-go.

- [ ] **Step 1: Register remotetrigger handler**

In `NewBridge`, after setting the core handler:

```go
import (
    "github.com/lorenzodonini/ocpp-go/ocpp1.6/remotetrigger"
)

// In NewBridge:
b.cp.SetRemoteTriggerHandler(b)
```

- [ ] **Step 2: Implement OnTriggerMessage**

Add to `bridge.go`:

```go
func (b *Bridge) OnTriggerMessage(request *remotetrigger.TriggerMessageRequest) (*remotetrigger.TriggerMessageResponse, error) {
    switch request.RequestedMessage {
    case remotetrigger.MessageTriggerBootNotification:
        b.dispatcher.Enqueue(OCPPCommand{Description: "TriggerBootNotification", Execute: b.SendBootNotification})
        return remotetrigger.NewTriggerMessageResponse(remotetrigger.TriggerMessageStatusAccepted), nil
    case remotetrigger.MessageTriggerHeartbeat:
        b.dispatcher.Enqueue(OCPPCommand{Description: "TriggerHeartbeat", Execute: b.SendHeartbeat})
        return remotetrigger.NewTriggerMessageResponse(remotetrigger.TriggerMessageStatusAccepted), nil
    case remotetrigger.MessageTriggerStatusNotification:
        connID := 1
        if request.ConnectorId != nil {
            connID = *request.ConnectorId
        }
        b.dispatcher.Enqueue(OCPPCommand{
            Description: "TriggerStatusNotification",
            Execute: func() error {
                return b.SendStatusNotification(connID, "NoError", b.engine.GetConnectorStatus(connID))
            },
        })
        return remotetrigger.NewTriggerMessageResponse(remotetrigger.TriggerMessageStatusAccepted), nil
    case remotetrigger.MessageTriggerMeterValues:
        connID := 1
        if request.ConnectorId != nil {
            connID = *request.ConnectorId
        }
        b.dispatcher.Enqueue(OCPPCommand{
            Description: "TriggerMeterValues",
            Execute: func() error {
                reading, txID := b.engine.GetMeterSnapshot(connID)
                return b.SendMeterValues(connID, reading, txID, "Trigger")
            },
        })
        return remotetrigger.NewTriggerMessageResponse(remotetrigger.TriggerMessageStatusAccepted), nil
    default:
        return remotetrigger.NewTriggerMessageResponse(remotetrigger.TriggerMessageStatusNotImplemented), nil
    }
}
```

- [ ] **Step 3: Build and run tests**

```bash
go build ./...
go test ./... -count=1 -timeout 30s
```

Expected: no errors, all tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/ocpp/bridge.go
git commit -m "feat(ocpp): TriggerMessage handler (BootNotification, Heartbeat, StatusNotification, MeterValues)"
```

---

## Task 5: End-to-End Verification

- [ ] **Step 1: Integration test against CSMS**

Start your CSMS and run `./chargeghost`. Perform the following sequence via the REST API:

```bash
# Plug in connector 1.
curl -s -X POST http://localhost:8080/api/v1/connectors/1/plug_in

# Send RemoteStart from CSMS UI or API.
# Verify: StartTransaction sent, CSMS assigns transaction ID, engine's session.TransactionID updated.

# Wait 60 seconds. Verify: MeterValues sent every 30s.

# Send RemoteStop from CSMS.
# Verify: StopTransaction sent with correct energy and reason="Remote".
```

- [ ] **Step 2: Run all tests**

```bash
go test ./... -count=1 -timeout 30s
```

Expected: all pass.
