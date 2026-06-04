# Plan 10: Runtime Config Re-apply (P1-10)

**Date:** 2026-06-04
**Version:** v1
**Source review:** 2026-06-04 OCPP communications review, recommendations P1-10
**Priority:** P1 — recovery

## Objective

When the CSMS changes config keys (`HeartbeatInterval`,
`MeterValueSampleInterval`, `WebSocketPingInterval`,
`RetryBackOffRepeatTimes`) at runtime, apply the new value without restart.

## Background

`internal/ocpp/v16/handlers.go:46-51` already handles
`HeartbeatInterval` changes by calling `b.restartHeartbeat()`. But:

- `MeterValueSampleInterval` (v1.6) is only read at meter-ticker start.
- v2.0.1 device model variables (`HeartbeatInterval`,
  `WebSocketPingInterval`, `RetryBackOffRepeatTimes`, `TxUpdatedInterval`)
  are stored in `DeviceModel` but the running heartbeat/meter/drain loops
  may not pick up changes.

## Design

### 1. ConfigChange channel

The `DeviceModel` already has a `ConfigChanges() <-chan struct{}` channel
(`internal/ocpp/v201/device_model.go:102-104`). Bridge201 subscribes in
`Start()` and on each signal, re-reads the relevant variables and restarts
the affected goroutines.

For v1.6, add an equivalent signal mechanism — either an explicit
`OnConfigKeyChanged` callback that the bridge subscribes to, or a poll every
N seconds (simpler).

### 2. Re-arm heartbeat

`b.restartHeartbeat()` already exists; call it from the config-change
handler when `HeartbeatInterval` changes.

### 3. Re-arm meter ticker

`internal/ocpp/meter_ticker.go` runs with a `time.Ticker`. Add a
`SetInterval(d time.Duration)` method that swaps the ticker. Subscribe to
config changes.

### 4. Re-arm drain interval

`Bridge201` drain loop interval comes from
`b.transactionMessageRetryInterval()` (`internal/ocpp/v201/queue_drain.go:48-50`).
On config change, the next drain tick uses the new value automatically (no
re-arm needed). Verify.

## Files Touched

- **Edit:** `internal/ocpp/v201/bridge.go` (subscribe to ConfigChanges in
  Start; handler re-arms)
- **Edit:** `internal/ocpp/v16/bridge.go` (config-change handler, meter
  re-arm)
- **Edit:** `internal/ocpp/meter_ticker.go` (SetInterval)
- **Edit:** `cmd/chargeghost/main.go` (wire v1.6 config signal)
- **Edit:** tests

## Acceptance Criteria

- CSMS changes `HeartbeatInterval` → next heartbeat uses new value.
- CSMS changes `MeterValueSampleInterval` → meter ticker uses new value.
- CSMS changes `RetryBackOffRepeatTimes` → next drain uses new value.
- Tests pass.

## Tasks

- [x] Add `SetInterval` to meter ticker (already supported via `ConfigChangeNotifier` subscription in `internal/ocpp/meter_ticker.go:22-23`)
- [x] Subscribe to `DeviceModel.ConfigChanges` in `Bridge201.Start` (already in `internal/ocpp/v201/bridge.go:372` and re-applied in `heartbeatLoopCtx`)
- [x] Implement v1.6 config-change signal (already in `internal/ocpp/v16/bridge.go:318` via `b.configKeys.ConfigChanges()`)
- [x] Re-arm heartbeat, meter, drain on change (heartbeat loops already re-arm; drain re-reads interval per tick)
- [x] Tests (`internal/ocpp/runtime_config_reapply_test.go` verifies the `ConfigChangeNotifier` contract)
- [x] Run `go build ./...` and `go test ./...`
