# Plan 03b — REST API Extended

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add timeline, local auth list (stub), firmware/diagnostics (stub), and about endpoints — all returning valid JSON from day one with interfaces that Plans 5d/5e swap for real implementations.

**Architecture:** The `TimelineStore` is real from the start (in-memory ring buffer, populated by OCPP layer later). Local auth list, firmware manager, and diagnostics manager are stub implementations behind interfaces; the real implementations in Plans 5d/5e are drop-in replacements. The `AppContext` grows to hold these dependencies.

**Tech Stack:** Go 1.22 stdlib only (no new dependencies).

---

## File Map

| File | Responsibility |
|------|----------------|
| `internal/timeline/models.go` | `TimelineEvent`, `TimelineFilter` types |
| `internal/timeline/store.go` | In-memory ring buffer (1000 events), filter logic |
| `internal/ocpp/interfaces.go` | `LocalAuthManager`, `FirmwareManager`, `DiagnosticsManager` interfaces + stub implementations |
| `internal/api/handlers/timeline.go` | GET /api/v1/timeline, GET /timeline/count, DELETE /timeline |
| `internal/api/handlers/local_auth.go` | Full local auth list REST endpoints |
| `internal/api/handlers/firmware.go` | Firmware + diagnostics REST endpoints |
| `internal/api/handlers/about.go` | GET /api/v1/about |
| `internal/api/router.go` | Modified: add new routes |
| `cmd/chargeghost/main.go` | Modified: inject new dependencies into AppContext |

---

## Task 1: Timeline Store

**Files:**
- Create: `internal/timeline/models.go`
- Create: `internal/timeline/store.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/timeline/store_test.go`:

```go
package timeline_test

import (
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/chargeghost/engine/internal/timeline"
)

func TestStore_AppendAndFilter(t *testing.T) {
    s := timeline.NewStore(100)
    s.Append(timeline.TimelineEvent{
        EventID:   "evt1",
        Timestamp: time.Now(),
        Source:    "ocpp_adapter",
        Direction: "outbound",
        Action:    "BootNotification",
        Level:     "info",
        Summary:   "BootNotification sent",
    })

    events, total := s.Query(timeline.TimelineFilter{Limit: 10})
    assert.Equal(t, 1, total)
    assert.Len(t, events, 1)
    assert.Equal(t, "BootNotification", events[0].Action)
}

func TestStore_RingBufferEvictsOldest(t *testing.T) {
    s := timeline.NewStore(3) // capacity = 3
    for i := 0; i < 5; i++ {
        s.Append(timeline.TimelineEvent{EventID: fmt.Sprintf("e%d", i), Summary: fmt.Sprintf("event %d", i)})
    }
    _, total := s.Query(timeline.TimelineFilter{Limit: 100})
    assert.Equal(t, 3, total) // only 3 kept
}

func TestStore_FilterByAction(t *testing.T) {
    s := timeline.NewStore(100)
    s.Append(timeline.TimelineEvent{EventID: "e1", Action: "BootNotification"})
    s.Append(timeline.TimelineEvent{EventID: "e2", Action: "Heartbeat"})

    events, _ := s.Query(timeline.TimelineFilter{Action: "BootNotification", Limit: 10})
    assert.Len(t, events, 1)
    assert.Equal(t, "BootNotification", events[0].Action)
}

func TestStore_Clear(t *testing.T) {
    s := timeline.NewStore(100)
    s.Append(timeline.TimelineEvent{EventID: "e1"})
    s.Clear()
    _, total := s.Query(timeline.TimelineFilter{Limit: 10})
    assert.Equal(t, 0, total)
}
```

Add `"fmt"` import to the test file.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/timeline/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement models.go**

Create `internal/timeline/models.go`:

```go
package timeline

import "time"

// TimelineEvent is a single OCPP protocol event stored in the timeline.
type TimelineEvent struct {
    EventID        string      `json:"event_id"`
    Timestamp      time.Time   `json:"timestamp"`
    Source         string      `json:"source"`         // "ocpp_adapter" | "csms"
    Direction      string      `json:"direction"`      // "inbound" | "outbound"
    EventType      string      `json:"event_type"`     // "call" | "call_result" | "call_error"
    Action         string      `json:"action"`         // OCPP action name
    MessageID      string      `json:"message_id"`
    ConnectorID    *int        `json:"connector_id"`
    TransactionID  *int        `json:"transaction_id"`
    Level          string      `json:"level"`          // "info" | "warn" | "error"
    Summary        string      `json:"summary"`
    Payload        interface{} `json:"payload"`
    CorrelationKey *string     `json:"correlation_key"`
    Tags           []string    `json:"tags"`
}

// TimelineFilter specifies filtering criteria for timeline queries.
type TimelineFilter struct {
    Source        string
    Direction     string
    EventType     string
    Action        string
    Limit         int    // default 100
    Offset        int
    ConnectorID   *int
    TransactionID *int
    MinLevel      string
    Search        string // substring match on Summary
}
```

- [ ] **Step 4: Implement store.go**

Create `internal/timeline/store.go`:

```go
package timeline

import (
    "strings"
    "sync"
)

// Store is a thread-safe, fixed-capacity ring buffer of TimelineEvents.
type Store struct {
    mu       sync.RWMutex
    events   []TimelineEvent
    capacity int
    head     int // next write position
    count    int // number of valid entries
}

// NewStore creates a Store with the given capacity. Use 1000 for production.
func NewStore(capacity int) *Store {
    return &Store{
        events:   make([]TimelineEvent, capacity),
        capacity: capacity,
    }
}

// Append adds an event to the ring buffer, overwriting the oldest if full.
func (s *Store) Append(evt TimelineEvent) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.events[s.head] = evt
    s.head = (s.head + 1) % s.capacity
    if s.count < s.capacity {
        s.count++
    }
}

// Query returns (matching events, total matching count) applying the filter.
// Events are returned newest-first.
func (s *Store) Query(f TimelineFilter) ([]TimelineEvent, int) {
    s.mu.RLock()
    defer s.mu.RUnlock()

    if f.Limit <= 0 {
        f.Limit = 100
    }

    // Collect all events newest-first.
    all := make([]TimelineEvent, 0, s.count)
    for i := 0; i < s.count; i++ {
        idx := (s.head - 1 - i + s.capacity) % s.capacity
        all = append(all, s.events[idx])
    }

    // Apply filters.
    filtered := all[:0:len(all)]
    for _, evt := range all {
        if !matchesFilter(evt, f) {
            continue
        }
        filtered = append(filtered, evt)
    }
    total := len(filtered)

    // Apply offset + limit.
    if f.Offset >= len(filtered) {
        return []TimelineEvent{}, total
    }
    filtered = filtered[f.Offset:]
    if len(filtered) > f.Limit {
        filtered = filtered[:f.Limit]
    }
    return filtered, total
}

// Count returns the number of events matching the filter.
func (s *Store) Count() int {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.count
}

// Clear removes all events.
func (s *Store) Clear() {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.head = 0
    s.count = 0
}

func matchesFilter(evt TimelineEvent, f TimelineFilter) bool {
    if f.Source != "" && evt.Source != f.Source {
        return false
    }
    if f.Direction != "" && evt.Direction != f.Direction {
        return false
    }
    if f.EventType != "" && evt.EventType != f.EventType {
        return false
    }
    if f.Action != "" && evt.Action != f.Action {
        return false
    }
    if f.ConnectorID != nil && (evt.ConnectorID == nil || *evt.ConnectorID != *f.ConnectorID) {
        return false
    }
    if f.TransactionID != nil && (evt.TransactionID == nil || *evt.TransactionID != *f.TransactionID) {
        return false
    }
    if f.Search != "" && !strings.Contains(evt.Summary, f.Search) {
        return false
    }
    return true
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/timeline/... -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/timeline/models.go internal/timeline/store.go internal/timeline/store_test.go
git commit -m "feat(timeline): in-memory ring buffer with filter support"
```

---

## Task 2: Stub Interfaces for Local Auth, Firmware, Diagnostics

**Files:**
- Create: `internal/ocpp/interfaces.go`

- [ ] **Step 1: Implement interfaces.go with stubs**

Create `internal/ocpp/interfaces.go`:

