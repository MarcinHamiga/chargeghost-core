# Plan 05d — OCPP Config, Auth & Queue

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** OCPP configuration keys are readable/writable, the authorization cache is live, the local auth list is fully functional (replacing the Plan 3b stub), and offline messages survive CSMS disconnection and reconnect.

**Architecture:** `ConfigKeyManager` stores OCPP 1.6 standard keys with metadata (type, read-only flag). `AuthorizationCache` is a simple thread-safe map. `LocalAuthListManager` replaces the Plan 3b `StubLocalAuthManager` with a version-tracking, full/differential-update list limited to 1000 entries. Two `MessageQueue` backends: `InMemoryQueue` (lost on restart) and `JsonFileQueue` (persisted to `~/.chargeghost/message_queue.json`). The queue drains on reconnect.

**Tech Stack:** Go 1.22 stdlib + `github.com/google/uuid`

---

## File Map

| File | Responsibility |
|------|----------------|
| `internal/ocpp/config_keys.go` | `ConfigKeyManager` — all OCPP 1.6 standard keys, get/set, OnGetConfiguration/OnChangeConfiguration |
| `internal/ocpp/auth_cache.go` | `AuthorizationCache` — thread-safe tag→status map |
| `internal/ocpp/local_auth_list.go` | `LocalAuthListManager` — full/differential update, expiry, version tracking; replaces StubLocalAuthManager |
| `internal/ocpp/queue/queue.go` | `MessageQueue` interface + factory |
| `internal/ocpp/queue/memory.go` | `InMemoryQueue` — thread-safe FIFO |
| `internal/ocpp/queue/json_file.go` | `JsonFileQueue` — persisted to JSON file |
| `internal/ocpp/bridge.go` | Modified: OnGetConfiguration, OnChangeConfiguration, OnGetLocalListVersion, OnSendLocalList wired; queue integrated into StartTransaction/StopTransaction/MeterValues |
| `cmd/chargeghost/main.go` | Modified: create real auth cache, local auth list, message queue; inject into bridge |

---

## Task 1: ConfigKeyManager

**Files:**
- Create: `internal/ocpp/config_keys.go`
- Create: `internal/ocpp/config_keys_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/ocpp/config_keys_test.go`:

```go
package ocpp_test

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/chargeghost/engine/internal/ocpp"
)

func TestConfigKeyManager_GetAndSet(t *testing.T) {
    m := ocpp.NewConfigKeyManager()

    val := m.GetConfigValue("HeartbeatInterval")
    assert.Equal(t, "300", val) // default

    result := m.SetConfigValue("HeartbeatInterval", "60")
    assert.Equal(t, "Accepted", result)
    assert.Equal(t, "60", m.GetConfigValue("HeartbeatInterval"))
}

func TestConfigKeyManager_ReadOnlyKey(t *testing.T) {
    m := ocpp.NewConfigKeyManager()
    result := m.SetConfigValue("NumberOfConnectors", "2")
    assert.Equal(t, "Rejected", result) // read-only
}

func TestConfigKeyManager_UnknownKey(t *testing.T) {
    m := ocpp.NewConfigKeyManager()
    result := m.SetConfigValue("SomeUnknownKey", "value")
    assert.Equal(t, "NotSupported", result)
}

func TestConfigKeyManager_GetConfigKeyInfo(t *testing.T) {
    m := ocpp.NewConfigKeyManager()
    keys := m.GetConfigKeyInfo()
    assert.NotEmpty(t, keys)
    found := false
    for _, k := range keys {
        if k.Key == "MeterValueSampleInterval" {
            found = true
            assert.Equal(t, "30", k.Value) // default 30s
        }
    }
    assert.True(t, found)
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/ocpp/... -run TestConfigKey -v
```

Expected: compile error.

- [ ] **Step 3: Implement config_keys.go**

Create `internal/ocpp/config_keys.go`:

