package main

import (
	"context"
	"errors"
	"fmt"
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

// StationLifecycleState tracks where a managed station is in its lifecycle.
type StationLifecycleState string

const (
	StationConfigured StationLifecycleState = "configured"
	StationStarting   StationLifecycleState = "starting"
	StationRunning    StationLifecycleState = "running"
	StationStopping   StationLifecycleState = "stopping"
	StationStopped    StationLifecycleState = "stopped"
	StationFailed     StationLifecycleState = "failed"
	StationDisabled   StationLifecycleState = "disabled"
)

// ErrAlreadyStarted is returned by Start when the runtime has already been
// started (or is starting/running/stopping/failed) — a StationRuntime is
// single-use; FleetManager must build a fresh one to restart a station.
var ErrAlreadyStarted = errors.New("station runtime already started")

// ErrStopTimeout is returned by Stop when the caller's context expires before
// all station goroutines have exited. The runtime remains in StationStopping
// and will transition to StationStopped in the background once its
// goroutines actually finish draining; callers must not build a replacement
// runtime for the same station until that happens (checked via LifecycleState).
var ErrStopTimeout = errors.New("station runtime stop timed out")

// StationRuntime owns one isolated single-station simulation: engine, OCPP
// bridge, dispatcher, managers, timeline, and persistence. The process-level
// FleetManager creates one runtime per effective station and supervises it.
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
	QueueDir        string
	Queue           queue.MessageQueue
	DeadLetterPath  string
	SaveBridgeState func()
	SaveManagers    func()
	StartTime       time.Time
	MultiStation    bool

	// onStateChange, when set (before Start), is invoked outside sr.mu on
	// every lifecycle transition, including asynchronous ones such as a
	// bridge goroutine failing mid-run. FleetManager wires this to broadcast
	// station_lifecycle_changed over the WebSocket hub.
	onStateChange func(state StationLifecycleState, errStr string)

	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	done           chan struct{}
	lifecycleState StationLifecycleState
	lastErr        string
	mu             sync.RWMutex

	appContextOnce sync.Once
	appContextVal  *api.AppContext
}

// SetOnStateChange registers the lifecycle-change hook. Must be called before
// Start (typically right after buildStationRuntime returns).
func (sr *StationRuntime) SetOnStateChange(fn func(state StationLifecycleState, errStr string)) {
	sr.mu.Lock()
	sr.onStateChange = fn
	sr.mu.Unlock()
}

// AppContext returns the API context for this station, built once and cached.
// The returned pointer is stable for the lifetime of this StationRuntime
// instance — callers (notably the router's per-station subrouter cache) use
// pointer identity to detect when a station has been replaced by a fresh
// runtime (e.g. after a restart).
func (sr *StationRuntime) AppContext() *api.AppContext {
	sr.appContextOnce.Do(func() {
		sr.appContextVal = &api.AppContext{
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
			Queue:             sr.Queue,
			DeadLetterPath:    sr.DeadLetterPath,
		}
	})
	return sr.appContextVal
}

// setState transitions the runtime's lifecycle state and fires onStateChange
// outside the lock. A late "Running" transition arriving after an async
// failure has already marked the runtime Failed (a race between Start's own
// happy-path setState(Running) call and a bridge goroutine's early failure)
// is dropped rather than resurrecting a dead runtime.
func (sr *StationRuntime) setState(state StationLifecycleState, err string) {
	sr.mu.Lock()
	if state == StationRunning && sr.lifecycleState == StationFailed {
		sr.mu.Unlock()
		return
	}
	sr.lifecycleState = state
	sr.lastErr = err
	hook := sr.onStateChange
	sr.mu.Unlock()
	if hook != nil {
		hook(state, err)
	}
}

// LifecycleState returns the current lifecycle state of the station.
func (sr *StationRuntime) LifecycleState() StationLifecycleState {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	return sr.lifecycleState
}

// LastError returns the last error that occurred during station lifecycle,
// or an empty string if none.
func (sr *StationRuntime) LastError() string {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	return sr.lastErr
}

// Done returns a channel that is closed once every Start-launched goroutine
// has exited (i.e. once the runtime reaches StationStopped). Returns nil if
// Start has not been called yet.
func (sr *StationRuntime) Done() <-chan struct{} {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	return sr.done
}

