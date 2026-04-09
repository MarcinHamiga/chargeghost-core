package v201

import (
	"context"
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
	cs           ocpp2.ChargingStation
	wsClient     *ws.Client
	dispatcher   *ocpppkg.CommandDispatcher
	engine       *engine.Engine
	hub          *wsapi.Hub
	cfg          *config.Config
	authCache    *ocpppkg.AuthorizationCache
	localAuth    ocpppkg.LocalAuthManager
	queue        queue.MessageQueue
	fwManager    ocpppkg.FirmwareManager
	diagManager  ocpppkg.DiagnosticsManager
	dataTransfer *ocpppkg.DataTransferRegistry
	connected    atomic.Bool
	pendingReset atomic.Bool
	heartbeatInt int // seconds

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
func NewBridge(e *engine.Engine, hub *wsapi.Hub, cfg *config.Config, dispatcher *ocpppkg.CommandDispatcher, q queue.MessageQueue) *Bridge201 {
	b := &Bridge201{
		engine:       e,
		hub:          hub,
		cfg:          cfg,
		dispatcher:   dispatcher,
		queue:        q,
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
func (b *Bridge201) GetHeartbeatInterval() int { return b.heartbeatInt }

// ProfileManager returns the bridge's smart charging profile manager.
func (b *Bridge201) ProfileManager() *ChargingProfileManager201 { return b.profileManager }

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
	interval := time.Duration(b.heartbeatInt) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !b.connected.Load() {
				return
			}
			b.dispatcher.Enqueue(ocpppkg.OCPPCommand{
				Description: "Heartbeat",
				Execute:     b.SendHeartbeat,
			})
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
			if req, ok := msg.Payload.(*transactions.TransactionEventRequest); ok {
				cb := func(resp ocpp.Response, err error) {
					if err != nil {
						slog.Error("queued TransactionEvent failed", "error", err)
					}
				}
				sendErr = b.cs.SendRequestAsync(req, cb)
			} else {
				// NOTE: If using JsonFileQueue, payload will be map[string]interface{} after
				// deserialization (JSON unmarshal into interface{}), causing this assertion to fail.
				// TransactionEvent persistence across restarts requires a typed queue envelope.
				// This is a known limitation; in-memory queue works correctly.
				slog.Warn("queued TransactionEvent payload is wrong type",
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
