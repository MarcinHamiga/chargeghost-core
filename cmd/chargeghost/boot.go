package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/chargeghost/engine/internal/api"
	ws "github.com/chargeghost/engine/internal/api/ws"
)

// Boot owns every process-level goroutine for one running instance:
// hub loop, fleet start, snapshot tickers, HTTP server.
type Boot struct {
	Hub    *ws.Hub
	Fleet  *FleetManager
	Server *api.Server
	Addr   string // actual bound address (ephemeral-safe)

	cancel  context.CancelFunc
	wg      sync.WaitGroup
	cfgPath string
	baseDir string
}

// StartBoot composes and starts everything. listen is the requested HTTP
// address (":8080" for server mode, "127.0.0.1:0" for TUI mode).
func StartBoot(cfgPath, baseDir, listen string) (*Boot, error) {
	hub := ws.NewHub()

	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	if err != nil {
		slog.Error("failed to create fleet manager", "err", err)
		return nil, err
	}

	slog.Info("fleet manager loaded", "path", cfgPath, "stations", len(fm.AllStationIDs()))

	ctx, cancel := context.WithCancel(context.Background())
	b := &Boot{
		Hub:     hub,
		Fleet:   fm,
		cfgPath: cfgPath,
		baseDir: baseDir,
		cancel:  cancel,
	}

	// Start the hub's Run loop before the fleet, so lifecycle broadcasts
	// fired while stations are starting up aren't dropped into a hub that
	// isn't reading its broadcast channel yet.
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		hub.Run(ctx)
	}()

	// Deliberately NOT ctx: station runtimes must be stopped exclusively
	// through fm.Shutdown()'s orderly per-station Stop() calls (which save
	// state before waiting for goroutines to drain), never by an ambient
	// context cancellation. ctx is cancelled before fm.Shutdown() runs;
	// if station runtimes were rooted in ctx too, that cancellation would
	// race Stop() — the runtime's own goroutines could observe it and exit
	// (marking the runtime Stopped) before Stop() ever calls SaveAll(),
	// silently dropping state on every shutdown.
	if err := fm.Start(context.Background()); err != nil {
		slog.Error("failed to start fleet", "err", err)
		cancel()
		return nil, err
	}

	// WebSocket tickers: one station-scoped ticker per station, plus a fleet
	// ticker for all-station subscriptions. Both read live fleet state via
	// FleetManager.EngineSnapshotSources on every tick rather than caching a
	// snapshot-source map themselves — with at most 8 stations this is
	// trivially cheap and avoids a second copy of fleet state to keep in sync.
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sources := fm.EngineSnapshotSources()
				for _, sr := range fm.AllSnapshots() {
					if src, ok := sources[sr.StationID]; ok {
						hub.BroadcastMessage(ws.BuildStationStatusSnapshot(sr.StationID, src.Engine, sr.Connected, sr.UptimeSeconds))
					}
				}
			}
		}
	}()
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				hub.BroadcastMessage(ws.BuildFleetStatusSnapshot(fm.EngineSnapshotSources()))
			}
		}
	}()

	router := api.NewFleetRouter(fm)
	srv := api.NewServer(listen, router)
	b.Server = srv

	ln, err := srv.Listen(listen)
	if err != nil {
		slog.Error("failed to bind HTTP listener", "addr", listen, "err", err)
		cancel()
		return nil, fmt.Errorf("bind %s: %w", listen, err)
	}
	b.Addr = ln.Addr().String()

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "err", err)
		}
	}()

	slog.Info("ChargeGhost engine started", "addr", listen, "stations", len(fm.AllStationIDs()))

	return b, nil
}

// Shutdown performs the server-mode shutdown sequence verbatim:
// cancel() → fm.Shutdown(15s ctx) → srv.Shutdown → wg.Wait.
func (b *Boot) Shutdown() {
	b.cancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutCancel()
	_ = b.Fleet.Shutdown(shutCtx)
	_ = b.Server.Shutdown(shutCtx)

	b.wg.Wait()
	slog.Info("all goroutines stopped — goodbye")
}