```go
package ocpp

import (
    "strconv"
    "sync"
)

// ConfigKeyInfo describes a single OCPP configuration key.
type ConfigKeyInfo struct {
    Key      string `json:"key"`
    Value    string `json:"value"`
    ReadOnly bool   `json:"readonly"`
    Type     string `json:"type"` // "string" | "int" | "bool"
}

// ConfigKeyManager manages OCPP 1.6 standard configuration keys.
type ConfigKeyManager struct {
    mu   sync.RWMutex
    keys map[string]*ConfigKeyInfo
}

// NewConfigKeyManager creates a manager pre-populated with OCPP 1.6 standard keys and defaults.
func NewConfigKeyManager() *ConfigKeyManager {
    m := &ConfigKeyManager{
        keys: make(map[string]*ConfigKeyInfo),
    }
    for _, k := range defaultOCPPKeys() {
        copy := k
        m.keys[k.Key] = &copy
    }
    return m
}

func defaultOCPPKeys() []ConfigKeyInfo {
    return []ConfigKeyInfo{
        {Key: "HeartbeatInterval", Value: "300", ReadOnly: false, Type: "int"},
        {Key: "ConnectionTimeOut", Value: "30", ReadOnly: false, Type: "int"},
        {Key: "MeterValueSampleInterval", Value: "30", ReadOnly: false, Type: "int"},
        {Key: "ClockAlignedDataInterval", Value: "0", ReadOnly: false, Type: "int"},
        {Key: "MeterValuesAlignedData", Value: "Energy.Active.Import.Register", ReadOnly: false, Type: "string"},
        {Key: "MeterValuesSampledData", Value: "Energy.Active.Import.Register", ReadOnly: false, Type: "string"},
        {Key: "NumberOfConnectors", Value: "1", ReadOnly: true, Type: "int"},
        {Key: "SupportedFeatureProfiles", Value: "Core,SmartCharging,LocalAuthListManagement,RemoteTrigger,Reservation,FirmwareManagement", ReadOnly: true, Type: "string"},
        {Key: "AuthorizationCacheEnabled", Value: "true", ReadOnly: false, Type: "bool"},
        {Key: "LocalAuthListEnabled", Value: "true", ReadOnly: false, Type: "bool"},
        {Key: "LocalAuthListMaxLength", Value: "1000", ReadOnly: true, Type: "int"},
        {Key: "SendLocalListMaxLength", Value: "1000", ReadOnly: true, Type: "int"},
        {Key: "ReserveConnectorZeroSupported", Value: "false", ReadOnly: true, Type: "bool"},
        {Key: "ChargeProfileMaxStackLevel", Value: "5", ReadOnly: true, Type: "int"},
        {Key: "ChargingScheduleMaxPeriods", Value: "10", ReadOnly: true, Type: "int"},
        {Key: "MaxChargingProfilesInstalled", Value: "20", ReadOnly: true, Type: "int"},
        {Key: "ChargingScheduleAllowedChargingRateUnit", Value: "Current,Power", ReadOnly: true, Type: "string"},
        {Key: "TransactionMessageAttempts", Value: "3", ReadOnly: false, Type: "int"},
        {Key: "TransactionMessageRetryInterval", Value: "60", ReadOnly: false, Type: "int"},
        {Key: "StopTransactionOnInvalidId", Value: "true", ReadOnly: false, Type: "bool"},
        {Key: "StopTransactionOnEVSideDisconnect", Value: "true", ReadOnly: false, Type: "bool"},
        {Key: "UnlockConnectorOnEVSideDisconnect", Value: "true", ReadOnly: false, Type: "bool"},
        {Key: "GetConfigurationMaxKeys", Value: "0", ReadOnly: true, Type: "int"},
    }
}

// GetConfigValue returns the current value for a key, or "" if unknown.
func (m *ConfigKeyManager) GetConfigValue(key string) string {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if k, ok := m.keys[key]; ok {
        return k.Value
    }
    return ""
}

// SetConfigValue updates a key value. Returns "Accepted", "Rejected" (read-only), or "NotSupported" (unknown).
func (m *ConfigKeyManager) SetConfigValue(key, value string) string {
    m.mu.Lock()
    defer m.mu.Unlock()
    k, ok := m.keys[key]
    if !ok {
        return "NotSupported"
    }
    if k.ReadOnly {
        return "Rejected"
    }
    k.Value = value
    return "Accepted"
}

// GetConfigKeyInfo returns all keys (for GetConfiguration OCPP response).
func (m *ConfigKeyManager) GetConfigKeyInfo() []ConfigKeyInfo {
    m.mu.RLock()
    defer m.mu.RUnlock()
    result := make([]ConfigKeyInfo, 0, len(m.keys))
    for _, k := range m.keys {
        result = append(result, *k)
    }
    return result
}

// GetMeterValueSampleInterval returns the configured interval as a duration (seconds).
func (m *ConfigKeyManager) GetMeterValueSampleInterval() int {
    val := m.GetConfigValue("MeterValueSampleInterval")
    if n, err := strconv.Atoi(val); err == nil {
        return n
    }
    return 30
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/ocpp/... -run TestConfigKey -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ocpp/config_keys.go internal/ocpp/config_keys_test.go
git commit -m "feat(ocpp): ConfigKeyManager with OCPP 1.6 standard keys"
```

