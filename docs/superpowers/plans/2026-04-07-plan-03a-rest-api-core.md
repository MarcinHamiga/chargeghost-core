# Plan 03a — REST API Core

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose a working HTTP server with status, connector CRUD + actions, session control, reservation, and in-memory config endpoints — all returning the standard response envelope.

**Architecture:** `chi` router, handlers are thin wrappers that call engine methods and marshal results. Config is in-memory only (disk persistence in Plan 6). All mutation endpoints return `{"success":bool,"message":"...","details":{...}}`. CORS middleware allows any origin (for 3rd-party GUIs).

**Tech Stack:** Go 1.22, `github.com/go-chi/chi/v5`, `github.com/stretchr/testify`

---

## File Map

| File | Responsibility |
|------|----------------|
| `internal/config/config.go` | `Config` struct, defaults, in-memory load |
| `internal/api/dto.go` | All JSON request/response structs |
| `internal/api/server.go` | HTTP server setup, CORS middleware, start/stop |
| `internal/api/router.go` | Route registration (all `/api/v1/*`) |
| `internal/api/handlers/status.go` | `GET /api/v1/status` |
| `internal/api/handlers/connectors.go` | Connector CRUD + actions |
| `internal/api/handlers/sessions.go` | Session start/stop/list/info |
| `internal/api/handlers/config.go` | Config GET/PATCH/save |
| `internal/api/handlers/reservations.go` | Reservation list/create/cancel |
| `internal/api/handlers_test.go` | HTTP integration tests using httptest |

---

## Task 1: Dependencies and Config

**Files:**
- Modify: `go.mod`
- Create: `internal/config/config.go`

- [ ] **Step 1: Add chi**

```bash
go get github.com/go-chi/chi/v5@latest
go mod tidy
```

- [ ] **Step 2: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config_test

import (
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/chargeghost/engine/internal/config"
)

