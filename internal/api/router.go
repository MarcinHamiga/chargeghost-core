package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/chargeghost/engine/internal/api/handlers"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
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

// writeJSON is a helper used by router-level code if needed.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
