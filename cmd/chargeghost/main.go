package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chargeghost/engine/internal/api"
	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfgPath := config.DefaultConfigPath()

	logLevelFlag := ""
	for i, arg := range os.Args {
		if arg == "-log-level" && i+1 < len(os.Args) {
			logLevelFlag = os.Args[i+1]
		} else if strings.HasPrefix(arg, "-log-level=") {
			logLevelFlag = strings.TrimPrefix(arg, "-log-level=")
		}
	}
	if v, ok := os.LookupEnv("LOG_LEVEL"); ok && v != "" {
		logLevelFlag = v
	}

	home, _ := os.UserHomeDir()
	baseDir := filepath.Join(home, ".chargeghost")

	hub := ws.NewHub()

	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	if err != nil {
		slog.Error("failed to create fleet manager", "err", err)
		os.Exit(1)
	}

	levelVar := configureLogger(fm.Config().LogMode)
	_ = levelVar
	if logLevelFlag != "" {
		_ = configureLogger(logLevelFlag)
	}

	slog.Info("fleet manager loaded", "path", cfgPath, "stations", len(fm.AllStationIDs()))

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Start the hub's Run loop before the fleet, so lifecycle broadcasts
	// fired while stations are starting up aren't dropped into a hub that
	// isn't reading its broadcast channel yet.
	wg.Add(1)
	go func() {
		defer wg.Done()
		hub.Run(ctx)
	}()

	// Deliberately NOT ctx: station runtimes must be stopped exclusively
	// through fm.Shutdown()'s orderly per-station Stop() calls (which save
	// state before waiting for goroutines to drain), never by an ambient
	// context cancellation. ctx is cancelled by the signal handler below
	// BEFORE fm.Shutdown() runs; if station runtimes were rooted in ctx too,
	// that cancellation would race Stop() — the runtime's own goroutines
	// could observe it and exit (marking the runtime Stopped) before Stop()
	// ever calls SaveAll(), silently dropping state on every shutdown.
	if err := fm.Start(context.Background()); err != nil {
		slog.Error("failed to start fleet", "err", err)
		os.Exit(1)
	}

	// WebSocket tickers: one station-scoped ticker per station, plus a fleet
	// ticker for all-station subscriptions. Both read live fleet state via
	// FleetManager.EngineSnapshotSources on every tick rather than caching a
	// snapshot-source map themselves — with at most 8 stations this is
	// trivially cheap and avoids a second copy of fleet state to keep in sync.
	wg.Add(1)
	go func() {
		defer wg.Done()
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
	wg.Add(1)
	go func() {
		defer wg.Done()
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
	srv := api.NewServer(":8080", router)

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "err", err)
		}
	}()

	slog.Info("ChargeGhost engine started", "addr", ":8080", "stations", len(fm.AllStationIDs()))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutdown signal received — stopping")
	cancel()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutCancel()
	_ = fm.Shutdown(shutCtx)
	_ = srv.Shutdown(shutCtx)

	wg.Wait()
	slog.Info("all goroutines stopped — goodbye")
}

func configureLogger(mode string) *slog.LevelVar {
	levelVar := &slog.LevelVar{}
	switch mode {
	case "debug":
		levelVar.Set(slog.LevelDebug)
	case "warn":
		levelVar.Set(slog.LevelWarn)
	case "error":
		levelVar.Set(slog.LevelError)
	default:
		levelVar.Set(slog.LevelInfo)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: levelVar})))
	return levelVar
}