func TestConfig_Defaults(t *testing.T) {
    c := config.DefaultConfig()
    assert.Equal(t, "CP_1", c.OCPPID)
    assert.Equal(t, "ChargeGhostV1", c.ChargePointModel)
    assert.Equal(t, "1.6", c.OCPPVersion)
    assert.Equal(t, 55.0, c.EVBatteryCapacity) // kWh
    assert.False(t, c.MultiEVSEMode)
    assert.Len(t, c.Connectors, 1)
    assert.Equal(t, 230.0, c.Connectors[0].Voltage)
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/config/... -v
```

Expected: compile error.

- [ ] **Step 4: Implement config.go**

Create `internal/config/config.go`:

```go
package config

// ConnectorConfig holds per-connector startup parameters.
type ConnectorConfig struct {
    Voltage float64 `json:"voltage"`
    Current float64 `json:"current"`
    Phase   int     `json:"phase"`
}

// Config is the full application configuration. Stored in-memory;
// persistence to ~/.chargeghost/config.json is added in Plan 6.
type Config struct {
    ConnectionURL      string            `json:"connection_url"`
    OCPPID             string            `json:"ocpp_id"`
    OCPPPassword       *string           `json:"ocpp_password,omitempty"`
    ChargePointModel   string            `json:"charge_point_model"`
    ChargePointVendor  string            `json:"charge_point_vendor"`
    Connectors         []ConnectorConfig `json:"connectors"`
    SkipTLSVerify      bool              `json:"skip_tls_verify"`
    LogMode            string            `json:"log_mode"`
    MultiEVSEMode      bool              `json:"multi_evse_mode"`
    EVBatteryCapacity  float64           `json:"ev_battery_capacity"` // kWh (user-facing)
    OCPPVersion        string            `json:"ocpp_version"`
    PersistMessageQueue bool             `json:"persist_message_queue"`
    RFIDTag            *string           `json:"rfid_tag"`
    IgnoredVersion     *string           `json:"ignored_version"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
    return &Config{
        ConnectionURL:     "wss://localhost:3000/CP_1",
        OCPPID:            "CP_1",
        ChargePointModel:  "ChargeGhostV1",
        ChargePointVendor: "ChargeGhost",
        Connectors: []ConnectorConfig{
            {Voltage: 230.0, Current: 32.0, Phase: 1},
        },
        LogMode:           "shallow",
        OCPPVersion:       "1.6",
        EVBatteryCapacity: 55.0,
    }
}
```

- [ ] **Step 5: Run test to verify it passes**

```bash
go test ./internal/config/... -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go go.mod go.sum
git commit -m "feat(config): Config struct with defaults"
```

---

## Task 2: DTOs and Response Envelope

**Files:**
- Create: `internal/api/dto.go`

- [ ] **Step 1: Implement dto.go**

Create `internal/api/dto.go`:

```go
package api

import "time"

// Response is the standard envelope for all mutation endpoints.
type Response struct {
    Success bool        `json:"success"`
    Message string      `json:"message"`
    Details interface{} `json:"details,omitempty"`
}

// ConnectorDTO is the JSON representation of a connector.
type ConnectorDTO struct {
    ID          int     `json:"id"`
    Status      string  `json:"status"`
    Voltage     float64 `json:"voltage"`
    Current     float64 `json:"current"`
    Phase       int     `json:"phase"`
    IsPluggedIn bool    `json:"is_plugged_in"`
    IDTag       *string `json:"id_tag"`
}

// SessionDTO is the JSON representation of an active session.
type SessionDTO struct {
    TransactionID int       `json:"transaction_id"`
    ConnectorID   int       `json:"connector_id"`
    EnergyWh      float64   `json:"energy_charged_wh"`
    StateOfCharge float64   `json:"state_of_charge"`
    StartTime     time.Time `json:"start_time"`
    IDTag         *string   `json:"id_tag"`
    IsCharging    bool      `json:"is_charging"`
}

// EnergyMeterDTO is the JSON representation of an energy meter.
type EnergyMeterDTO struct {
    ReadingWh  float64 `json:"reading_wh"`
    IsCharging bool    `json:"is_charging"`
}

// StatusResponseDTO is returned by GET /api/v1/status.
type StatusResponseDTO struct {
    OCPPConnected  bool                       `json:"ocpp_connected"`
    UptimeSeconds  float64                    `json:"uptime_seconds"`
    Connectors     []ConnectorDTO             `json:"connectors"`
    ActiveSessions []SessionDTO               `json:"active_sessions"`
    EnergyMeters   map[string]EnergyMeterDTO  `json:"energy_meters"`
}

// CreateConnectorRequest is the body for POST /api/v1/connectors.
type CreateConnectorRequest struct {
    Voltage float64 `json:"voltage"`
    Current float64 `json:"current"`
    Phase   int     `json:"phase"`
}

// UpdateConnectorRequest is the body for PUT /api/v1/connectors/{id}.
type UpdateConnectorRequest struct {
    Voltage *float64 `json:"voltage"`
    Current *float64 `json:"current"`
    Phase   *int     `json:"phase"`
}

// StartSessionRequest is the body for POST /api/v1/sessions/start.
type StartSessionRequest struct {
    ConnectorID int     `json:"connector_id"`
    MaxEnergy   float64 `json:"max_energy"` // Wh; 0 = no limit
    IDTag       *string `json:"id_tag"`
}

// StoppedSessionDTO is returned by GET /api/v1/sessions/last-stopped.
type StoppedSessionDTO struct {
    TransactionID int     `json:"transaction_id"`
    ConnectorID   int     `json:"connector_id"`
    EnergyWh      float64 `json:"energy_charged_wh"`
    MeterStop     float64 `json:"meter_stop"`
    Reason        string  `json:"reason"`
    IDTag         *string `json:"id_tag"`
}

// PatchConfigRequest is the body for PATCH /api/v1/config.
type PatchConfigRequest struct {
    ConnectionURL       *string  `json:"connection_url"`
    OCPPID              *string  `json:"ocpp_id"`
    OCPPPassword        *string  `json:"ocpp_password"`
    ChargePointModel    *string  `json:"charge_point_model"`
    ChargePointVendor   *string  `json:"charge_point_vendor"`
    SkipTLSVerify       *bool    `json:"skip_tls_verify"`
    LogMode             *string  `json:"log_mode"`
    MultiEVSEMode       *bool    `json:"multi_evse_mode"`
    EVBatteryCapacity   *float64 `json:"ev_battery_capacity"`
    OCPPVersion         *string  `json:"ocpp_version"`
    PersistMessageQueue *bool    `json:"persist_message_queue"`
    RFIDTag             *string  `json:"rfid_tag"`
}

// PatchConfigResponse is returned by PATCH /api/v1/config.
type PatchConfigResponse struct {
    Success       bool     `json:"success"`
    Action        string   `json:"action"` // "no-op" | "bridge_restart_required" | "runtime_rebuild_required" | "rejected"
    ChangedFields []string `json:"changed_fields"`
    Message       string   `json:"message"`
}

// ReservationDTO is the JSON representation of a reservation.
type ReservationDTO struct {
    ReservationID int     `json:"reservation_id"`
    ConnectorID   int     `json:"connector_id"`
    IDTag         string  `json:"id_tag"`
    ExpiryDate    string  `json:"expiry_date"`
    ParentIDTag   *string `json:"parent_id_tag"`
}

// CreateReservationRequest is the body for POST /api/v1/reservations.
type CreateReservationRequest struct {
    ConnectorID   int     `json:"connector_id"`
    ReservationID int     `json:"reservation_id"`
    IDTag         string  `json:"id_tag"`
    ExpiryDate    string  `json:"expiry_date"` // RFC3339
    ParentIDTag   *string `json:"parent_id_tag"`
}
```

- [ ] **Step 2: Build check**

```bash
go build ./internal/api/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/api/dto.go
git commit -m "feat(api): JSON DTOs and response envelope"
```

---

## Task 3: HTTP Server and Router

**Files:**
- Create: `internal/api/server.go`
- Create: `internal/api/router.go`

- [ ] **Step 1: Implement server.go**

Create `internal/api/server.go`:

```go
package api

import (
    "context"
    "log/slog"
    "net/http"
    "time"
)

// Server wraps net/http.Server with structured logging and lifecycle methods.
type Server struct {
    httpServer *http.Server
}

// NewServer creates the HTTP server bound to addr (e.g. ":8080").
func NewServer(addr string, handler http.Handler) *Server {
    return &Server{
        httpServer: &http.Server{
            Addr:         addr,
            Handler:      handler,
            ReadTimeout:  15 * time.Second,
            WriteTimeout: 15 * time.Second,
            IdleTimeout:  60 * time.Second,
        },
    }
}

// Start begins listening. Blocks until the server stops.
func (s *Server) Start() error {
    slog.Info("HTTP server listening", "addr", s.httpServer.Addr)
    return s.httpServer.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
    return s.httpServer.Shutdown(ctx)
}
```

- [ ] **Step 2: Implement router.go**

Create `internal/api/router.go`:

```go
package api

import (
    "encoding/json"
    "net/http"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    engine "github.com/chargeghost/engine/internal/engine"
    "github.com/chargeghost/engine/internal/config"
    "github.com/chargeghost/engine/internal/api/handlers"
)

// AppContext holds shared dependencies injected into all handlers.
type AppContext struct {
    Engine    *engine.Engine
    Config    *config.Config
    StartTime time.Time
}

// NewRouter builds and returns the chi router with all routes registered.
func NewRouter(app *AppContext) http.Handler {
    r := chi.NewRouter()

    // Middleware
    r.Use(middleware.RealIP)
    r.Use(middleware.Logger)
    r.Use(middleware.Recoverer)
    r.Use(corsMiddleware)

    r.Route("/api/v1", func(r chi.Router) {
        r.Get("/status", handlers.GetStatus(app.Engine, app.StartTime))

        r.Route("/connectors", func(r chi.Router) {
            r.Get("/", handlers.ListConnectors(app.Engine))
            r.Post("/", handlers.CreateConnector(app.Engine))
            r.Route("/{id}", func(r chi.Router) {
                r.Get("/", handlers.GetConnector(app.Engine))
                r.Put("/", handlers.UpdateConnector(app.Engine))
                r.Delete("/", handlers.DeleteConnector(app.Engine))
                r.Post("/plug_in", handlers.PlugIn(app.Engine))
                r.Post("/unplug", handlers.Unplug(app.Engine))
                r.Post("/suspend_ev", handlers.SuspendEV(app.Engine))
                r.Post("/resume_charging", handlers.ResumeCharging(app.Engine))
                r.Post("/start-charging", handlers.StartCharging(app.Engine))
                r.Post("/stop-charging", handlers.StopCharging(app.Engine))
                r.Put("/rfid", handlers.SetRFID(app.Engine))
                r.Delete("/rfid", handlers.ClearRFID(app.Engine))
            })
        })

        r.Route("/sessions", func(r chi.Router) {
            r.Get("/", handlers.ListSessions(app.Engine))
            r.Post("/start", handlers.StartSession(app.Engine))
            r.Post("/stop", handlers.StopAllSessions(app.Engine))
            r.Get("/last-stopped", handlers.GetLastStoppedSession(app.Engine))
            r.Get("/active", handlers.GetActiveSession(app.Engine))
            r.Get("/info", handlers.GetSessionInfo(app.Engine))
            r.Get("/{connector_id}", handlers.GetSessionByConnector(app.Engine))
        })

        r.Route("/config", func(r chi.Router) {
            r.Get("/", handlers.GetConfig(app.Config))
            r.Patch("/", handlers.PatchConfig(app.Config, app.Engine))
            r.Post("/save", handlers.SaveConfig(app.Config))
        })

        r.Route("/reservations", func(r chi.Router) {
            r.Get("/", handlers.ListReservations(app.Engine))
            r.Post("/", handlers.CreateReservation(app.Engine))
            r.Delete("/{reservation_id}", handlers.CancelReservation(app.Engine))
        })
    })

    return r
}

func corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
        if r.Method == http.MethodOptions {
            w.WriteHeader(http.StatusNoContent)
            return
        }
        next.ServeHTTP(w, r)
    })
}

// writeJSON is a helper used by all handlers.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 3: Build check**

```bash
go build ./internal/api/...
```

Expected: compile error — `handlers` subpackage not created yet. Proceed to Task 4.

---

## Task 4: Status Handler

**Files:**
- Create: `internal/api/handlers/status.go`
- Create: `internal/api/handlers_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/api/handlers_test.go`:

```go
package api_test

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/chargeghost/engine/internal/api"
    "github.com/chargeghost/engine/internal/config"
    engine "github.com/chargeghost/engine/internal/engine"
)

func newTestApp() *api.AppContext {
    e := engine.NewEngine(false, 55000.0)
    e.AddConnector(230.0, 32.0, 1)
    return &api.AppContext{
        Engine:    e,
        Config:    config.DefaultConfig(),
        StartTime: time.Now(),
    }
}

func TestGetStatus(t *testing.T) {
    app := newTestApp()
    r := api.NewRouter(app)

    req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
    w := httptest.NewRecorder()
    r.ServeHTTP(w, req)

    assert.Equal(t, http.StatusOK, w.Code)

    var body map[string]interface{}
    require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
    assert.Contains(t, body, "connectors")
    assert.Contains(t, body, "active_sessions")
    assert.Contains(t, body, "energy_meters")
}

func TestListConnectors(t *testing.T) {
    app := newTestApp()
    r := api.NewRouter(app)

    req := httptest.NewRequest(http.MethodGet, "/api/v1/connectors", nil)
    w := httptest.NewRecorder()
    r.ServeHTTP(w, req)

    assert.Equal(t, http.StatusOK, w.Code)
    var body []map[string]interface{}
    require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
    assert.Len(t, body, 1)
}

func TestCreateConnector(t *testing.T) {
    app := newTestApp()
    r := api.NewRouter(app)

    body := `{"voltage":400,"current":16,"phase":3}`
    req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()
    r.ServeHTTP(w, req)

    assert.Equal(t, http.StatusCreated, w.Code)
    var resp map[string]interface{}
    require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
    assert.Equal(t, true, resp["success"])
}

func TestPlugInAndStartSession(t *testing.T) {
    app := newTestApp()
    r := api.NewRouter(app)

    plug := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/plug_in", nil)
    w := httptest.NewRecorder()
    r.ServeHTTP(w, plug)
    assert.Equal(t, http.StatusOK, w.Code)

    start := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/1/start-charging", nil)
    w2 := httptest.NewRecorder()
    r.ServeHTTP(w2, start)
    assert.Equal(t, http.StatusOK, w2.Code)

    sessions := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
    w3 := httptest.NewRecorder()
    r.ServeHTTP(w3, sessions)
    var list []map[string]interface{}
    require.NoError(t, json.NewDecoder(w3.Body).Decode(&list))
    assert.Len(t, list, 1)
}

func TestGetConfig(t *testing.T) {
    app := newTestApp()
    r := api.NewRouter(app)

    req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
    w := httptest.NewRecorder()
    r.ServeHTTP(w, req)

    assert.Equal(t, http.StatusOK, w.Code)
    var cfg map[string]interface{}
    require.NoError(t, json.NewDecoder(w.Body).Decode(&cfg))
    assert.Equal(t, "CP_1", cfg["ocpp_id"])
}
```

Add `"strings"` to the import block of the test file.

- [ ] **Step 2: Implement status.go**

Create `internal/api/handlers/status.go`:

```go
package handlers

import (
    "fmt"
    "net/http"
    "time"

    engine "github.com/chargeghost/engine/internal/engine"
    "github.com/chargeghost/engine/internal/api"
)

// GetStatus handles GET /api/v1/status.
func GetStatus(e *engine.Engine, startTime time.Time) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        connectorIDs := e.GetConnectorIDs()
        connectors := make([]api.ConnectorDTO, 0, len(connectorIDs))
        for _, id := range connectorIDs {
            c := e.GetConnector(id)
            if c == nil {
                continue
            }
            connectors = append(connectors, api.ConnectorDTO{
                ID:          c.ID,
                Status:      string(c.Status),
                Voltage:     c.Voltage,
                Current:     c.Current,
                Phase:       c.Phase,
                IsPluggedIn: c.IsPluggedIn,
                IDTag:       c.IDTag,
            })
        }

        sessions := e.GetSessionInfo()
        sessionDTOs := make([]api.SessionDTO, 0, len(sessions))
        for _, s := range sessions {
            sessionDTOs = append(sessionDTOs, api.SessionDTO{
                TransactionID: s.TransactionID,
                ConnectorID:   s.ConnectorID,
                EnergyWh:      s.EnergyCharged,
                StateOfCharge: s.StateOfCharge,
                StartTime:     s.StartTime,
                IDTag:         s.IDTag,
                IsCharging:    s.IsCharging,
            })
        }

        meters := make(map[string]api.EnergyMeterDTO)
        for _, id := range connectorIDs {
            m := e.GetEnergyMeter(id)
            if m != nil {
                meters[fmt.Sprintf("%d", id)] = api.EnergyMeterDTO{
                    ReadingWh:  m.Value,
                    IsCharging: m.IsCharging,
                }
            }
        }

        writeJSON(w, http.StatusOK, api.StatusResponseDTO{
            OCPPConnected:  false, // wired to OCPP adapter in Plan 5a
            UptimeSeconds:  time.Since(startTime).Seconds(),
            Connectors:     connectors,
            ActiveSessions: sessionDTOs,
            EnergyMeters:   meters,
        })
    }
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
    // Delegate to the api package helper — defined in router.go
    // We duplicate it here to avoid circular imports.
    import "encoding/json"
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(v)
}
```

Note: The `import` inside a function is invalid Go. Move the `writeJSON` helper to a shared `handlers/helpers.go`:

Create `internal/api/handlers/helpers.go`:

```go
package handlers