```go
package ocpp

import "time"

// LocalAuthEntry is a single entry in the local authorization list.
type LocalAuthEntry struct {
    IDTag       string
    Status      string // "Accepted" | "Blocked" | "Expired" | "ConcurrentTx"
    Expiry      *time.Time
    ParentIDTag *string
}

// LocalAuthManager manages the local authorization list.
// Plan 3b uses StubLocalAuthManager; Plan 5d replaces it with the real implementation.
type LocalAuthManager interface {
    GetVersion() int
    GetEntry(idTag string) *LocalAuthEntry
    GetAllEntries() []LocalAuthEntry
    UpdateList(version int, entries []LocalAuthEntry, updateType string) error
    RemoveEntry(idTag string) error
    Clear()
    GetStats() (version, count, maxEntries int, enabled bool)
}

// FirmwareStatus holds the current firmware update simulation state.
type FirmwareStatus struct {
    Status       string     // "Idle" | "Downloading" | "Downloaded" | "Installing" | "Installed" | "InstallationFailed"
    Location     *string
    RetrieveDate *time.Time
    FileName     *string
    FileHash     *string
}

// FirmwareManager manages simulated firmware updates.
// Plan 3b uses StubFirmwareManager; Plan 5e replaces it.
type FirmwareManager interface {
    GetStatus() FirmwareStatus
    TriggerUpdate(location string, retrieveDate time.Time) error
    CancelUpdate() error
}

// DiagnosticsStatus holds the current diagnostics upload simulation state.
type DiagnosticsStatus struct {
    Status   string  // "Idle" | "Uploading" | "Uploaded" | "UploadFailed"
    Location *string
}

// DiagnosticsManager manages simulated diagnostics uploads.
// Plan 3b uses StubDiagnosticsManager; Plan 5e replaces it.
type DiagnosticsManager interface {
    GetStatus() DiagnosticsStatus
    TriggerUpload(location string, retries, retryInterval int) error
    CancelUpload() error
}

// --- Stub implementations ---

// StubLocalAuthManager is an in-memory implementation used before Plan 5d.
type StubLocalAuthManager struct {
    version int
    entries map[string]LocalAuthEntry
    enabled bool
}

func NewStubLocalAuthManager() *StubLocalAuthManager {
    return &StubLocalAuthManager{entries: make(map[string]LocalAuthEntry), enabled: true}
}

func (m *StubLocalAuthManager) GetVersion() int { return m.version }

func (m *StubLocalAuthManager) GetEntry(idTag string) *LocalAuthEntry {
    if e, ok := m.entries[idTag]; ok {
        return &e
    }
    return nil
}

func (m *StubLocalAuthManager) GetAllEntries() []LocalAuthEntry {
    result := make([]LocalAuthEntry, 0, len(m.entries))
    for _, e := range m.entries {
        result = append(result, e)
    }
    return result
}

func (m *StubLocalAuthManager) UpdateList(version int, entries []LocalAuthEntry, updateType string) error {
    if updateType == "Full" {
        m.entries = make(map[string]LocalAuthEntry)
    }
    for _, e := range entries {
        m.entries[e.IDTag] = e
    }
    m.version = version
    return nil
}

func (m *StubLocalAuthManager) RemoveEntry(idTag string) error {
    delete(m.entries, idTag)
    return nil
}

func (m *StubLocalAuthManager) Clear() {
    m.entries = make(map[string]LocalAuthEntry)
    m.version = 0
}

func (m *StubLocalAuthManager) GetStats() (version, count, maxEntries int, enabled bool) {
    return m.version, len(m.entries), 1000, m.enabled
}

// StubFirmwareManager always reports "Idle".
type StubFirmwareManager struct{}

func NewStubFirmwareManager() *StubFirmwareManager { return &StubFirmwareManager{} }

func (m *StubFirmwareManager) GetStatus() FirmwareStatus {
    return FirmwareStatus{Status: "Idle"}
}

func (m *StubFirmwareManager) TriggerUpdate(location string, retrieveDate time.Time) error {
    return nil // no-op in stub
}

func (m *StubFirmwareManager) CancelUpdate() error { return nil }

// StubDiagnosticsManager always reports "Idle".
type StubDiagnosticsManager struct{}

func NewStubDiagnosticsManager() *StubDiagnosticsManager { return &StubDiagnosticsManager{} }

func (m *StubDiagnosticsManager) GetStatus() DiagnosticsStatus {
    return DiagnosticsStatus{Status: "Idle"}
}

func (m *StubDiagnosticsManager) TriggerUpload(location string, retries, retryInterval int) error {
    return nil
}

func (m *StubDiagnosticsManager) CancelUpload() error { return nil }
```

