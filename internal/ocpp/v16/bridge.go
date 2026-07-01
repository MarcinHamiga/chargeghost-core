package v16

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	ocpp16 "github.com/lorenzodonini/ocpp-go/ocpp1.6"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
	"github.com/lorenzodonini/ocpp-go/ws"

	wsapi "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpp "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

// Bridge16 connects the engine to a CSMS via the lorenzodonini/ocpp-go 1.6J library.
// Implements ocpp.OCPPBridge.
type Bridge16 struct {
	cp             ocpp16.ChargePoint
	wsClient       *ws.Client
	dispatcher     *ocpp.CommandDispatcher
	engine         *engine.Engine
	hub            *wsapi.Hub
	cfg            *config.Config
	profileManager *ChargingProfileManager
	configKeys     *ConfigKeyManager
	authCache      *ocpp.AuthorizationCache
	localAuth      ocpp.LocalAuthManager
	queue          queue.MessageQueue
	fwManager      ocpp.FirmwareManager
	diagManager    ocpp.DiagnosticsManager
	dataTransfer   *ocpp.DataTransferRegistry
	tl             *ocpp.TimelineLogger
	connected      atomic.Bool
	heartbeatInt   int // seconds
	startupErr     error
	statusTracker  *ocpp.StatusTracker
	pendingReset   bool
	stationID      string

	heartbeatMu     sync.Mutex
	heartbeatCancel context.CancelFunc
}

