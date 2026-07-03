package v201

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	ocpp2 "github.com/lorenzodonini/ocpp-go/ocpp2.0.1"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/lorenzodonini/ocpp-go/ws"

	wsapi "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

const (
	connectBackoffDefault = time.Second
	connectBackoffMax     = 60 * time.Second
)

// Bridge201 connects the engine to a CSMS via the lorenzodonini/ocpp-go 2.0.1 library.
// Implements ocpppkg.OCPPBridge.
type Bridge201 struct {
	cs             ocpp2.ChargingStation
	wsClient       *ws.Client
	dispatcher     *ocpppkg.CommandDispatcher
	enqueueCommand func(ocpppkg.OCPPCommand)
	engine         *engine.Engine
	hub            *wsapi.Hub
	cfg            *config.Config
	authCache      *ocpppkg.AuthorizationCache
	localAuth      ocpppkg.LocalAuthManager
	queue          queue.MessageQueue
	fwManager      ocpppkg.FirmwareManager
	diagManager    ocpppkg.DiagnosticsManager
	dataTransfer   *ocpppkg.DataTransferRegistry
	tl             *ocpppkg.TimelineLogger
	connected      atomic.Bool
	pendingReset   atomic.Bool
	// registered reports whether the most recent BootNotification was
	// Accepted. Per OCPP 2.0.1 §B01/B02, the Charging Station SHALL NOT
	// send any other request (TransactionEvent(Started) in particular)
	// until registered. Zero value is false, matching a station that has
	// never booted.
	registered    atomic.Bool
	diagRequestID atomic.Int64
	heartbeatInt  int // seconds
	startupErr    error
	statusTracker *ocpppkg.StatusTracker
	// draining single-flights drainQueue: it can be triggered concurrently
	// by the reconnect handler, the periodic drain loop, and an explicit
	// DrainOfflineQueue call (e.g. from a REST request) — without this guard
	// two overlapping passes could both Peek the same message and send it
	// twice, or race on Dequeue/Update against the same queue entry.
	// StatusTracker.DrainInProgress is a separate, purely informational flag
	// exposed via GET /ocpp/status and does not itself prevent overlap.
	draining atomic.Bool

	heartbeatMu     sync.Mutex
	heartbeatCancel context.CancelFunc

	// connectBackoffBase is the initial delay between connect retries in
	// Start's retry loop (doubling up to connectBackoffMax). Zero means "use
	// the default" (connectBackoffDefault) — tests override this to keep
	// retry-loop tests fast without a real 1s+ wait.
	connectBackoffBase time.Duration

	mu          sync.Mutex
	txBuilders  map[int]*TransactionEventBuilder
	txIntToEVSE map[int]int
	// txStringToEVSE maps the OCPP 2.0.1 string transactionId (UUID) to its EVSE id,
	// enabling O(1) lookups of "is this string transaction id still active?" queries
	// from messages like GetTransactionStatus(request.TransactionID).
	txStringToEVSE map[string]int
	nextTxInt      int

	deviceModel       *DeviceModel
	profileManager    *ChargingProfileManager201
	monitoringManager *MonitoringManager
	displayStore      *DisplayMessageStore
	costStore         *CostStore
	stationID         string
}

