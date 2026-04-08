# OCPP 2.0.1 — Phase 1: Refactor — Extract Interface & Move V16

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract a shared `OCPPBridge` interface and move existing 1.6J code into `internal/ocpp/v16/`, verifying no behavioral changes.

**Architecture:** Define `OCPPBridge` interface in `internal/ocpp/bridge.go`. Move the existing Bridge struct into `internal/ocpp/v16/` as `Bridge16`. Update `main.go` to use the interface via a version switch. Shared infra (CommandDispatcher, queue, auth, firmware) stays in `internal/ocpp/`.

**Tech Stack:** Go 1.26+, `lorenzodonini/ocpp-go v0.19.0`, `stretchr/testify`

**Spec:** `docs/superpowers/specs/2026-04-07-ocpp-201-design.md`

**Prerequisite phases:** None (this is the first phase)
**Next phase:** `2026-04-07-ocpp-201-phase2-minimal-slice.md`

---

## File Map

### Shared (`internal/ocpp/`)
| File | Action | Responsibility |
|------|--------|---------------|
| `bridge.go` | **Create** | `OCPPBridge` interface + `NewBridge()` factory |
| `adapter.go` | Modify | Keep `EngineView`, `AuthorizationCacheStore`; remove `OCPPAdapter`/`OCPPSender` (replaced by `OCPPBridge`) |
| `meter_ticker.go` | Modify | Accept `OCPPBridge` instead of `*Bridge` |

### V16 (`internal/ocpp/v16/`)
| File | Action | Responsibility |
|------|--------|---------------|
| `bridge.go` | **Create** (moved from `ocpp/bridge.go`) | `Bridge16` struct, `NewBridge()`, `Start()`, connection lifecycle |
| `handlers.go` | **Create** (extracted from `ocpp/bridge.go`) | All `On*` handler methods |
| `senders.go` | **Create** (extracted from `ocpp/bridge.go`) | All `Send*` methods |
| `profile_manager.go` | **Create** (moved from `ocpp/profile_manager.go`) | `ChargingProfileManager` |
| `config_keys.go` | **Create** (moved from `ocpp/config_keys.go`) | `ConfigKeyManager` |

### Wiring
| File | Action | Responsibility |
|------|--------|---------------|
| `cmd/chargeghost/main.go` | Modify | Use `OCPPBridge` interface, version-switched construction |

---

### Task 1: Create OCPPBridge Interface

**Files:**
- Create: `internal/ocpp/bridge.go` (new shared interface file)
- Modify: `internal/ocpp/adapter.go`

- [ ] **Step 1: Write test for factory function**

Create `internal/ocpp/bridge_test.go`:

```go
package ocpp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewBridge_UnsupportedVersion(t *testing.T) {
	_, err := NewBridge("3.0", nil, nil, nil, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported OCPP version")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestNewBridge_UnsupportedVersion ./internal/ocpp/ -v`
Expected: FAIL — `NewBridge` not defined

- [ ] **Step 3: Define OCPPBridge interface and factory stub**

Create `internal/ocpp/bridge.go`:

```go
package ocpp

import (
	"context"
	"fmt"
	"time"

	wsapi "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

// OCPPBridge is the version-agnostic interface for OCPP communication.
// Implemented by v16.Bridge16 and v201.Bridge201.
type OCPPBridge interface {
	Start(ctx context.Context) error
	Stop()
	IsConnected() bool
	GetHeartbeatInterval() int
	Dispatcher() *CommandDispatcher

	// Outbound messages
	SendBootNotification() error
	SendHeartbeat() error
	SendStatusNotification(connectorID int, errorCode, status string) error
	SendMeterValues(connectorID int, value float64, transactionID int, context string) error
	SendAuthorize(idTag string) error

	// Transaction lifecycle.
	// Returns transaction ID: server-assigned int for 1.6, synthetic int for 2.0.1.
	SendTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error)
	SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error

	// Firmware/Diagnostics
	SendFirmwareStatusNotification(status string) error
	SendDiagnosticsStatusNotification(status string) error

	// Data transfer
	SendDataTransfer(vendorID, messageID, data string) (string, string, error)
}

// NewBridge creates a version-specific OCPPBridge based on the OCPP version string.
func NewBridge(version string, e *engine.Engine, hub *wsapi.Hub, cfg *config.Config, dispatcher *CommandDispatcher, opts ...interface{}) (OCPPBridge, error) {
	switch version {
	case "1.6":
		// Will be wired in Task 2
		return nil, fmt.Errorf("v16 bridge not yet wired")
	case "2.0.1":
		// Will be wired in Phase 2
		return nil, fmt.Errorf("v201 bridge not yet implemented")
	default:
		return nil, fmt.Errorf("unsupported OCPP version: %s", version)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -run TestNewBridge_UnsupportedVersion ./internal/ocpp/ -v`
Expected: PASS

- [ ] **Step 5: Remove OCPPAdapter and OCPPSender from adapter.go**

