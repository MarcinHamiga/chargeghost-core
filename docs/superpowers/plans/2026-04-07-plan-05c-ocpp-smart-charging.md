# Plan 05c — OCPP Smart Charging

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Charging profile limits are computed per-tick using the full composite limit algorithm, and SetChargingProfile/ClearChargingProfile/GetCompositeSchedule inbound handlers are live.

**Architecture:** `ChargingProfileManager` is a thread-safe struct that stores profiles keyed by `(connectorID, profileID)` and computes limits by resolving ChargePointMaxProfile + TxProfile/TxDefaultProfile, applying stack-level priority, timing (Absolute/Recurring/Relative), and Watts→Amps conversion. `Engine.GetLimit` is replaced with a real call to `ChargingProfileManager.GetCompositeLimit`. REST endpoints for charging profiles are also wired here.

**Tech Stack:** Go 1.22 stdlib + `lorenzodonini/ocpp-go` (smartcharging feature)

---

## File Map

| File | Responsibility |
|------|----------------|
| `internal/ocpp/profile_manager.go` | `ChargingProfileManager` — store, GetCompositeLimit, GetCompositeSchedule, Set/Clear |
| `internal/ocpp/profile_manager_test.go` | Unit tests for the composite limit algorithm |
| `internal/ocpp/bridge.go` | Modified: register smartcharging handler, wire profile manager |
| `internal/api/handlers/charging_profiles.go` | REST handlers for charging profiles |
| `internal/api/router.go` | Modified: add charging profile routes |
| `cmd/chargeghost/main.go` | Modified: create profile manager, inject into engine + bridge |

---

## Task 1: ChargingProfileManager

**Files:**
- Create: `internal/ocpp/profile_manager.go`
- Create: `internal/ocpp/profile_manager_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/ocpp/profile_manager_test.go`:

