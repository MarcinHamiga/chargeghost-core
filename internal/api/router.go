package api

import (
	"net/http"
	"time"

	"github.com/chargeghost/engine/internal/api/handlers"
	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
	"github.com/chargeghost/engine/internal/timeline"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// AppContext holds shared dependencies injected into all handlers.
type AppContext struct {
	Engine            *engine.Engine
	Config            *config.Config
	GlobalConfig      *config.Config
	AdmitLocalSession func(idTag *string) error
	StartTime         time.Time
	Timeline          *timeline.Store
	LocalAuth         ocpp.LocalAuthManager
	Firmware          ocpp.FirmwareManager
	Diagnostics       ocpp.DiagnosticsManager
	Hub               *ws.Hub
	ProfileManager    ocpp.ChargingProfileManagerAPI
	ConfigKeys        ocpp.ConfigKeyAPI
	Queue             queue.MessageQueue
	DeadLetterPath    string
	OCPP              handlers.OCPPSendAPI
	// OCPPBridge is the full version-agnostic bridge (OCPP 1.6J or 2.0.1).
	// It exposes the link-health snapshot returned by GET /api/v1/ocpp/status.
	OCPPBridge ocpp.OCPPBridge
	// StationID identifies the station this context belongs to. Empty for
	// backwards-compatible single-station contexts.
	StationID string
	// MultiStation is true when the process is running more than one station.
	// Used to disable operations that do not make sense for station-scoped routes.
	MultiStation bool
}

// StationRegistry maps station IDs to their per-station API contexts.
type StationRegistry struct {
	DefaultID string
	Stations  map[string]*AppContext
}

// NewRouter builds and returns the chi router with all routes registered.
// It is a backwards-compatible wrapper around NewMultiRouter that uses the
// supplied AppContext as the default (and only) station.
//
// Test/scaffolding only: production (cmd/chargeghost) always uses
// NewFleetRouter, which resolves station routing dynamically against live
// FleetManager state instead of a fixed AppContext captured at construction
// time. NewRouter/NewMultiRouter remain for handler- and router-level tests
// that want to exercise routes against a fixed, hand-built AppContext
// without going through a full FleetManager.
func NewRouter(app *AppContext) http.Handler {
	registry := &StationRegistry{DefaultID: app.StationID, Stations: map[string]*AppContext{app.StationID: app}}
	if registry.DefaultID == "" {
		registry.DefaultID = "default"
		if _, ok := registry.Stations[registry.DefaultID]; !ok {
			registry.Stations[registry.DefaultID] = app
		}
	}
	return NewMultiRouter(registry)
}

// NewFleetRouter builds a router that includes all fleet administration routes
// in addition to the station-scoped and default-station routes. Unlike the
// legacy NewMultiRouter, station routing is resolved dynamically on every
// request against the fleet's live state (via fleet.GetAppContext /
// fleet.DefaultStationID) rather than a one-time registry snapshot taken at
// router-construction time — a station created, restarted, or made default
// after the router was built is reachable immediately, and a restarted
// station's routes always target its current runtime, never a stale one.
func NewFleetRouter(fleet FleetManager) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Mount("/", newDefaultStationDispatcher(fleet))

		// Admin-only routes: station list/creation, per-station admin routes
		// (mounted dynamically below), and fleet-wide routes.
		r.Get("/stations", requireAdmin(fleet, ListStations(fleet)))
		r.Post("/stations", requireAdmin(fleet, CreateStation(fleet)))
		r.Route("/fleet", func(r chi.Router) {
			r.Get("/status", requireAdmin(fleet, GetFleetStatus(fleet)))
			r.Get("/config", requireAdmin(fleet, GetFleetConfig(fleet)))
			r.Post("/config/save", requireAdmin(fleet, SaveFleetConfig(fleet)))
			r.Get("/operations", requireAdmin(fleet, ListOperations(fleet)))
			r.Post("/reload", requireAdmin(fleet, ReloadFleet(fleet)))
			r.Get("/operations/{operation_id}", requireAdmin(fleet, GetOperation(fleet)))
		})
		r.Mount("/stations/{station_id}", newStationDispatcher(fleet))
	})

	cfg := fleet.Config()
	allowedOrigins := []string(nil)
	adminAuthEnabled := false
	if cfg != nil {
		allowedOrigins = cfg.AllowedOrigins
		adminAuthEnabled = cfg.AdminAuthEnabled
	}
	upgrader := ws.NewUpgrader(allowedOrigins)
	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		if adminAuthEnabled {
			if !ws.AuthorizeAdmin(r, config.GetAdminToken()) {
				writeJSON(w, http.StatusUnauthorized, Response{Success: false, Message: "unauthorized"})
				return
			}
		}
		stationID, scope := ws.ScopeFromRequest(r, fleet.DefaultStationID())
		var snapshot ws.Message
		switch scope {
		case ws.ScopeAll:
			snapshot = ws.Message{Type: "state_snapshot", Data: map[string]interface{}{"scope": "all"}}
		default:
			if _, ok := fleet.Snapshot(stationID); !ok {
				http.Error(w, "station not found", http.StatusNotFound)
				return
			}
			if app, ok := fleet.GetAppContext(stationID); ok {
				ocppConnected := app.OCPP != nil && app.OCPP.IsConnected()
				snapshot = ws.BuildStationStatusSnapshot(app.StationID, app.Engine, ocppConnected, time.Since(app.StartTime).Seconds())
			} else {
				snapshot = ws.Message{Type: "state_snapshot", StationID: stationID, Data: map[string]interface{}{"lifecycle_state": "not_running"}}
			}
		}
		fleet.Hub().ServeWSWithUpgrader(w, r, upgrader, snapshot, scope, stationID)
	})

	return r
}