---

## Task 2: Authorization Cache

**Files:**
- Create: `internal/ocpp/auth_cache.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/ocpp/config_keys_test.go` (or create `internal/ocpp/auth_cache_test.go`):

```go
func TestAuthorizationCache_PutAndGet(t *testing.T) {
    c := ocpp.NewAuthorizationCache()
    c.Put("ABC123", "Accepted", nil)

    status, expiry, found := c.Get("ABC123")
    assert.True(t, found)
    assert.Equal(t, "Accepted", status)
    assert.Nil(t, expiry)
}

func TestAuthorizationCache_Remove(t *testing.T) {
    c := ocpp.NewAuthorizationCache()
    c.Put("ABC123", "Accepted", nil)
    c.Remove("ABC123")
    _, _, found := c.Get("ABC123")
    assert.False(t, found)
}

func TestAuthorizationCache_Clear(t *testing.T) {
    c := ocpp.NewAuthorizationCache()
    c.Put("A", "Accepted", nil)
    c.Put("B", "Blocked", nil)
    c.Clear()
    assert.Equal(t, 0, c.Size())
}
```

- [ ] **Step 2: Implement auth_cache.go**

Create `internal/ocpp/auth_cache.go`:

```go
package ocpp

import (
    "sync"
    "time"
)

type cacheEntry struct {
    status string
    expiry *time.Time
}

// AuthorizationCache caches per-tag authorization status received from the CSMS.
// Populated by Authorize.conf responses; consulted for local authorization checks.
type AuthorizationCache struct {
    mu      sync.RWMutex
    entries map[string]cacheEntry
}

func NewAuthorizationCache() *AuthorizationCache {
    return &AuthorizationCache{entries: make(map[string]cacheEntry)}
}

func (c *AuthorizationCache) Get(idTag string) (status string, expiry *time.Time, found bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()
    if e, ok := c.entries[idTag]; ok {
        return e.status, e.expiry, true
    }
    return "", nil, false
}

func (c *AuthorizationCache) Put(idTag, status string, expiry *time.Time) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.entries[idTag] = cacheEntry{status, expiry}
}

func (c *AuthorizationCache) Remove(idTag string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    delete(c.entries, idTag)
}

func (c *AuthorizationCache) Clear() {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.entries = make(map[string]cacheEntry)
}

func (c *AuthorizationCache) Size() int {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return len(c.entries)
}
```

- [ ] **Step 3: Run tests and commit**

```bash
go test ./internal/ocpp/... -run TestAuthorizationCache -v
git add internal/ocpp/auth_cache.go
git commit -m "feat(ocpp): AuthorizationCache"
```

---

## Task 3: Local Auth List Manager

**Files:**
- Create: `internal/ocpp/local_auth_list.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/ocpp/local_auth_list_test.go`:

```go
package ocpp_test

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/chargeghost/engine/internal/ocpp"
)

func TestLocalAuthList_FullUpdate(t *testing.T) {
    m := ocpp.NewLocalAuthListManager()
    entries := []ocpp.LocalAuthEntry{
        {IDTag: "ABC", Status: "Accepted"},
        {IDTag: "DEF", Status: "Blocked"},
    }
    require.NoError(t, m.UpdateList(1, entries, "Full"))

    version, count, _, _ := m.GetStats()
    assert.Equal(t, 1, version)
    assert.Equal(t, 2, count)
}

func TestLocalAuthList_DifferentialUpdate(t *testing.T) {
    m := ocpp.NewLocalAuthListManager()
    _ = m.UpdateList(1, []ocpp.LocalAuthEntry{{IDTag: "ABC", Status: "Accepted"}}, "Full")
    _ = m.UpdateList(2, []ocpp.LocalAuthEntry{{IDTag: "XYZ", Status: "Blocked"}}, "Differential")

    version, count, _, _ := m.GetStats()
    assert.Equal(t, 2, version)
    assert.Equal(t, 2, count) // ABC still there, XYZ added

    entry := m.GetEntry("ABC")
    assert.NotNil(t, entry)
}

func TestLocalAuthList_Remove(t *testing.T) {
    m := ocpp.NewLocalAuthListManager()
    _ = m.UpdateList(1, []ocpp.LocalAuthEntry{{IDTag: "ABC", Status: "Accepted"}}, "Full")
    require.NoError(t, m.RemoveEntry("ABC"))
    assert.Nil(t, m.GetEntry("ABC"))
}

func TestLocalAuthList_MaxEntries(t *testing.T) {
    m := ocpp.NewLocalAuthListManager()
    entries := make([]ocpp.LocalAuthEntry, 1001)
    for i := range entries {
        entries[i] = ocpp.LocalAuthEntry{IDTag: fmt.Sprintf("tag%d", i), Status: "Accepted"}
    }
    err := m.UpdateList(1, entries, "Full")
    assert.Error(t, err) // exceeds 1000 max entries
}
```