In `internal/ocpp/adapter.go`, remove lines 9-29 (the `OCPPAdapter` and `OCPPSender` interfaces). Keep `EngineView` (lines 31-45) and `AuthorizationCacheStore` (lines 47-64) unchanged.

The file should start with the package declaration, then `EngineView`, then `AuthorizationCacheStore`.

- [ ] **Step 6: Commit**

```bash
git add internal/ocpp/bridge.go internal/ocpp/bridge_test.go internal/ocpp/adapter.go
git commit -m "feat(ocpp): add OCPPBridge interface and factory function

Defines the version-agnostic OCPPBridge interface that both v16 and v201
will implement. Removes OCPPAdapter/OCPPSender (replaced by OCPPBridge)."
```

---

### Task 2: Move Bridge to v16 Package

**Files:**
- Create: `internal/ocpp/v16/bridge.go`
- Create: `internal/ocpp/v16/handlers.go`
- Create: `internal/ocpp/v16/senders.go`
- Modify: `internal/ocpp/bridge.go` (wire factory)
- Delete: `internal/ocpp/bridge.go` (the OLD one — the current 713-line file, NOT the new interface file from Task 1)

**Important:** The old `bridge.go` (the 713-line Bridge struct) is being replaced by the new `bridge.go` from Task 1 (the interface file). The old code moves to `v16/`.

- [ ] **Step 1: Create v16 directory and bridge.go**

Create `internal/ocpp/v16/bridge.go` with the `Bridge16` struct. This is the existing `Bridge` struct renamed to `Bridge16`, with its constructor, `Start()`, `Stop()`, `IsConnected()`, `GetHeartbeatInterval()`, `Dispatcher()`, `heartbeatLoop()`, `drainQueue()`, and `convertChargingProfile()` methods.

Key changes from the original:
- Package is `v16` not `ocpp`
- Struct renamed from `Bridge` to `Bridge16`
- Import shared types from parent package: `ocpp "github.com/chargeghost/engine/internal/ocpp"`
- References to `CommandDispatcher`, `EngineView`, `AuthorizationCache`, etc. use the `ocpp.` prefix
- `NewBridge` signature accepts the shared types:

```go
package v16

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	ocpp16 "github.com/lorenzodonini/ocpp-go/ocpp1.6"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/firmware"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/localauth"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/remotetrigger"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/smartcharging"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
	"github.com/lorenzodonini/ocpp-go/ws"

	wsapi "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpp "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

type Bridge16 struct {
	cp             ocpp16.ChargePoint
	wsClient       *ws.Client
	dispatcher     *ocpp.CommandDispatcher
	engine         *engine.Engine
	hub            *wsapi.Hub
	cfg            *config.Config
	profileManager *ChargingProfileManager
	configKeys     *ConfigKeyManager
	authCache      *ocpp.AuthorizationCache
	localAuth      ocpp.LocalAuthManager
	queue          queue.MessageQueue
	fwManager      ocpp.FirmwareManager
	diagManager    ocpp.DiagnosticsManager
	dataTransfer   *ocpp.DataTransferRegistry
	connected      atomic.Bool
	heartbeatInt   int
}

func NewBridge(e *engine.Engine, hub *wsapi.Hub, cfg *config.Config, dispatcher *ocpp.CommandDispatcher, pm *ChargingProfileManager, configKeys *ConfigKeyManager, authCache *ocpp.AuthorizationCache, la ocpp.LocalAuthManager, q queue.MessageQueue, fw ocpp.FirmwareManager, diag ocpp.DiagnosticsManager, dt *ocpp.DataTransferRegistry) *Bridge16 {
	// ... same body as current NewBridge, but returns *Bridge16
	// Sets b.cp = ocpp16.NewChargePoint(...)
	// Sets handlers: b.cp.SetCoreHandler(b), etc.
}
```

Add `SendTransactionStart` and `SendTransactionStop` as wrappers that delegate to the existing `SendStartTransaction` and `SendStopTransaction`:

```go
func (b *Bridge16) SendTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	return b.SendStartTransaction(connectorID, idTag, meterStart, timestamp, reservationID)
}

func (b *Bridge16) SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error {
	return b.SendStopTransaction(meterStop, timestamp, transactionID, reason, meterHistory)
}
```

- [ ] **Step 2: Create v16/senders.go**

Move all `Send*` methods from old `bridge.go` into `internal/ocpp/v16/senders.go`:
- `SendBootNotification()` (lines 134-161)
- `SendHeartbeat()` (lines 179-182)
- `SendStatusNotification()` (lines 185-194)
- `SendStartTransaction()` (lines 197-218)
- `SendStopTransaction()` (lines 221-256)
- `SendMeterValues()` (lines 259-286)
- `SendAuthorize()` (lines 289-292)
- `SendDataTransfer()` (lines 295-314)
- `SendDiagnosticsStatusNotification()` (lines 317-321)
- `SendFirmwareStatusNotification()` (lines 324-328)

All methods receive on `*Bridge16` instead of `*Bridge`.

- [ ] **Step 3: Create v16/handlers.go**