// NewBridge creates a Bridge201. Call SetManagers() then Start(ctx) to connect.
func NewBridge(e *engine.Engine, hub *wsapi.Hub, cfg *config.Config, dispatcher *ocpppkg.CommandDispatcher, q queue.MessageQueue, tl *ocpppkg.TimelineLogger) *Bridge201 {
	b := &Bridge201{
		engine:         e,
		hub:            hub,
		cfg:            cfg,
		dispatcher:     dispatcher,
		queue:          q,
		tl:             tl,
		heartbeatInt:   300,
		txBuilders:     make(map[int]*TransactionEventBuilder),
		txIntToEVSE:    make(map[int]int),
		txStringToEVSE: make(map[string]int),
		statusTracker:  ocpppkg.NewStatusTracker(cfg.ConnectionURL, cfg.OCPPID, "2.0.1"),
	}

	b.deviceModel = NewDeviceModel()
	b.deviceModel.PopulateDefaults(cfg.ChargePointModel, cfg.ChargePointVendor, cfg.OCPPID, "1.0.0", cfg.ConnectorType, len(cfg.Connectors))

	b.profileManager = NewChargingProfileManager201()
	// Wire the resolver so TxProfile / TxDefaultProfile requests with a
	// zero evseId can be re-scoped to the EVSE that owns the transaction.
	b.profileManager.SetTxEvseResolver(func(txID string) (int, bool) {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.findEVSEByTxIDLocked(txID)
	})
	b.monitoringManager = NewMonitoringManager(b.deviceModel)
	b.displayStore = NewDisplayMessageStore()
	b.costStore = NewCostStore()

	wsClient, err := ocpppkg.NewWebSocketClient(cfg)
	if err != nil {
		b.startupErr = err
		wsClient = ws.NewClient()
	}
	wsClient.SetDisconnectedHandler(func(err error) {
		reason := ocpppkg.FormatDisconnectReason(err)
		// Log at Warn for transport-level errors, Info for graceful
		// closures so operators can tell CSMS-driven kicks from network
		// blips at a glance.
		attrs := []any{"reason", reason}
		if isGracefulClose(err) {
			slog.Info("OCPP 2.0.1 disconnected (graceful)", attrs...)
		} else {
			slog.Warn("OCPP 2.0.1 disconnected", attrs...)
		}
		b.connected.Store(false)
		b.statusTracker.OnDisconnect(reason)
		b.broadcastWS(wsapi.Message{
			Type: "connection_state_changed",
			Data: map[string]bool{"connected": false},
		})
		b.broadcastWS(wsapi.Message{
			Type: wsapi.MsgOCPPDisconnected,
			Data: map[string]interface{}{"reason": reason},
		})
	})
	wsClient.SetReconnectedHandler(func() {
		slog.Info("OCPP 2.0.1 reconnected")
		b.connected.Store(true)
		b.statusTracker.OnConnect()
		b.broadcastWS(wsapi.Message{
			Type: "connection_state_changed",
			Data: map[string]bool{"connected": true},
		})
		b.broadcastWS(wsapi.Message{
			Type: wsapi.MsgOCPPReconnected,
			Data: map[string]int{"reconnectCount": int(b.statusTracker.Snapshot("", "", "").ReconnectCount)},
		})
		go b.drainQueue()
		b.dispatcher.Enqueue(ocpppkg.OCPPCommand{
			Description: "BootNotification",
			Execute:     b.SendBootNotification,
		})
	})

	b.wsClient = wsClient
	b.cs = ocpp2.NewChargingStation(cfg.OCPPID, nil, wsClient)
	b.cs.SetProvisioningHandler(b)
	b.cs.SetAvailabilityHandler(b)
	b.cs.SetTransactionsHandler(b)
	b.cs.SetAuthorizationHandler(b)
	b.cs.SetRemoteControlHandler(b)
	b.cs.SetSmartChargingHandler(b)
	b.cs.SetDiagnosticsHandler(b)
	b.cs.SetDisplayHandler(b)
	b.cs.SetTariffCostHandler(b)
	b.cs.SetFirmwareHandler(b)
	b.cs.SetLocalAuthListHandler(b)
	b.cs.SetDataHandler(b)
	b.cs.SetReservationHandler(b)
	b.cs.SetISO15118Handler(b)

	return b
}

// SetStationID records the owning station identifier for WebSocket broadcasts.
func (b *Bridge201) SetStationID(id string) {
	b.stationID = id
}

// SetManagers injects optional managers after construction.
func (b *Bridge201) SetManagers(authCache *ocpppkg.AuthorizationCache, la ocpppkg.LocalAuthManager, fw ocpppkg.FirmwareManager, diag ocpppkg.DiagnosticsManager, dt *ocpppkg.DataTransferRegistry) {
	b.authCache = authCache
	b.localAuth = la
	b.fwManager = fw
	b.diagManager = diag
	b.dataTransfer = dt
}

// SetStatusTracker overrides the bridge's default status tracker.
// Used by main.go to share a tracker between the bridge and the
// command dispatcher.
func (b *Bridge201) SetStatusTracker(t *ocpppkg.StatusTracker) {
	if t != nil {
		b.statusTracker = t
	}
}

// Status returns the current OCPP link health snapshot. The v2.0.1-specific
// queue depth and drain-in-progress fields are sourced from the live queue.
func (b *Bridge201) Status() ocpppkg.Status {
	s := b.statusTracker.Snapshot(b.cfg.ConnectionURL, b.cfg.OCPPID, "2.0.1")
	if b.queue != nil {
		s.QueueDepth = b.queue.Len()
		s.QueueDropped = b.queue.Dropped()
	}
	return s
}

// IsConnected returns true when the OCPP WebSocket is connected.
func (b *Bridge201) IsConnected() bool { return b.connected.Load() }

// Dispatcher returns the bridge's command dispatcher.
func (b *Bridge201) Dispatcher() *ocpppkg.CommandDispatcher { return b.dispatcher }

