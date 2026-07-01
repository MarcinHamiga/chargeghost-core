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

	if err := fm.Start(ctx); err != nil {
		slog.Error("failed to start fleet", "err", err)
		os.Exit(1)
	}

	hub.SetDefaultStationID(fm.DefaultStationID())

	wg.Add(1)
	go func() {
		defer wg.Done()
		hub.Run(ctx)
	}()

	// WebSocket tickers: one station-scoped ticker per station, plus a fleet
	// ticker for all-station subscriptions.
	snapshotSources := make(map[string]*ws.EngineSnapshotSource)
	var snapshotMu sync.RWMutex
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
				snapshotMu.RLock()
				for _, sr := range fm.AllSnapshots() {
					sr := sr
					if src, ok := snapshotSources[sr.StationID]; ok && src != nil {
						hub.BroadcastMessage(ws.BuildStationStatusSnapshot(sr.StationID, src.Engine, sr.Connected, sr.UptimeSeconds))
					}
				}
				snapshotMu.RUnlock()
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
				snapshotMu.RLock()
				hub.BroadcastMessage(ws.BuildFleetStatusSnapshot(snapshotSources))
				snapshotMu.RUnlock()
			}
		}
	}()

	// Keep snapshot sources in sync with fleet state.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				newSources := make(map[string]*ws.EngineSnapshotSource)
				fm.mu.RLock()
				for id, ms := range fm.stations {
					if ms.Runtime != nil {
						newSources[id] = &ws.EngineSnapshotSource{
							Engine:    ms.Runtime.Engine,
							Bridge:    ms.Runtime.Bridge,
							StartTime: ms.Runtime.StartTime,
						}
					}
				}
				fm.mu.RUnlock()
				snapshotMu.Lock()
				snapshotSources = newSources
				snapshotMu.Unlock()
			}
		}
	}()

	registry := fm.Registry()
	router := api.NewFleetRouter(fm)
	_ = registry
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