import (
    "encoding/json"
    "net/http"
    "strconv"

    "github.com/go-chi/chi/v5"
)

// writeJSON serializes v to JSON and writes it with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(v)
}

// connectorIDFromURL parses the {id} URL parameter as an integer.
// Returns (0, false) on parse failure.
func connectorIDFromURL(r *http.Request) (int, bool) {
    s := chi.URLParam(r, "id")
    id, err := strconv.Atoi(s)
    if err != nil || id <= 0 {
        return 0, false
    }
    return id, true
}
```

Then rewrite `status.go` without the embedded import:

```go
package handlers

import (
    "fmt"
    "net/http"
    "time"

    engine "github.com/chargeghost/engine/internal/engine"
    "github.com/chargeghost/engine/internal/api"
)

// GetStatus handles GET /api/v1/status.
func GetStatus(e *engine.Engine, startTime time.Time) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        connectorIDs := e.GetConnectorIDs()
        connectors := make([]api.ConnectorDTO, 0, len(connectorIDs))
        for _, id := range connectorIDs {
            c := e.GetConnector(id)
            if c == nil {
                continue
            }
            connectors = append(connectors, api.ConnectorDTO{
                ID:          c.ID,
                Status:      string(c.Status),
                Voltage:     c.Voltage,
                Current:     c.Current,
                Phase:       c.Phase,
                IsPluggedIn: c.IsPluggedIn,
                IDTag:       c.IDTag,
            })
        }

        sessions := e.GetSessionInfo()
        sessionDTOs := make([]api.SessionDTO, 0, len(sessions))
        for _, s := range sessions {
            sessionDTOs = append(sessionDTOs, api.SessionDTO{
                TransactionID: s.TransactionID,
                ConnectorID:   s.ConnectorID,
                EnergyWh:      s.EnergyCharged,
                StateOfCharge: s.StateOfCharge,
                StartTime:     s.StartTime,
                IDTag:         s.IDTag,
                IsCharging:    s.IsCharging,
            })
        }

        meters := make(map[string]api.EnergyMeterDTO)
        for _, id := range connectorIDs {
            m := e.GetEnergyMeter(id)
            if m != nil {
                meters[fmt.Sprintf("%d", id)] = api.EnergyMeterDTO{
                    ReadingWh:  m.Value,
                    IsCharging: m.IsCharging,
                }
            }
        }

        writeJSON(w, http.StatusOK, api.StatusResponseDTO{
            OCPPConnected:  false,
            UptimeSeconds:  time.Since(startTime).Seconds(),
            Connectors:     connectors,
            ActiveSessions: sessionDTOs,
            EnergyMeters:   meters,
        })
    }
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/api/handlers/helpers.go internal/api/handlers/status.go
git commit -m "feat(api): GET /api/v1/status handler"
```

---

## Task 5: Connector Handlers

**Files:**
- Create: `internal/api/handlers/connectors.go`

- [ ] **Step 1: Implement connectors.go**

Create `internal/api/handlers/connectors.go`:

```go
package handlers

