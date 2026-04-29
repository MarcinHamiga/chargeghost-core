package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/chargeghost/engine/internal/api"
	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
	v16 "github.com/chargeghost/engine/internal/ocpp/v16"
	v201 "github.com/chargeghost/engine/internal/ocpp/v201"
	"github.com/chargeghost/engine/internal/persistence"
	rt "github.com/chargeghost/engine/internal/runtime"
	"github.com/chargeghost/engine/internal/timeline"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// Load config from disk (or use defaults if not found).
	cfgPath := config.DefaultConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("failed to load config", "path", cfgPath, "err", err)
		os.Exit(1)
	}
	configureLogger(cfg.LogMode)
	slog.Info("config loaded", "path", cfgPath, "id", cfg.OCPPID)

	home, _ := os.UserHomeDir()
	engineDir := filepath.Join(home, ".chargeghost", "engine")

	e := engine.NewEngine(cfg.MultiEVSEMode, cfg.EVBatteryCapacity*1000) // kWh → Wh
	if err := e.LoadState(engineDir); err != nil {
		slog.Warn("could not load engine state, starting fresh", "err", err)
	}
	// Only add connectors from config if engine has none (fresh start).
	if len(e.GetConnectorIDs()) == 0 {
		for _, cc := range cfg.Connectors {
			e.AddConnector(cc.Voltage, cc.Current, cc.Phase)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	// Simulation loop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rt.NewRuntime(e).Run(ctx)
	}()

	// WebSocket hub.
	hub := ws.NewHub()
	wg.Add(1)
	go func() {
		defer wg.Done()
		hub.Run(ctx)
	}()

	dispatcher := ocpp.NewCommandDispatcher()
	wg.Add(1)
	go func() {
		defer wg.Done()
		dispatcher.Run(ctx)
	}()

	profileManager := v16.NewChargingProfileManager()
	e.GetLimit = func(connectorID int, transactionID int, voltage float64, phases int, txStart *time.Time) *float64 {
		return profileManager.GetCompositeLimit(connectorID, transactionID, time.Now(), voltage, txStart, phases)
	}
	var apiProfileManager ocpp.ChargingProfileManagerAPI = profileManager

	configKeys := v16.NewConfigKeyManager()
	authCache := ocpp.NewAuthorizationCache()
	localAuthReal := ocpp.NewLocalAuthListManager()

	// Load persisted state for OCPP managers.
	localAuthReal.SetPersistDir(engineDir)
	_ = localAuthReal.LoadState(engineDir)
	authCache.SetPersistDir(engineDir)
	_ = authCache.LoadState(engineDir)
	configKeys.SetPersistDir(engineDir)
	_ = configKeys.LoadState(engineDir)

	queuePath := filepath.Join(func() string { h, _ := os.UserHomeDir(); return h }(), ".chargeghost", "message_queue.json")
	messageQueue, err := queue.NewQueue(cfg.PersistMessageQueue, queuePath, 3)
	if err != nil {
		slog.Error("failed to create message queue", "err", err)
		os.Exit(1)
	}

	// Create real firmware/diagnostics managers and data transfer registry.
	// Callbacks will be set after bridge is created (they reference bridge).
	var fwOnStatus func(string)
	var diagOnStatus func(string)
	firmwareManager := ocpp.NewFirmwareManager(func(status string) {
		if fwOnStatus != nil {
			fwOnStatus(status)
		}
	})
	diagnosticsManager := ocpp.NewDiagnosticsManager(func(status string) {
		if diagOnStatus != nil {
			diagOnStatus(status)
		}
	})
	dataTransferReg := ocpp.NewDataTransferRegistry()

	timelineStore := timeline.NewStore(1000)
	_ = timelineStore.LoadState(engineDir)
	tl := ocpp.NewTimelineLogger(timelineStore)

	var bridge ocpp.OCPPBridge
	var configKeysAPI ocpp.ConfigKeyAPI = configKeys // default: v1.6 config keys
	var bridgeSave func()                            // version-specific shutdown persist
	var admitLocalSession func(*string) error
	switch cfg.OCPPVersion {
	case "1.6", "":
		profileManager.SetPersistDir(engineDir)
		_ = profileManager.LoadState(engineDir)
		bridge = v16.NewBridge(e, hub, cfg, dispatcher, profileManager, configKeys, authCache, localAuthReal, messageQueue, firmwareManager, diagnosticsManager, dataTransferReg, tl)
		admitLocalSession = newV16LocalSessionAdmission(configKeys, localAuthReal, authCache, func() bool { return bridge.IsConnected() })
		bridgeSave = func() {} // v1.6 managers already saved individually
	case "2.0.1":
		b201 := v201.NewBridge(e, hub, cfg, dispatcher, messageQueue, tl)
		b201.SetManagers(authCache, localAuthReal, firmwareManager, diagnosticsManager, dataTransferReg)
		b201.SetPersistDir(engineDir)
		_ = b201.LoadState(engineDir)
		pm201 := b201.ProfileManager()
		e.GetLimit = func(connectorID int, transactionID int, voltage float64, phases int, txStart *time.Time) *float64 {
			return pm201.GetCompositeLimit(connectorID, time.Now(), voltage, txStart, phases)
		}
		apiProfileManager = pm201
		configKeysAPI = b201.DeviceModel()
		bridge = b201
		admitLocalSession = newV201LocalSessionAdmission(b201.DeviceModel(), localAuthReal, authCache, func() bool { return bridge.IsConnected() })
		bridgeSave = func() { _ = b201.SaveState(engineDir) }
	default:
		slog.Error("unsupported OCPP version", "version", cfg.OCPPVersion)
		os.Exit(1)
	}

	// Wire firmware/diagnostics status callbacks now that bridge exists.
	fwOnStatus = func(status string) {
		hub.BroadcastMessage(ws.Message{
			Type: "firmware_status_changed",
			Data: map[string]string{"status": status},
		})
		if bridge.IsConnected() {
			dispatcher.Enqueue(ocpp.OCPPCommand{
				Description: "FirmwareStatusNotification",
				Execute: func() error {
					return bridge.SendFirmwareStatusNotification(status)
				},
			})
		}
	}

	diagOnStatus = func(status string) {
		hub.BroadcastMessage(ws.Message{
			Type: "diagnostics_status_changed",
			Data: map[string]string{"status": status},
		})
		if bridge.IsConnected() {
			dispatcher.Enqueue(ocpp.OCPPCommand{
				Description: "DiagnosticsStatusNotification",
				Execute: func() error {
					return bridge.SendDiagnosticsStatusNotification(status)
				},
			})
		}
	}

	// Wire engine event callbacks to WebSocket broadcasts.
	e.OnConnectorStatusChanged = newConnectorStatusChangedCallback(hub, bridge, dispatcher)

	e.OnConnectorParamsChanged = func(connectorID int, voltage, current float64, phase int) {
		hub.BroadcastMessage(ws.Message{
			Type: "connector_params_changed",
			Data: map[string]interface{}{
				"connector_id": connectorID,
				"voltage":      voltage,
				"current":      current,
				"phase":        phase,
			},
		})
	}

	e.OnSessionStarted = newSessionStartedCallback(e, hub, bridge, dispatcher)

	e.OnSessionStopped = newSessionStoppedCallback(hub, bridge, dispatcher)

	e.OnReservationExpired = func(reservationID, connectorID int) {
		hub.BroadcastMessage(ws.Message{
			Type: "reservation_changed",
			Data: map[string]interface{}{
				"action":         "expired",
				"reservation_id": reservationID,
				"connector_id":   connectorID,
			},
		})
	}

	app := &api.AppContext{
		Engine:            e,
		Config:            cfg,
		AdmitLocalSession: admitLocalSession,
		StartTime:         time.Now(),
		Timeline:          timelineStore,
		LocalAuth:         localAuthReal,
		Firmware:          firmwareManager,
		Diagnostics:       diagnosticsManager,
		Hub:               hub,
		ProfileManager:    apiProfileManager,
		ConfigKeys:        configKeysAPI,
		OCPP:              bridge,
	}
	router := api.NewRouter(app)
	srv := api.NewServer(":8080", router)

	// Tick broadcaster (started after bridge is available so IsConnected() is valid).
	wg.Add(1)
	go func() {
		defer wg.Done()
		ws.StartTicker(ctx, hub, e, bridge, 1*time.Second)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "err", err)
		}
	}()

	// Start the bridge (connects to CSMS).
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := bridge.Start(ctx); err != nil {
			slog.Error("OCPP bridge error", "err", err)
		}
	}()

	// Start periodic MeterValues ticker.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ocpp.StartMeterValueTicker(ctx, e, bridge, configKeysAPI)
	}()

	// Periodic state persistence (engine + timeline every 5 seconds).
	coord := persistence.NewCoordinator(engineDir, 5*time.Second, e, timelineStore)
	wg.Add(1)
	go func() {
		defer wg.Done()
		coord.Run(ctx)
	}()

	slog.Info("ChargeGhost engine started", "addr", ":8080", "id", cfg.OCPPID)

	// Wait for shutdown signal.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutdown signal received — stopping")
	cancel()

	// Persist all state before shutdown.
	slog.Info("persisting state before exit")
	coord.SaveAll()
	_ = localAuthReal.SaveState(engineDir)
	_ = authCache.SaveState(engineDir)
	_ = configKeys.SaveState(engineDir)
	_ = profileManager.SaveState(engineDir)
	bridgeSave()

	// Shutdown HTTP server gracefully.
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)

	// Wait for all goroutines.
	wg.Wait()
	slog.Info("all goroutines stopped — goodbye")
}

func configureLogger(mode string) {
	level := slog.LevelInfo
	switch mode {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}
