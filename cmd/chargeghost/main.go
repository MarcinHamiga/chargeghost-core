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
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "path", cfgPath, "err", err)
		os.Exit(1)
	}

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
	if logLevelFlag != "" {
		cfg.LogMode = logLevelFlag
	}
	levelVar := configureLogger(cfg.LogMode)
	_ = levelVar

	effectiveCfgs, err := cfg.EffectiveStationConfigs()
	if err != nil {
		slog.Error("invalid station configuration", "err", err)
		os.Exit(1)
	}
	if len(effectiveCfgs) == 0 {
		slog.Error("no stations configured")
		os.Exit(1)
	}

	slog.Info("config loaded", "path", cfgPath, "stations", len(effectiveCfgs), "logMode", cfg.LogMode)

	home, _ := os.UserHomeDir()
	baseDir := filepath.Join(home, ".chargeghost")
	legacyDir := filepath.Join(baseDir, "engine")
	legacyQueueDir := baseDir

	// Determine whether we are running in legacy single-station mode.
	multiStationMode := len(effectiveCfgs) > 1

	// Shared WebSocket hub across all stations.
	hub := ws.NewHub()
	hub.SetDefaultStationID(effectiveCfgs[0].OCPPID)

	stations := make([]*StationRuntime, 0, len(effectiveCfgs))
	for _, stationCfg := range effectiveCfgs {
		stationCfg := stationCfg
		persistDir := legacyDir
		queueDir := legacyQueueDir
		if multiStationMode {
			persistDir = config.StationPersistDir(baseDir, stationCfg.OCPPID)
			queueDir = persistDir
		}
		sr, err := buildStationRuntime(stationCfg, hub, persistDir, queueDir)
		if err != nil {
			slog.Error("failed to build station runtime", "ocpp_id", stationCfg.OCPPID, "err", err)
			os.Exit(1)
		}
		sr.MultiStation = multiStationMode
		stations = append(stations, sr)
	}

	registry := &api.StationRegistry{
		DefaultID: effectiveCfgs[0].OCPPID,
		Stations:  make(map[string]*api.AppContext, len(stations)),
	}
	for _, sr := range stations {
		app := sr.AppContext()
		app.GlobalConfig = cfg
		registry.Stations[sr.ID] = app
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Simulation / OCPP / persistence goroutines for each station.
	for _, sr := range stations {
		sr.Start(ctx, &wg)
	}

	// WebSocket hub goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		hub.Run(ctx)
	}()

	// WebSocket tickers: one station-scoped ticker per station, plus a fleet
	// ticker for all-station subscriptions.
	snapshotSources := make(map[string]*ws.EngineSnapshotSource, len(stations))
	for _, sr := range stations {
		sr := sr
		snapshotSources[sr.ID] = &ws.EngineSnapshotSource{
			Engine:    sr.Engine,
			Bridge:    sr.Bridge,
			StartTime: sr.StartTime,
		}
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
					ocppConnected := sr.Bridge != nil && sr.Bridge.IsConnected()
					hub.BroadcastMessage(ws.BuildStationStatusSnapshot(sr.ID, sr.Engine, ocppConnected, time.Since(sr.StartTime).Seconds()))
				}
			}
		}()
	}
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
				hub.BroadcastMessage(ws.BuildFleetStatusSnapshot(snapshotSources))
			}
		}
	}()

	router := api.NewMultiRouter(registry)
	srv := api.NewServer(":8080", router)

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "err", err)
		}
	}()

	slog.Info("ChargeGhost engine started", "addr", ":8080", "stations", len(stations))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutdown signal received — stopping")
	cancel()

	slog.Info("persisting state before exit")
	for _, sr := range stations {
		_ = sr.Engine.SaveState(sr.PersistDir)
		_ = sr.Timeline.SaveState(sr.PersistDir)
		sr.SaveManagers()
		sr.SaveBridgeState()
	}

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
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