import (
    "encoding/json"
    "net/http"
    "strconv"

    "github.com/go-chi/chi/v5"
    engine "github.com/chargeghost/engine/internal/engine"
    "github.com/chargeghost/engine/internal/api"
)

func connectorToDTO(c *engine.Connector) api.ConnectorDTO {
    return api.ConnectorDTO{
        ID:          c.ID,
        Status:      string(c.Status),
        Voltage:     c.Voltage,
        Current:     c.Current,
        Phase:       c.Phase,
        IsPluggedIn: c.IsPluggedIn,
        IDTag:       c.IDTag,
    }
}

// ListConnectors handles GET /api/v1/connectors.
func ListConnectors(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ids := e.GetConnectorIDs()
        result := make([]api.ConnectorDTO, 0, len(ids))
        for _, id := range ids {
            if c := e.GetConnector(id); c != nil {
                result = append(result, connectorToDTO(c))
            }
        }
        writeJSON(w, http.StatusOK, result)
    }
}

// GetConnector handles GET /api/v1/connectors/{id}.
func GetConnector(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, ok := connectorIDFromURL(r)
        if !ok {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid connector id"})
            return
        }
        c := e.GetConnector(id)
        if c == nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "connector not found"})
            return
        }
        writeJSON(w, http.StatusOK, connectorToDTO(c))
    }
}