// GetHeartbeatInterval returns the CSMS-assigned heartbeat interval in seconds.
func (b *Bridge201) GetHeartbeatInterval() int {
	interval := b.deviceModel.GetHeartbeatInterval()
	if interval > 0 {
		return interval
	}
	return b.heartbeatInt
}

// ProfileManager returns the bridge's smart charging profile manager.
func (b *Bridge201) ProfileManager() *ChargingProfileManager201 { return b.profileManager }

// DeviceModel returns the bridge's device model, which implements ocpppkg.ConfigKeyAPI.
func (b *Bridge201) DeviceModel() *DeviceModel { return b.deviceModel }

// Start connects to the CSMS and runs until ctx is cancelled. The initial
// dial is retried with capped exponential backoff — the underlying ocpp-go
// client only auto-reconnects after a connection that once succeeded drops;
// a dial failure on the very first attempt previously left the bridge
// permanently disconnected until something externally restarted it.
func (b *Bridge201) Start(ctx context.Context) error {
	if b.startupErr != nil {
		return b.startupErr
	}

	serverURL := b.cfg.ConnectionURL
	slog.Info("OCPP 2.0.1 bridge connecting", "url", serverURL, "id", b.cfg.OCPPID)

	backoff := b.connectBackoffBase
	if backoff <= 0 {
		backoff = connectBackoffDefault
	}
	for {
		err := b.cs.Start(serverURL)
		if err == nil {
			break
		}
		slog.Error("OCPP 2.0.1 bridge connect failed", "error", err)
		b.statusTracker.OnConnectAttemptFailed(err)
		b.broadcastWS(wsapi.Message{
			Type: "connection_state_changed",
			Data: map[string]bool{"connected": false},
		})
		select {
		case <-ctx.Done():
			slog.Info("OCPP 2.0.1 bridge stopped before connecting")
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > connectBackoffMax {
			backoff = connectBackoffMax
		}
	}

	slog.Info("OCPP 2.0.1 connected")
	b.connected.Store(true)
	b.statusTracker.OnConnect()
	b.broadcastWS(wsapi.Message{
		Type: "connection_state_changed",
		Data: map[string]bool{"connected": true},
	})
	b.broadcastWS(wsapi.Message{
		Type: wsapi.MsgOCPPConnected,
		Data: map[string]string{"url": serverURL},
	})
	b.dispatcher.Enqueue(ocpppkg.OCPPCommand{
		Description: "BootNotification",
		Execute:     b.SendBootNotification,
	})

	// Periodic drain loop: if the queue has messages and the link is
	// up but the most recent drain attempt exited (e.g. CSMS rejected
	// a queued message, or the link dropped mid-drain), retry the
	// drain every `interval` so stuck messages don't sit forever.
	if b.queue != nil {
		go b.startDrainLoop(ctx, b.drainLoopInterval())
	}

	<-ctx.Done()
	b.cs.Stop()
	b.heartbeatMu.Lock()
	if b.heartbeatCancel != nil {
		b.heartbeatCancel()
	}
	b.heartbeatMu.Unlock()
	slog.Info("OCPP 2.0.1 bridge stopped")
	return nil
}

// QueueDepth returns the current number of queued offline messages.
// Returns 0 when no queue is attached.
func (b *Bridge201) QueueDepth() int {
	if b.queue == nil {
		return 0
	}
	return b.queue.Len()
}

// findEVSEByTxIDLocked returns the EVSE id for a given string OCPP 2.0.1
// transaction id, or false if no such transaction is active.  Caller must
// hold b.mu.
func (b *Bridge201) findEVSEByTxIDLocked(txID string) (int, bool) {
	evseID, ok := b.txStringToEVSE[txID]
	if !ok {
		return 0, false
	}
	return evseID, true
}

// ActiveTxIDForEVSE returns the OCPP 2.0.1 string transactionId currently
// active on the given EVSE id, or an empty string if no transaction is active.
// The reverse of txStringToEVSE lookup.
func (b *Bridge201) ActiveTxIDForEVSE(evseID int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for txID, e := range b.txStringToEVSE {
		if e == evseID {
			return txID
		}
	}
	return ""
}

// engineHasSessionForTxID returns true if any engine session is active.
// OCPP 2.0.1 transaction ids are UUID strings; the engine tracks them as
// ints (assigned at engine layer), so we cannot do a direct string→int
// lookup.  When GetTransactionStatus is called with a txId we don't know,
// we conservatively report "ongoing" if any session is active in the
// engine, matching the pre-fix behavior of "any tx active → ongoing=true".
func (b *Bridge201) engineHasSessionForTxID(txID string) bool {
	if txID == "" {
		return len(b.engine.GetSessionInfo()) > 0
	}
	// We do not have a stable mapping from string txId → engine int txId;
	// fall back to "is any session active".
	return len(b.engine.GetSessionInfo()) > 0
}

func (b *Bridge201) MaybeCompleteReset() {
	if b.pendingReset.Load() && !b.hasActiveBridgeTransactions() {
		b.completeReset()
	}
}

func (b *Bridge201) completeReset() {
	b.pendingReset.Store(false)
	b.engine.NormalizeAfterReset()
	b.diagRequestID.Store(0)

	b.mu.Lock()
	clear(b.txBuilders)
	clear(b.txIntToEVSE)
	clear(b.txStringToEVSE)
	b.mu.Unlock()

	// A reset restarts the application, so the Charging Station must
	// re-register with a fresh BootNotification before sending any further
	// traffic — same as after a physical power-on. The already-enqueued
	// TransactionEvent(Ended) for the interrupted session(s) is unaffected
	// since it is sent by SendTransactionStop, which does not check this flag.
	b.registered.Store(false)

	b.enqueue(ocpppkg.OCPPCommand{
		Description: "BootNotification (post-reset)",
		Execute:     b.SendBootNotification,
	})
}

func (b *Bridge201) triggerReset(reason string) {
	sessions := b.engine.GetSessionInfo()
	if len(sessions) == 0 {
		b.completeReset()
		return
	}

	b.pendingReset.Store(true)
	for _, session := range sessions {
		connectorID := session.ConnectorID
		b.engine.StopSession(&connectorID, reason)
	}

	if !b.hasActiveBridgeTransactions() && b.pendingReset.CompareAndSwap(true, false) {
		b.completeReset()
	}
}

// to 30s; can be overridden via the OCPPCommCtrlr device-model variable
// `TransactionMessageRetryInterval` (in seconds) when set.
func (b *Bridge201) drainLoopInterval() time.Duration {
	secs := b.deviceModelInt("OCPPCommCtrlr", "TransactionMessageRetryInterval", 30)
	if secs <= 0 {
		secs = 30
	}
	return time.Duration(secs) * time.Second
}

// startDrainLoop launches a goroutine that periodically invokes drainQueue
// while there are messages to send. The loop respects ctx cancellation
// and never runs more than one drain at a time (drainQueue itself is
// guarded by the StatusTracker.DrainInProgress flag).
func (b *Bridge201) startDrainLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	// Kick off a drain immediately on loop start so messages that
	// accumulated before the first tick get a chance to flush.
	if b.QueueDepth() > 0 {
		b.drainQueue()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if b.QueueDepth() == 0 {
				continue
			}
			if !b.IsConnected() {
				continue
			}
			b.drainQueue()
		}
	}
}