func mountFleetStationRoutesAuth(r chi.Router, fleet FleetManager, stationID string) {
	r.Get("/status", requireAdmin(fleet, GetStationStatus(fleet)))
	r.Patch("/config", requireAdmin(fleet, PatchStationConfig(fleet)))
	r.Delete("/", requireAdmin(fleet, DeleteStation(fleet)))
	r.Post("/start", requireAdmin(fleet, StartStation(fleet)))
	r.Post("/stop", requireAdmin(fleet, StopStation(fleet)))
	r.Post("/restart", requireAdmin(fleet, RestartStation(fleet)))
	r.Post("/enable", requireAdmin(fleet, EnableStation(fleet)))
	r.Post("/disable", requireAdmin(fleet, DisableStation(fleet)))
	r.Post("/reload", requireAdmin(fleet, ReloadStation(fleet)))
	r.Post("/persist", requireAdmin(fleet, PersistStation(fleet)))

	// Direct leaf pattern (not r.Route("/ocpp", ...)) so this coexists with
	// mountStationRoutes's own /ocpp/* registrations when both are mounted
	// on the same combined subrouter; see the comment there.
	r.Post("/ocpp/reconnect", requireAdmin(fleet, ReconnectStation(fleet)))

	r.Route("/credentials", func(r chi.Router) {
		r.Put("/ocpp-password", requireAdmin(fleet, SetOCPPPassword(fleet)))
		r.Delete("/ocpp-password", requireAdmin(fleet, ClearOCPPPassword(fleet)))
		r.Post("/test", requireAdmin(fleet, TestCredentials(fleet)))
	})

	r.Route("/queue", func(r chi.Router) {
		r.Get("/status", requireAdmin(fleet, GetQueueStatus(fleet)))
		r.Post("/drain", requireAdmin(fleet, DrainQueue(fleet)))
		r.Post("/clear", requireAdmin(fleet, ClearQueue(fleet)))
		r.Get("/dead-letter", requireAdmin(fleet, GetDeadLetter(fleet)))
		r.Delete("/dead-letter", requireAdmin(fleet, ClearDeadLetter(fleet)))
	})
}

// requireAdmin wraps a handler so that it requires a valid admin bearer token
// when admin auth is enabled in the global config.
func requireAdmin(fleet FleetManager, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := fleet.Config()
		if cfg == nil || !cfg.AdminAuthEnabled {
			next(w, r)
			return
		}
		expected := config.GetAdminToken()
		if expected == "" {
			writeJSON(w, http.StatusForbidden, Response{Success: false, Message: "admin auth enabled but no admin token configured"})
			return
		}
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) < len(prefix) || auth[:len(prefix)] != prefix {
			writeJSON(w, http.StatusUnauthorized, Response{Success: false, Message: "missing or invalid authorization header"})
			return
		}
		if auth[len(prefix):] != expected {
			writeJSON(w, http.StatusUnauthorized, Response{Success: false, Message: "invalid admin token"})
			return
		}
		next(w, r)
	}
}

