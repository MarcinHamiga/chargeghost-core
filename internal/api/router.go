package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/chargeghost/engine/internal/api/handlers"
	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
	v16 "github.com/chargeghost/engine/internal/ocpp/v16"
	"github.com/chargeghost/engine/internal/timeline"
)

// AppContext holds shared dependencies injected into all handlers.
type AppContext struct {
	Engine         *engine.Engine
	Config         *config.Config
	StartTime      time.Time
	Timeline       *timeline.Store
	LocalAuth      ocpp.LocalAuthManager
	Firmware       ocpp.FirmwareManager
	Diagnostics    ocpp.DiagnosticsManager
	Hub            *ws.Hub
	ProfileManager ocpp.ChargingProfileManagerAPI
	ConfigKeys     *v16.ConfigKeyManager
	OCPP           handlers.OCPPSendAPI
}

// NewRouter builds and returns the chi router with all routes registered.
func NewRouter(app *AppContext) http.Handler {
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
		r.Get("/status", GetStatus(app.Engine, app.StartTime))

		r.Route("/connectors", func(r chi.Router) {
			r.Get("/", ListConnectors(app.Engine))
			r.Post("/", CreateConnector(app.Engine))
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", GetConnector(app.Engine))
				r.Put("/", UpdateConnector(app.Engine))
				r.Delete("/", DeleteConnector(app.Engine))
				r.Post("/plug_in", PlugIn(app.Engine))
				r.Post("/unplug", Unplug(app.Engine))
				r.Post("/suspend_ev", SuspendEV(app.Engine))
				r.Post("/resume_charging", ResumeCharging(app.Engine))
				r.Post("/start-charging", StartCharging(app.Engine))
				r.Post("/stop-charging", StopCharging(app.Engine))
				r.Put("/rfid", SetRFID(app.Engine))
				r.Delete("/rfid", ClearRFID(app.Engine))
			})
		})

		r.Route("/sessions", func(r chi.Router) {
			r.Get("/", ListSessions(app.Engine))
			r.Post("/start", StartSession(app.Engine))
			r.Post("/stop", StopAllSessions(app.Engine))
			r.Get("/last-stopped", GetLastStoppedSession(app.Engine))
			r.Get("/active", GetActiveSession(app.Engine))
			r.Get("/info", GetSessionInfo(app.Engine))
			r.Get("/{connector_id}", GetSessionByConnector(app.Engine))
		})

		r.Route("/config", func(r chi.Router) {
			r.Get("/", GetConfig(app.Config))
			r.Patch("/", PatchConfig(app.Config, app.Engine))
			r.Post("/save", SaveConfig(app.Config))
		})

		r.Route("/reservations", func(r chi.Router) {
			r.Get("/", ListReservations(app.Engine))
			r.Post("/", CreateReservation(app.Engine))
			r.Delete("/{reservation_id}", CancelReservation(app.Engine))
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

		r.Route("/ocpp", func(r chi.Router) {
			r.Get("/config-keys", handlers.GetOCPPConfigKeys(app.ConfigKeys))
			r.Patch("/config-keys", handlers.PatchOCPPConfigKey(app.ConfigKeys))
			r.Post("/authorize", handlers.SendAuthorize(app.OCPP))
			r.Post("/heartbeat", handlers.SendHeartbeat(app.OCPP))
			r.Route("/raw", func(r chi.Router) {
				r.Post("/status-notification", handlers.SendRawStatusNotification(app.Engine, app.OCPP))
				r.Post("/meter-values", handlers.SendRawMeterValues(app.Engine, app.OCPP))
				r.Post("/data-transfer", handlers.SendRawDataTransfer(app.OCPP))
				r.Post("/start-transaction", StartCharging(app.Engine))
				r.Post("/stop-transaction", StopCharging(app.Engine))
			})
		})

		r.Get("/about", handlers.GetAbout())
	})

	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		snapshot := ws.Message{
			Type: "state_snapshot",
			Data: ws.BuildStatusSnapshot(app.Engine),
		}
		app.Hub.ServeWS(w, r, snapshot)
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