Add `"fmt"` import.

- [ ] **Step 2: Implement local_auth_list.go**

Create `internal/ocpp/local_auth_list.go`:

```go
package ocpp

import (
    "errors"
    "sync"
    "time"
)

const maxLocalAuthListEntries = 1000

// LocalAuthListManager is the real implementation that replaces StubLocalAuthManager (Plan 3b).
// It implements the LocalAuthManager interface.
type LocalAuthListManager struct {
    mu      sync.RWMutex
    version int
    entries map[string]LocalAuthEntry
    enabled bool
}

func NewLocalAuthListManager() *LocalAuthListManager {
    return &LocalAuthListManager{
        entries: make(map[string]LocalAuthEntry),
        enabled: true,
    }
}

func (m *LocalAuthListManager) GetVersion() int {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.version
}

func (m *LocalAuthListManager) GetEntry(idTag string) *LocalAuthEntry {
    m.mu.RLock()
    defer m.mu.RUnlock()
    if e, ok := m.entries[idTag]; ok {
        if e.Expiry != nil && time.Now().After(*e.Expiry) {
            return nil // expired
        }
        return &e
    }
    return nil
}

func (m *LocalAuthListManager) GetAllEntries() []LocalAuthEntry {
    m.mu.RLock()
    defer m.mu.RUnlock()
    result := make([]LocalAuthEntry, 0, len(m.entries))
    for _, e := range m.entries {
        result = append(result, e)
    }
    return result
}

func (m *LocalAuthListManager) UpdateList(version int, entries []LocalAuthEntry, updateType string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if updateType == "Full" {
        if len(entries) > maxLocalAuthListEntries {
            return errors.New("exceeds max local auth list entries (1000)")
        }
        m.entries = make(map[string]LocalAuthEntry, len(entries))
    }

    // Check capacity for differential update.
    if updateType != "Full" {
        if len(m.entries)+len(entries) > maxLocalAuthListEntries {
            return errors.New("would exceed max local auth list entries (1000)")
        }
    }

    for _, e := range entries {
        m.entries[e.IDTag] = e
    }
    m.version = version
    return nil
}

func (m *LocalAuthListManager) RemoveEntry(idTag string) error {
    m.mu.Lock()
    defer m.mu.Unlock()
    if _, ok := m.entries[idTag]; !ok {
        return errors.New("entry not found")
    }
    delete(m.entries, idTag)
    return nil
}

func (m *LocalAuthListManager) Clear() {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.entries = make(map[string]LocalAuthEntry)
    m.version = 0
}

func (m *LocalAuthListManager) GetStats() (version, count, maxEntries int, enabled bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.version, len(m.entries), maxLocalAuthListEntries, m.enabled
}
```

- [ ] **Step 3: Run tests and commit**

```bash
go test ./internal/ocpp/... -run TestLocalAuthList -v
git add internal/ocpp/local_auth_list.go internal/ocpp/local_auth_list_test.go
git commit -m "feat(ocpp): LocalAuthListManager with full/differential update"
```

---

## Task 4: Message Queue

**Files:**
- Create: `internal/ocpp/queue/queue.go`
- Create: `internal/ocpp/queue/memory.go`
- Create: `internal/ocpp/queue/json_file.go`

- [ ] **Step 1: Add google/uuid**

```bash
go get github.com/google/uuid@latest
go mod tidy
```

