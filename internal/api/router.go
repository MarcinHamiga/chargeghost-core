package api

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
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

// httpAccessLog receives chi request logs. It defaults to stdout with the
// same shape middleware.Logger uses, so server mode is unchanged. TUI mode
// redirects it via SetHTTPAccessLog because Bubble Tea owns the terminal.
var httpAccessLog = log.New(os.Stdout, "", log.LstdFlags)

// SetHTTPAccessLog redirects the HTTP access log to w.
func SetHTTPAccessLog(w io.Writer) {
	httpAccessLog.SetOutput(w)
}

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
	// recordPeerAddr runs before RealIP so requireLoopback can judge the
	// actual socket peer, not a client-supplied proxy header.
	r.Use(recordPeerAddr)
	r.Use(middleware.RealIP)
	r.Use(middleware.RequestLogger(&middleware.DefaultLogFormatter{Logger: httpAccessLog, NoColor: false}))
	r.Use(middleware.Recoverer)
	fleetAllowedOrigins := fleetConfiguredOrigins(fleet)
	r.Use(corsMiddlewareWithOrigins(fleetAllowedOrigins))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Mount("/", newDefaultStationDispatcher(fleet))

		// Station list/creation, per-station routes (mounted dynamically
		// below), and fleet-wide routes. The sidecar is localhost-only, so
		// none of these require auth. Sensitive mutation surfaces (raw OCPP
		// injection, queue administration, credentials) are additionally
		// gated to loopback callers at their route groups.
		r.Get("/stations", ListStations(fleet))
		r.Post("/stations", CreateStation(fleet))
		r.Route("/fleet", func(r chi.Router) {
			r.Get("/status", GetFleetStatus(fleet))
			r.Get("/config", GetFleetConfig(fleet))
			r.Post("/config/save", SaveFleetConfig(fleet))
			r.Get("/operations", ListOperations(fleet))
			r.Post("/reload", ReloadFleet(fleet))
			r.Get("/operations/{operation_id}", GetOperation(fleet))
		})
		r.Mount("/stations/{station_id}", newStationDispatcher(fleet))
	})

	cfg := fleet.Config()
	allowedOrigins := []string(nil)
	if cfg != nil {
		allowedOrigins = cfg.AllowedOrigins
	}
	upgrader := ws.NewUpgrader(allowedOrigins)
	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
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
	r.Get("/status", GetStationStatus(fleet))
	r.Patch("/config", PatchStationConfig(fleet))
	r.Delete("/", DeleteStation(fleet))
	r.Post("/start", StartStation(fleet))
	r.Post("/stop", StopStation(fleet))
	r.Post("/restart", RestartStation(fleet))
	r.Post("/enable", EnableStation(fleet))
	r.Post("/disable", DisableStation(fleet))
	r.Post("/reload", ReloadStation(fleet))
	r.Post("/persist", PersistStation(fleet))

	// Direct leaf pattern (not r.Route("/ocpp", ...)) so this coexists with
	// mountStationRoutes's own /ocpp/* registrations when both are mounted
	// on the same combined subrouter; see the comment there.
	r.Post("/ocpp/reconnect", ReconnectStation(fleet))

	r.Route("/credentials", func(r chi.Router) {
		r.Use(requireLoopback)
		r.Put("/ocpp-password", SetOCPPPassword(fleet))
		r.Delete("/ocpp-password", ClearOCPPPassword(fleet))
		r.Post("/test", TestCredentials(fleet))
	})

	r.Route("/queue", func(r chi.Router) {
		r.Get("/status", GetQueueStatus(fleet))
		r.Get("/dead-letter", GetDeadLetter(fleet))
		r.With(requireLoopback).Post("/drain", DrainQueue(fleet))
		r.With(requireLoopback).Post("/clear", ClearQueue(fleet))
		r.With(requireLoopback).Delete("/dead-letter", ClearDeadLetter(fleet))
	})
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
	// recordPeerAddr runs before RealIP so requireLoopback can judge the
	// actual socket peer, not a client-supplied proxy header.
	r.Use(recordPeerAddr)
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
		r.Use(requireLoopback)
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
	return corsMiddlewareWithOrigins(nil)(next)
}

// corsMiddlewareWithOrigins reflects an Origin header only when it is
// explicitly allowed: either listed in the configured allowed_origins or a
// loopback origin (local dashboards/TUI). Unlike the previous wildcard `*`,
// arbitrary cross-origin sites can no longer drive the API from a browser.
// Requests without an Origin header (curl, Go clients, TUI) are unaffected.
// An explicit "*" entry in allowed_origins restores the old wildcard for
// operators who opt into it.
func corsMiddlewareWithOrigins(allowed []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin := r.Header.Get("Origin"); origin != "" && originAllowed(origin, allowed) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func originAllowed(origin string, allowed []string) bool {
	for _, a := range allowed {
		if a == "*" || a == origin {
			return true
		}
	}
	return isLoopbackOrigin(origin)
}

func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// fleetConfiguredOrigins returns the CORS origins from fleet config, or nil
// when the fleet or its config is unavailable (loopback-only default).
func fleetConfiguredOrigins(fleet FleetManager) []string {
	if fleet == nil {
		return nil
	}
	cfg := fleet.Config()
	if cfg == nil {
		return nil
	}
	return cfg.AllowedOrigins
}

// peerAddrCtxKey carries the socket peer address snapshot taken before the
// RealIP middleware runs. RealIP rewrites r.RemoteAddr from client-supplied
// proxy headers (X-Forwarded-For and friends), so judging loopback on
// r.RemoteAddr after RealIP would let a remote caller spoof 127.0.0.1.
type peerAddrCtxKey struct{}

// recordPeerAddr snapshots the connection peer address into the request
// context. It must run before middleware.RealIP in every router.
func recordPeerAddr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), peerAddrCtxKey{}, r.RemoteAddr)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// peerAddr returns the pre-RealIP socket peer address when recorded, or the
// live RemoteAddr otherwise (e.g. unit tests invoking requireLoopback
// directly without the middleware chain).
func peerAddr(r *http.Request) string {
	if addr, ok := r.Context().Value(peerAddrCtxKey{}).(string); ok && addr != "" {
		return addr
	}
	return r.RemoteAddr
}

// requireLoopback gates sensitive sidecar surfaces (raw OCPP injection,
// queue administration, credential management) to loopback callers. Server
// mode binds :8080 on all interfaces, so without this gate any host that can
// reach the port could inject OCPP traffic or wipe the durable queue.
// The check runs against the pre-RealIP peer snapshot, never against
// client-supplied proxy headers.
func requireLoopback(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(peerAddr(r))
		if err != nil {
			host = peerAddr(r)
		}
		if host == "localhost" {
			next.ServeHTTP(w, r)
			return
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "forbidden: endpoint accepts loopback callers only", http.StatusForbidden)
	})
}