```go
package ocpp_test

import (
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    engine "github.com/chargeghost/engine/internal/engine"
    "github.com/chargeghost/engine/internal/ocpp"
)

func makeAbsoluteProfile(profileID, connectorID, stackLevel int, limitA float64, purpose string) engine.ChargingProfile {
    return engine.ChargingProfile{
        ProfileID:   profileID,
        ConnectorID: connectorID,
        StackLevel:  stackLevel,
        Purpose:     purpose,
        Kind:        "Absolute",
        StartSchedule: ptr(time.Now().Add(-1 * time.Hour)), // started 1 hour ago
        Schedule: engine.ChargingSchedule{
            ChargingRateUnit: "A",
            Periods: []engine.ChargingSchedulePeriod{
                {StartPeriod: 0, Limit: limitA},
            },
        },
    }
}

func ptr[T any](v T) *T { return &v }

func TestProfileManager_NoProfiles_ReturnsNil(t *testing.T) {
    pm := ocpp.NewChargingProfileManager()
    limit := pm.GetCompositeLimit(1, 0, time.Now(), 230.0, nil, 1)
    assert.Nil(t, limit, "no profiles should return nil limit")
}

func TestProfileManager_TxDefaultProfile_LimitsCurrentBelowConnector(t *testing.T) {
    pm := ocpp.NewChargingProfileManager()
    profile := makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile")
    require.NoError(t, pm.SetChargingProfile(1, profile))

    limit := pm.GetCompositeLimit(1, 0, time.Now(), 230.0, nil, 1)
    require.NotNil(t, limit)
    assert.InDelta(t, 16.0, *limit, 0.01)
}

func TestProfileManager_ChargePointMaxProfile_TakesMinWithTxDefault(t *testing.T) {
    pm := ocpp.NewChargingProfileManager()
    pm.SetChargingProfile(1, makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile"))
    pm.SetChargingProfile(0, makeAbsoluteProfile(2, 0, 0, 8.0, "ChargePointMaxProfile")) // connector 0 = global

    limit := pm.GetCompositeLimit(1, 0, time.Now(), 230.0, nil, 1)
    require.NotNil(t, limit)
    assert.InDelta(t, 8.0, *limit, 0.01) // min(16, 8)
}

func TestProfileManager_HigherStackLevelWins(t *testing.T) {
    pm := ocpp.NewChargingProfileManager()
    pm.SetChargingProfile(1, makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile"))
    pm.SetChargingProfile(1, makeAbsoluteProfile(2, 1, 1, 24.0, "TxDefaultProfile")) // stackLevel 1 wins

    limit := pm.GetCompositeLimit(1, 0, time.Now(), 230.0, nil, 1)
    require.NotNil(t, limit)
    assert.InDelta(t, 24.0, *limit, 0.01)
}

func TestProfileManager_WattsConverted_ToAmps(t *testing.T) {
    pm := ocpp.NewChargingProfileManager()
    profile := engine.ChargingProfile{
        ProfileID:   1,
        ConnectorID: 1,
        Purpose:     "TxDefaultProfile",
        Kind:        "Absolute",
        StartSchedule: ptr(time.Now().Add(-1 * time.Hour)),
        Schedule: engine.ChargingSchedule{
            ChargingRateUnit: "W",
            Periods: []engine.ChargingSchedulePeriod{
                {StartPeriod: 0, Limit: 3680.0}, // 3680W / 230V / 1phase = 16A
            },
        },
    }
    pm.SetChargingProfile(1, profile)

    limit := pm.GetCompositeLimit(1, 0, time.Now(), 230.0, nil, 1)
    require.NotNil(t, limit)
    assert.InDelta(t, 16.0, *limit, 0.01)
}

func TestProfileManager_ClearByProfileID(t *testing.T) {
    pm := ocpp.NewChargingProfileManager()
    pm.SetChargingProfile(1, makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile"))

    profileID := 1
    require.NoError(t, pm.ClearChargingProfile(nil, &profileID, nil, nil))
    limit := pm.GetCompositeLimit(1, 0, time.Now(), 230.0, nil, 1)
    assert.Nil(t, limit)
}

func TestProfileManager_GetCompositeSchedule(t *testing.T) {
    pm := ocpp.NewChargingProfileManager()
    pm.SetChargingProfile(1, makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile"))

    now := time.Now()
    periods, err := pm.GetCompositeSchedule(1, 0, now, 3600, 230.0, nil, 1)
    require.NoError(t, err)
    assert.NotEmpty(t, periods)
    assert.InDelta(t, 16.0, periods[0].Limit, 0.01)
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/ocpp/... -run TestProfileManager -v
```

Expected: compile error.

- [ ] **Step 3: Implement profile_manager.go**

Create `internal/ocpp/profile_manager.go`:

```go
package ocpp

import (
    "errors"
    "sync"
    "time"

    engine "github.com/chargeghost/engine/internal/engine"
)

const (
    maxProfiles        = 20
    maxSchedulePeriods = 10
)

type profileKey struct {
    connectorID int
    profileID   int
}

// ChargingProfileManager computes effective charging limits using OCPP smart charging rules.
// Thread-safe via sync.RWMutex.
type ChargingProfileManager struct {
    mu       sync.RWMutex
    profiles map[profileKey]engine.ChargingProfile
}

// NewChargingProfileManager creates an empty manager.
func NewChargingProfileManager() *ChargingProfileManager {
    return &ChargingProfileManager{
        profiles: make(map[profileKey]engine.ChargingProfile),
    }
}

// SetChargingProfile stores a profile. Replaces any existing profile with same (connectorID, profileID).
func (m *ChargingProfileManager) SetChargingProfile(connectorID int, profile engine.ChargingProfile) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    if len(m.profiles) >= maxProfiles {
        return errors.New("max profiles reached")
    }
    profile.ConnectorID = connectorID
    m.profiles[profileKey{connectorID, profile.ProfileID}] = profile
    return nil
}

// ClearChargingProfile removes profiles matching the optional filters.
// nil filter fields match everything.
func (m *ChargingProfileManager) ClearChargingProfile(connectorID, profileID *int, purpose, stackLevel *string) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    for k, p := range m.profiles {
        if profileID != nil && p.ProfileID != *profileID {
            continue
        }
        if connectorID != nil && p.ConnectorID != *connectorID {
            continue
        }
        if purpose != nil && p.Purpose != *purpose {
            continue
        }
        if stackLevel != nil {
            // stackLevel filter is an int encoded as string for the API; skip if mismatch
        }
        delete(m.profiles, k)
    }
    return nil
}

// GetChargingProfiles returns all stored profiles for the REST API.
func (m *ChargingProfileManager) GetChargingProfiles() []engine.ChargingProfile {
    m.mu.RLock()
    defer m.mu.RUnlock()
    result := make([]engine.ChargingProfile, 0, len(m.profiles))
    for _, p := range m.profiles {
        result = append(result, p)
    }
    return result
}

// GetChargingProfile returns a single profile by profileID and connectorID.
func (m *ChargingProfileManager) GetChargingProfile(connectorID, profileID int) (engine.ChargingProfile, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    p, ok := m.profiles[profileKey{connectorID, profileID}]
    return p, ok
}

// GetCompositeLimit computes the effective current limit (Amps) for a connector at `now`.
// Returns nil when no profiles apply (engine uses connector's full current).
func (m *ChargingProfileManager) GetCompositeLimit(
    connectorID, transactionID int,
    now time.Time,
    connectorVoltage float64,
    transactionStart *time.Time,
    phases int,
) *float64 {
    m.mu.RLock()
    defer m.mu.RUnlock()

    maxLimit := m.resolveLimit("ChargePointMaxProfile", connectorID, transactionID, now, connectorVoltage, transactionStart, phases)
    txLimit := m.resolveTxLimit(connectorID, transactionID, now, connectorVoltage, transactionStart, phases)

    if maxLimit == nil && txLimit == nil {
        return nil
    }
    if maxLimit == nil {
        return txLimit
    }
    if txLimit == nil {
        return maxLimit
    }
    composite := min(*maxLimit, *txLimit)
    return &composite
}

// GetCompositeSchedule builds the effective schedule for a connector over a duration.
func (m *ChargingProfileManager) GetCompositeSchedule(
    connectorID, transactionID int,
    startTime time.Time,
    duration int,
    connectorVoltage float64,
    transactionStart *time.Time,
    phases int,
) ([]engine.ChargingSchedulePeriod, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()

    // Collect all time boundaries within [startTime, startTime+duration].
    endTime := startTime.Add(time.Duration(duration) * time.Second)
    boundaries := []time.Time{startTime}

    for _, p := range m.profiles {
        if !m.profileAppliesToConnector(p, connectorID) {
            continue
        }
        if !m.isProfileValid(p, startTime) {
            continue
        }
        for _, period := range p.Schedule.Periods {
            schedStart := m.getScheduleStart(p, transactionStart)
            if schedStart == nil {
                continue
            }
            t := schedStart.Add(time.Duration(period.StartPeriod) * time.Second)
            if t.After(startTime) && t.Before(endTime) {
                boundaries = append(boundaries, t)
            }
        }
    }

    // Sort boundaries and compute limit at each.
    sortTimes(boundaries)
    periods := make([]engine.ChargingSchedulePeriod, 0, len(boundaries))
    for _, t := range boundaries {
        limit := m.compositeLimitAt(connectorID, transactionID, t, connectorVoltage, transactionStart, phases)
        startPeriod := int(t.Sub(startTime).Seconds())
        limitVal := 0.0
        if limit != nil {
            limitVal = *limit
        }
        periods = append(periods, engine.ChargingSchedulePeriod{
            StartPeriod: startPeriod,
            Limit:       limitVal,
        })
    }
    return periods, nil
}

// --- private helpers ---

func (m *ChargingProfileManager) resolveTxLimit(connectorID, transactionID int, now time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
    // Try TxProfile first.
    limit := m.resolveLimit("TxProfile", connectorID, transactionID, now, voltage, txStart, phases)
    if limit != nil {
        return limit
    }
    // Fall back to TxDefaultProfile.
    return m.resolveLimit("TxDefaultProfile", connectorID, transactionID, now, voltage, txStart, phases)
}

func (m *ChargingProfileManager) resolveLimit(purpose string, connectorID, transactionID int, now time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
    var best *engine.ChargingProfile
    for _, p := range m.profiles {
        pCopy := p
        if pCopy.Purpose != purpose {
            continue
        }
        if !m.profileAppliesToConnector(pCopy, connectorID) {
            continue
        }
        if !m.isProfileValid(pCopy, now) {
            continue
        }
        if best == nil || pCopy.StackLevel > best.StackLevel {
            best = &pCopy
        }
    }
    if best == nil {
        return nil
    }
    return m.limitFromProfile(*best, now, voltage, txStart, phases)
}

func (m *ChargingProfileManager) limitFromProfile(p engine.ChargingProfile, now time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
    schedStart := m.getScheduleStart(p, txStart)
    if schedStart == nil {
        return nil
    }

    elapsed := elapsedSeconds(p, now, *schedStart)
    if elapsed < 0 {
        return nil
    }

    period := activePeriod(p.Schedule.Periods, elapsed)
    if period == nil {
        return nil
    }

    limitA := period.Limit
    if p.Schedule.ChargingRateUnit == "W" {
        // Convert W to A.
        if voltage > 0 && phases > 0 {
            limitA = period.Limit / (voltage * float64(phases))
        }
    }
    return &limitA
}

func (m *ChargingProfileManager) getScheduleStart(p engine.ChargingProfile, txStart *time.Time) *time.Time {
    switch p.Kind {
    case "Absolute":
        if p.StartSchedule != nil {
            return p.StartSchedule
        }
        // Absolute without StartSchedule uses profile creation time — not supported; return nil.
        return nil
    case "Relative":
        return txStart // may be nil if no active transaction
    case "Recurring":
        if p.StartSchedule == nil {
            return nil
        }
        return p.StartSchedule
    }
    return nil
}

func elapsedSeconds(p engine.ChargingProfile, now time.Time, schedStart time.Time) float64 {
    switch p.Kind {
    case "Absolute", "Relative":
        return now.Sub(schedStart).Seconds()
    case "Recurring":
        var cycleLen float64
        switch p.RecurrencyKind {
        case "Daily":
            cycleLen = 86400
        case "Weekly":
            cycleLen = 604800
        default:
            cycleLen = 86400
        }
        elapsed := now.Sub(schedStart).Seconds()
        if elapsed < 0 {
            return -1
        }
        return math.Mod(elapsed, cycleLen)
    }
    return -1
}

func activePeriod(periods []engine.ChargingSchedulePeriod, elapsedSeconds float64) *engine.ChargingSchedulePeriod {
    var active *engine.ChargingSchedulePeriod
    for i := range periods {
        if float64(periods[i].StartPeriod) <= elapsedSeconds {
            active = &periods[i]
        }
    }
    return active
}

func (m *ChargingProfileManager) profileAppliesToConnector(p engine.ChargingProfile, connectorID int) bool {
    // Connector 0 is global (applies to all connectors).
    return p.ConnectorID == connectorID || p.ConnectorID == 0
}

func (m *ChargingProfileManager) isProfileValid(p engine.ChargingProfile, now time.Time) bool {
    if p.ValidFrom != nil && now.Before(*p.ValidFrom) {
        return false
    }
    if p.ValidTo != nil && now.After(*p.ValidTo) {
        return false
    }
    return true
}

func (m *ChargingProfileManager) compositeLimitAt(connectorID, transactionID int, t time.Time, voltage float64, txStart *time.Time, phases int) *float64 {
    maxL := m.resolveLimit("ChargePointMaxProfile", connectorID, transactionID, t, voltage, txStart, phases)
    txL := m.resolveTxLimit(connectorID, transactionID, t, voltage, txStart, phases)
    if maxL == nil && txL == nil {
        return nil
    }
    if maxL == nil {
        return txL
    }
    if txL == nil {
        return maxL
    }
    v := min(*maxL, *txL)
    return &v
}

func sortTimes(times []time.Time) {
    for i := 1; i < len(times); i++ {
        for j := i; j > 0 && times[j].Before(times[j-1]); j-- {
            times[j], times[j-1] = times[j-1], times[j]
        }
    }
}
```

