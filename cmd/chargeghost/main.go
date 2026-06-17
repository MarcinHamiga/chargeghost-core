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
	// Apply CLI flag / env var overrides for log level before the
	// baseline config mode. This lets operators turn on debug logging
	// (or quiet the system to warn/error) without editing config.
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
	slog.Info("config loaded", "path", cfgPath, "id", cfg.OCPPID, "logMode", cfg.LogMode)
	_ = levelVar // reserved for future SIGHUP-based level changes

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
	// Dead-letter file lives next to the queue. Messages that exhaust their
	// retry budget or are evicted under memory pressure are appended here
	// for post-mortem inspection. The Cap=0 means "unlimited" for now —
	// operators can tune via a future config field; Plan 6 deliberately
	// leaves the cap unset to avoid surprising existing deployments.
	deadLetterPath := filepath.Join(func() string { h, _ := os.UserHomeDir(); return h }(), ".chargeghost", "message_dead_letter.jsonl")
	messageQueue, err := queue.NewQueueWithConfig(cfg.PersistMessageQueue, queuePath, 3, queue.Config{DeadLetterPath: deadLetterPath})
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

	// StatusTracker is shared between the bridge (for connect/disconnect and
	// per-sender outcome updates) and the command dispatcher (for command
	// execution errors that don't go through a typed sender). main.go owns
	// the instance so the HTTP layer can reach the same snapshot via
	// GET /api/v1/ocpp/status.
	statusTracker := ocpp.NewStatusTracker(cfg.ConnectionURL, cfg.OCPPID, cfg.OCPPVersion)
	dispatcher.SetStatusTracker(statusTracker)
	dispatcher.SetTimelineLogger(tl)
	dispatcher.SetHubBroadcaster(hub)

	var bridge ocpp.OCPPBridge
	var configKeysAPI ocpp.ConfigKeyAPI = configKeys // default: v1.6 config keys
	var bridgeSave func()                            // version-specific shutdown persist
	var admitLocalSession func(*string) error
	switch cfg.OCPPVersion {
	case "1.6", "":
		profileManager.SetPersistDir(engineDir)
		_ = profileManager.LoadState(engineDir)
		b16 := v16.NewBridge(e, hub, cfg, dispatcher, profileManager, configKeys, authCache, localAuthReal, messageQueue, firmwareManager, diagnosticsManager, dataTransferReg, tl)
		b16.SetStatusTracker(statusTracker)
		bridge = b16
		admitLocalSession = newV16LocalSessionAdmission(configKeys, localAuthReal, authCache, func() bool { return bridge.IsConnected() })
		bridgeSave = func() {} // v1.6 managers already saved individually
	case "2.0.1":
		b201 := v201.NewBridge(e, hub, cfg, dispatcher, messageQueue, tl)
		b201.SetManagers(authCache, localAuthReal, firmwareManager, diagnosticsManager, dataTransferReg)
		b201.SetStatusTracker(statusTracker)
		b201.SetPersistDir(engineDir)
		_ = b201.LoadState(engineDir)
		pm201 := b201.ProfileManager()
		e.GetLimit = func(connectorID int, transactionID int, voltage float64, phases int, txStart *time.Time) *float64 {
			return pm201.GetCompositeLimit(connectorID, time.Now(), voltage, txStart, phases, b201.ActiveTxIDForEVSE(connectorID))
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

	// Wire the dispatcher's defensive link-up check to bridge.IsConnected().
	// When the CSMS link is down, the dispatcher re-queues commands at
	// the back of the channel with a 200ms backoff instead of letting a
	// single slow send stall every subsequent command in the queue.
	dispatcher.SetLinkUpFunc(func() bool { return bridge.IsConnected() })

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
	e.OnConnectorStatusChanged = newConnectorStatusChangedCallback(e, hub, bridge, dispatcher)
	e.OnConnectorPlugChanged = newConnectorPlugChangedCallback(hub)
	e.OnConnectorIDTagChanged = newConnectorIDTagChangedCallback(hub)
	e.OnTransactionIDChanged = newTransactionIDChangedCallback(hub)

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

	e.OnChargingStateChanged = newChargingStateChangedCallback(hub, bridge, dispatcher)

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
		OCPPBridge:        bridge,
	}
	router := api.NewRouter(app)
	srv := api.NewServer(":8080", router)

	// Tick broadcaster (started after bridge is available so IsConnected() is valid).
	wg.Add(1)
	go func() {
		defer wg.Done()
		ws.StartTicker(ctx, hub, e, bridge, app.StartTime, 1*time.Second)
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
	// Start periodic MeterValues ticker.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ocpp.StartMeterValueTicker(ctx, e, bridge, configKeysAPI)
	}()

	// Periodic OCPP link health log (every 60s) so operators have a
	// consistent heartbeat line to grep for during incident reviews.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ocpp.StartHealthTicker(ctx, statusTracker, 60*time.Second)
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
