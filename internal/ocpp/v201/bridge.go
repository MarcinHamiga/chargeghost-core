package v201

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	ocpp2 "github.com/lorenzodonini/ocpp-go/ocpp2.0.1"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	"github.com/lorenzodonini/ocpp-go/ws"

	wsapi "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
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
	diagRequestID  atomic.Int64
	heartbeatInt   int // seconds

	heartbeatMu     sync.Mutex
	heartbeatCancel context.CancelFunc

	mu          sync.Mutex
	txBuilders  map[int]*TransactionEventBuilder
	txIntToEVSE map[int]int
	nextTxInt   int

	deviceModel       *DeviceModel
	profileManager    *ChargingProfileManager201
	monitoringManager *MonitoringManager
	displayStore      *DisplayMessageStore
	costStore         *CostStore
}

// NewBridge creates a Bridge201. Call SetManagers() then Start(ctx) to connect.
func NewBridge(e *engine.Engine, hub *wsapi.Hub, cfg *config.Config, dispatcher *ocpppkg.CommandDispatcher, q queue.MessageQueue, tl *ocpppkg.TimelineLogger) *Bridge201 {
	b := &Bridge201{
		engine:       e,
		hub:          hub,
		cfg:          cfg,
		dispatcher:   dispatcher,
		queue:        q,
		tl:           tl,
		heartbeatInt: 300,
		txBuilders:   make(map[int]*TransactionEventBuilder),
		txIntToEVSE:  make(map[int]int),
	}

	b.deviceModel = NewDeviceModel()
	b.deviceModel.PopulateDefaults(cfg.ChargePointModel, cfg.ChargePointVendor, cfg.OCPPID, "1.0.0", cfg.ConnectorType, len(cfg.Connectors))

	b.profileManager = NewChargingProfileManager201()
	b.monitoringManager = NewMonitoringManager(b.deviceModel)
	b.displayStore = NewDisplayMessageStore()
	b.costStore = NewCostStore()

	wsClient := ws.NewClient()
	if cfg.OCPPPassword != nil {
		wsClient.SetBasicAuth(cfg.OCPPID, *cfg.OCPPPassword)
	} else if pw := config.GetPassword(cfg.OCPPID); pw != "" {
		wsClient.SetBasicAuth(cfg.OCPPID, pw)
	}
	wsClient.SetDisconnectedHandler(func(err error) {
		slog.Warn("OCPP 2.0.1 disconnected", "error", err)
		b.connected.Store(false)
		if b.hub != nil {
			b.hub.BroadcastMessage(wsapi.Message{
				Type: "connection_state_changed",
				Data: map[string]bool{"connected": false},
			})
		}
	})
	wsClient.SetReconnectedHandler(func() {
		slog.Info("OCPP 2.0.1 reconnected")
		b.connected.Store(true)
		if b.hub != nil {
			b.hub.BroadcastMessage(wsapi.Message{
				Type: "connection_state_changed",
				Data: map[string]bool{"connected": true},
			})
		}
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

// SetManagers injects optional managers after construction.
func (b *Bridge201) SetManagers(authCache *ocpppkg.AuthorizationCache, la ocpppkg.LocalAuthManager, fw ocpppkg.FirmwareManager, diag ocpppkg.DiagnosticsManager, dt *ocpppkg.DataTransferRegistry) {
	b.authCache = authCache
	b.localAuth = la
	b.fwManager = fw
	b.diagManager = diag
	b.dataTransfer = dt
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

// Start connects to the CSMS and runs until ctx is cancelled.
func (b *Bridge201) Start(ctx context.Context) error {
	serverURL := b.cfg.ConnectionURL
	slog.Info("OCPP 2.0.1 bridge connecting", "url", serverURL, "id", b.cfg.OCPPID)

	if err := b.cs.Start(serverURL); err != nil {
		slog.Error("OCPP 2.0.1 bridge connect failed", "error", err)
	} else {
		slog.Info("OCPP 2.0.1 connected")
		b.connected.Store(true)
		if b.hub != nil {
			b.hub.BroadcastMessage(wsapi.Message{
				Type: "connection_state_changed",
				Data: map[string]bool{"connected": true},
			})
		}
		b.dispatcher.Enqueue(ocpppkg.OCPPCommand{
			Description: "BootNotification",
			Execute:     b.SendBootNotification,
		})
	}

	<-ctx.Done()
	b.cs.Stop()
	slog.Info("OCPP 2.0.1 bridge stopped")
	return nil
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

func (b *Bridge201) completeReset() {
	b.pendingReset.Store(false)
	b.engine.NormalizeAfterReset()
	b.diagRequestID.Store(0)

	b.mu.Lock()
	clear(b.txBuilders)
	clear(b.txIntToEVSE)
	b.mu.Unlock()

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

	// StopSession triggers SendTransactionStop through the engine callback path.
	// That callback clears txBuilders asynchronously, so this immediate check can
	// still observe bridge-local transactions until the queued stop work runs.
	if !b.hasActiveBridgeTransactions() && b.pendingReset.CompareAndSwap(true, false) {
		b.completeReset()
	}
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

// drainQueue re-sends queued offline messages after reconnecting.
func (b *Bridge201) drainQueue() {
	for b.queue != nil && b.IsConnected() {
		msg, ok := b.queue.Peek()
		if !ok {
			return
		}

		var sendErr error
		switch msg.Type {
		case "TransactionEvent":
			req, err := queuedTransactionEventRequest(msg.Payload)
			if err == nil {
				cb := func(resp ocpp.Response, err error) {
					if err != nil {
						slog.Error("queued TransactionEvent failed", "error", err)
					}
				}
				sendErr = b.cs.SendRequestAsync(req, cb)
			} else {
				slog.Warn("queued TransactionEvent payload is invalid",
					"error", err,
					"expected", "*transactions.TransactionEventRequest",
					"got", fmt.Sprintf("%T", msg.Payload))
			}
		default:
			slog.Warn("unknown queued message type", "type", msg.Type)
		}

		if sendErr != nil {
			slog.Error("failed to drain queued message", "type", msg.Type, "error", sendErr)
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
