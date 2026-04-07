package ocpp

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	ocpp16 "github.com/lorenzodonini/ocpp-go/ocpp1.6"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
	"github.com/lorenzodonini/ocpp-go/ws"

	engine "github.com/chargeghost/engine/internal/engine"
	wsapi "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
)

// Bridge connects the engine to a CSMS via the lorenzodonini/ocpp-go library.
type Bridge struct {
	cp           ocpp16.ChargePoint
	wsClient     *ws.Client
	dispatcher   *CommandDispatcher
	engine       *engine.Engine
	hub          *wsapi.Hub
	cfg          *config.Config
	connected    atomic.Bool
	heartbeatInt int // seconds
}

// NewBridge creates a Bridge. Call Start(ctx) to connect.
func NewBridge(e *engine.Engine, hub *wsapi.Hub, cfg *config.Config, dispatcher *CommandDispatcher) *Bridge {
	b := &Bridge{
		engine:       e,
		hub:          hub,
		cfg:          cfg,
		dispatcher:   dispatcher,
		heartbeatInt: 300, // default; overridden by BootNotification response
	}

	// Create explicit ws client so we can register disconnect/reconnect handlers.
	wsClient := ws.NewClient()
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
		b.dispatcher.Enqueue(OCPPCommand{
			Description: "BootNotification",
			Execute:     b.SendBootNotification,
		})
	})

	b.wsClient = wsClient
	b.cp = ocpp16.NewChargePoint(cfg.OCPPID, nil, wsClient)
	b.cp.SetCoreHandler(b)

	return b
}

// IsConnected returns true when the OCPP WebSocket is connected.
func (b *Bridge) IsConnected() bool { return b.connected.Load() }

// GetHeartbeatInterval returns the CSMS-assigned heartbeat interval in seconds.
func (b *Bridge) GetHeartbeatInterval() int { return b.heartbeatInt }

// Start connects to the CSMS and runs until ctx is cancelled.
func (b *Bridge) Start(ctx context.Context) {
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
		b.dispatcher.Enqueue(OCPPCommand{
			Description: "BootNotification",
			Execute:     b.SendBootNotification,
		})
	}

	<-ctx.Done()
	b.cp.Stop()
	slog.Info("OCPP bridge stopped")
}

// SendBootNotification sends a BootNotification to the CSMS.
func (b *Bridge) SendBootNotification() error {
	resp, err := b.cp.SendRequest(core.NewBootNotificationRequest(b.cfg.ChargePointModel, b.cfg.ChargePointVendor))
	if err != nil {
		return fmt.Errorf("BootNotification send: %w", err)
	}
	bootResp, ok := resp.(*core.BootNotificationConfirmation)
	if !ok {
		return fmt.Errorf("unexpected BootNotification response type")
	}
	slog.Info("BootNotification response", "status", bootResp.Status, "interval", bootResp.Interval)

	if bootResp.Status == core.RegistrationStatusAccepted {
		b.heartbeatInt = bootResp.Interval
		// Send StatusNotification for each connector.
		for _, id := range b.engine.GetConnectorIDs() {
			connID := id
			b.dispatcher.Enqueue(OCPPCommand{
				Description: fmt.Sprintf("StatusNotification connector %d", connID),
				Execute: func() error {
					return b.SendStatusNotification(connID, "NoError", b.engine.GetConnectorStatus(connID))
				},
			})
		}
		// Start heartbeat loop.
		go b.heartbeatLoop()
	}
	return nil
}

func (b *Bridge) heartbeatLoop() {
	interval := time.Duration(b.heartbeatInt) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		if !b.connected.Load() {
			return
		}
		b.dispatcher.Enqueue(OCPPCommand{
			Description: "Heartbeat",
			Execute:     b.SendHeartbeat,
		})
	}
}

// SendHeartbeat sends a Heartbeat to the CSMS.
func (b *Bridge) SendHeartbeat() error {
	_, err := b.cp.SendRequest(core.NewHeartbeatRequest())
	return err
}

// SendStatusNotification sends StatusNotification for a connector.
func (b *Bridge) SendStatusNotification(connectorID int, errorCode, status string) error {
	req := core.NewStatusNotificationRequest(
		connectorID,
		core.ChargePointErrorCode(errorCode),
		core.ChargePointStatus(status),
	)
	req.Timestamp = types.NewDateTime(time.Now())
	_, err := b.cp.SendRequest(req)
	return err
}