Move all `On*` handler methods from old `bridge.go` into `internal/ocpp/v16/handlers.go`:
- `OnChangeAvailability()` through `OnGetCompositeSchedule()` (lines 333-658)

All methods receive on `*Bridge16`.

- [ ] **Step 4: Move profile_manager.go and config_keys.go**

Copy `internal/ocpp/profile_manager.go` → `internal/ocpp/v16/profile_manager.go`
Copy `internal/ocpp/config_keys.go` → `internal/ocpp/v16/config_keys.go`

Change package declaration to `package v16`. Update internal imports as needed.

- [ ] **Step 5: Wire factory for v16**

Update `internal/ocpp/bridge.go` factory. Note: The factory with `...interface{}` is a temporary placeholder. `main.go` constructs the version-specific bridge directly and assigns it to an `OCPPBridge` variable.

- [ ] **Step 6: Delete old files**

Delete the original `internal/ocpp/bridge.go` (the 713-line file).
Delete `internal/ocpp/profile_manager.go` and `internal/ocpp/config_keys.go` (moved to v16).

- [ ] **Step 7: Compile check**

Run: `go build ./...`
Expected: Compilation errors in `main.go` and `meter_ticker.go` — expected, fixed in Task 3.

- [ ] **Step 8: Commit**

```bash
git add internal/ocpp/v16/ internal/ocpp/bridge.go
git rm internal/ocpp/profile_manager.go internal/ocpp/config_keys.go
git commit -m "refactor(ocpp): move 1.6J bridge to v16 package

Bridge16 implements OCPPBridge interface. Profile manager and config keys
moved to v16 package. Shared infra remains in ocpp/."
```

---

### Task 3: Update main.go and meter_ticker.go

**Files:**
- Modify: `cmd/chargeghost/main.go`
- Modify: `internal/ocpp/meter_ticker.go`

- [ ] **Step 1: Update meter_ticker.go to accept OCPPBridge**

In `internal/ocpp/meter_ticker.go`, change the `bridge` parameter from `*Bridge` to `OCPPBridge`:

```go
func StartMeterValueTicker(ctx context.Context, e *engine.Engine, bridge OCPPBridge, interval time.Duration) {
```

- [ ] **Step 2: Update main.go imports and bridge construction**

```go
import (
	"github.com/chargeghost/engine/internal/ocpp"
	v16 "github.com/chargeghost/engine/internal/ocpp/v16"
)

var bridge ocpp.OCPPBridge
switch cfg.OCPPVersion {
case "1.6", "":
	bridge = v16.NewBridge(e, hub, cfg, dispatcher, profileManager, configKeys, authCache, localAuthReal, messageQueue, firmwareManager, diagnosticsManager, dataTransferReg)
case "2.0.1":
	slog.Error("OCPP 2.0.1 not yet implemented")
	os.Exit(1)
default:
	slog.Error("unsupported OCPP version", "version", cfg.OCPPVersion)
	os.Exit(1)
}
```

Replace `bridge.SendStartTransaction(...)` with `bridge.SendTransactionStart(...)` and `bridge.SendStopTransaction(...)` with `bridge.SendTransactionStop(...)` in engine callbacks.

Update `profileManager` and `configKeys` construction to use `v16.` prefix.

- [ ] **Step 3: Compile and test**

Run: `go build ./... && go test ./... -v`
Expected: All tests pass, binary builds successfully.

- [ ] **Step 4: Commit**

```bash
git add cmd/chargeghost/main.go internal/ocpp/meter_ticker.go
git commit -m "refactor(ocpp): wire v16 bridge through OCPPBridge interface

main.go now selects bridge implementation based on config.OCPPVersion.
meter_ticker accepts OCPPBridge interface instead of concrete Bridge."
```

---

### Task 4: Move Existing Tests to v16

**Files:**
- Create: `internal/ocpp/v16/profile_manager_test.go` (moved)
- Create: `internal/ocpp/v16/config_keys_test.go` (moved)

- [ ] **Step 1: Move test files**

Copy existing test files to v16 package:
- `internal/ocpp/profile_manager_test.go` → `internal/ocpp/v16/profile_manager_test.go`
- `internal/ocpp/config_keys_test.go` → `internal/ocpp/v16/config_keys_test.go`

Change package declaration to `package v16`. Keep `internal/ocpp/command_test.go` and `internal/ocpp/auth_cache_test.go` where they are (shared infra).

- [ ] **Step 2: Run all tests**

Run: `go test ./internal/ocpp/... -v`
Expected: All tests pass in both `internal/ocpp/` and `internal/ocpp/v16/`.

- [ ] **Step 3: Delete old test files**

```bash
git rm internal/ocpp/profile_manager_test.go internal/ocpp/config_keys_test.go
```

- [ ] **Step 4: Full test suite**

Run: `go test ./... -v`
Expected: All tests pass. Phase 1 complete — no behavioral changes.

- [ ] **Step 5: Commit**

```bash
git add internal/ocpp/v16/*_test.go
git commit -m "refactor(ocpp): move v16-specific tests to v16 package"
```