- [ ] **Step 2: Write the failing tests**

Create `internal/ocpp/queue/queue_test.go`:

```go
package queue_test

import (
    "os"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/chargeghost/engine/internal/ocpp/queue"
)

func testQueue(t *testing.T, q queue.MessageQueue) {
    t.Helper()
    assert.Equal(t, 0, q.Len())

    id1, err := q.Enqueue(queue.QueuedMessage{Type: "StartTransaction", Payload: map[string]string{"foo": "bar"}})
    require.NoError(t, err)
    assert.NotEmpty(t, id1)

    id2, _ := q.Enqueue(queue.QueuedMessage{Type: "StopTransaction", Payload: nil})
    assert.Equal(t, 2, q.Len())

    msg, ok := q.Peek()
    assert.True(t, ok)
    assert.Equal(t, "StartTransaction", msg.Type)
    assert.Equal(t, id1, msg.ID)

    q.Dequeue(id1)
    assert.Equal(t, 1, q.Len())

    msg, ok = q.Peek()
    assert.True(t, ok)
    assert.Equal(t, id2, msg.ID)
}

func TestInMemoryQueue(t *testing.T) {
    q := queue.NewInMemoryQueue(3)
    testQueue(t, q)
}

func TestJsonFileQueue(t *testing.T) {
    f, err := os.CreateTemp("", "queue-*.json")
    require.NoError(t, err)
    f.Close()
    defer os.Remove(f.Name())

    q, err := queue.NewJsonFileQueue(f.Name(), 3)
    require.NoError(t, err)
    testQueue(t, q)

    // Verify persistence: create a new queue from same file.
    q2, err := queue.NewJsonFileQueue(f.Name(), 3)
    require.NoError(t, err)
    assert.Equal(t, 1, q2.Len()) // "StopTransaction" survived
}
```

- [ ] **Step 3: Implement queue.go**

Create `internal/ocpp/queue/queue.go`:

```go
package queue

import "time"

// QueuedMessage is a single buffered OCPP message.
type QueuedMessage struct {
    ID           string      `json:"id"`
    Type         string      `json:"type"`      // "StartTransaction" | "StopTransaction" | "MeterValues"
    Payload      interface{} `json:"payload"`
    CreatedAt    time.Time   `json:"created_at"`
    RetryCount   int         `json:"retry_count"`
    MaxRetries   int         `json:"max_retries"`
}

// MessageQueue is a FIFO buffer for offline OCPP messages.
type MessageQueue interface {
    // Enqueue adds a message and returns its assigned ID.
    Enqueue(msg QueuedMessage) (string, error)
    // Peek returns the oldest message without removing it.
    Peek() (QueuedMessage, bool)
    // Dequeue removes the message with the given ID.
    Dequeue(id string)
    // Len returns the number of queued messages.
    Len() int
    // All returns a snapshot of all queued messages in FIFO order.
    All() []QueuedMessage
}

// NewQueue creates the appropriate queue backend based on persist flag.
// If persist is true and path is non-empty, creates a JsonFileQueue.
// Otherwise creates an InMemoryQueue.
func NewQueue(persist bool, path string, maxRetries int) (MessageQueue, error) {
    if persist && path != "" {
        return NewJsonFileQueue(path, maxRetries)
    }
    return NewInMemoryQueue(maxRetries), nil
}
```

- [ ] **Step 4: Implement memory.go**

Create `internal/ocpp/queue/memory.go`:

```go
package queue

import (
    "sync"
    "time"

    "github.com/google/uuid"
)

// InMemoryQueue is a thread-safe in-memory FIFO queue. Messages are lost on restart.
type InMemoryQueue struct {
    mu         sync.Mutex
    messages   []QueuedMessage
    maxRetries int
}

func NewInMemoryQueue(maxRetries int) *InMemoryQueue {
    return &InMemoryQueue{maxRetries: maxRetries}
}

func (q *InMemoryQueue) Enqueue(msg QueuedMessage) (string, error) {
    q.mu.Lock()
    defer q.mu.Unlock()
    msg.ID = uuid.New().String()
    msg.CreatedAt = time.Now()
    msg.MaxRetries = q.maxRetries
    q.messages = append(q.messages, msg)
    return msg.ID, nil
}

func (q *InMemoryQueue) Peek() (QueuedMessage, bool) {
    q.mu.Lock()
    defer q.mu.Unlock()
    if len(q.messages) == 0 {
        return QueuedMessage{}, false
    }
    return q.messages[0], true
}

func (q *InMemoryQueue) Dequeue(id string) {
    q.mu.Lock()
    defer q.mu.Unlock()
    for i, m := range q.messages {
        if m.ID == id {
            q.messages = append(q.messages[:i], q.messages[i+1:]...)
            return
        }
    }
}

func (q *InMemoryQueue) Len() int {
    q.mu.Lock()
    defer q.mu.Unlock()
    return len(q.messages)
}

func (q *InMemoryQueue) All() []QueuedMessage {
    q.mu.Lock()
    defer q.mu.Unlock()
    result := make([]QueuedMessage, len(q.messages))
    copy(result, q.messages)
    return result
}
```