// SendStartTransaction sends a StartTransaction request and returns the CSMS-assigned transaction ID.
func (b *Bridge) SendStartTransaction(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	req := core.NewStartTransactionRequest(connectorID, idTag, int(meterStart), types.NewDateTime(timestamp))
	if reservationID != nil {
		req.ReservationId = reservationID
	}
	resp, err := b.cp.SendRequest(req)
	if err != nil {
		return 0, err
	}
	startResp := resp.(*core.StartTransactionConfirmation)
	return startResp.TransactionId, nil
}

// SendStopTransaction sends a StopTransaction request.
func (b *Bridge) SendStopTransaction(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error {
	req := core.NewStopTransactionRequest(int(meterStop), types.NewDateTime(timestamp), transactionID)
	req.Reason = core.Reason(reason)
	// MeterValues from history — omit for now; added in Plan 5b.
	_, err := b.cp.SendRequest(req)
	return err
}

// SendMeterValues sends a MeterValues message.
func (b *Bridge) SendMeterValues(connectorID int, value float64, transactionID int, context string) error {
	// Implemented fully in Plan 5b.
	return nil
}

// SendAuthorize sends an Authorize request.
func (b *Bridge) SendAuthorize(idTag string) error {
	_, err := b.cp.SendRequest(core.NewAuthorizationRequest(idTag))
	return err
}

// SendDataTransfer sends a DataTransfer request.
func (b *Bridge) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
	// Implemented in Plan 5e.
	return "Accepted", "", nil
}

// SendDiagnosticsStatusNotification sends DiagnosticsStatusNotification.
func (b *Bridge) SendDiagnosticsStatusNotification(status string) error {
	// Implemented in Plan 5e.
	return nil
}

// SendFirmwareStatusNotification sends FirmwareStatusNotification.
func (b *Bridge) SendFirmwareStatusNotification(status string) error {
	// Implemented in Plan 5e.
	return nil
}

// --- OCPPReceiver stubs (inbound handlers from CSMS) ---
// All inbound handlers are no-ops until Plan 5b wires the full transaction flow.

func (b *Bridge) OnChangeAvailability(request *core.ChangeAvailabilityRequest) (*core.ChangeAvailabilityConfirmation, error) {
	return core.NewChangeAvailabilityConfirmation(core.AvailabilityStatusRejected), nil
}

func (b *Bridge) OnChangeConfiguration(request *core.ChangeConfigurationRequest) (*core.ChangeConfigurationConfirmation, error) {
	return core.NewChangeConfigurationConfirmation(core.ConfigurationStatusNotSupported), nil
}

func (b *Bridge) OnClearCache(request *core.ClearCacheRequest) (*core.ClearCacheConfirmation, error) {
	return core.NewClearCacheConfirmation(core.ClearCacheStatusAccepted), nil
}

func (b *Bridge) OnDataTransfer(request *core.DataTransferRequest) (*core.DataTransferConfirmation, error) {
	return core.NewDataTransferConfirmation(core.DataTransferStatusUnknownVendorId), nil
}

func (b *Bridge) OnGetConfiguration(request *core.GetConfigurationRequest) (*core.GetConfigurationConfirmation, error) {
	return core.NewGetConfigurationConfirmation([]core.ConfigurationKey{}), nil
}

func (b *Bridge) OnRemoteStartTransaction(request *core.RemoteStartTransactionRequest) (*core.RemoteStartTransactionConfirmation, error) {
	return core.NewRemoteStartTransactionConfirmation(types.RemoteStartStopStatusRejected), nil
}

func (b *Bridge) OnRemoteStopTransaction(request *core.RemoteStopTransactionRequest) (*core.RemoteStopTransactionConfirmation, error) {
	return core.NewRemoteStopTransactionConfirmation(types.RemoteStartStopStatusRejected), nil
}

func (b *Bridge) OnReset(request *core.ResetRequest) (*core.ResetConfirmation, error) {
	return core.NewResetConfirmation(core.ResetStatusRejected), nil
}

func (b *Bridge) OnUnlockConnector(request *core.UnlockConnectorRequest) (*core.UnlockConnectorConfirmation, error) {
	return core.NewUnlockConnectorConfirmation(core.UnlockStatusUnlockFailed), nil
}