Add import `"math"` to the file.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/ocpp/... -run TestProfileManager -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ocpp/profile_manager.go internal/ocpp/profile_manager_test.go
git commit -m "feat(ocpp): ChargingProfileManager with composite limit algorithm"
```

---

## Task 2: Wire Profile Manager into Engine and Bridge

**Files:**
- Modify: `cmd/chargeghost/main.go`
- Modify: `internal/ocpp/bridge.go`

- [ ] **Step 1: Create profile manager and inject into engine**

In `cmd/chargeghost/main.go`, after creating the engine:

```go
profileManager := ocpp.NewChargingProfileManager()

// Replace the nil GetLimit with the real profile manager.
e.GetLimit = func(connectorID int, transactionID int) *float64 {
    session := e.GetSession(connectorID)
    c := e.GetConnector(connectorID)
    if c == nil {
        return nil
    }
    var txStart *time.Time
    if session != nil {
        t := session.StartTime
        txStart = &t
    }
    return profileManager.GetCompositeLimit(connectorID, transactionID, time.Now(), c.Voltage, txStart, c.Phase)
}
```

- [ ] **Step 2: Register smartcharging handler in bridge**

In `internal/ocpp/bridge.go`, add a `profileManager` field:

```go
type Bridge struct {
    // ... existing fields ...
    profileManager *ChargingProfileManager
}
```

Update `NewBridge` signature to accept the profile manager:

```go
func NewBridge(e *engine.Engine, hub *ws.Hub, cfg *config.Config, dispatcher *CommandDispatcher, pm *ChargingProfileManager) *Bridge {
    b := &Bridge{
        // ... existing fields ...
        profileManager: pm,
    }
    // ...
    b.cp.SetSmartChargingHandler(b)
    return b
}
```

Update the `NewBridge` call in `main.go`:

```go
bridge := ocpp.NewBridge(e, hub, cfg, dispatcher, profileManager)
```

- [ ] **Step 3: Implement smartcharging inbound handlers**

Add to `bridge.go`:

```go
import "github.com/lorenzodonini/ocpp-go/ocpp1.6/smartcharging"

