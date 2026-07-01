package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
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

// StationRuntime owns one isolated single-station simulation: engine, OCPP
// bridge, dispatcher, managers, timeline, and persistence. The process-level
// code in main.go creates one runtime per effective station and supervises them.
type StationRuntime struct {
	ID              string
	Config          *config.Config
	Engine          *engine.Engine
	Hub             *ws.Hub
	Dispatcher      *ocpp.CommandDispatcher
	Bridge          ocpp.OCPPBridge
	StatusTracker   *ocpp.StatusTracker
	Timeline        *timeline.Store
	LocalAuth       ocpp.LocalAuthManager
	Firmware        ocpp.FirmwareManager
	Diagnostics     ocpp.DiagnosticsManager
	ProfileManager  ocpp.ChargingProfileManagerAPI
	ConfigKeys      ocpp.ConfigKeyAPI
	AdmitLocal      func(*string) error
	PersistDir      string
	SaveBridgeState func()
	SaveManagers    func()
	StartTime       time.Time
	MultiStation    bool
}

// AppContext returns the API context for this station. Each station gets its
// own AppContext so REST handlers can remain unchanged.
func (sr *StationRuntime) AppContext() *api.AppContext {
	return &api.AppContext{
		Engine:            sr.Engine,
		Config:            sr.Config,
		AdmitLocalSession: sr.AdmitLocal,
		StartTime:         sr.StartTime,
		Timeline:          sr.Timeline,
		LocalAuth:         sr.LocalAuth,
		Firmware:          sr.Firmware,
		Diagnostics:       sr.Diagnostics,
		Hub:               sr.Hub,
		ProfileManager:    sr.ProfileManager,
		ConfigKeys:        sr.ConfigKeys,
		OCPP:              sr.Bridge,
		OCPPBridge:        sr.Bridge,
		StationID:         sr.ID,
		MultiStation:      sr.MultiStation,
	}
}

