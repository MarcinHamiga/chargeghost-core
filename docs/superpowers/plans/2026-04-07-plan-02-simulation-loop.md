# Plan 02 — Simulation Loop

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run the engine in real time using a fixed-timestep loop that accumulates energy and auto-suspends/resumes via the `GetLimit` callback.

**Architecture:** A `Runtime` struct owns a goroutine that ticks at 20 Hz, accumulates time, and calls `Engine.Simulate` in 100 ms steps (max 5 per wake-up). `GetLimit` defaults to nil (no profiles). `cmd/chargeghost/main.go` wires and starts the runtime.

**Tech Stack:** Go 1.22 stdlib only (`context`, `time`).

---

## File Map

| File | Responsibility |
|------|----------------|
| `internal/runtime/runtime.go` | `Runtime` struct — fixed-timestep loop, context cancellation |
| `cmd/chargeghost/main.go` | Entry point: create engine, create runtime, run until signal |
| `internal/runtime/runtime_test.go` | Integration test: verify energy accumulates over real time |

---

## Task 1: Runtime Struct

**Files:**
- Create: `internal/runtime/runtime.go`
- Create: `internal/runtime/runtime_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/runtime/runtime_test.go`:

```go
package runtime_test

import (
    "context"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    engine "github.com/chargeghost/engine/internal/engine"
    rt "github.com/chargeghost/engine/internal/runtime"
)

func TestRuntime_AccumulatesEnergy(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)
    e.PlugIn(1)
    err := e.StartSession(1, -1, 0.0, nil, 0)
    if err != nil {
        t.Fatalf("StartSession: %v", err)
    }

    ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
    defer cancel()

    r := rt.NewRuntime(e)
    go r.Run(ctx)

    <-ctx.Done()

    meter := e.GetEnergyMeter(1)
    // 230V × 32A × 1 phase × 0.5s = 1.022 Wh — expect at least a small accumulation
    assert.Greater(t, meter.Value, 0.0, "energy meter should have accumulated Wh")
}

func TestRuntime_StopsOnContextCancel(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)

    ctx, cancel := context.WithCancel(context.Background())
    r := rt.NewRuntime(e)

    done := make(chan struct{})
    go func() {
        r.Run(ctx)
        close(done)
    }()

    cancel()
    select {
    case <-done:
        // good
    case <-time.After(500 * time.Millisecond):
        t.Fatal("runtime did not stop after context cancel")
    }
}

func TestRuntime_GetLimitSuspendResume(t *testing.T) {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)
    e.PlugIn(1)
    _ = e.StartSession(1, -1, 0.0, nil, 0)

    // Inject a limit of 0 → should trigger EVSE suspension
    zero := 0.0
    e.GetLimit = func(connectorID, transactionID int) *float64 {
        return &zero
    }

    ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
    defer cancel()
    r := rt.NewRuntime(e)
    go r.Run(ctx)
    <-ctx.Done()

    // Connector should be SuspendedEVSE
    c := e.GetConnector(1)
    assert.Equal(t, engine.StateSuspendedEVSE, c.Status)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/runtime/... -v
```

Expected: compile error — `runtime` package does not exist yet.

- [ ] **Step 3: Implement runtime.go**

Create `internal/runtime/runtime.go`:

```go
package runtime

import (
    "context"
    "time"

    engine "github.com/chargeghost/engine/internal/engine"
)

const (
    tickInterval = 50 * time.Millisecond  // 20 Hz wake-up
    stepInterval = 0.1                    // 100 ms simulation step in seconds
    maxSteps     = 5                      // spiral-of-death guard
)

// Runtime drives the fixed-timestep simulation loop.
type Runtime struct {
    engine      *engine.Engine
    lastTick    time.Time
    accumulator float64
}

// NewRuntime creates a Runtime that will drive the given engine.
func NewRuntime(e *engine.Engine) *Runtime {
    return &Runtime{
        engine:   e,
        lastTick: time.Now(),
    }
}

// Run blocks, calling engine.Simulate on a fixed timestep until ctx is cancelled.
// Call in a dedicated goroutine.
func (r *Runtime) Run(ctx context.Context) {
    ticker := time.NewTicker(tickInterval)
    defer ticker.Stop()
    r.lastTick = time.Now()

    for {
        select {
        case <-ctx.Done():
            return
        case now := <-ticker.C:
            delta := now.Sub(r.lastTick).Seconds()
            r.lastTick = now
            r.accumulator += delta

            steps := 0
            for r.accumulator >= stepInterval && steps < maxSteps {
                r.engine.Simulate(stepInterval)
                r.accumulator -= stepInterval
                steps++
            }
        }
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/runtime/... -v -timeout 10s
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/runtime.go internal/runtime/runtime_test.go
git commit -m "feat(runtime): fixed-timestep simulation loop"
```

---

## Task 2: Main Entry Point

**Files:**
- Create: `cmd/chargeghost/main.go`

- [ ] **Step 1: Implement main.go**

Create `cmd/chargeghost/main.go`:

```go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    engine "github.com/chargeghost/engine/internal/engine"
    rt "github.com/chargeghost/engine/internal/runtime"
)

func main() {
    slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo,
    })))

    // Default engine: single-EVSE, 55 kWh battery.
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    runtime := rt.NewRuntime(e)
    go runtime.Run(ctx)

    slog.Info("ChargeGhost engine started", "connectors", 1)

    // Wait for SIGINT or SIGTERM.
    sig := make(chan os.Signal, 1)
    signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
    <-sig

    slog.Info("shutting down")
    cancel()
}
```

- [ ] **Step 2: Build and run**

```bash
go build ./cmd/chargeghost/...
```

Expected: `chargeghost` binary produced with no errors.

```bash
./chargeghost &
sleep 1
kill %1
```

Expected output:
```
time=... level=INFO msg="ChargeGhost engine started" connectors=1
time=... level=INFO msg="shutting down"
```

- [ ] **Step 3: Commit**

```bash
git add cmd/chargeghost/main.go
git commit -m "feat(cmd): main entry point with graceful signal handling"
```

---

## Task 3: Final Verification

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -count=1 -timeout 30s
```

Expected: all tests PASS.

- [ ] **Step 2: Build check**

```bash
go build ./...
```

Expected: no errors.