// NewBridge creates a Bridge16. Call Start(ctx) to connect.
func NewBridge(e *engine.Engine, hub *wsapi.Hub, cfg *config.Config, dispatcher *ocpp.CommandDispatcher, pm *ChargingProfileManager, configKeys *ConfigKeyManager, authCache *ocpp.AuthorizationCache, la ocpp.LocalAuthManager, q queue.MessageQueue, fw ocpp.FirmwareManager, diag ocpp.DiagnosticsManager, dt *ocpp.DataTransferRegistry, tl *ocpp.TimelineLogger) *Bridge16 {
	b := &Bridge16{
		engine:         e,
		hub:            hub,
		cfg:            cfg,
		dispatcher:     dispatcher,
		profileManager: pm,
		configKeys:     configKeys,
		authCache:      authCache,
		localAuth:      la,
		queue:          q,
		fwManager:      fw,
		diagManager:    diag,
		dataTransfer:   dt,
		tl:             tl,
		heartbeatInt:   300, // default; overridden by BootNotification response
		statusTracker:  ocpp.NewStatusTracker(cfg.ConnectionURL, cfg.OCPPID, "1.6"),
	}

	wsClient, err := ocpp.NewWebSocketClient(cfg)
	if err != nil {
		b.startupErr = err
		wsClient = ws.NewClient()
	}
	wsClient.SetDisconnectedHandler(func(err error) {
		reason := ocpp.FormatDisconnectReason(err)
		// Log at Warn for transport-level errors, Info for graceful
		// closures so operators can tell CSMS-driven kicks from network
		// blips at a glance.
		attrs := []any{"reason", reason}
		if isGracefulClose(err) {
			slog.Info("OCPP disconnected (graceful)", attrs...)
		} else {
			slog.Warn("OCPP disconnected", attrs...)
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
		slog.Info("OCPP reconnected")
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
		// Drain offline queue.
		go b.drainQueue()
		b.dispatcher.Enqueue(ocpp.OCPPCommand{
			Description: "BootNotification",
			Execute:     b.SendBootNotification,
		})
	})

	b.wsClient = wsClient
	b.cp = ocpp16.NewChargePoint(cfg.OCPPID, nil, wsClient)
	b.cp.SetCoreHandler(b)
	b.cp.SetLocalAuthListHandler(b)
	b.cp.SetRemoteTriggerHandler(b)
	b.cp.SetSmartChargingHandler(b)
	b.cp.SetFirmwareManagementHandler(b)
	b.cp.SetReservationHandler(b)

	return b
}

// SetStationID records the owning station identifier for WebSocket broadcasts.
func (b *Bridge16) SetStationID(id string) {
	b.stationID = id
}

// SetStatusTracker overrides the bridge's default status tracker.
// Used by main.go to share a tracker between the bridge and the
// command dispatcher.
func (b *Bridge16) SetStatusTracker(t *ocpp.StatusTracker) {
	if t != nil {
		b.statusTracker = t
	}
}

// Status returns the current OCPP link health snapshot.
func (b *Bridge16) Status() ocpp.Status {
	return b.statusTracker.Snapshot(b.cfg.ConnectionURL, b.cfg.OCPPID, "1.6")
}

// IsConnected returns true when the OCPP WebSocket is connected.
func (b *Bridge16) IsConnected() bool { return b.connected.Load() }

// Dispatcher returns the bridge's command dispatcher.
func (b *Bridge16) Dispatcher() *ocpp.CommandDispatcher { return b.dispatcher }

// GetHeartbeatInterval returns the CSMS-assigned heartbeat interval in seconds.
func (b *Bridge16) GetHeartbeatInterval() int {
	interval := b.configKeys.GetHeartbeatInterval()
	if interval > 0 {
		return interval
	}
	return b.heartbeatInt
}

func (b *Bridge16) authorizationCacheDecision(idTag string, now time.Time) ocpp.AuthorizationDecision {
	if b.authCache == nil || !b.configKeys.GetAuthorizationCacheEnabled() {
		return ocpp.AuthorizationDecisionMissing
	}
	return b.authCache.Decision(idTag, now)
}

func (b *Bridge16) cacheAuthorizationDecision(idTag, status string, expiry *time.Time) {
	if b.authCache == nil || !b.configKeys.GetAuthorizationCacheEnabled() {
		return
	}
	b.authCache.Put(idTag, status, expiry)
}

func (b *Bridge16) localAuthorizationDecision(idTag string, now time.Time) ocpp.AuthorizationDecision {
	if b.localAuth == nil || !b.configKeys.GetLocalAuthListEnabled() {
		return ocpp.AuthorizationDecisionMissing
	}
	return b.localAuth.Decision(idTag, now)
}

func (b *Bridge16) admitRemoteStart(idTag string, now time.Time) bool {
	if b.IsConnected() {
		return b.SendAuthorize(idTag) == nil
	}

	decision := b.localAuthorizationDecision(idTag, now)
	if decision != ocpp.AuthorizationDecisionMissing {
		return decision == ocpp.AuthorizationDecisionAccepted
	}

	return b.authorizationCacheDecision(idTag, now) == ocpp.AuthorizationDecisionAccepted
}

// Start connects to the CSMS and runs until ctx is cancelled.
func (b *Bridge16) Start(ctx context.Context) error {
	if b.startupErr != nil {
		return b.startupErr
	}

	serverURL := b.cfg.ConnectionURL
	slog.Info("OCPP bridge connecting", "url", serverURL, "id", b.cfg.OCPPID)

	if err := b.cp.Start(serverURL); err != nil {
		slog.Error("OCPP bridge connect failed", "error", err)
	} else {
		slog.Info("OCPP connected")
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
		b.dispatcher.Enqueue(ocpp.OCPPCommand{
			Description: "BootNotification",
			Execute:     b.SendBootNotification,
		})
	}

	// Periodic drain loop: if the queue has messages and the link is
	// up but the most recent drain attempt exited (e.g. CSMS rejected
	// a queued message, or the link dropped mid-drain), retry the
	// drain every `interval` so stuck messages don't sit forever.
	if b.queue != nil {
		go b.startDrainLoop(ctx, b.drainLoopInterval())
	}

	<-ctx.Done()
	b.cp.Stop()
	slog.Info("OCPP bridge stopped")
	return nil
}

// QueueDepth returns the current number of queued offline messages.
// Returns 0 when no queue is attached.
func (b *Bridge16) QueueDepth() int {
	if b.queue == nil {
		return 0
	}
	return b.queue.Len()
}

// drainLoopInterval returns the periodic drain retry interval. Defaults
// to 30s; can be overridden via the HeartbeatInterval config key (re-used
// since v1.6 has no dedicated TransactionMessageRetryInterval variable)
// or stays at 30s when not configured.
func (b *Bridge16) drainLoopInterval() time.Duration {
	// v1.6 has no OCPPCommCtrlr device model. We use 30s as a safe default
	// because v1.6 messages are typically lower volume than 2.0.1
	// TransactionEvents. Operators can tune by adjusting the queue size
	// or by restarting the service.
	return 30 * time.Second
}

// startDrainLoop launches a goroutine that periodically invokes drainQueue
// while there are messages to send. The loop respects ctx cancellation.
func (b *Bridge16) startDrainLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
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
func (b *Bridge16) Stop() {
	b.cp.Stop()
}

// SendTransactionStart wraps SendStartTransaction to satisfy ocpp.OCPPBridge.
func (b *Bridge16) SendTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	return b.SendStartTransaction(connectorID, idTag, meterStart, timestamp, reservationID)
}