// CreateConnector handles POST /api/v1/connectors.
func CreateConnector(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req api.CreateConnectorRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid request body"})
            return
        }
        // Validate via engine (will panic on invalid — catch with recover or validate here)
        if req.Voltage < 120 || req.Voltage > 1000 {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "voltage out of range (120–1000V)"})
            return
        }
        if req.Current < 6 || req.Current > 150 {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "current out of range (6–150A)"})
            return
        }
        if req.Phase != 1 && req.Phase != 3 {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "phase must be 1 or 3"})
            return
        }
        c := e.AddConnector(req.Voltage, req.Current, req.Phase)
        writeJSON(w, http.StatusCreated, api.Response{
            Success: true,
            Message: "Created connector " + strconv.Itoa(c.ID),
            Details: map[string]interface{}{"connector": connectorToDTO(c)},
        })
    }
}

// UpdateConnector handles PUT /api/v1/connectors/{id}.
func UpdateConnector(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, ok := connectorIDFromURL(r)
        if !ok {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid connector id"})
            return
        }
        var req api.UpdateConnectorRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid request body"})
            return
        }
        if err := e.UpdateConnector(id, req.Voltage, req.Current, req.Phase); err != nil {
            status := http.StatusBadRequest
            writeJSON(w, status, api.Response{Success: false, Message: err.Error()})
            return
        }
        c := e.GetConnector(id)
        writeJSON(w, http.StatusOK, api.Response{
            Success: true,
            Message: "Connector updated",
            Details: map[string]interface{}{"connector": connectorToDTO(c)},
        })
    }
}

// DeleteConnector handles DELETE /api/v1/connectors/{id}.
func DeleteConnector(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, ok := connectorIDFromURL(r)
        if !ok {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid connector id"})
            return
        }
        if err := e.RemoveConnector(id); err != nil {
            writeJSON(w, http.StatusConflict, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Connector removed"})
    }
}

// PlugIn handles POST /api/v1/connectors/{id}/plug_in.
func PlugIn(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, ok := connectorIDFromURL(r)
        if !ok {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid connector id"})
            return
        }
        e.PlugIn(id)
        c := e.GetConnector(id)
        if c == nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "connector not found"})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "EV plugged in", Details: map[string]interface{}{"connector": connectorToDTO(c)}})
    }
}

// Unplug handles POST /api/v1/connectors/{id}/unplug.
func Unplug(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, ok := connectorIDFromURL(r)
        if !ok {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid connector id"})
            return
        }
        e.Unplug(id)
        c := e.GetConnector(id)
        if c == nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "connector not found"})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "EV unplugged", Details: map[string]interface{}{"connector": connectorToDTO(c)}})
    }
}

// SuspendEV handles POST /api/v1/connectors/{id}/suspend_ev.
func SuspendEV(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, ok := connectorIDFromURL(r)
        if !ok {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid connector id"})
            return
        }
        if err := e.SuspendEV(id); err != nil {
            writeJSON(w, http.StatusConflict, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "EV suspended"})
    }
}

