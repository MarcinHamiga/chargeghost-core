package api

import (
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
)

// stationRouterCache memoizes the built subrouter for one station, keyed by
// station ID. A cached entry is rebuilt whenever the station's *AppContext
// pointer changes — StationRuntime.AppContext returns a pointer that is
// stable for the lifetime of one runtime, so a pointer change means the
// station's runtime was replaced (restart, or stop-then-start).
type stationRouterCache struct {
	mu      sync.Mutex
	entries map[string]cachedStationRouter
	build   func(fleet FleetManager, app *AppContext) http.Handler
}

type cachedStationRouter struct {
	app     *AppContext
	handler http.Handler
}

func newStationRouterCache(build func(fleet FleetManager, app *AppContext) http.Handler) *stationRouterCache {
	return &stationRouterCache{entries: make(map[string]cachedStationRouter), build: build}
}

// getOrBuild returns the cached subrouter for id, rebuilding it if app's
// pointer differs from what's cached (or nothing is cached yet).
func (c *stationRouterCache) getOrBuild(fleet FleetManager, id string, app *AppContext) http.Handler {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[id]; ok && entry.app == app {
		return entry.handler
	}
	handler := c.build(fleet, app)
	c.entries[id] = cachedStationRouter{app: app, handler: handler}
	return handler
}

// evict drops any cached entry for id. Called when a station is confirmed
// gone from the fleet entirely, so a deleted-and-later-recreated station
// with the same ID doesn't risk reusing a stale cache slot indefinitely.
func (c *stationRouterCache) evict(id string) {
	c.mu.Lock()
	delete(c.entries, id)
	c.mu.Unlock()
}

// buildStationSubrouter returns the combined operational + fleet-admin routes
// for one running station, mounted under /api/v1/stations/{station_id}.
func buildStationSubrouter(fleet FleetManager, app *AppContext) http.Handler {
	r := chi.NewRouter()
	mountStationRoutes(r, app, true, fleet)
	mountFleetStationRoutesAuth(r, fleet, app.StationID)
	return r
}

// buildDefaultStationRouter returns the operational-only routes for one
// running station, mounted at /api/v1/* (the default station has no
// fleet-admin surface of its own — that always lives under
// /stations/{station_id}/*, even for the station that happens to be default).
func buildDefaultStationRouter(fleet FleetManager, app *AppContext) http.Handler {
	r := chi.NewRouter()
	mountStationRoutes(r, app, false, fleet)
	return r
}

// buildAdminOnlyStationRouter returns just the fleet-admin routes, shared by
// every station that exists in the fleet's config but currently has no live
// runtime (stopped, disabled, or failed to build). Built once — its handlers
// resolve the target station from the URL per request via chi.URLParam, so a
// single instance safely serves any number of non-running stations.
// Operational paths (connectors, sessions, ...) have nothing to serve without
// a runtime, so they fall through to a 503 instead of chi's default 404.
func buildAdminOnlyStationRouter(fleet FleetManager) http.Handler {
	r := chi.NewRouter()
	mountFleetStationRoutesAuth(r, fleet, "")
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: "station is not running"})
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: "station is not running"})
	})
	return r
}

// newStationDispatcher resolves {station_id} against the fleet's live state
// on every request and serves either the station's cached combined subrouter
// (if it has a running runtime), the shared admin-only router (if it exists
// but isn't running — so start/enable/delete/config-patch still work), or a
// 404 for a station the fleet has never heard of.
//
// This replaces the previous one-time Registry() snapshot taken when the
// router was built: that left runtime-created stations completely
// unreachable, and left a restarted station's routes bound to its old, dead
// runtime while the replacement runtime — the one actually talking to the
// CSMS — was unreachable via REST.
func newStationDispatcher(fleet FleetManager) http.Handler {
	cache := newStationRouterCache(buildStationSubrouter)
	adminOnly := buildAdminOnlyStationRouter(fleet)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		if _, ok := fleet.Snapshot(id); !ok {
			cache.evict(id)
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "station not found"})
			return
		}
		if app, ok := fleet.GetAppContext(id); ok {
			cache.getOrBuild(fleet, id, app).ServeHTTP(w, r)
			return
		}
		adminOnly.ServeHTTP(w, r)
	})
}

// newDefaultStationDispatcher is like newStationDispatcher but resolves the
// target station via fleet.DefaultStationID() on every request instead of a
// URL parameter, so a change in which station is default (e.g. after
// deleting the previous default) takes effect immediately.
func newDefaultStationDispatcher(fleet FleetManager) http.Handler {
	cache := newStationRouterCache(buildDefaultStationRouter)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := fleet.DefaultStationID()
		app, ok := fleet.GetAppContext(id)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: "default station is not running"})
			return
		}
		cache.getOrBuild(fleet, id, app).ServeHTTP(w, r)
	})
}
