package api

import (
	"context"
	"time"

	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

// FleetManager is the process-local authority for fleet mutations. The concrete
// implementation lives in the command package; internal/api depends only on
// this interface so that handlers can remain testable.
type FleetManager interface {
	// DefaultStationID returns the stable ID of the default station.
	DefaultStationID() string
	// GetAppContext returns the API context for a station by stable ID.
	// Returns false when the station is unknown or has no running runtime.
	// The returned *AppContext is stable for the lifetime of the underlying
	// runtime, so callers may use pointer identity to detect that a station
	// has been replaced (e.g. by a restart).
	GetAppContext(id string) (*AppContext, bool)
	// AllStationIDs returns all configured station IDs, including disabled ones.
	AllStationIDs() []string
	// Config returns the current global config clone.
	Config() *config.Config
	// Hub returns the shared WebSocket hub.
	Hub() *ws.Hub

	// Station administration.
	CreateStation(ctx context.Context, req CreateStationRequest) (StationSnapshot, string, error)
	UpdateStation(ctx context.Context, id string, req PatchStationConfigRequest) (StationUpdateResult, error)
	DeleteStation(ctx context.Context, id string, opts DeleteStationOptions) error
	StartStation(ctx context.Context, id string) (string, error)
	StopStation(ctx context.Context, id string) (string, error)
	RestartStation(ctx context.Context, id string) (string, error)
	EnableStation(ctx context.Context, id string) (string, error)
	DisableStation(ctx context.Context, id string) (string, error)
	Reload(ctx context.Context) error

	// Snapshots.
	Snapshot(id string) (StationSnapshot, bool)
	AllSnapshots() []StationSnapshot

	// Operations.
	Operations() []Operation
	Operation(id string) (Operation, bool)

	// Queue and persistence.
	QueueStatus(id string) (QueueStatus, error)
	QueueDrain(id string) (string, error)
	QueueClear(id string) error
	QueueDeadLetter(id string) ([]DeadLetterEntry, error)
	QueueDeadLetterClear(id string) error
	PersistStation(id string) error

	// Credentials.
	SetOCPPPassword(id string, password string) error
	ClearOCPPPassword(id string) error
	TestCredentials(id string) error

	// Save persists the global config to disk.
	Save() error
}

// StationSnapshot is a runtime view of a single station.
type StationSnapshot struct {
	StationID          string  `json:"station_id"`
	OCPPID             string  `json:"ocpp_id"`
	Enabled            bool    `json:"enabled"`
	LifecycleState     string  `json:"lifecycle_state"`
	OCPPVersion        string  `json:"ocpp_version"`
	ConnectionURL      string  `json:"connection_url"`
	Connected          bool    `json:"connected"`
	ConnectorCount     int     `json:"connector_count"`
	ActiveSessionCount int     `json:"active_session_count"`
	QueueDepth         int     `json:"queue_depth"`
	LastError          string  `json:"last_error"`
	RestartRequired    bool    `json:"restart_required"`
	UptimeSeconds      float64 `json:"uptime_seconds"`
}

// CreateStationRequest is the body for POST /api/v1/stations.
type CreateStationRequest struct {
	ID            string                   `json:"id"`
	OCPPID        string                   `json:"ocpp_id"`
	ConnectionURL string                   `json:"connection_url"`
	OCPPVersion   string                   `json:"ocpp_version"`
	Enabled       bool                     `json:"enabled"`
	Connectors    []config.ConnectorConfig `json:"connectors"`
	Start         bool                     `json:"start"`
	Save          bool                     `json:"save"`
	OCPPPassword  string                   `json:"ocpp_password"`
}

// PatchStationConfigRequest is the body for PATCH /api/v1/stations/{id}/config.
type PatchStationConfigRequest struct {
	PatchConfigRequest
	Enabled *bool `json:"enabled,omitempty"`
	Save    bool  `json:"save"`
	Restart bool  `json:"restart"`
}

// StationUpdateResult is returned by FleetManager.UpdateStation.
type StationUpdateResult struct {
	Snapshot        StationSnapshot
	ChangedFields   []string
	RestartRequired bool
	// Restarted reports whether the caller's Restart:true request was
	// actually honored (it is skipped if the resulting config is disabled —
	// there is nothing to restart into).
	Restarted   bool
	OperationID string
}

// DeleteStationOptions controls DELETE /api/v1/stations/{id}.
type DeleteStationOptions struct {
	Force         bool
	DeleteState   bool
	ClearPassword bool
	NewDefaultID  string
	AllowEmpty    bool
}

// StopStationOptions controls stop/restart behavior.
type StopStationOptions struct {
	Force        bool
	StopSessions bool
	Reason       string
}

// Operation represents an async fleet/station operation.
type Operation struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	StationID string     `json:"station_id,omitempty"`
	State     string     `json:"state"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// QueueStatus is the runtime state of a station's OCPP message queue.
type QueueStatus struct {
	Depth   int `json:"depth"`
	Dropped int `json:"dropped"`
	Cap     int `json:"cap"`
}

// DeadLetterEntry is a single record from the dead-letter queue.
type DeadLetterEntry struct {
	MovedAt time.Time           `json:"moved_at"`
	Reason  string              `json:"reason"`
	Message queue.QueuedMessage `json:"message"`
}

// FleetStatusResponse is returned by GET /api/v1/fleet/status.
type FleetStatusResponse struct {
	DefaultStationID string            `json:"default_station_id"`
	Stations         []StationSnapshot `json:"stations"`
}

// FleetConfigResponse is returned by GET /api/v1/fleet/config.
type FleetConfigResponse struct {
	Config *config.Config `json:"config"`
}

// OperationResponse is returned by async lifecycle endpoints.
type OperationResponse struct {
	Success     bool            `json:"success"`
	OperationID string          `json:"operation_id,omitempty"`
	Message     string          `json:"message"`
	Scope       string          `json:"scope,omitempty"`
	Snapshot    StationSnapshot `json:"snapshot,omitempty"`
}

// PatchStationResponse is returned by PATCH /api/v1/stations/{id}/config.
type PatchStationResponse struct {
	Success         bool     `json:"success"`
	Action          string   `json:"action"`
	ChangedFields   []string `json:"changed_fields"`
	RestartRequired bool     `json:"restart_required"`
	OperationID     string   `json:"operation_id,omitempty"`
	Message         string   `json:"message,omitempty"`
	Warning         string   `json:"warning,omitempty"`
}