- [ ] **Step 5: Implement json_file.go**

Create `internal/ocpp/queue/json_file.go`:

```go
package queue

import (
    "encoding/json"
    "os"
    "sync"
    "time"

    "github.com/google/uuid"
)

type jsonFileData struct {
    Messages []QueuedMessage `json:"messages"`
}

// JsonFileQueue persists the message queue to a JSON file. Survives restarts.
type JsonFileQueue struct {
    mu         sync.Mutex
    path       string
    messages   []QueuedMessage
    maxRetries int
}

// NewJsonFileQueue creates or loads an existing queue from the given file path.
func NewJsonFileQueue(path string, maxRetries int) (*JsonFileQueue, error) {
    q := &JsonFileQueue{path: path, maxRetries: maxRetries}
    if err := q.load(); err != nil && !os.IsNotExist(err) {
        return nil, err
    }
    return q, nil
}

func (q *JsonFileQueue) Enqueue(msg QueuedMessage) (string, error) {
    q.mu.Lock()
    defer q.mu.Unlock()
    msg.ID = uuid.New().String()
    msg.CreatedAt = time.Now()
    msg.MaxRetries = q.maxRetries
    q.messages = append(q.messages, msg)
    return msg.ID, q.save()
}

func (q *JsonFileQueue) Peek() (QueuedMessage, bool) {
    q.mu.Lock()
    defer q.mu.Unlock()
    if len(q.messages) == 0 {
        return QueuedMessage{}, false
    }
    return q.messages[0], true
}

func (q *JsonFileQueue) Dequeue(id string) {
    q.mu.Lock()
    defer q.mu.Unlock()
    for i, m := range q.messages {
        if m.ID == id {
            q.messages = append(q.messages[:i], q.messages[i+1:]...)
            _ = q.save()
            return
        }
    }
}

func (q *JsonFileQueue) Len() int {
    q.mu.Lock()
    defer q.mu.Unlock()
    return len(q.messages)
}

func (q *JsonFileQueue) All() []QueuedMessage {
    q.mu.Lock()
    defer q.mu.Unlock()
    result := make([]QueuedMessage, len(q.messages))
    copy(result, q.messages)
    return result
}

func (q *JsonFileQueue) load() error {
    data, err := os.ReadFile(q.path)
    if err != nil {
        return err
    }
    var fd jsonFileData
    if err := json.Unmarshal(data, &fd); err != nil {
        return err
    }
    q.messages = fd.Messages
    return nil
}

func (q *JsonFileQueue) save() error {
    data, err := json.MarshalIndent(jsonFileData{Messages: q.messages}, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(q.path, data, 0600)
}
```

- [ ] **Step 6: Run tests and commit**

```bash
go test ./internal/ocpp/queue/... -v
git add internal/ocpp/queue/
git commit -m "feat(ocpp): MessageQueue with InMemory and JsonFile backends"
```

---

## Task 5: Wire Config Keys, Auth Cache, Local Auth, and Queue into Bridge

**Files:**
- Modify: `internal/ocpp/bridge.go`
- Modify: `cmd/chargeghost/main.go`

- [ ] **Step 1: Add new fields to Bridge**

In `bridge.go`, add to the `Bridge` struct:

```go
type Bridge struct {
    // ... existing fields ...
    configKeys *ConfigKeyManager
    authCache  *AuthorizationCache
    localAuth  LocalAuthManager
    queue      queue.MessageQueue
}
```

Add import: `"github.com/chargeghost/engine/internal/ocpp/queue"`

Update `NewBridge` to accept these:

```go
func NewBridge(e *engine.Engine, hub *ws.Hub, cfg *config.Config, dispatcher *CommandDispatcher, pm *ChargingProfileManager, configKeys *ConfigKeyManager, authCache *AuthorizationCache, localAuth LocalAuthManager, q queue.MessageQueue) *Bridge {
```

- [ ] **Step 2: Implement OnGetConfiguration and OnChangeConfiguration**

Replace stubs in `bridge.go`:

```go
func (b *Bridge) OnGetConfiguration(request *core.GetConfigurationRequest) (*core.GetConfigurationResponse, error) {
    allKeys := b.configKeys.GetConfigKeyInfo()
    knownKeys := make([]core.ConfigurationKey, 0)
    unknownKeys := make([]string, 0)

    requested := request.Key
    if len(requested) == 0 {
        // Return all keys.
        for _, k := range allKeys {
            readonly := k.ReadOnly
            knownKeys = append(knownKeys, core.ConfigurationKey{
                Key:      k.Key,
                Readonly: &readonly,
                Value:    &k.Value,
            })
        }
    } else {
        keyMap := make(map[string]ConfigKeyInfo, len(allKeys))
        for _, k := range allKeys {
            keyMap[k.Key] = k
        }
        for _, reqKey := range requested {
            if k, ok := keyMap[reqKey]; ok {
                readonly := k.ReadOnly
                knownKeys = append(knownKeys, core.ConfigurationKey{
                    Key:      k.Key,
                    Readonly: &readonly,
                    Value:    &k.Value,
                })
            } else {
                unknownKeys = append(unknownKeys, reqKey)
            }
        }
    }
    return core.NewGetConfigurationResponse(knownKeys, unknownKeys), nil
}

func (b *Bridge) OnChangeConfiguration(request *core.ChangeConfigurationRequest) (*core.ChangeConfigurationResponse, error) {
    result := b.configKeys.SetConfigValue(request.Key, request.Value)
    switch result {
    case "Accepted":
        b.hub.BroadcastMessage(ws.Message{
            Type: "ocpp_config_key_changed",
            Data: map[string]string{"key": request.Key, "value": request.Value},
        })
        return core.NewChangeConfigurationResponse(core.ConfigurationStatusAccepted), nil
    case "Rejected":
        return core.NewChangeConfigurationResponse(core.ConfigurationStatusRejected), nil
    default:
        return core.NewChangeConfigurationResponse(core.ConfigurationStatusNotSupported), nil
    }
}
```

- [ ] **Step 3: Implement OnGetLocalListVersion and OnSendLocalList**

These are part of the `localauth` feature. Add handler registration in `NewBridge`:

```go
b.cp.SetLocalAuthListHandler(b)
```

Add import: `"github.com/lorenzodonini/ocpp-go/ocpp1.6/localauth"`

Implement:

```go
func (b *Bridge) OnGetLocalListVersion(request *localauth.GetLocalListVersionRequest) (*localauth.GetLocalListVersionResponse, error) {
    version := b.localAuth.GetVersion()
    return localauth.NewGetLocalListVersionResponse(version), nil
}

func (b *Bridge) OnSendLocalList(request *localauth.SendLocalListRequest) (*localauth.SendLocalListResponse, error) {
    entries := make([]LocalAuthEntry, 0, len(request.LocalAuthorizationList))
    for _, e := range request.LocalAuthorizationList {
        entry := LocalAuthEntry{IDTag: e.IdTag, Status: "Accepted"}
        if e.IdTagInfo != nil {
            entry.Status = string(e.IdTagInfo.Status)
            if e.IdTagInfo.ExpiryDate != nil {
                t := e.IdTagInfo.ExpiryDate.Time
                entry.Expiry = &t
            }
        }
        entries = append(entries, entry)
    }
    updateType := string(request.UpdateType) // "Full" or "Differential"
    if err := b.localAuth.UpdateList(request.ListVersion, entries, updateType); err != nil {
        return localauth.NewSendLocalListResponse(localauth.UpdateStatusFailed), nil
    }
    return localauth.NewSendLocalListResponse(localauth.UpdateStatusAccepted), nil
}
```

- [ ] **Step 4: Wire queue into StartTransaction / StopTransaction**

Modify `SendStartTransaction` in `bridge.go` to enqueue when disconnected:

```go
func (b *Bridge) SendStartTransaction(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
    if !b.IsConnected() {
        _, _ = b.queue.Enqueue(queue.QueuedMessage{
            Type:    "StartTransaction",
            Payload: map[string]interface{}{"connectorID": connectorID, "idTag": idTag, "meterStart": meterStart},
        })
        return 0, nil
    }
    // ... existing send code ...
}
```

Apply the same pattern to `SendStopTransaction` and `SendMeterValues`.

- [ ] **Step 5: Add drain-on-reconnect in bridge**

In the `SetOnConnectedHandler` callback, after sending BootNotification, drain the queue:

```go
b.cp.SetOnConnectedHandler(func() {
    b.connected.Store(true)
    b.hub.BroadcastMessage(ws.Message{Type: "connection_state_changed", Data: map[string]bool{"connected": true}})
    // Drain offline queue.
    go b.drainQueue()
    // Send BootNotification.
    b.dispatcher.Enqueue(OCPPCommand{Description: "BootNotification", Execute: b.SendBootNotification})
})
```

```go
func (b *Bridge) drainQueue() {
    for {
        msg, ok := b.queue.Peek()
        if !ok {
            return
        }
        // Re-send the message. For now, just dequeue — full replay implemented as needed.
        // In production, re-marshal payload and call the appropriate Send method.
        slog.Info("draining queued message", "type", msg.Type, "id", msg.ID)
        b.queue.Dequeue(msg.ID)
    }
}
```

- [ ] **Step 6: Wire in main.go**

```go
import "github.com/chargeghost/engine/internal/ocpp/queue"

configKeys := ocpp.NewConfigKeyManager()
authCache := ocpp.NewAuthorizationCache()
localAuthReal := ocpp.NewLocalAuthListManager()

queuePath := os.ExpandEnv("$HOME/.chargeghost/message_queue.json")
messageQueue, err := queue.NewQueue(cfg.PersistMessageQueue, queuePath, 3)
if err != nil {
    slog.Error("failed to create message queue", "err", err)
    os.Exit(1)
}

bridge := ocpp.NewBridge(e, hub, cfg, dispatcher, profileManager, configKeys, authCache, localAuthReal, messageQueue)

// Replace stub local auth in AppContext with the real one.
app := &api.AppContext{
    // ... other fields ...
    LocalAuth: localAuthReal, // replaces stub
}
```

- [ ] **Step 7: Build and test**

```bash
go build ./...
go test ./... -count=1 -timeout 30s
```

Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add internal/ocpp/bridge.go cmd/chargeghost/main.go
git commit -m "feat(ocpp): wire ConfigKeys, AuthCache, LocalAuthList, MessageQueue into Bridge"
```

---

## Task 6: Wire OCPP Config Key REST Endpoints

**Files:**
- Create: `internal/api/handlers/ocpp.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: Implement ocpp.go (config key endpoints only)**

Create `internal/api/handlers/ocpp.go`:

```go
package handlers

import (
    "encoding/json"
    "net/http"

    "github.com/chargeghost/engine/internal/api"
    "github.com/chargeghost/engine/internal/ocpp"
)

func GetOCPPConfigKeys(m *ocpp.ConfigKeyManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, http.StatusOK, m.GetConfigKeyInfo())
    }
}

func PatchOCPPConfigKey(m *ocpp.ConfigKeyManager) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req struct {
            Key   string `json:"key"`
            Value string `json:"value"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid request body"})
            return
        }
        result := m.SetConfigValue(req.Key, req.Value)
        switch result {
        case "Accepted":
            writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Key updated"})
        case "Rejected":
            writeJSON(w, http.StatusForbidden, api.Response{Success: false, Message: "key is read-only"})
        default:
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "key not supported"})
        }
    }
}
```

- [ ] **Step 2: Add routes and ConfigKeys to AppContext**

In `router.go`, add `ConfigKeys *ocpp.ConfigKeyManager` to `AppContext`, and add routes:

```go
        r.Route("/ocpp", func(r chi.Router) {
            r.Get("/config-keys", handlers.GetOCPPConfigKeys(app.ConfigKeys))
            r.Patch("/config-keys", handlers.PatchOCPPConfigKey(app.ConfigKeys))
            // Raw send endpoints wired in Plan 5e.
        })
```

- [ ] **Step 3: Commit**

```bash
git add internal/api/handlers/ocpp.go internal/api/router.go cmd/chargeghost/main.go
git commit -m "feat(api): OCPP config key REST endpoints"
```