// ResumeCharging handles POST /api/v1/connectors/{id}/resume_charging.
func ResumeCharging(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, ok := connectorIDFromURL(r)
        if !ok {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid connector id"})
            return
        }
        if err := e.ResumeCharging(id); err != nil {
            writeJSON(w, http.StatusConflict, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Charging resumed"})
    }
}

// StartCharging handles POST /api/v1/connectors/{id}/start-charging.
// Auto-generates a local transaction ID (negative to distinguish from CSMS-assigned).
func StartCharging(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, ok := connectorIDFromURL(r)
        if !ok {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid connector id"})
            return
        }
        if err := e.StartSession(id, -1, 0.0, nil, 0); err != nil {
            writeJSON(w, http.StatusConflict, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Charging started"})
    }
}

// StopCharging handles POST /api/v1/connectors/{id}/stop-charging.
func StopCharging(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, ok := connectorIDFromURL(r)
        if !ok {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid connector id"})
            return
        }
        info := e.StopSession(&id, "Local")
        if info == nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "no active session"})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Charging stopped"})
    }
}

// SetRFID handles PUT /api/v1/connectors/{id}/rfid?rfid_tag=ABC123.
func SetRFID(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, ok := connectorIDFromURL(r)
        if !ok {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid connector id"})
            return
        }
        tag := r.URL.Query().Get("rfid_tag")
        if tag == "" {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "rfid_tag query param required"})
            return
        }
        c := e.GetConnector(id)
        if c == nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "connector not found"})
            return
        }
        c.IDTag = &tag
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "RFID tag set"})
    }
}

// ClearRFID handles DELETE /api/v1/connectors/{id}/rfid.
func ClearRFID(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, ok := connectorIDFromURL(r)
        if !ok {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid connector id"})
            return
        }
        c := e.GetConnector(id)
        if c == nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "connector not found"})
            return
        }
        c.IDTag = nil
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "RFID tag cleared"})
    }
}
```

- [ ] **Step 2: Commit**

```bash
git add internal/api/handlers/connectors.go
git commit -m "feat(api): connector CRUD and action handlers"
```

---

## Task 6: Session and Config Handlers

**Files:**
- Create: `internal/api/handlers/sessions.go`
- Create: `internal/api/handlers/config.go`

- [ ] **Step 1: Implement sessions.go**

Create `internal/api/handlers/sessions.go`:

```go
package handlers

import (
    "encoding/json"
    "net/http"
    "strconv"

    "github.com/go-chi/chi/v5"
    engine "github.com/chargeghost/engine/internal/engine"
    "github.com/chargeghost/engine/internal/api"
)

// ListSessions handles GET /api/v1/sessions.
func ListSessions(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        sessions := e.GetSessionInfo()
        result := make([]api.SessionDTO, 0, len(sessions))
        for _, s := range sessions {
            result = append(result, api.SessionDTO{
                TransactionID: s.TransactionID,
                ConnectorID:   s.ConnectorID,
                EnergyWh:      s.EnergyCharged,
                StateOfCharge: s.StateOfCharge,
                StartTime:     s.StartTime,
                IDTag:         s.IDTag,
                IsCharging:    s.IsCharging,
            })
        }
        writeJSON(w, http.StatusOK, result)
    }
}

// StartSession handles POST /api/v1/sessions/start.
func StartSession(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        connectorID, _ := strconv.Atoi(r.URL.Query().Get("connector_id"))
        if connectorID == 0 {
            var req api.StartSessionRequest
            if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.ConnectorID != 0 {
                connectorID = req.ConnectorID
            }
        }
        if connectorID == 0 {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "connector_id required"})
            return
        }
        if err := e.StartSession(connectorID, -1, 0.0, nil, 0); err != nil {
            writeJSON(w, http.StatusConflict, api.Response{Success: false, Message: err.Error()})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Session started"})
    }
}

// StopAllSessions handles POST /api/v1/sessions/stop.
func StopAllSessions(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        info := e.StopSession(nil, "Local")
        if info == nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "no active session"})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Session stopped"})
    }
}

// GetLastStoppedSession handles GET /api/v1/sessions/last-stopped.
func GetLastStoppedSession(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        info := e.GetLastStoppedSession()
        if info == nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "no stopped session"})
            return
        }
        writeJSON(w, http.StatusOK, api.StoppedSessionDTO{
            TransactionID: info.TransactionID,
            ConnectorID:   info.ConnectorID,
            EnergyWh:      info.EnergyCharged,
            MeterStop:     info.MeterStop,
            Reason:        info.Reason,
            IDTag:         info.IDTag,
        })
    }
}

// GetActiveSession handles GET /api/v1/sessions/active?connector_id=1.
func GetActiveSession(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, _ := strconv.Atoi(r.URL.Query().Get("connector_id"))
        if id == 0 {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "connector_id required"})
            return
        }
        s := e.GetSession(id)
        if s == nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "no active session"})
            return
        }
        m := e.GetEnergyMeter(id)
        writeJSON(w, http.StatusOK, api.SessionDTO{
            TransactionID: s.TransactionID,
            ConnectorID:   s.ConnectorID,
            EnergyWh:      s.EnergyCharged,
            StateOfCharge: s.StateOfCharge,
            StartTime:     s.StartTime,
            IDTag:         s.IDTag,
            IsCharging:    m != nil && m.IsCharging,
        })
    }
}