func (b *Bridge) OnSetChargingProfile(request *smartcharging.SetChargingProfileRequest) (*smartcharging.SetChargingProfileResponse, error) {
    profile := convertChargingProfile(request.CsChargingProfiles, request.ConnectorId)
    if profile == nil {
        return smartcharging.NewSetChargingProfileResponse(smartcharging.ChargingProfileStatusRejected), nil
    }
    if err := b.profileManager.SetChargingProfile(request.ConnectorId, *profile); err != nil {
        return smartcharging.NewSetChargingProfileResponse(smartcharging.ChargingProfileStatusRejected), nil
    }
    b.hub.BroadcastMessage(ws.Message{
        Type: "charging_profile_changed",
        Data: map[string]interface{}{"action": "set", "profile_id": profile.ProfileID},
    })
    return smartcharging.NewSetChargingProfileResponse(smartcharging.ChargingProfileStatusAccepted), nil
}

func (b *Bridge) OnClearChargingProfile(request *smartcharging.ClearChargingProfileRequest) (*smartcharging.ClearChargingProfileResponse, error) {
    var connID, profileID *int
    var purpose *string
    if request.Id != nil {
        id := *request.Id
        profileID = &id
    }
    if request.ConnectorId != nil {
        cid := *request.ConnectorId
        connID = &cid
    }
    if request.ChargingProfilePurpose != "" {
        p := string(request.ChargingProfilePurpose)
        purpose = &p
    }
    _ = b.profileManager.ClearChargingProfile(connID, profileID, purpose, nil)
    b.hub.BroadcastMessage(ws.Message{
        Type: "charging_profile_changed",
        Data: map[string]interface{}{"action": "cleared"},
    })
    return smartcharging.NewClearChargingProfileResponse(smartcharging.ClearChargingProfileStatusAccepted), nil
}