// SendTransactionStop wraps SendStopTransaction to satisfy ocpp.OCPPBridge.
func (b *Bridge16) SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error {
	return b.SendStopTransaction(meterStop, timestamp, transactionID, reason, idTag, meterHistory)
}

func (b *Bridge16) MaybeCompleteReset() {
	if !b.pendingReset {
		return
	}
	for _, id := range b.engine.GetConnectorIDs() {
		if b.engine.GetSession(id) != nil {
			return
		}
	}
	b.completeReset()
}

func (b *Bridge16) completeReset() {
	b.pendingReset = false
	b.engine.NormalizeAfterReset()
	b.dispatcher.Enqueue(ocpp.OCPPCommand{
		Description: "BootNotification (post-reset)",
		Execute:     b.SendBootNotification,
	})
}

// restartHeartbeat cancels any running heartbeat loop and starts a new one.
// Safe to call concurrently.
func (b *Bridge16) restartHeartbeat() {
	b.heartbeatMu.Lock()
	if b.heartbeatCancel != nil {
		b.heartbeatCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.heartbeatCancel = cancel
	b.heartbeatMu.Unlock()
	go b.heartbeatLoopCtx(ctx)
}

func (b *Bridge16) heartbeatLoopCtx(ctx context.Context) {
	interval := time.Duration(b.GetHeartbeatInterval()) * time.Second
	if interval <= 0 {
		interval = 300 * time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	changeC := b.configKeys.ConfigChanges()
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
			b.dispatcher.Enqueue(ocpp.OCPPCommand{
				Description: "Heartbeat",
				Execute:     b.SendHeartbeat,
			})
			timer.Reset(interval)
		}
	}
}

// drainQueue re-sends queued offline messages after reconnecting.
func (b *Bridge16) drainQueue() {
	if b.queue == nil {
		return
	}

	for {
		if !b.IsConnected() {
			return
		}
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
				"retryCount", msg.RetryCount,
				"maxRetries", msg.MaxRetries,
				"lastError", msg.LastError)
			return
		}

		slog.Info("draining queued message", "type", msg.Type, "id", msg.ID)

		var sendErr error
		switch msg.Type {
		case "StartTransaction":
			payload, err := queuedStartTransactionPayload(msg.Payload)
			if err != nil {
				b.markQueuedMessageFailure(msg, err)
				return
			}
			txID, err := b.SendStartTransaction(payload.ConnectorID, payload.IDTag, payload.MeterStart, payload.Timestamp, payload.ReservationID)
			if err != nil {
				sendErr = err
			} else if txID != 0 {
				b.engine.SetActiveTransaction(payload.ConnectorID, txID)
			}
		case "StopTransaction":
			payload, err := queuedStopTransactionPayload(msg.Payload)
			if err != nil {
				b.markQueuedMessageFailure(msg, err)
				return
			}
			sendErr = b.SendStopTransaction(payload.MeterStop, payload.Timestamp, payload.TransactionID, payload.Reason, payload.IDTag, payload.MeterHistory)
		case "MeterValues":
			payload, err := queuedMeterValuesPayload(msg.Payload)
			if err != nil {
				b.markQueuedMessageFailure(msg, err)
				return
			}
			sendErr = b.sendMeterValuesAt(payload.ConnectorID, payload.Value, payload.TransactionID, payload.Context, payload.Timestamp)
		default:
			b.markQueuedMessageFailure(msg, fmt.Errorf("unknown queued message type: %s", msg.Type))
			return
		}

		if sendErr != nil {
			b.markQueuedMessageFailure(msg, sendErr)
			slog.Error("drainQueue: send failed, stopping drain", "type", msg.Type, "error", sendErr)
			return
		}
		b.queue.Dequeue(msg.ID)
	}
}