// GetSessionByConnector handles GET /api/v1/sessions/{connector_id}.
func GetSessionByConnector(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, err := strconv.Atoi(chi.URLParam(r, "connector_id"))
        if err != nil || id <= 0 {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid connector_id"})
            return
        }
        s := e.GetSession(id)
        if s == nil {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "no active session"})
            return
        }
        m := e.GetEnergyMeter(id)
        writeJSON(w, http.StatusOK, api.SessionDTO{
            TransactionID: s.TransactionID,
            ConnectorID:   s.ConnectorID,
            EnergyWh:      s.EnergyCharged,
            StateOfCharge: s.StateOfCharge,
            StartTime:     s.StartTime,
            IDTag:         s.IDTag,
            IsCharging:    m != nil && m.IsCharging,
        })
    }
}

// GetSessionInfo handles GET /api/v1/sessions/info.
func GetSessionInfo(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        sessions := e.GetSessionInfo()
        writeJSON(w, http.StatusOK, sessions)
    }
}
```

- [ ] **Step 2: Implement config.go**

Create `internal/api/handlers/config.go`:

```go
package handlers

import (
    "encoding/json"
    "net/http"

    engine "github.com/chargeghost/engine/internal/engine"
    "github.com/chargeghost/engine/internal/api"
    "github.com/chargeghost/engine/internal/config"
)

// bridgeFields are config keys that require an OCPP bridge restart.
var bridgeFields = map[string]bool{
    "connection_url": true, "ocpp_id": true, "ocpp_password": true,
    "skip_tls_verify": true, "charge_point_model": true,
    "charge_point_vendor": true, "ocpp_version": true,
}

// topologyFields are config keys that require a full runtime rebuild.
var topologyFields = map[string]bool{
    "multi_evse_mode": true, "connectors": true, "ev_battery_capacity": true,
}

// GetConfig handles GET /api/v1/config.
func GetConfig(cfg *config.Config) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        writeJSON(w, http.StatusOK, cfg)
    }
}

// PatchConfig handles PATCH /api/v1/config.
func PatchConfig(cfg *config.Config, e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        sessions := e.GetSessionInfo()
        var req api.PatchConfigRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid request body"})
            return
        }

        changed := []string{}
        action := "no-op"

        applyChange := func(field string, apply func()) {
            apply()
            changed = append(changed, field)
            if topologyFields[field] {
                if len(sessions) > 0 {
                    action = "rejected"
                } else {
                    action = "runtime_rebuild_required"
                }
            } else if bridgeFields[field] && action != "runtime_rebuild_required" && action != "rejected" {
                action = "bridge_restart_required"
            }
        }

        if req.ConnectionURL != nil {
            applyChange("connection_url", func() { cfg.ConnectionURL = *req.ConnectionURL })
        }
        if req.OCPPID != nil {
            applyChange("ocpp_id", func() { cfg.OCPPID = *req.OCPPID })
        }
        if req.ChargePointModel != nil {
            applyChange("charge_point_model", func() { cfg.ChargePointModel = *req.ChargePointModel })
        }
        if req.ChargePointVendor != nil {
            applyChange("charge_point_vendor", func() { cfg.ChargePointVendor = *req.ChargePointVendor })
        }
        if req.SkipTLSVerify != nil {
            applyChange("skip_tls_verify", func() { cfg.SkipTLSVerify = *req.SkipTLSVerify })
        }
        if req.LogMode != nil {
            applyChange("log_mode", func() { cfg.LogMode = *req.LogMode })
        }
        if req.MultiEVSEMode != nil {
            applyChange("multi_evse_mode", func() { cfg.MultiEVSEMode = *req.MultiEVSEMode })
        }
        if req.EVBatteryCapacity != nil {
            applyChange("ev_battery_capacity", func() { cfg.EVBatteryCapacity = *req.EVBatteryCapacity })
        }
        if req.OCPPVersion != nil {
            applyChange("ocpp_version", func() { cfg.OCPPVersion = *req.OCPPVersion })
        }
        if req.PersistMessageQueue != nil {
            applyChange("persist_message_queue", func() { cfg.PersistMessageQueue = *req.PersistMessageQueue })
        }
        if req.RFIDTag != nil {
            applyChange("rfid_tag", func() { cfg.RFIDTag = req.RFIDTag })
        }

        if action == "rejected" {
            writeJSON(w, http.StatusConflict, api.PatchConfigResponse{
                Success:       false,
                Action:        "rejected",
                ChangedFields: changed,
                Message:       "Topology changes rejected: active sessions in progress",
            })
            return
        }

        msg := "Configuration updated in memory."
        if action == "bridge_restart_required" {
            msg += " Bridge restart required."
        } else if action == "runtime_rebuild_required" {
            msg += " Save required to rebuild runtime."
        }

        writeJSON(w, http.StatusOK, api.PatchConfigResponse{
            Success:       true,
            Action:        action,
            ChangedFields: changed,
            Message:       msg,
        })
    }
}

// SaveConfig handles POST /api/v1/config/save.
// Disk persistence is added in Plan 6; for now just returns success.
func SaveConfig(cfg *config.Config) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Plan 6 will write cfg to ~/.chargeghost/config.json here.
        writeJSON(w, http.StatusOK, api.Response{
            Success: true,
            Message: "Configuration saved (in-memory; disk persistence added in Plan 6)",
        })
    }
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/api/handlers/sessions.go internal/api/handlers/config.go
git commit -m "feat(api): session and config handlers"
```

---

## Task 7: Reservation Handlers

**Files:**
- Create: `internal/api/handlers/reservations.go`

- [ ] **Step 1: Implement reservations.go**

Create `internal/api/handlers/reservations.go`:

```go
package handlers