// Start launches all station-local goroutines. The station runs until the
// supplied context is cancelled or Stop is called. A StationRuntime is
// single-use: Start returns ErrAlreadyStarted unless the runtime is still in
// its initial StationConfigured state. To restart a station, FleetManager
// builds a fresh StationRuntime (see replaceRuntime) rather than reusing this
// one — that also avoids the sync.WaitGroup-reuse hazard of restarting a
// runtime whose previous shutdown hasn't fully drained.
func (sr *StationRuntime) Start(ctx context.Context) error {
	sr.mu.Lock()
	if sr.lifecycleState != StationConfigured {
		state := sr.lifecycleState
		sr.mu.Unlock()
		return fmt.Errorf("%w: station %s is %s", ErrAlreadyStarted, sr.ID, state)
	}
	sr.ctx, sr.cancel = context.WithCancel(ctx)
	sr.lifecycleState = StationStarting
	sr.lastErr = ""
	sr.StartTime = time.Now()
	sr.done = make(chan struct{})
	hook := sr.onStateChange
	sr.mu.Unlock()
	if hook != nil {
		hook(StationStarting, "")
	}

	sr.wg.Add(1)
	go func() {
		defer sr.wg.Done()
		rt.NewRuntime(sr.Engine).Run(sr.ctx)
	}()

	sr.wg.Add(1)
	go func() {
		defer sr.wg.Done()
		sr.Dispatcher.Run(sr.ctx)
	}()

	sr.wg.Add(1)
	go func() {
		defer sr.wg.Done()
		if err := sr.Bridge.Start(sr.ctx); err != nil {
			slog.Error("OCPP bridge error", "station_id", sr.ID, "ocpp_id", sr.Config.OCPPID, "err", err)
			sr.setState(StationFailed, err.Error())
		}
	}()

	sr.wg.Add(1)
	go func() {
		defer sr.wg.Done()
		ocpp.StartMeterValueTicker(sr.ctx, sr.Engine, sr.Bridge, sr.ConfigKeys)
	}()

	sr.wg.Add(1)
	go func() {
		defer sr.wg.Done()
		ocpp.StartHealthTicker(sr.ctx, sr.StatusTracker, 60*time.Second)
	}()

	coord := persistence.NewCoordinator(sr.PersistDir, 5*time.Second, sr.Engine, sr.Timeline)
	sr.wg.Add(1)
	go func() {
		defer sr.wg.Done()
		coord.Run(sr.ctx)
	}()

	sr.setState(StationRunning, "")

	// superviseShutdown owns the transition to StationStopped: it fires
	// exactly once per Start call, independent of how many times (or with
	// what timeout) Stop is called, so a Stop that times out still converges
	// to Stopped in the background once goroutines actually drain.
	go sr.superviseShutdown()

	return nil
}

// superviseShutdown waits for every Start-launched goroutine to exit, then
// marks the runtime Stopped and closes done. Runs once per Start call.
func (sr *StationRuntime) superviseShutdown() {
	sr.wg.Wait()
	sr.mu.RLock()
	done := sr.done
	sr.mu.RUnlock()
	sr.setState(StationStopped, "")
	if done != nil {
		close(done)
	}
}

// Stop gracefully stops the station runtime. It triggers a best-effort save
// immediately after cancelling the context (concurrently with the goroutines
// actually shutting down) and then waits for them to drain, bounded by ctx.
//
// Stop is legal from any non-terminal state, including StationFailed — a
// failed bridge goroutine does not stop the other station goroutines (sim
// loop, dispatcher, persistence coordinator), so they still need a proper
// shutdown. If ctx expires before the goroutines drain, Stop returns
// ErrStopTimeout and leaves the runtime in StationStopping; superviseShutdown
// will still flip it to StationStopped once the drain eventually completes.
// Callers must not treat a timed-out Stop as safe to replace — check
// LifecycleState() for StationStopped before building a new runtime for the
// same station (see FleetManager.replaceRuntime).
func (sr *StationRuntime) Stop(ctx context.Context) error {
	sr.mu.Lock()
	switch sr.lifecycleState {
	case StationStopped:
		sr.mu.Unlock()
		return nil
	case StationConfigured:
		// Never started; nothing to drain.
		sr.mu.Unlock()
		sr.setState(StationStopped, "")
		return nil
	case StationStopping:
		done := sr.done
		sr.mu.Unlock()
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: station %s", ErrStopTimeout, sr.ID)
		case <-done:
			return nil
		}
	}
	// Starting, Running, or Failed: goroutines may still be live — cancel and drain.
	if sr.cancel != nil {
		sr.cancel()
	}
	done := sr.done
	sr.mu.Unlock()
	sr.setState(StationStopping, "")

	_ = sr.SaveAll()

	select {
	case <-ctx.Done():
		slog.Warn("station stop timed out", "station_id", sr.ID)
		return fmt.Errorf("%w: station %s", ErrStopTimeout, sr.ID)
	case <-done:
		return nil
	}
}