// Stop disconnects the bridge immediately.
func (b *Bridge201) Stop() {
	b.cs.Stop()
}

func (b *Bridge201) enqueue(cmd ocpppkg.OCPPCommand) {
	if b.enqueueCommand != nil {
		b.enqueueCommand(cmd)
		return
	}
	b.dispatcher.Enqueue(cmd)
}

func (b *Bridge201) hasActiveBridgeTransactions() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.txBuilders) > 0
}

// restartHeartbeat cancels any running heartbeat loop and starts a new one.
// Safe to call concurrently.
func (b *Bridge201) restartHeartbeat() {
	b.heartbeatMu.Lock()
	if b.heartbeatCancel != nil {
		b.heartbeatCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.heartbeatCancel = cancel
	b.heartbeatMu.Unlock()
	go b.heartbeatLoopCtx(ctx)
}

func (b *Bridge201) heartbeatLoopCtx(ctx context.Context) {
	interval := time.Duration(b.GetHeartbeatInterval()) * time.Second
	if interval <= 0 {
		interval = 300 * time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	changeC := b.deviceModel.ConfigChanges()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changeC:
			next := time.Duration(b.GetHeartbeatInterval()) * time.Second
			if next <= 0 {
				next = 300 * time.Second
			}
			if next == interval {
				continue
			}
			interval = next
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(interval)
		case <-timer.C:
			if !b.connected.Load() {
				return
			}
			b.dispatcher.Enqueue(ocpppkg.OCPPCommand{
				Description: "Heartbeat",
				Execute:     b.SendHeartbeat,
			})
			timer.Reset(interval)
		}
	}
}

// DrainOfflineQueue runs one replay pass over the offline message queue.
// Satisfies ocpppkg.OCPPBridge.
func (b *Bridge201) DrainOfflineQueue() {
	b.drainQueue()
}

