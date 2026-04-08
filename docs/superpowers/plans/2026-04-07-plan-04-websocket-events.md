# Plan 04 — WebSocket Events

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Any WebSocket client connecting to `/ws` receives a full state snapshot immediately, then live event broadcasts for every engine state change and a periodic tick every ~1 second.

**Architecture:** The Hub uses the canonical Go pattern: a single goroutine owns the client map, communicating via channels (register, unregister, broadcast). No mutex needed on the Hub. Engine callbacks enqueue events non-blocking via `BroadcastAsync`. A separate tick goroutine fires every 1s with full status. The `gorilla/websocket` library handles the WebSocket protocol.

**Tech Stack:** Go 1.22, `github.com/gorilla/websocket v1.5.x`

---

## File Map

| File | Responsibility |
|------|----------------|
| `internal/api/ws/messages.go` | `Message` struct — JSON envelope |
| `internal/api/ws/hub.go` | Hub: single-goroutine broadcast manager + Client |
| `internal/api/ws/tick.go` | Periodic tick goroutine (1s interval, full status payload) |
| `internal/api/router.go` | Modified: add `/ws` route, inject Hub into AppContext |
| `cmd/chargeghost/main.go` | Modified: start Hub goroutine, wire engine callbacks |

---

## Task 1: Dependencies

- [ ] **Step 1: Add gorilla/websocket**

```bash
go get github.com/gorilla/websocket@v1.5.3
go mod tidy
```

---

## Task 2: Message Struct

**Files:**
- Create: `internal/api/ws/messages.go`

- [ ] **Step 1: Implement messages.go**

Create `internal/api/ws/messages.go`:

```go
package ws

import "time"

// Message is the JSON envelope sent to all WebSocket clients.
// Callers construct a Message and call hub.BroadcastMessage — the hub marshals it internally.
type Message struct {
    Type      string    `json:"type"`
    Timestamp time.Time `json:"timestamp"`
    Data      any       `json:"data"`
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/api/ws/messages.go
git commit -m "feat(ws): Message envelope type"
```

---

## Task 3: Hub and Client

**Files:**
- Create: `internal/api/ws/hub.go`
- Create: `internal/api/ws/hub_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/api/ws/hub_test.go`:

```go
package ws_test

import (
    "context"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
    "time"

    "github.com/gorilla/websocket"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    ws "github.com/chargeghost/engine/internal/api/ws"
)

func TestHub_BroadcastReachesConnectedClient(t *testing.T) {
    hub := ws.NewHub()
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go hub.Run(ctx)

    // Start a test HTTP server that upgrades to WebSocket.
    upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        hub.ServeWS(w, r, ws.Message{Type: "state_snapshot", Data: "test"})
    }))
    defer srv.Close()

    // Connect a WebSocket client.
    wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
    conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
    require.NoError(t, err)
    defer conn.Close()

    // Read the state snapshot sent on connect.
    conn.SetReadDeadline(time.Now().Add(2 * time.Second))
    _, msg, err := conn.ReadMessage()
    require.NoError(t, err)
    assert.Contains(t, string(msg), "state_snapshot")

    // Broadcast a message and verify it's received.
    hub.BroadcastMessage(ws.Message{Type: "test_event", Data: "hello"})
    _, msg, err = conn.ReadMessage()
    require.NoError(t, err)
    assert.Contains(t, string(msg), "test_event")

    _ = upgrader // suppress unused warning
}

func TestHub_SlowClientDropped(t *testing.T) {
    hub := ws.NewHub()
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go hub.Run(ctx)

    // Flood the broadcast channel faster than a client can read.
    // Hub should not block; slow clients are dropped.
    for i := 0; i < 300; i++ {
        hub.BroadcastMessage(ws.Message{Type: "flood", Data: i})
    }
    // No panic or deadlock — test passes by completing.
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/api/ws/... -v
```

Expected: compile error — `ws` package does not exist.

- [ ] **Step 3: Implement hub.go**

Create `internal/api/ws/hub.go`:

```go
package ws

import (
    "context"
    "encoding/json"
    "log/slog"
    "net/http"
    "time"

    "github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
    ReadBufferSize:  1024,
    WriteBufferSize: 1024,
    CheckOrigin:     func(r *http.Request) bool { return true }, // allow any origin
}

// Hub manages WebSocket client lifecycle via a single goroutine.
// All client map mutations happen in Run(), never in handler goroutines.
type Hub struct {
    clients    map[*Client]bool
    register   chan *Client
    unregister chan *Client
    broadcast  chan []byte
}

// Client represents a single WebSocket connection.
type Client struct {
    hub  *Hub
    conn *websocket.Conn
    send chan []byte
}

// NewHub creates an idle Hub. Call Run(ctx) in a goroutine before use.
func NewHub() *Hub {
    return &Hub{
        clients:    make(map[*Client]bool),
        register:   make(chan *Client),
        unregister: make(chan *Client),
        broadcast:  make(chan []byte, 256),
    }
}

// Run is the single goroutine that owns the client map. Blocks until ctx is done.
func (h *Hub) Run(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            return
        case client := <-h.register:
            h.clients[client] = true
        case client := <-h.unregister:
            if _, ok := h.clients[client]; ok {
                delete(h.clients, client)
                close(client.send)
            }
        case message := <-h.broadcast:
            dead := make([]*Client, 0)
            for client := range h.clients {
                select {
                case client.send <- message:
                default:
                    // Client's send buffer full — mark for removal.
                    dead = append(dead, client)
                }
            }
            for _, client := range dead {
                close(client.send)
                delete(h.clients, client)
            }
        }
    }
}

// BroadcastAsync enqueues pre-marshaled bytes for broadcast.
// Non-blocking — safe to call from engine callbacks (which hold the engine mutex).
func (h *Hub) BroadcastAsync(msg []byte) {
    select {
    case h.broadcast <- msg:
    default:
        // Broadcast channel full — drop rather than block.
    }
}

// BroadcastMessage marshals a Message and enqueues it for broadcast.
func (h *Hub) BroadcastMessage(msg Message) {
    msg.Timestamp = time.Now()
    b, err := json.Marshal(msg)
    if err != nil {
        slog.Error("ws: marshal failed", "type", msg.Type, "error", err)
        return
    }
    h.BroadcastAsync(b)
}

// ServeWS upgrades the HTTP connection to WebSocket, sends the snapshot,
// and registers the client with the Hub.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, snapshot Message) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        slog.Error("ws: upgrade failed", "error", err)
        return
    }

    client := &Client{
        hub:  h,
        conn: conn,
        send: make(chan []byte, 256),
    }
    h.register <- client

    // Send state snapshot immediately.
    snapshot.Timestamp = time.Now()
    if b, err := json.Marshal(snapshot); err == nil {
        client.send <- b
    }

    go client.writePump()
    go client.readPump()
}

// writePump drains the client's send channel to the WebSocket connection.
func (c *Client) writePump() {
    ticker := time.NewTicker(54 * time.Second) // ping interval
    defer func() {
        ticker.Stop()
        c.conn.Close()
    }()
    for {
        select {
        case message, ok := <-c.send:
            c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
            if !ok {
                _ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
                return
            }
            if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
                return
            }
        case <-ticker.C:
            c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
            if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
                return
            }
        }
    }
}

// readPump reads from the WebSocket to detect disconnects.
// Clients don't send commands to the server (read-only stream).
func (c *Client) readPump() {
    defer func() {
        c.hub.unregister <- c
        c.conn.Close()
    }()
    c.conn.SetReadLimit(512)
    c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
    c.conn.SetPongHandler(func(string) error {
        c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
        return nil
    })
    for {
        _, _, err := c.conn.ReadMessage()
        if err != nil {
            return
        }
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/api/ws/... -v -timeout 10s
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/ws/hub.go internal/api/ws/hub_test.go
git commit -m "feat(ws): Hub with single-goroutine client management"
```

---

## Task 4: Tick Goroutine

**Files:**
- Create: `internal/api/ws/tick.go`

- [ ] **Step 1: Implement tick.go**

Create `internal/api/ws/tick.go`:

```go
package ws

import (
    "context"
    "fmt"
    "time"

    engine "github.com/chargeghost/engine/internal/engine"
)

// StartTicker broadcasts a full status snapshot to all WebSocket clients every interval.
// Call in a dedicated goroutine.
func StartTicker(ctx context.Context, hub *Hub, e *engine.Engine, interval time.Duration) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            hub.BroadcastMessage(Message{
                Type: "tick",
                Data: buildStatusSnapshot(e),
            })
        }
    }
}

// buildStatusSnapshot assembles the full status payload (same as GET /api/v1/status).
func buildStatusSnapshot(e *engine.Engine) map[string]interface{} {
    ids := e.GetConnectorIDs()
    connectors := make([]map[string]interface{}, 0, len(ids))
    for _, id := range ids {
        c := e.GetConnector(id)
        if c == nil {
            continue
        }
        connectors = append(connectors, map[string]interface{}{
            "id":           c.ID,
            "status":       string(c.Status),
            "voltage":      c.Voltage,
            "current":      c.Current,
            "phase":        c.Phase,
            "is_plugged_in": c.IsPluggedIn,
            "id_tag":       c.IDTag,
        })
    }

    sessions := e.GetSessionInfo()
    sessionList := make([]map[string]interface{}, 0, len(sessions))
    for _, s := range sessions {
        sessionList = append(sessionList, map[string]interface{}{
            "transaction_id":    s.TransactionID,
            "connector_id":      s.ConnectorID,
            "energy_charged_wh": s.EnergyCharged,
            "state_of_charge":   s.StateOfCharge,
            "start_time":        s.StartTime,
            "id_tag":            s.IDTag,
            "is_charging":       s.IsCharging,
        })
    }

    meters := make(map[string]interface{})
    for _, id := range ids {
        m := e.GetEnergyMeter(id)
        if m != nil {
            meters[fmt.Sprintf("%d", id)] = map[string]interface{}{
                "reading_wh":  m.Value,
                "is_charging": m.IsCharging,
            }
        }
    }

    return map[string]interface{}{
        "ocpp_connected":  false, // updated in Plan 5a
        "connectors":      connectors,
        "active_sessions": sessionList,
        "energy_meters":   meters,
    }
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/api/ws/tick.go
git commit -m "feat(ws): periodic tick broadcaster"
```