// Start launches all station-local goroutines and registers them with the wait group.
func (sr *StationRuntime) Start(ctx context.Context, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		rt.NewRuntime(sr.Engine).Run(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		sr.Dispatcher.Run(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := sr.Bridge.Start(ctx); err != nil {
			slog.Error("OCPP bridge error", "station_id", sr.ID, "ocpp_id", sr.Config.OCPPID, "err", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		ocpp.StartMeterValueTicker(ctx, sr.Engine, sr.Bridge, sr.ConfigKeys)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		ocpp.StartHealthTicker(ctx, sr.StatusTracker, 60*time.Second)
	}()

	coord := persistence.NewCoordinator(sr.PersistDir, 5*time.Second, sr.Engine, sr.Timeline)
	wg.Add(1)
	go func() {
		defer wg.Done()
		coord.Run(ctx)
	}()
}

// buildStationRuntime creates an isolated single-station runtime from an effective
// station config. The hub is shared across stations; everything else is station-local.
// queueDir is the directory used for the offline message queue and dead-letter file.
func buildStationRuntime(cfg *config.Config, hub *ws.Hub, persistDir, queueDir string) (*StationRuntime, error) {
	stationID := cfg.OCPPID
	if err := os.MkdirAll(persistDir, 0750); err != nil {
		return nil, err
	}

	e := engine.NewEngine(cfg.MultiEVSEMode, cfg.EVBatteryCapacity*1000) // kWh → Wh
	if err := e.LoadState(persistDir); err != nil {
		slog.Warn("could not load engine state, starting fresh", "station_id", stationID, "err", err)
	}
	if len(e.GetConnectorIDs()) == 0 {
		for _, cc := range cfg.Connectors {
			e.AddConnector(cc.Voltage, cc.Current, cc.Phase)
		}
	}

	timelineStore := timeline.NewStore(1000)
	if err := timelineStore.LoadState(persistDir); err != nil {
		slog.Warn("could not load timeline state, starting fresh", "station_id", stationID, "err", err)
	}
	tl := ocpp.NewTimelineLogger(timelineStore)

	dispatcher := ocpp.NewCommandDispatcher()
	statusTracker := ocpp.NewStatusTracker(cfg.ConnectionURL, cfg.OCPPID, cfg.OCPPVersion)
	dispatcher.SetStatusTracker(statusTracker)
	dispatcher.SetTimelineLogger(tl)

	authCache := ocpp.NewAuthorizationCache()
	localAuthReal := ocpp.NewLocalAuthListManager()
	authCache.SetPersistDir(persistDir)
	localAuthReal.SetPersistDir(persistDir)
	_ = localAuthReal.LoadState(persistDir)
	_ = authCache.LoadState(persistDir)

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

	queuePath := filepath.Join(queueDir, "message_queue.json")
	deadLetterPath := filepath.Join(queueDir, "message_dead_letter.jsonl")
	messageQueue, err := queue.NewQueueWithConfig(cfg.PersistMessageQueue, queuePath, 3, queue.Config{DeadLetterPath: deadLetterPath})
	if err != nil {
		return nil, err
	}

	sr := &StationRuntime{
		ID:            stationID,
		Config:        cfg,
		Engine:        e,
		Hub:           hub,
		Dispatcher:    dispatcher,
		StatusTracker: statusTracker,
		Timeline:      timelineStore,
		LocalAuth:     localAuthReal,
		Firmware:      firmwareManager,
		Diagnostics:   diagnosticsManager,
		PersistDir:    persistDir,
		StartTime:     time.Now(),
	}

	var bridge ocpp.OCPPBridge
	var configKeysAPI ocpp.ConfigKeyAPI
	var admitLocalSession func(*string) error

	switch cfg.OCPPVersion {
	case "1.6", "":
		profileManager := v16.NewChargingProfileManager()
		profileManager.SetPersistDir(persistDir)
		_ = profileManager.LoadState(persistDir)
		configKeys := v16.NewConfigKeyManager()
		configKeys.SetPersistDir(persistDir)
		_ = configKeys.LoadState(persistDir)

		e.GetLimit = func(connectorID int, transactionID int, voltage float64, phases int, txStart *time.Time) *float64 {
			return profileManager.GetCompositeLimit(connectorID, transactionID, time.Now(), voltage, txStart, phases)
		}
		sr.ProfileManager = profileManager
		configKeysAPI = configKeys

		b16 := v16.NewBridge(e, hub, cfg, dispatcher, profileManager, configKeys, authCache, localAuthReal, messageQueue, firmwareManager, diagnosticsManager, dataTransferReg, tl)
		b16.SetStatusTracker(statusTracker)
		b16.SetStationID(stationID)
		bridge = b16
		admitLocalSession = newV16LocalSessionAdmission(configKeys, localAuthReal, authCache, func() bool { return bridge.IsConnected() })
		sr.SaveBridgeState = func() {}
		sr.SaveManagers = func() {
			_ = localAuthReal.SaveState(persistDir)
			_ = authCache.SaveState(persistDir)
			_ = configKeys.SaveState(persistDir)
			_ = profileManager.SaveState(persistDir)
		}
	case "2.0.1":
		b201 := v201.NewBridge(e, hub, cfg, dispatcher, messageQueue, tl)
		b201.SetManagers(authCache, localAuthReal, firmwareManager, diagnosticsManager, dataTransferReg)
		b201.SetStatusTracker(statusTracker)
		b201.SetPersistDir(persistDir)
		_ = b201.LoadState(persistDir)
		b201.SetStationID(stationID)
		pm201 := b201.ProfileManager()
		e.GetLimit = func(connectorID int, transactionID int, voltage float64, phases int, txStart *time.Time) *float64 {
			return pm201.GetCompositeLimit(connectorID, time.Now(), voltage, txStart, phases, b201.ActiveTxIDForEVSE(connectorID))
		}
		sr.ProfileManager = pm201
		configKeysAPI = b201.DeviceModel()
		bridge = b201
		admitLocalSession = newV201LocalSessionAdmission(b201.DeviceModel(), localAuthReal, authCache, func() bool { return bridge.IsConnected() })
		sr.SaveBridgeState = func() { _ = b201.SaveState(persistDir) }
		sr.SaveManagers = func() {
			_ = localAuthReal.SaveState(persistDir)
			_ = authCache.SaveState(persistDir)
		}
	default:
		return nil, ocpp.NewBridgeForVersion(cfg.OCPPVersion)
	}

	sr.Bridge = bridge
	sr.ConfigKeys = configKeysAPI
	sr.AdmitLocal = admitLocalSession
	dispatcher.SetLinkUpFunc(func() bool { return bridge.IsConnected() })

	fwOnStatus = func(status string) {
		hub.BroadcastMessage(ws.Message{
			Type:      "firmware_status_changed",
			StationID: stationID,
			Data:      map[string]string{"status": status},
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
			Type:      "diagnostics_status_changed",
			StationID: stationID,
			Data:      map[string]string{"status": status},
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

	e.OnConnectorStatusChanged = newConnectorStatusChangedCallback(stationID, e, hub, bridge, dispatcher)
	e.OnConnectorPlugChanged = newConnectorPlugChangedCallback(stationID, hub)
	e.OnConnectorIDTagChanged = newConnectorIDTagChangedCallback(stationID, hub)
	e.OnTransactionIDChanged = newTransactionIDChangedCallback(stationID, hub)

	e.OnConnectorParamsChanged = func(connectorID int, voltage, current float64, phase int) {
		hub.BroadcastMessage(ws.Message{
			Type:      "connector_params_changed",
			StationID: stationID,
			Data: map[string]interface{}{
				"connector_id": connectorID,
				"voltage":      voltage,
				"current":      current,
				"phase":        phase,
			},
		})
	}

	e.OnSessionStarted = newSessionStartedCallback(stationID, e, hub, bridge, dispatcher)
	e.OnSessionStopped = newSessionStoppedCallback(stationID, hub, bridge, dispatcher)
	e.OnChargingStateChanged = newChargingStateChangedCallback(stationID, hub, bridge, dispatcher)

	e.OnReservationExpired = func(reservationID, connectorID int) {
		hub.BroadcastMessage(ws.Message{
			Type:      "reservation_changed",
			StationID: stationID,
			Data: map[string]interface{}{
				"action":         "expired",
				"reservation_id": reservationID,
				"connector_id":   connectorID,
			},
		})
	}

	// Wrap the shared hub so dispatcher overflow events carry the station ID.
	dispatcher.SetHubBroadcaster(&stationHubBroadcaster{hub: hub, stationID: stationID})

	return sr, nil
}

// stationHubBroadcaster adapts the process-level hub to the ocpp.HubBroadcaster
// interface, tagging overflow events with the originating station ID.
type stationHubBroadcaster struct {
	hub       *ws.Hub
	stationID string
}

func (b *stationHubBroadcaster) BroadcastOCPPQueueOverflow(description string, queueDepth, queueCap, droppedTotal int) {
	b.hub.BroadcastMessage(ws.Message{
		Type:      ws.MsgOCPPQueueOverflow,
		StationID: b.stationID,
		Data: map[string]interface{}{
			"description":  description,
			"queueDepth":   queueDepth,
			"queueCap":     queueCap,
			"droppedTotal": droppedTotal,
		},
	})
}