// NewMultiRouter builds a router that serves the default station at /api/v1/* and
// any configured station at /api/v1/stations/{station_id}/*.
//
// Test/scaffolding only — see NewRouter's doc comment. Station routing here
// is fixed at construction time from the supplied StationRegistry, which is
// exactly the bug NewFleetRouter's dynamic dispatch (router_station.go)
// exists to avoid in production: a station created, restarted, or made
// default after this router was built would be unreachable or bound to a
// stale AppContext.
func NewMultiRouter(registry *StationRegistry) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/api/v1", func(r chi.Router) {
		defaultApp := registry.Stations[registry.DefaultID]
		mountStationRoutes(r, defaultApp, false, nil)

		r.Get("/stations", listStations(registry))
		for id, app := range registry.Stations {
			id, app := id, app
			sr := chi.NewRouter()
			mountStationRoutes(sr, app, true, nil)
			r.Mount("/stations/"+id, sr)
		}
	})

	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		stationID, scope := ws.ScopeFromRequest(r, registry.DefaultID)
		var app *AppContext
		if scope != ws.ScopeAll {
			app = registry.Stations[stationID]
			if app == nil {
				http.Error(w, "station not found", http.StatusNotFound)
				return
			}
		}
		var snapshot ws.Message
		switch scope {
		case ws.ScopeAll:
			snapshot = ws.Message{Type: "state_snapshot", Data: map[string]interface{}{"scope": "all"}}
		default:
			ocppConnected := app.OCPP != nil && app.OCPP.IsConnected()
			snapshot = ws.BuildStationStatusSnapshot(app.StationID, app.Engine, ocppConnected, time.Since(app.StartTime).Seconds())
		}
		registry.Stations[registry.DefaultID].Hub.ServeWS(w, r, snapshot, scope, stationID)
	})

	return r
}