---

## Task 5: Wire Hub and Engine Callbacks

**Files:**
- Modify: `internal/api/router.go`
- Modify: `cmd/chargeghost/main.go`

- [ ] **Step 1: Add Hub to AppContext and /ws route**

In `internal/api/router.go`:

Add `Hub *ws.Hub` field to `AppContext`:

```go
type AppContext struct {
    Engine      *engine.Engine
    Config      *config.Config
    StartTime   time.Time
    Timeline    *timeline.Store
    LocalAuth   ocpp.LocalAuthManager
    Firmware    ocpp.FirmwareManager
    Diagnostics ocpp.DiagnosticsManager
    Hub         *ws.Hub
}
```

Add import: `ws "github.com/chargeghost/engine/internal/api/ws"`

In `NewRouter`, after the `/api/v1` block:

```go
    r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
        snapshot := ws.Message{
            Type: "state_snapshot",
            Data: buildWSSnapshot(app),
        }
        app.Hub.ServeWS(w, r, snapshot)
    })
```

Add the snapshot builder to `router.go`:

```go
func buildWSSnapshot(app *AppContext) map[string]interface{} {
    // Reuse tick's snapshot builder.
    return ws.BuildStatusSnapshot(app.Engine)
}
```

Make `buildStatusSnapshot` in `tick.go` exported by renaming it to `BuildStatusSnapshot`.

- [ ] **Step 2: Wire engine callbacks and start goroutines in main.go**

In `cmd/chargeghost/main.go`, after creating the hub:

```go
hub := ws.NewHub()
go hub.Run(ctx)
go ws.StartTicker(ctx, hub, e, 1*time.Second)

// Wire engine event callbacks to WebSocket broadcasts.
// All callbacks must be non-blocking — BroadcastMessage is non-blocking.
e.OnConnectorStatusChanged = func(connectorID int, status engine.ConnectorState) {
    hub.BroadcastMessage(ws.Message{
        Type: "connector_status_changed",
        Data: map[string]interface{}{
            "connector_id": connectorID,
            "status":       string(status),
        },
    })
}

e.OnConnectorParamsChanged = func(connectorID int, voltage, current float64, phase int) {
    hub.BroadcastMessage(ws.Message{
        Type: "connector_params_changed",
        Data: map[string]interface{}{
            "connector_id": connectorID,
            "voltage":      voltage,
            "current":      current,
            "phase":        phase,
        },
    })
}

e.OnSessionStarted = func(connectorID int) {
    s := e.GetSession(connectorID)
    m := e.GetEnergyMeter(connectorID)
    hub.BroadcastMessage(ws.Message{
        Type: "session_started",
        Data: map[string]interface{}{
            "connector_id":   connectorID,
            "transaction_id": s.TransactionID,
            "id_tag":         s.IDTag,
            "start_time":     s.StartTime,
            "is_charging":    m != nil && m.IsCharging,
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
}

e.OnReservationExpired = func(reservationID, connectorID int) {
    hub.BroadcastMessage(ws.Message{
        Type: "reservation_changed",
        Data: map[string]interface{}{
            "action":         "expired",
            "reservation_id": reservationID,
            "connector_id":   connectorID,
        },
    })
}
```

Add `hub` to `AppContext`:

```go
app := &api.AppContext{
    Engine:      e,
    Config:      cfg,
    StartTime:   time.Now(),
    Timeline:    timelineStore,
    LocalAuth:   localAuth,
    Firmware:    firmware,
    Diagnostics: diagnostics,
    Hub:         hub,
}
```

- [ ] **Step 3: Build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 4: Integration test**

```bash
./chargeghost &
sleep 1

# Connect a WebSocket client and verify snapshot + events.
# Using wscat (npm install -g wscat) or websocat:
wscat -c ws://localhost:8080/ws &
sleep 1

# Trigger an event.
curl -s -X POST http://localhost:8080/api/v1/connectors/1/plug_in > /dev/null

# Should see connector_status_changed in wscat output.
sleep 1
kill %1 %2
```

Expected: state snapshot received on connect; `connector_status_changed` event received after plug_in.

- [ ] **Step 5: Run all tests**

```bash
go test ./... -count=1 -timeout 30s
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/api/ws/tick.go internal/api/router.go cmd/chargeghost/main.go
git commit -m "feat(ws): wire hub, engine callbacks, and /ws endpoint"
```