func (b *Bridge16) applyReplayPolicy(msg queue.QueuedMessage) queue.QueuedMessage {
	effectiveAttempts := b.transactionMessageAttempts()
	if msg.MaxRetries != effectiveAttempts {
		msg.MaxRetries = effectiveAttempts
		if err := b.queue.Update(msg); err != nil {
			slog.Warn("drainQueue: failed to update queued message policy", "type", msg.Type, "id", msg.ID, "error", err)
		}
	}
	return msg
}

func (b *Bridge16) retryPending(msg queue.QueuedMessage) bool {
	if msg.RetryCount == 0 || msg.LastAttemptAt == nil {
		return false
	}
	retryInterval := time.Duration(b.transactionMessageRetryInterval()) * time.Second
	if retryInterval <= 0 {
		return false
	}
	return time.Since(*msg.LastAttemptAt) < retryInterval
}

func (b *Bridge16) messageAttemptsExhausted(msg queue.QueuedMessage) bool {
	maxRetries := msg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = b.transactionMessageAttempts()
	}
	return msg.RetryCount >= maxRetries
}

func (b *Bridge16) transactionMessageAttempts() int {
	if b.configKeys == nil {
		return 3
	}
	return b.configKeys.GetTransactionMessageAttempts()
}

func (b *Bridge16) transactionMessageRetryInterval() int {
	if b.configKeys == nil {
		return 60
	}
	return b.configKeys.GetTransactionMessageRetryInterval()
}

func (b *Bridge16) markQueuedMessageFailure(msg queue.QueuedMessage, err error) {
	now := time.Now().UTC()
	msg = b.applyReplayPolicy(msg)
	if !b.messageAttemptsExhausted(msg) {
		msg.RetryCount++
	}
	msg.LastAttemptAt = &now
	msg.LastError = err.Error()
	if msg.RetryCount >= msg.MaxRetries {
		slog.Warn("drainQueue: queued message moved to exhausted state",
			"type", msg.Type,
			"id", msg.ID,
			"retryCount", msg.RetryCount,
			"maxRetries", msg.MaxRetries,
			"error", err)
	}
	if updateErr := b.queue.Update(msg); updateErr != nil {
		slog.Warn("drainQueue: failed to persist queued message failure", "type", msg.Type, "id", msg.ID, "error", updateErr)
	}
}

func queuedStartTransactionPayload(payload interface{}) (queuedStartTransaction16, error) {
	if req, ok := payload.(queuedStartTransaction16); ok {
		return req, nil
	}

	var req queuedStartTransaction16
	if err := decodeQueuedPayload(payload, &req); err == nil && req.ConnectorID > 0 && req.IDTag != "" && !req.Timestamp.IsZero() {
		return req, nil
	}

	var legacy struct {
		ConnectorID   int       `json:"connectorID"`
		IDTag         string    `json:"idTag"`
		MeterStart    float64   `json:"meterStart"`
		Timestamp     time.Time `json:"timestamp"`
		ReservationID *int      `json:"reservationID,omitempty"`
	}
	if err := decodeQueuedPayload(payload, &legacy); err != nil {
		return queuedStartTransaction16{}, err
	}
	if legacy.ConnectorID <= 0 || legacy.IDTag == "" || legacy.Timestamp.IsZero() {
		return queuedStartTransaction16{}, fmt.Errorf("invalid StartTransaction payload")
	}
	return queuedStartTransaction16{
		ConnectorID:   legacy.ConnectorID,
		IDTag:         legacy.IDTag,
		MeterStart:    legacy.MeterStart,
		Timestamp:     legacy.Timestamp,
		ReservationID: legacy.ReservationID,
	}, nil
}