import (
    "encoding/json"
    "net/http"
    "strconv"
    "time"

    "github.com/go-chi/chi/v5"
    engine "github.com/chargeghost/engine/internal/engine"
    "github.com/chargeghost/engine/internal/api"
)

// ListReservations handles GET /api/v1/reservations.
func ListReservations(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ids := e.GetConnectorIDs()
        result := make([]api.ReservationDTO, 0)
        for _, cid := range ids {
            // Access reservations via engine's GetReservation method (added to Engine below)
            if res := e.GetReservation(cid); res != nil {
                result = append(result, api.ReservationDTO{
                    ReservationID: res.ReservationID,
                    ConnectorID:   res.ConnectorID,
                    IDTag:         res.IDTag,
                    ExpiryDate:    res.ExpiryDate.UTC().Format(time.RFC3339),
                    ParentIDTag:   res.ParentIDTag,
                })
            }
        }
        writeJSON(w, http.StatusOK, result)
    }
}

// CreateReservation handles POST /api/v1/reservations.
func CreateReservation(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req api.CreateReservationRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid request body"})
            return
        }
        expiry, err := time.Parse(time.RFC3339, req.ExpiryDate)
        if err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "expiry_date must be RFC3339"})
            return
        }
        result := e.ReserveConnector(req.ConnectorID, req.ReservationID, req.IDTag, expiry, req.ParentIDTag)
        if result != "accepted" {
            writeJSON(w, http.StatusConflict, api.Response{Success: false, Message: result})
            return
        }
        writeJSON(w, http.StatusCreated, api.Response{Success: true, Message: "Reservation created"})
    }
}

// CancelReservation handles DELETE /api/v1/reservations/{reservation_id}.
func CancelReservation(e *engine.Engine) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        id, err := strconv.Atoi(chi.URLParam(r, "reservation_id"))
        if err != nil {
            writeJSON(w, http.StatusBadRequest, api.Response{Success: false, Message: "invalid reservation_id"})
            return
        }
        result := e.CancelReservation(id)
        if result != "accepted" {
            writeJSON(w, http.StatusNotFound, api.Response{Success: false, Message: "reservation not found"})
            return
        }
        writeJSON(w, http.StatusOK, api.Response{Success: true, Message: "Reservation cancelled"})
    }
}
```

Note: `ListReservations` calls `e.GetReservation(cid)` which looks up a reservation by connector ID. Add this method to `Engine` in `internal/engine/engine.go`:

```go
// GetReservation returns the reservation for a connector, or nil.
func (e *Engine) GetReservation(connectorID int) *Reservation {
    e.mu.RLock()
    defer e.mu.RUnlock()
    for _, res := range e.reservations {
        if res.ConnectorID == connectorID {
            return res
        }
    }
    return nil
}
```

- [ ] **Step 2: Build and test**

```bash
go build ./...
go test ./... -count=1 -timeout 30s
```

Expected: all pass.

- [ ] **Step 3: Commit**

```bash
git add internal/api/handlers/reservations.go internal/engine/engine.go
git commit -m "feat(api): reservation handlers + Engine.GetReservation"
```

---

## Task 8: Wire Server into main.go and Final Verification

**Files:**
- Modify: `cmd/chargeghost/main.go`

- [ ] **Step 1: Update main.go to start the HTTP server**

Replace the contents of `cmd/chargeghost/main.go`:

```go
package main

import (
    "context"
    "errors"
    "log/slog"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/chargeghost/engine/internal/api"
    "github.com/chargeghost/engine/internal/config"
    engine "github.com/chargeghost/engine/internal/engine"
    rt "github.com/chargeghost/engine/internal/runtime"
)

func main() {
    slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelInfo,
    })))

    cfg := config.DefaultConfig()
    e := engine.NewEngine(cfg.MultiEVSEMode, cfg.EVBatteryCapacity*1000) // kWh → Wh
    for _, cc := range cfg.Connectors {
        e.AddConnector(cc.Voltage, cc.Current, cc.Phase)
    }

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    runtime := rt.NewRuntime(e)
    go runtime.Run(ctx)

    app := &api.AppContext{
        Engine:    e,
        Config:    cfg,
        StartTime: time.Now(),
    }
    router := api.NewRouter(app)
    srv := api.NewServer(":8080", router)

    go func() {
        if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
            slog.Error("HTTP server error", "err", err)
        }
    }()

    slog.Info("ChargeGhost engine started", "addr", ":8080")

    sig := make(chan os.Signal, 1)
    signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
    <-sig

    slog.Info("shutting down")
    cancel()
    shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer shutCancel()
    _ = srv.Shutdown(shutCtx)
}
```

- [ ] **Step 2: Build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Smoke test**

```bash
./chargeghost &
sleep 1
curl -s http://localhost:8080/api/v1/status | jq .
curl -s http://localhost:8080/api/v1/connectors | jq .
curl -s -X POST http://localhost:8080/api/v1/connectors/1/plug_in | jq .
curl -s -X POST http://localhost:8080/api/v1/connectors/1/start-charging | jq .
curl -s http://localhost:8080/api/v1/sessions | jq .
curl -s -X POST http://localhost:8080/api/v1/connectors/1/stop-charging | jq .
kill %1
```

Expected: all endpoints return valid JSON with `"success": true`.

- [ ] **Step 4: Run tests**

```bash
go test ./... -count=1 -timeout 30s
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/chargeghost/main.go
git commit -m "feat(cmd): wire HTTP server into main"
```