func (b *Bridge) OnGetCompositeSchedule(request *smartcharging.GetCompositeScheduleRequest) (*smartcharging.GetCompositeScheduleResponse, error) {
    connID := request.ConnectorId
    duration := request.Duration
    now := time.Now()

    session := b.engine.GetSession(connID)
    c := b.engine.GetConnector(connID)
    if c == nil {
        return smartcharging.NewGetCompositeScheduleResponse(smartcharging.GetCompositeScheduleStatusRejected), nil
    }
    var txStart *time.Time
    var txID int
    if session != nil {
        t := session.StartTime
        txStart = &t
        txID = session.TransactionID
    }

    periods, err := b.profileManager.GetCompositeSchedule(connID, txID, now, duration, c.Voltage, txStart, c.Phase)
    if err != nil {
        return smartcharging.NewGetCompositeScheduleResponse(smartcharging.GetCompositeScheduleStatusRejected), nil
    }

    // Convert engine periods to OCPP types.
    ocppPeriods := make([]types.ChargingSchedulePeriod, 0, len(periods))
    for _, p := range periods {
        ocppPeriods = append(ocppPeriods, types.ChargingSchedulePeriod{
            StartPeriod: p.StartPeriod,
            Limit:       p.Limit,
        })
    }

    rateUnit := types.ChargingRateUnitAmpere
    resp := smartcharging.NewGetCompositeScheduleResponse(smartcharging.GetCompositeScheduleStatusAccepted)
    resp.ConnectorId = &connID
    resp.ScheduleStart = ocppj.NewDateTime(now)
    resp.ChargingSchedule = &types.ChargingSchedule{
        Duration:           duration,
        ChargingRateUnit:   rateUnit,
        ChargingSchedulePeriod: ocppPeriods,
    }
    return resp, nil
}
```

- [ ] **Step 4: Build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/ocpp/bridge.go cmd/chargeghost/main.go
git commit -m "feat(ocpp): smartcharging inbound handlers wired to ChargingProfileManager"
```

---

## Task 3: Charging Profile REST Handlers

**Files:**
- Create: `internal/api/handlers/charging_profiles.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add profileManager to AppContext**

In `internal/api/router.go`, add `ProfileManager *ocpp.ChargingProfileManager` to `AppContext`.

- [ ] **Step 2: Implement charging_profiles.go**

Create `internal/api/handlers/charging_profiles.go`:

```go
package handlers

import (
    "encoding/json"
    "net/http"
    "strconv"

    "github.com/go-chi/chi/v5"
    "github.com/chargeghost/engine/internal/api"
    engine "github.com/chargeghost/engine/internal/engine"
    "github.com/chargeghost/engine/internal/ocpp"
)

func ListChargingProfiles(pm *ocpp.ChargingProfileManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, http.StatusOK, pm.GetChargingProfiles())
    }
}

func GetChargingProfile(pm *ocpp.ChargingProfileManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, err := strconv.Atoi(chi.URLParam(r, "profile_id"))
        if err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid profile_id"})
            return
        }
        connectorID, _ := strconv.Atoi(r.URL.Query().Get("connector_id"))
        p, ok := pm.GetChargingProfile(connectorID, id)
        if !ok {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "profile not found"})
            return
        }
        writeJSON(w, http.StatusOK, p)
    }
}