func queuedStopTransactionPayload(payload interface{}) (queuedStopTransaction16, error) {
	if req, ok := payload.(queuedStopTransaction16); ok {
		return req, nil
	}

	var req queuedStopTransaction16
	if err := decodeQueuedPayload(payload, &req); err == nil && req.TransactionID > 0 && !req.Timestamp.IsZero() {
		return req, nil
	}

	var legacy struct {
		TransactionID int                  `json:"transactionID"`
		MeterStop     float64              `json:"meterStop"`
		Timestamp     time.Time            `json:"timestamp"`
		Reason        string               `json:"reason"`
		IDTag         *string              `json:"idTag,omitempty"`
		MeterHistory  []engine.MeterRecord `json:"meterHistory,omitempty"`
	}
	if err := decodeQueuedPayload(payload, &legacy); err != nil {
		return queuedStopTransaction16{}, err
	}
	if legacy.TransactionID <= 0 || legacy.Timestamp.IsZero() {
		return queuedStopTransaction16{}, fmt.Errorf("invalid StopTransaction payload")
	}
	return queuedStopTransaction16{
		TransactionID: legacy.TransactionID,
		MeterStop:     legacy.MeterStop,
		Timestamp:     legacy.Timestamp,
		Reason:        legacy.Reason,
		IDTag:         legacy.IDTag,
		MeterHistory:  legacy.MeterHistory,
	}, nil
}

func queuedMeterValuesPayload(payload interface{}) (queuedMeterValues16, error) {
	if req, ok := payload.(queuedMeterValues16); ok {
		return req, nil
	}

	var req queuedMeterValues16
	if err := decodeQueuedPayload(payload, &req); err == nil && req.ConnectorID > 0 && !req.Timestamp.IsZero() {
		return req, nil
	}

	var legacy struct {
		ConnectorID   int       `json:"connectorID"`
		Value         float64   `json:"value"`
		TransactionID int       `json:"transactionID"`
		Context       string    `json:"context"`
		Timestamp     time.Time `json:"timestamp"`
	}
	if err := decodeQueuedPayload(payload, &legacy); err != nil {
		return queuedMeterValues16{}, err
	}
	if legacy.ConnectorID <= 0 || legacy.Timestamp.IsZero() {
		return queuedMeterValues16{}, fmt.Errorf("invalid MeterValues payload")
	}
	return queuedMeterValues16{
		ConnectorID:   legacy.ConnectorID,
		Value:         legacy.Value,
		TransactionID: legacy.TransactionID,
		Context:       legacy.Context,
		Timestamp:     legacy.Timestamp,
	}, nil
}

func decodeQueuedPayload(payload interface{}, dest interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal queued payload: %w", err)
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("unmarshal queued payload: %w", err)
	}
	return nil
}

// convertChargingProfile maps the lorenzodonini ChargingProfile type to the engine type.
func convertChargingProfile(p *types.ChargingProfile, connectorID int) *engine.ChargingProfile {
	if p == nil {
		return nil
	}
	profile := &engine.ChargingProfile{
		ProfileID:   p.ChargingProfileId,
		ConnectorID: connectorID,
		StackLevel:  p.StackLevel,
		Purpose:     string(p.ChargingProfilePurpose),
		Kind:        string(p.ChargingProfileKind),
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
	if p.ChargingSchedule != nil {
		sched := engine.ChargingSchedule{
			ChargingRateUnit: string(p.ChargingSchedule.ChargingRateUnit),
			Duration:         0,
		}
		if p.ChargingSchedule.Duration != nil {
			sched.Duration = *p.ChargingSchedule.Duration
		}
		if p.ChargingSchedule.StartSchedule != nil {
			t1 := p.ChargingSchedule.StartSchedule.Time
			sched.StartSchedule = &t1
			t2 := p.ChargingSchedule.StartSchedule.Time
			profile.StartSchedule = &t2
		}
		for _, period := range p.ChargingSchedule.ChargingSchedulePeriod {
			p2 := period
			sp := engine.ChargingSchedulePeriod{
				StartPeriod: p2.StartPeriod,
				Limit:       p2.Limit,
			}
			if p2.NumberPhases != nil {
				n := *p2.NumberPhases
				sp.NumberPhases = &n
			}
			sched.Periods = append(sched.Periods, sp)
		}
		profile.Schedule = sched
	}
	return profile
}
