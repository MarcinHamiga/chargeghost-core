package v16

import (
	"context"
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
	}

	wsClient, err := ocpp.NewWebSocketClient(cfg)
	if err != nil {
		b.startupErr = err
		wsClient = ws.NewClient()
	}
	wsClient.SetDisconnectedHandler(func(err error) {
		slog.Warn("OCPP disconnected", "error", err)
		b.connected.Store(false)
		b.hub.BroadcastMessage(wsapi.Message{
			Type: "connection_state_changed",
			Data: map[string]bool{"connected": false},
		})
	})
	wsClient.SetReconnectedHandler(func() {
		slog.Info("OCPP reconnected")
		b.connected.Store(true)
		b.hub.BroadcastMessage(wsapi.Message{
			Type: "connection_state_changed",
			Data: map[string]bool{"connected": true},
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
		b.hub.BroadcastMessage(wsapi.Message{
			Type: "connection_state_changed",
			Data: map[string]bool{"connected": true},
		})
		b.dispatcher.Enqueue(ocpp.OCPPCommand{
			Description: "BootNotification",
			Execute:     b.SendBootNotification,
		})
	}

	<-ctx.Done()
	b.cp.Stop()
	slog.Info("OCPP bridge stopped")
	return nil
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
func (b *Bridge16) SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error {
	return b.SendStopTransaction(meterStop, timestamp, transactionID, reason, meterHistory)
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
	for {
		if !b.IsConnected() {
			return
		}
		msg, ok := b.queue.Peek()
		if !ok {
			return
		}
		slog.Info("draining queued message", "type", msg.Type, "id", msg.ID)

		payload, ok := msg.Payload.(map[string]interface{})
		if !ok {
			slog.Warn("drainQueue: unexpected payload type, discarding", "type", msg.Type, "payloadType", fmt.Sprintf("%T", msg.Payload))
			b.queue.Dequeue(msg.ID)
			continue
		}

		var sendErr error
		switch msg.Type {
		case "StartTransaction":
			connectorID := asInt(payload["connectorID"])
			idTag := asStr(payload["idTag"])
			meterStart := asFloat(payload["meterStart"])
			txID, err := b.SendStartTransaction(connectorID, idTag, meterStart, time.Now(), nil)
			if err != nil {
				sendErr = err
			} else if txID != 0 {
				b.engine.SetActiveTransaction(connectorID, txID)
			}
		case "StopTransaction":
			transactionID := asInt(payload["transactionID"])
			meterStop := asFloat(payload["meterStop"])
			reason := asStr(payload["reason"])
			sendErr = b.SendStopTransaction(meterStop, time.Now(), transactionID, reason, nil)
		case "MeterValues":
			connectorID := asInt(payload["connectorID"])
			value := asFloat(payload["value"])
			transactionID := asInt(payload["transactionID"])
			meterContext := asStr(payload["context"])
			if meterContext == "" {
				meterContext = string(types.ReadingContextSamplePeriodic)
			}
			sendErr = b.SendMeterValues(connectorID, value, transactionID, meterContext)
		default:
			slog.Warn("drainQueue: unknown message type, discarding", "type", msg.Type)
		}

		if sendErr != nil {
			slog.Error("drainQueue: send failed, stopping drain", "type", msg.Type, "error", sendErr)
			return
		}
		b.queue.Dequeue(msg.ID)
	}
}

// asInt converts interface{} to int, handling both in-memory (int) and JSON-deserialized (float64) types.
func asInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	}
	return 0
}

// asFloat converts interface{} to float64.
func asFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

// asStr converts interface{} to string.
func asStr(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
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