// mountStationRoutes registers the operational routes for one station's
// AppContext. When fleet is non-nil and stationScoped is true, the caller is
// building a combined subrouter that also mounts mountFleetStationRoutesAuth
// on the same chi.Router — GET /status and PATCH /config are skipped here so
// the fleet-backed versions (fleet snapshot status, fleet.UpdateStation-backed
// config patch) registered there aren't shadowed or double-registered.
func mountStationRoutes(r chi.Router, app *AppContext, stationScoped bool, fleet FleetManager) {
	combinedWithFleetAdmin := fleet != nil && stationScoped
	if !combinedWithFleetAdmin {
		r.Get("/status", GetStatus(app.Engine, app.StartTime, app.OCPP))
	}

	r.Route("/connectors", func(r chi.Router) {
		r.Get("/", ListConnectors(app.Engine))
		r.Post("/", CreateConnector(app.Engine))
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", GetConnector(app.Engine))
			r.Put("/", UpdateConnector(app.Engine))
			r.Delete("/", DeleteConnector(app.Engine))
			r.Put("/availability", UpdateAvailability(app.Engine))
			r.Post("/plug_in", PlugIn(app.Engine))
			r.Post("/unplug", Unplug(app.Engine))
			r.Post("/suspend_ev", SuspendEV(app.Engine))
			r.Post("/resume_charging", ResumeCharging(app.Engine))
			r.Post("/start-charging", StartCharging(app.Engine, app.Config, app.AdmitLocalSession))
			r.Post("/stop-charging", StopCharging(app.Engine))
			r.Put("/rfid", SetRFID(app.Engine))
			r.Delete("/rfid", ClearRFID(app.Engine))
		})
	})

	r.Route("/sessions", func(r chi.Router) {
		r.Get("/", ListSessions(app.Engine))
		r.Post("/start", StartSession(app.Engine, app.Config, app.AdmitLocalSession))
		r.Post("/stop", StopAllSessions(app.Engine))
		r.Get("/last-stopped", GetLastStoppedSession(app.Engine))
		r.Get("/active", GetActiveSession(app.Engine))
		r.Get("/info", GetSessionInfo(app.Engine))
		r.Get("/{connector_id}", GetSessionByConnector(app.Engine))
	})

	r.Route("/config", func(r chi.Router) {
		r.Get("/", GetConfig(app.Config))
		switch {
		case combinedWithFleetAdmin:
			// PATCH is registered by mountFleetStationRoutesAuth on the
			// combined subrouter (fleet.UpdateStation-backed); station-scoped
			// save stays unsupported (matches the legacy message/behavior).
			r.Post("/save", SaveConfig(app.Config, true, true))
		case fleet != nil:
			// Default-station route under the fleet router: write through to
			// the global config instead of mutating an in-memory clone that
			// POST /config/save could never persist (see PatchDefaultStationConfig).
			r.Patch("/", PatchDefaultStationConfig(fleet))
			r.Post("/save", SaveFleetConfig(fleet))
		default:
			r.Patch("/", PatchConfig(app.Config, app.Engine))
			saveCfg := app.Config
			if app.MultiStation && !stationScoped {
				saveCfg = app.GlobalConfig
			}
			r.Post("/save", SaveConfig(saveCfg, app.MultiStation, stationScoped))
		}
	})

	r.Route("/reservations", func(r chi.Router) {
		r.Get("/", ListReservations(app.Engine))
		r.Post("/", CreateReservation(app.Engine, app.Hub, app.StationID))
		r.Delete("/{reservation_id}", CancelReservation(app.Engine, app.Hub, app.StationID))
	})

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

	r.Route("/charging-profiles", func(r chi.Router) {
		r.Get("/", handlers.ListChargingProfiles(app.ProfileManager))
		r.Post("/", handlers.InstallChargingProfile(app.ProfileManager))
		r.Delete("/", handlers.ClearChargingProfiles(app.ProfileManager))
		r.Get("/{profile_id}", handlers.GetChargingProfile(app.ProfileManager))
		r.Post("/composite-schedule", handlers.GetCompositeScheduleHandler(app.ProfileManager, app.Engine))
	})

	// Registered as direct leaf patterns (not wrapped in r.Route("/ocpp", ...))
	// so this coexists with mountFleetStationRoutesAuth's own /ocpp/reconnect
	// registration on the same combined subrouter — chi panics if two
	// separate r.Route/r.Mount calls target the same prefix, even when the
	// leaf paths inside don't otherwise overlap.
	r.Get("/ocpp/status", handlers.GetOCPPStatus(app.OCPPBridge))
	r.Get("/ocpp/config-keys", handlers.GetOCPPConfigKeys(app.ConfigKeys))
	r.Patch("/ocpp/config-keys", handlers.PatchOCPPConfigKey(app.ConfigKeys))
	r.Post("/ocpp/authorize", handlers.SendAuthorize(app.OCPP))
	r.Post("/ocpp/heartbeat", handlers.SendHeartbeat(app.OCPP))
	r.Route("/ocpp/raw", func(r chi.Router) {
		r.Post("/status-notification", handlers.SendRawStatusNotification(app.Engine, app.OCPP))
		r.Post("/meter-values", handlers.SendRawMeterValues(app.Engine, app.OCPP))
		r.Post("/data-transfer", handlers.SendRawDataTransfer(app.OCPP))
		r.Post("/start-transaction", handlers.SendRawStartTransaction(app.Engine, app.OCPP))
		r.Post("/stop-transaction", handlers.SendRawStopTransaction(app.Engine, app.OCPP))
	})

	r.Get("/about", handlers.GetAbout())
}

func listStations(registry *StationRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items := make([]stationListItemDTO, 0, len(registry.Stations))
		for id, app := range registry.Stations {
			ocppConnected := app.OCPP != nil && app.OCPP.IsConnected()
			items = append(items, stationListItemDTO{
				StationID:      id,
				OCPPID:         app.Config.OCPPID,
				OCPPVersion:    app.Config.OCPPVersion,
				Connected:      ocppConnected,
				ConnectorCount: len(app.Engine.GetConnectorIDs()),
				ActiveSessions: len(app.Engine.GetSessionInfo()),
				ConnectionURL:  app.Config.ConnectionURL,
			})
		}
		writeJSON(w, http.StatusOK, items)
	}
}

// stationListItemDTO is the payload for GET /api/v1/stations.
type stationListItemDTO struct {
	StationID      string `json:"station_id"`
	OCPPID         string `json:"ocpp_id"`
	OCPPVersion    string `json:"ocpp_version"`
	Connected      bool   `json:"connected"`
	ConnectorCount int    `json:"connector_count"`
	ActiveSessions int    `json:"active_sessions"`
	ConnectionURL  string `json:"connection_url"`
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