// SaveAll persists all station state to disk.
func (sr *StationRuntime) SaveAll() error {
	var errs []error
	if err := sr.Engine.SaveState(sr.PersistDir); err != nil {
		errs = append(errs, err)
	}
	if err := sr.Timeline.SaveState(sr.PersistDir); err != nil {
		errs = append(errs, err)
	}
	if sr.SaveManagers != nil {
		sr.SaveManagers()
	}
	if sr.SaveBridgeState != nil {
		sr.SaveBridgeState()
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// Snapshot returns a point-in-time snapshot of the station runtime. Defensive
// against a partially-built runtime (nil Config/Engine) so a construction
// bug can never turn GET /stations into a nil-pointer panic.
//
// Enabled is always true here: a StationRuntime only ever exists (built and
// assigned to a ManagedStation) for a station whose effective config said to
// run it — see FleetManager.startRuntimeFor and its callers, which are all
// gated on the enabled flag. "Enabled" is a config property, not a lifecycle
// one, so it stays true across every runtime state (Starting/Running/
// Stopping/Stopped/Failed); a disabled station has no StationRuntime at all,
// and FleetManager.snapshotForLocked derives its Enabled field from config.
func (sr *StationRuntime) Snapshot() api.StationSnapshot {
	sr.mu.RLock()
	state := sr.lifecycleState
	lastErr := sr.lastErr
	sr.mu.RUnlock()
	if sr.Config == nil || sr.Engine == nil {
		return api.StationSnapshot{
			StationID:      sr.ID,
			Enabled:        true,
			LifecycleState: string(state),
			LastError:      lastErr,
		}
	}
	ocppConnected := sr.Bridge != nil && sr.Bridge.IsConnected()
	activeSessions := len(sr.Engine.GetSessionInfo())
	queueDepth := 0
	if sr.Queue != nil {
		queueDepth = sr.Queue.Len()
	}
	// The lifecycle error (bridge goroutine crashed, build failure, ...) takes
	// priority; when there isn't one and the link is down, surface the
	// bridge's own last error (e.g. "connection refused") so GET /stations
	// explains why a Running station shows Connected: false instead of
	// leaving that unexplained.
	if lastErr == "" && !ocppConnected && sr.Bridge != nil {
		lastErr = sr.Bridge.Status().LastError
	}
	return api.StationSnapshot{
		StationID:          sr.ID,
		OCPPID:             sr.Config.OCPPID,
		Enabled:            true,
		LifecycleState:     string(state),
		Connected:          ocppConnected,
		ConnectorCount:     len(sr.Engine.GetConnectorIDs()),
		ActiveSessionCount: activeSessions,
		ConnectionURL:      sr.Config.ConnectionURL,
		OCPPVersion:        sr.Config.OCPPVersion,
		QueueDepth:         queueDepth,
		LastError:          lastErr,
		UptimeSeconds:      time.Since(sr.StartTime).Seconds(),
	}
}

// buildStationRuntime creates an isolated single-station runtime from an effective
// station config. The hub is shared across stations; everything else is station-local.
// queueDir is the directory used for the offline message queue and dead-letter file.
func buildStationRuntime(stationID string, cfg *config.Config, hub *ws.Hub, persistDir, queueDir string) (*StationRuntime, error) {
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
	// finishingSimTimeout is how long a connector stays in Finishing before
	// auto-reverting to Available/Preparing. OCPP does not mandate a value;
	// this is a simulator-local grace period for observing the Finishing
	// status notification before the connector becomes reusable.
	const finishingSimTimeout = 5 * time.Second
	e.FinishingTimeout = finishingSimTimeout

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

	// ValidateIDTag backs the engine's StopTransactionOnInvalidId /
	// StopTxOnInvalidId simulation tick: an idTag is treated as invalid only
	// when the local auth list or authorization cache positively says so
	// (Blocked/Expired/Malformed); an idTag neither list knows about is left
	// alone rather than deauthorized. Called from Engine.Simulate while the
	// engine write lock is held, so it must not call back into the engine —
	// Decision() on both managers is self-contained.
	e.ValidateIDTag = func(idTag string) bool {
		now := time.Now()
		if d := localAuthReal.Decision(idTag, now); d != ocpp.AuthorizationDecisionMissing {
			return d == ocpp.AuthorizationDecisionAccepted
		}
		if d := authCache.Decision(idTag, now); d != ocpp.AuthorizationDecisionMissing {
			return d == ocpp.AuthorizationDecisionAccepted
		}
		return true
	}

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
		ID:             stationID,
		Config:         cfg,
		Engine:         e,
		Hub:            hub,
		Dispatcher:     dispatcher,
		StatusTracker:  statusTracker,
		Timeline:       timelineStore,
		LocalAuth:      localAuthReal,
		Firmware:       firmwareManager,
		Diagnostics:    diagnosticsManager,
		PersistDir:     persistDir,
		QueueDir:       queueDir,
		Queue:          messageQueue,
		DeadLetterPath: deadLetterPath,
		StartTime:      time.Now(),
		lifecycleState: StationConfigured,
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
		e.GetConfigValue = configKeys.GetConfigValue
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
		// OCPP 2.0.1 has no flat config-key namespace; map the engine's
		// v1.6-style key names onto their TxCtrlr device-model equivalents.
		// Unmapped keys return "" (engine treats that as "unset").
		dm201 := b201.DeviceModel()
		e.GetConfigValue = func(key string) string {
			switch key {
			case "StopTransactionOnEVSideDisconnect":
				return dm201.GetVariable("TxCtrlr", "", 0, "StopTxOnEVSideDisconnect").Value
			case "StopTransactionOnInvalidId":
				return dm201.GetVariable("TxCtrlr", "", 0, "StopTxOnInvalidId").Value
			default:
				return ""
			}
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

	e.OnReservationExpired = newReservationExpiredCallback(stationID, hub, bridge, dispatcher)

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