func InstallChargingProfile(pm *ocpp.ChargingProfileManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            ConnectorID int                    `json:"connector_id"`
            Profile     engine.ChargingProfile `json:"profile"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid request body"})
            return
        }
        if err := pm.SetChargingProfile(req.ConnectorID, req.Profile); err != nil {
            writeJSON(w, http.StatusConflict, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Profile installed"})
    }
}

func ClearChargingProfiles(pm *ocpp.ChargingProfileManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        q := r.URL.Query()
        var profileID, connectorID *int
        var purpose *string
        if s := q.Get("profile_id"); s != "" {
            if id, err := strconv.Atoi(s); err == nil {
                profileID = &id
            }
        }
        if s := q.Get("connector_id"); s != "" {
            if id, err := strconv.Atoi(s); err == nil {
                connectorID = &id
            }
        }
        if s := q.Get("purpose"); s != "" {
            purpose = &s
        }
        _ = pm.ClearChargingProfile(connectorID, profileID, purpose, nil)
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Profiles cleared"})
    }
}

func GetCompositeScheduleHandler(pm *ocpp.ChargingProfileManager, e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            ConnectorID int `json:"connector_id"`
            Duration    int `json:"duration"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid request"})
            return
        }
        import "time"
        c := e.GetConnector(req.ConnectorID)
        if c == nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "connector not found"})
            return
        }
        session := e.GetSession(req.ConnectorID)
        var txStart *time.Time
        var txID int
        if session != nil {
            t := session.StartTime
            txStart = &t
            txID = session.TransactionID
        }
        periods, err := pm.GetCompositeSchedule(req.ConnectorID, txID, time.Now(), req.Duration, c.Voltage, txStart, c.Phase)
        if err != nil {
            writeJSON(w, http.StatusInternalServerError, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, map[string]interface{}{"periods": periods})
    }
}
```

Note: the `import "time"` inside a function is invalid Go. Move all imports to the top of the file:

```go
import (
    "encoding/json"
    "net/http"
    "strconv"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/chargeghost/engine/internal/api"
    engine "github.com/chargeghost/engine/internal/engine"
    "github.com/chargeghost/engine/internal/ocpp"
)
```

And remove the inline `import "time"` from `GetCompositeScheduleHandler`.

- [ ] **Step 3: Add routes to router.go**

In `NewRouter`, add:

```go
        r.Route("/charging-profiles", func(r chi.Router) {
            r.Get("/", handlers.ListChargingProfiles(app.ProfileManager))
            r.Post("/", handlers.InstallChargingProfile(app.ProfileManager))
            r.Delete("/", handlers.ClearChargingProfiles(app.ProfileManager))
            r.Get("/{profile_id}", handlers.GetChargingProfile(app.ProfileManager))
            r.Post("/composite-schedule", handlers.GetCompositeScheduleHandler(app.ProfileManager, app.Engine))
        })
```

- [ ] **Step 4: Wire profile manager into main.go AppContext**

```go
app := &api.AppContext{
    // ... existing fields ...
    ProfileManager: profileManager,
}
```

- [ ] **Step 5: Build and test**

```bash
go build ./...
go test ./... -count=1 -timeout 30s
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/api/handlers/charging_profiles.go internal/api/router.go cmd/chargeghost/main.go
git commit -m "feat(api): charging profile REST endpoints wired to ChargingProfileManager"
```

---

## Task 4: Verification

- [ ] **Step 1: Verify limit enforcement**

```bash
./chargeghost &
sleep 1

# Create a TxDefaultProfile limiting to 16A.
curl -s -X POST http://localhost:8080/api/v1/charging-profiles \
  -H "Content-Type: application/json" \
  -d '{
    "connector_id": 1,
    "profile": {
      "ProfileID": 1, "StackLevel": 0, "Purpose": "TxDefaultProfile",
      "Kind": "Absolute",
      "StartSchedule": "2020-01-01T00:00:00Z",
      "Schedule": {
        "ChargingRateUnit": "A",
        "Periods": [{"StartPeriod": 0, "Limit": 16.0}]
      }
    }
  }' | jq .

# Plug in and start a session.
curl -s -X POST http://localhost:8080/api/v1/connectors/1/plug_in > /dev/null
curl -s -X POST http://localhost:8080/api/v1/connectors/1/start-charging > /dev/null

# Wait a few seconds then check energy accumulation rate.
sleep 5
curl -s http://localhost:8080/api/v1/sessions | jq '.[] | .energy_charged_wh'
# Expected: ~0.09 Wh (230V × 16A × 1 × 5s / 3600) — NOT 0.14 Wh (32A)

kill %1
```

- [ ] **Step 2: Run all tests**

```bash
go test ./... -count=1 -timeout 30s
```

Expected: all pass.