// drainQueue re-sends queued offline messages after reconnecting. Single-
// flighted via the draining flag — an overlapping call (from the reconnect
// handler, the periodic drain loop, or an explicit DrainOfflineQueue) is a
// no-op rather than racing the in-progress pass.
func (b *Bridge201) drainQueue() {
	if !b.draining.CompareAndSwap(false, true) {
		return
	}
	defer b.draining.Store(false)
	if b.statusTracker != nil {
		b.statusTracker.SetDrainInProgress(true)
		defer b.statusTracker.SetDrainInProgress(false)
	}
	for b.queue != nil && b.IsConnected() {
		msg, ok := b.queue.Peek()
		if !ok {
			return
		}

		msg = b.applyReplayPolicy(msg)
		if b.retryPending(msg) {
			return
		}
		if b.messageAttemptsExhausted(msg) {
			slog.Warn("drainQueue: queued message is exhausted",
				"type", msg.Type,
				"id", msg.ID,
				"idempotencyKey", formatIdempotencyKey(msg.IdempotencyKey),
				"retryCount", msg.RetryCount,
				"maxRetries", msg.MaxRetries,
				"lastError", msg.LastError)
			return
		}

		slog.Info("draining queued message", "type", msg.Type, "id", msg.ID, "idempotencyKey", formatIdempotencyKey(msg.IdempotencyKey))

		var sendErr error
		switch msg.Type {
		case "TransactionEvent":
			sendErr = b.sendQueuedTransactionEvent(msg.Payload)
		default:
			sendErr = fmt.Errorf("unknown queued message type: %s", msg.Type)
		}

		if sendErr != nil {
			b.markQueuedMessageFailure(msg, sendErr)
			slog.Error("drainQueue: send failed, stopping drain", "type", msg.Type, "error", sendErr)
			return
		}
		b.queue.Dequeue(msg.ID)
	}
}

func queuedTransactionEventRequest(payload interface{}) (*transactions.TransactionEventRequest, error) {
	if req, ok := payload.(*transactions.TransactionEventRequest); ok {
		return req, nil
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal queued payload: %w", err)
	}

	var req transactions.TransactionEventRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, fmt.Errorf("unmarshal queued payload: %w", err)
	}
	return &req, nil
}

// convertChargingProfile201 maps the OCPP 2.0.1 ChargingProfile type to the engine type.
// In 2.0.1 the profile has a `[]ChargingSchedule` (one or more) and the
// `TransactionID` is part of the profile itself (used to identify TxProfile /
// TxDefaultProfile).  We flatten the first schedule onto the engine type and
// preserve the TransactionID so downstream consumers can scope correctly.
func convertChargingProfile201(p *types.ChargingProfile, evseID int) *engine.ChargingProfile {
	if p == nil || len(p.ChargingSchedule) == 0 {
		return nil
	}
	sched0 := p.ChargingSchedule[0]
	profile := &engine.ChargingProfile{
		ProfileID:     p.ID,
		ConnectorID:   evseID,
		StackLevel:    p.StackLevel,
		Purpose:       string(p.ChargingProfilePurpose),
		Kind:          string(p.ChargingProfileKind),
		TransactionID: p.TransactionID,
	}
	if p.RecurrencyKind != "" {
		profile.RecurrencyKind = string(p.RecurrencyKind)
	}
	if p.ValidFrom != nil {
		t := p.ValidFrom.Time
		profile.ValidFrom = &t
	}
	if p.ValidTo != nil {
		t := p.ValidTo.Time
		profile.ValidTo = &t
	}
	sched := engine.ChargingSchedule{
		ChargingRateUnit: string(sched0.ChargingRateUnit),
	}
	if sched0.Duration != nil {
		sched.Duration = *sched0.Duration
	}
	if sched0.MinChargingRate != nil {
		sched.MinChargingRate = *sched0.MinChargingRate
	}
	if sched0.StartSchedule != nil {
		t1 := sched0.StartSchedule.Time
		sched.StartSchedule = &t1
		t2 := sched0.StartSchedule.Time
		profile.StartSchedule = &t2
	}
	for _, period := range sched0.ChargingSchedulePeriod {
		sp := engine.ChargingSchedulePeriod{
			StartPeriod: period.StartPeriod,
			Limit:       period.Limit,
		}
		if period.NumberPhases != nil {
			n := *period.NumberPhases
			sp.NumberPhases = &n
		}
		sched.Periods = append(sched.Periods, sp)
	}
	profile.Schedule = sched
	return profile
}