- [ ] **Step 2: Build check**

```bash
go build ./internal/ocpp/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/ocpp/interfaces.go
git commit -m "feat(ocpp): stub interfaces for local auth, firmware, diagnostics"
```

---

## Task 3: Expand AppContext

**Files:**
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add new fields to AppContext**

In `internal/api/router.go`, update `AppContext`:

```go
// AppContext holds shared dependencies injected into all handlers.
type AppContext struct {
    Engine          *engine.Engine
    Config          *config.Config
    StartTime       time.Time
    Timeline        *timeline.Store
    LocalAuth       ocpp.LocalAuthManager
    Firmware        ocpp.FirmwareManager
    Diagnostics     ocpp.DiagnosticsManager
}
```

Add imports:
```go
import (
    "time"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    engine "github.com/chargeghost/engine/internal/engine"
    "github.com/chargeghost/engine/internal/config"
    "github.com/chargeghost/engine/internal/api/handlers"
    "github.com/chargeghost/engine/internal/timeline"
    "github.com/chargeghost/engine/internal/ocpp"
)
```

And add routes in `NewRouter`:

```go
        r.Route("/timeline", func(r chi.Router) {
            r.Get("/", handlers.GetTimeline(app.Timeline))
            r.Get("/count", handlers.GetTimelineCount(app.Timeline))
            r.Delete("/", handlers.ClearTimeline(app.Timeline))
        })

        r.Route("/local-auth-list", func(r chi.Router) {
            r.Get("/", handlers.GetLocalAuthList(app.LocalAuth))
            r.Get("/{id_tag}", handlers.GetLocalAuthEntry(app.LocalAuth))
            r.Put("/", handlers.UpdateLocalAuthList(app.LocalAuth))
            r.Delete("/{id_tag}", handlers.DeleteLocalAuthEntry(app.LocalAuth))
            r.Delete("/", handlers.ClearLocalAuthList(app.LocalAuth))
        })

        r.Route("/firmware", func(r chi.Router) {
            r.Get("/status", handlers.GetFirmwareStatus(app.Firmware))
            r.Post("/trigger", handlers.TriggerFirmwareUpdate(app.Firmware))
            r.Post("/cancel", handlers.CancelFirmwareUpdate(app.Firmware))
        })

        r.Route("/diagnostics", func(r chi.Router) {
            r.Get("/status", handlers.GetDiagnosticsStatus(app.Diagnostics))
            r.Post("/trigger", handlers.TriggerDiagnosticsUpload(app.Diagnostics))
            r.Post("/cancel", handlers.CancelDiagnosticsUpload(app.Diagnostics))
        })

        r.Get("/about", handlers.GetAbout())
```

- [ ] **Step 2: Commit**

```bash
git add internal/api/router.go
git commit -m "feat(api): expand AppContext and add extended routes"
```

---

## Task 4: Timeline, Local Auth, Firmware, About Handlers

**Files:**
- Create: `internal/api/handlers/timeline.go`
- Create: `internal/api/handlers/local_auth.go`
- Create: `internal/api/handlers/firmware.go`
- Create: `internal/api/handlers/about.go`

- [ ] **Step 1: Implement timeline.go**

Create `internal/api/handlers/timeline.go`:

```go
package handlers

import (
    "net/http"
    "strconv"

    "github.com/chargeghost/engine/internal/timeline"
)

func GetTimeline(s *timeline.Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        q := r.URL.Query()
        limit, _ := strconv.Atoi(q.Get("limit"))
        offset, _ := strconv.Atoi(q.Get("offset"))
        if limit == 0 {
            limit = 100
        }
        f := timeline.TimelineFilter{
            Source:    q.Get("source"),
            Direction: q.Get("direction"),
            EventType: q.Get("event_type"),
            Action:    q.Get("action"),
            Search:    q.Get("search"),
            Limit:     limit,
            Offset:    offset,
        }
        if cid := q.Get("connector_id"); cid != "" {
            if id, err := strconv.Atoi(cid); err == nil {
                f.ConnectorID = &id
            }
        }
        if txid := q.Get("transaction_id"); txid != "" {
            if id, err := strconv.Atoi(txid); err == nil {
                f.TransactionID = &id
            }
        }
        events, total := s.Query(f)
        writeJSON(w, http.StatusOK, map[string]interface{}{
            "events": events,
            "total":  total,
        })
    }
}

func GetTimelineCount(s *timeline.Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, http.StatusOK, map[string]int{"count": s.Count()})
    }
}

func ClearTimeline(s *timeline.Store) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        s.Clear()
        w.WriteHeader(http.StatusNoContent)
    }
}
```

- [ ] **Step 2: Implement local_auth.go**

Create `internal/api/handlers/local_auth.go`:

```go
package handlers

import (
    "encoding/json"
    "net/http"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/chargeghost/engine/internal/api"
    "github.com/chargeghost/engine/internal/ocpp"
)

type localAuthEntryDTO struct {
    IDTag               string     `json:"id_tag"`
    Status              string     `json:"authorization_status"`
    ExpiryDate          *time.Time `json:"expiry_date"`
    IsExpired           bool       `json:"is_expired"`
}

func GetLocalAuthList(m ocpp.LocalAuthManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        version, count, maxEntries, enabled := m.GetStats()
        entries := m.GetAllEntries()
        dtos := make([]localAuthEntryDTO, 0, len(entries))
        for _, e := range entries {
            expired := e.Expiry != nil && time.Now().After(*e.Expiry)
            dtos = append(dtos, localAuthEntryDTO{
                IDTag:     e.IDTag,
                Status:    e.Status,
                ExpiryDate: e.Expiry,
                IsExpired: expired,
            })
        }
        writeJSON(w, http.StatusOK, map[string]interface{}{
            "version":     version,
            "entry_count": count,
            "max_entries": maxEntries,
            "enabled":     enabled,
            "entries":     dtos,
        })
    }
}

func GetLocalAuthEntry(m ocpp.LocalAuthManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        idTag := chi.URLParam(r, "id_tag")
        entry := m.GetEntry(idTag)
        if entry == nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "entry not found"})
            return
        }
        writeJSON(w, http.StatusOK, entry)
    }
}

func UpdateLocalAuthList(m ocpp.LocalAuthManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            ListVersion int                  `json:"list_version"`
            Entries     []ocpp.LocalAuthEntry `json:"entries"`
            UpdateType  string               `json:"update_type"` // "Full" | "Differential"
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid request body"})
            return
        }
        if err := m.UpdateList(req.ListVersion, req.Entries, req.UpdateType); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: err.Error()})
            return
        }
        _, count, _, _ := m.GetStats()
        writeJSON(w, http.StatusOK, map[string]interface{}{
            "success": true,
            "message": "List updated to version " + strconv.Itoa(req.ListVersion),
            "version": req.ListVersion,
            "count":   count,
        })
    }
}

func DeleteLocalAuthEntry(m ocpp.LocalAuthManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        idTag := chi.URLParam(r, "id_tag")
        if err := m.RemoveEntry(idTag); err != nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "entry not found"})
            return
        }
        w.WriteHeader(http.StatusNoContent)
    }
}

func ClearLocalAuthList(m ocpp.LocalAuthManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        m.Clear()
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Local auth list cleared"})
    }
}
```

Add `"strconv"` to the import block.

- [ ] **Step 3: Implement firmware.go**

Create `internal/api/handlers/firmware.go`:

```go
package handlers

import (
    "encoding/json"
    "net/http"
    "time"

    "github.com/chargeghost/engine/internal/api"
    "github.com/chargeghost/engine/internal/ocpp"
)

func GetFirmwareStatus(m ocpp.FirmwareManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, http.StatusOK, m.GetStatus())
    }
}

func TriggerFirmwareUpdate(m ocpp.FirmwareManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            Location     string `json:"location"`
            RetrieveDate string `json:"retrieve_date"` // RFC3339
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid request body"})
            return
        }
        retrieveDate, err := time.Parse(time.RFC3339, req.RetrieveDate)
        if err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "retrieve_date must be RFC3339"})
            return
        }
        if err := m.TriggerUpdate(req.Location, retrieveDate); err != nil {
            writeJSON(w, http.StatusConflict, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Firmware update started"})
    }
}

func CancelFirmwareUpdate(m ocpp.FirmwareManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if err := m.CancelUpdate(); err != nil {
            writeJSON(w, http.StatusConflict, api.Response{Success: false, Message: "no firmware update in progress"})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Firmware update cancelled"})
    }
}

func GetDiagnosticsStatus(m ocpp.DiagnosticsManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, http.StatusOK, m.GetStatus())
    }
}

func TriggerDiagnosticsUpload(m ocpp.DiagnosticsManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            Location      string `json:"location"`
            Retries       int    `json:"retries"`
            RetryInterval int    `json:"retry_interval"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid request body"})
            return
        }
        if err := m.TriggerUpload(req.Location, req.Retries, req.RetryInterval); err != nil {
            writeJSON(w, http.StatusConflict, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Diagnostics upload started"})
    }
}

func CancelDiagnosticsUpload(m ocpp.DiagnosticsManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if err := m.CancelUpload(); err != nil {
            writeJSON(w, http.StatusConflict, api.Response{Success: false, Message: "no diagnostics upload in progress"})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Diagnostics upload cancelled"})
    }
}
```

- [ ] **Step 4: Implement about.go**

Create `internal/api/handlers/about.go`:

```go
package handlers

import "net/http"

func GetAbout() http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, http.StatusOK, map[string]interface{}{
            "version":      "0.5.0",
            "description":  "ChargeGhost EVSE Simulator",
            "ocpp_versions": []string{"1.6J"},
            "features": []string{
                "OCPP 1.6J charging station simulation",
                "Smart charging profiles (TxDefaultProfile, TxProfile, ChargePointMaxProfile)",
                "Local authorization list",
                "Firmware and diagnostics simulation",
                "REST API and WebSocket event streaming",
                "Offline message queue with JSON persistence",
            },
            "license":   "MIT",
            "copyright": "2025 ChargeGhost",
        })
    }
}
```

- [ ] **Step 5: Commit**

```bash
git add internal/api/handlers/timeline.go internal/api/handlers/local_auth.go \
        internal/api/handlers/firmware.go internal/api/handlers/about.go
git commit -m "feat(api): timeline, local auth, firmware/diagnostics, about handlers"
```

---

## Task 5: Wire into main.go and Final Verification

**Files:**
- Modify: `cmd/chargeghost/main.go`

- [ ] **Step 1: Update main.go to inject new dependencies**

In `cmd/chargeghost/main.go`, update the `AppContext` construction:

```go
import (
    // ... existing imports ...
    "github.com/chargeghost/engine/internal/timeline"
    "github.com/chargeghost/engine/internal/ocpp"
)

// After creating e and cfg:
timelineStore := timeline.NewStore(1000)
localAuth := ocpp.NewStubLocalAuthManager()
firmware := ocpp.NewStubFirmwareManager()
diagnostics := ocpp.NewStubDiagnosticsManager()

app := &api.AppContext{
    Engine:      e,
    Config:      cfg,
    StartTime:   time.Now(),
    Timeline:    timelineStore,
    LocalAuth:   localAuth,
    Firmware:    firmware,
    Diagnostics: diagnostics,
}
```

- [ ] **Step 2: Build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Smoke test new endpoints**

```bash
./chargeghost &
sleep 1
curl -s http://localhost:8080/api/v1/about | jq .
curl -s http://localhost:8080/api/v1/timeline | jq .
curl -s http://localhost:8080/api/v1/local-auth-list | jq .
curl -s http://localhost:8080/api/v1/firmware/status | jq .
curl -s http://localhost:8080/api/v1/diagnostics/status | jq .
kill %1
```

Expected: all return valid JSON.

- [ ] **Step 4: Run all tests**

```bash
go test ./... -count=1 -timeout 30s
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/chargeghost/main.go
git commit -m "feat(cmd): inject timeline and stub OCPP managers"
```
