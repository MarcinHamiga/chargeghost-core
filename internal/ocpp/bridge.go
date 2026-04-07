package ocpp

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	ocpp16 "github.com/lorenzodonini/ocpp-go/ocpp1.6"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/firmware"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/localauth"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/remotetrigger"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/smartcharging"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
	"github.com/lorenzodonini/ocpp-go/ws"

	wsapi "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

// Bridge connects the engine to a CSMS via the lorenzodonini/ocpp-go library.
type Bridge struct {
	cp             ocpp16.ChargePoint
	wsClient       *ws.Client
	dispatcher     *CommandDispatcher
	engine         *engine.Engine
	hub            *wsapi.Hub
	cfg            *config.Config
	profileManager *ChargingProfileManager
	configKeys     *ConfigKeyManager
	authCache      *AuthorizationCache
	localAuth      LocalAuthManager
	queue          queue.MessageQueue
	fwManager      FirmwareManager
	diagManager    DiagnosticsManager
	dataTransfer   *DataTransferRegistry
	connected      atomic.Bool
	heartbeatInt   int // seconds
}

// NewBridge creates a Bridge. Call Start(ctx) to connect.
func NewBridge(e *engine.Engine, hub *wsapi.Hub, cfg *config.Config, dispatcher *CommandDispatcher, pm *ChargingProfileManager, configKeys *ConfigKeyManager, authCache *AuthorizationCache, la LocalAuthManager, q queue.MessageQueue, fw FirmwareManager, diag DiagnosticsManager, dt *DataTransferRegistry) *Bridge {
	b := &Bridge{
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
		heartbeatInt:   300, // default; overridden by BootNotification response
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
		// Drain offline queue.
		go b.drainQueue()
		b.dispatcher.Enqueue(OCPPCommand{
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
	if !b.IsConnected() {
		_, _ = b.queue.Enqueue(queue.QueuedMessage{
			Type:    "StartTransaction",
			Payload: map[string]interface{}{"connectorID": connectorID, "idTag": idTag, "meterStart": meterStart},
		})
		return 0, nil
	}
	req := core.NewStartTransactionRequest(connectorID, idTag, int(meterStart), types.NewDateTime(timestamp))
	if reservationID != nil {
		req.ReservationId = reservationID
	}
	resp, err := b.cp.SendRequest(req)
	if err != nil {
		return 0, err
	}
	startResp, ok := resp.(*core.StartTransactionConfirmation)
	if !ok {
		return 0, fmt.Errorf("unexpected StartTransaction response type: %T", resp)
	}
	return startResp.TransactionId, nil
}

// SendStopTransaction sends a StopTransaction request.
func (b *Bridge) SendStopTransaction(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error {
	if !b.IsConnected() {
		_, _ = b.queue.Enqueue(queue.QueuedMessage{
			Type:    "StopTransaction",
			Payload: map[string]interface{}{"transactionID": transactionID, "meterStop": meterStop, "reason": reason},
		})
		return nil
	}
	req := core.NewStopTransactionRequest(int(meterStop), types.NewDateTime(timestamp), transactionID)
	req.Reason = core.Reason(reason)

	if len(meterHistory) > 0 {
		var sampledValues []types.SampledValue
		for _, record := range meterHistory {
			sampledValues = append(sampledValues, types.SampledValue{
				Value:     fmt.Sprintf("%.2f", record.Value),
				Context:   types.ReadingContextSamplePeriodic,
				Unit:      types.UnitOfMeasureWh,
				Measurand: types.MeasurandEnergyActiveImportRegister,
			})
		}
		last := meterHistory[len(meterHistory)-1]
		ts, err := time.Parse(time.RFC3339Nano, last.Timestamp)
		if err != nil {
			ts = time.Now()
		}
		req.TransactionData = []types.MeterValue{
			{
				Timestamp:    types.NewDateTime(ts),
				SampledValue: sampledValues,
			},
		}
	}
	_, err := b.cp.SendRequest(req)
	return err
}

// SendMeterValues sends a MeterValues message.
func (b *Bridge) SendMeterValues(connectorID int, value float64, transactionID int, meterContext string) error {
	if !b.IsConnected() {
		_, _ = b.queue.Enqueue(queue.QueuedMessage{
			Type:    "MeterValues",
			Payload: map[string]interface{}{"connectorID": connectorID, "value": value, "transactionID": transactionID},
		})
		return nil
	}
	req := core.NewMeterValuesRequest(connectorID, []types.MeterValue{
		{
			Timestamp: types.NewDateTime(time.Now()),
			SampledValue: []types.SampledValue{
				{
					Value:     fmt.Sprintf("%.2f", value),
					Context:   types.ReadingContextSamplePeriodic,
					Format:    types.ValueFormatRaw,
					Measurand: types.MeasurandEnergyActiveImportRegister,
					Unit:      types.UnitOfMeasureWh,
				},
			},
		},
	})
	if transactionID != 0 {
		req.TransactionId = &transactionID
	}
	_, err := b.cp.SendRequest(req)
	return err
}

// SendAuthorize sends an Authorize request.
func (b *Bridge) SendAuthorize(idTag string) error {
	_, err := b.cp.SendRequest(core.NewAuthorizationRequest(idTag))
	return err
}

// SendDataTransfer sends a DataTransfer request.
func (b *Bridge) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
	req := core.NewDataTransferRequest(vendorID)
	req.MessageId = messageID
	if data != "" {
		req.Data = data
	}
	resp, err := b.cp.SendRequest(req)
	if err != nil {
		return "", "", err
	}
	dtResp, ok := resp.(*core.DataTransferConfirmation)
	if !ok {
		return "", "", fmt.Errorf("unexpected DataTransfer response type: %T", resp)
	}
	respData := ""
	if dtResp.Data != nil {
		respData = fmt.Sprintf("%v", dtResp.Data)
	}
	return string(dtResp.Status), respData, nil
}

// SendDiagnosticsStatusNotification sends DiagnosticsStatusNotification.
func (b *Bridge) SendDiagnosticsStatusNotification(status string) error {
	req := firmware.NewDiagnosticsStatusNotificationRequest(firmware.DiagnosticsStatus(status))
	_, err := b.cp.SendRequest(req)
	return err
}

// SendFirmwareStatusNotification sends FirmwareStatusNotification.
func (b *Bridge) SendFirmwareStatusNotification(status string) error {
	req := firmware.NewFirmwareStatusNotificationRequest(firmware.FirmwareStatus(status))
	_, err := b.cp.SendRequest(req)
	return err
}

// --- OCPPReceiver stubs (inbound handlers from CSMS) ---
// All inbound handlers are no-ops until Plan 5b wires the full transaction flow.

func (b *Bridge) OnChangeAvailability(request *core.ChangeAvailabilityRequest) (*core.ChangeAvailabilityConfirmation, error) {
	availType := string(request.Type)
	result := b.engine.SetConnectorAvailability(request.ConnectorId, availType)
	switch result {
	case "accepted":
		return core.NewChangeAvailabilityConfirmation(core.AvailabilityStatusAccepted), nil
	case "scheduled":
		return core.NewChangeAvailabilityConfirmation(core.AvailabilityStatusScheduled), nil
	default:
		return core.NewChangeAvailabilityConfirmation(core.AvailabilityStatusRejected), nil
	}
}

func (b *Bridge) OnChangeConfiguration(request *core.ChangeConfigurationRequest) (*core.ChangeConfigurationConfirmation, error) {
	result := b.configKeys.SetConfigValue(request.Key, request.Value)
	switch result {
	case "Accepted":
		b.hub.BroadcastMessage(wsapi.Message{
			Type: "ocpp_config_key_changed",
			Data: map[string]string{"key": request.Key, "value": request.Value},
		})
		return core.NewChangeConfigurationConfirmation(core.ConfigurationStatusAccepted), nil
	case "Rejected":
		return core.NewChangeConfigurationConfirmation(core.ConfigurationStatusRejected), nil
	default:
		return core.NewChangeConfigurationConfirmation(core.ConfigurationStatusNotSupported), nil
	}
}

func (b *Bridge) OnClearCache(request *core.ClearCacheRequest) (*core.ClearCacheConfirmation, error) {
	b.authCache.Clear()
	return core.NewClearCacheConfirmation(core.ClearCacheStatusAccepted), nil
}

func (b *Bridge) OnDataTransfer(request *core.DataTransferRequest) (*core.DataTransferConfirmation, error) {
	messageID := request.MessageId
	data := ""
	if request.Data != nil {
		data = fmt.Sprintf("%v", request.Data)
	}
	status, responseData := b.dataTransfer.Dispatch(request.VendorId, messageID, messageID, data)
	resp := core.NewDataTransferConfirmation(core.DataTransferStatus(status))
	if responseData != "" {
		resp.Data = responseData
	}
	return resp, nil
}

func (b *Bridge) OnUpdateFirmware(request *firmware.UpdateFirmwareRequest) (*firmware.UpdateFirmwareConfirmation, error) {
	retrieveDate := request.RetrieveDate.Time
	err := b.fwManager.TriggerUpdate(request.Location, retrieveDate)
	if err != nil {
		slog.Warn("firmware update trigger failed", "error", err)
	}
	return firmware.NewUpdateFirmwareConfirmation(), nil
}

func (b *Bridge) OnGetDiagnostics(request *firmware.GetDiagnosticsRequest) (*firmware.GetDiagnosticsConfirmation, error) {
	retries := 0
	retryInterval := 30
	if request.Retries != nil {
		retries = *request.Retries
	}
	if request.RetryInterval != nil {
		retryInterval = *request.RetryInterval
	}
	err := b.diagManager.TriggerUpload(request.Location, retries, retryInterval)
	if err != nil {
		slog.Warn("diagnostics upload trigger failed", "error", err)
		return firmware.NewGetDiagnosticsConfirmation(), nil
	}
	resp := firmware.NewGetDiagnosticsConfirmation()
	resp.FileName = "diagnostics.tgz"
	return resp, nil
}

func (b *Bridge) OnGetConfiguration(request *core.GetConfigurationRequest) (*core.GetConfigurationConfirmation, error) {
	allKeys := b.configKeys.GetConfigKeyInfo()
	knownKeys := make([]core.ConfigurationKey, 0)
	unknownKeys := make([]string, 0)

	requested := request.Key
	if len(requested) == 0 {
		// Return all keys.
		for _, k := range allKeys {
			val := k.Value
			knownKeys = append(knownKeys, core.ConfigurationKey{
				Key:      k.Key,
				Readonly: k.ReadOnly,
				Value:    &val,
			})
		}
	} else {
		keyMap := make(map[string]ConfigKeyInfo, len(allKeys))
		for _, k := range allKeys {
			keyMap[k.Key] = k
		}
		for _, reqKey := range requested {
			if k, ok := keyMap[reqKey]; ok {
				val := k.Value
				knownKeys = append(knownKeys, core.ConfigurationKey{
					Key:      k.Key,
					Readonly: k.ReadOnly,
					Value:    &val,
				})
			} else {
				unknownKeys = append(unknownKeys, reqKey)
			}
		}
	}
	resp := core.NewGetConfigurationConfirmation(knownKeys)
	if len(unknownKeys) > 0 {
		resp.UnknownKey = unknownKeys
	}
	return resp, nil
}

func (b *Bridge) OnRemoteStartTransaction(request *core.RemoteStartTransactionRequest) (*core.RemoteStartTransactionConfirmation, error) {
	connectorID := 1 // default
	if request.ConnectorId != nil {
		connectorID = *request.ConnectorId
	}

	var profile *engine.ChargingProfile
	if request.ChargingProfile != nil {
		profile = convertChargingProfile(request.ChargingProfile, connectorID)
	}

	idTag := request.IdTag
	err := b.engine.StartSession(connectorID, -1, 0.0, &idTag, 30)
	if err != nil {
		return core.NewRemoteStartTransactionConfirmation(types.RemoteStartStopStatusRejected), nil
	}

	if session := b.engine.GetSession(connectorID); session != nil && profile != nil {
		session.RemoteStartChargingProfile = profile
	}

	return core.NewRemoteStartTransactionConfirmation(types.RemoteStartStopStatusAccepted), nil
}

func (b *Bridge) OnRemoteStopTransaction(request *core.RemoteStopTransactionRequest) (*core.RemoteStopTransactionConfirmation, error) {
	connectorID := b.engine.GetConnectorByTransaction(request.TransactionId)
	if connectorID == nil {
		return core.NewRemoteStopTransactionConfirmation(types.RemoteStartStopStatusRejected), nil
	}
	b.engine.StopSession(connectorID, "Remote")
	return core.NewRemoteStopTransactionConfirmation(types.RemoteStartStopStatusAccepted), nil
}

func (b *Bridge) OnReset(request *core.ResetRequest) (*core.ResetConfirmation, error) {
	reason := "SoftReset"
	if request.Type == core.ResetTypeHard {
		reason = "HardReset"
	}
	for _, id := range b.engine.GetConnectorIDs() {
		cid := id
		b.engine.StopSession(&cid, reason)
	}
	return core.NewResetConfirmation(core.ResetStatusAccepted), nil
}

func (b *Bridge) OnUnlockConnector(request *core.UnlockConnectorRequest) (*core.UnlockConnectorConfirmation, error) {
	return core.NewUnlockConnectorConfirmation(core.UnlockStatusUnlocked), nil
}

// OnTriggerMessage handles TriggerMessage requests from the CSMS.
func (b *Bridge) OnTriggerMessage(request *remotetrigger.TriggerMessageRequest) (*remotetrigger.TriggerMessageConfirmation, error) {
	switch request.RequestedMessage {
	case remotetrigger.MessageTrigger(core.BootNotificationFeatureName):
		b.dispatcher.Enqueue(OCPPCommand{Description: "TriggerBootNotification", Execute: b.SendBootNotification})
		return remotetrigger.NewTriggerMessageConfirmation(remotetrigger.TriggerMessageStatusAccepted), nil
	case remotetrigger.MessageTrigger(core.HeartbeatFeatureName):
		b.dispatcher.Enqueue(OCPPCommand{Description: "TriggerHeartbeat", Execute: b.SendHeartbeat})
		return remotetrigger.NewTriggerMessageConfirmation(remotetrigger.TriggerMessageStatusAccepted), nil
	case remotetrigger.MessageTrigger(core.StatusNotificationFeatureName):
		connID := 1
		if request.ConnectorId != nil {
			connID = *request.ConnectorId
		}
		b.dispatcher.Enqueue(OCPPCommand{
			Description: "TriggerStatusNotification",
			Execute: func() error {
				return b.SendStatusNotification(connID, "NoError", b.engine.GetConnectorStatus(connID))
			},
		})
		return remotetrigger.NewTriggerMessageConfirmation(remotetrigger.TriggerMessageStatusAccepted), nil
	case remotetrigger.MessageTrigger(core.MeterValuesFeatureName):
		connID := 1
		if request.ConnectorId != nil {
			connID = *request.ConnectorId
		}
		b.dispatcher.Enqueue(OCPPCommand{
			Description: "TriggerMeterValues",
			Execute: func() error {
				reading, txID := b.engine.GetMeterSnapshot(connID)
				return b.SendMeterValues(connID, reading, txID, "Trigger")
			},
		})
		return remotetrigger.NewTriggerMessageConfirmation(remotetrigger.TriggerMessageStatusAccepted), nil
	default:
		return remotetrigger.NewTriggerMessageConfirmation(remotetrigger.TriggerMessageStatusNotImplemented), nil
	}
}

// --- LocalAuthList inbound handlers ---

func (b *Bridge) OnGetLocalListVersion(request *localauth.GetLocalListVersionRequest) (*localauth.GetLocalListVersionConfirmation, error) {
	version := b.localAuth.GetVersion()
	return localauth.NewGetLocalListVersionConfirmation(version), nil
}

func (b *Bridge) OnSendLocalList(request *localauth.SendLocalListRequest) (*localauth.SendLocalListConfirmation, error) {
	entries := make([]LocalAuthEntry, 0, len(request.LocalAuthorizationList))
	for _, e := range request.LocalAuthorizationList {
		entry := LocalAuthEntry{IDTag: e.IdTag, Status: "Accepted"}
		if e.IdTagInfo != nil {
			entry.Status = string(e.IdTagInfo.Status)
			if e.IdTagInfo.ExpiryDate != nil {
				t := e.IdTagInfo.ExpiryDate.Time
				entry.Expiry = &t
			}
		}
		entries = append(entries, entry)
	}
	updateType := string(request.UpdateType) // "Full" or "Differential"
	if err := b.localAuth.UpdateList(request.ListVersion, entries, updateType); err != nil {
		return localauth.NewSendLocalListConfirmation(localauth.UpdateStatusFailed), nil
	}
	return localauth.NewSendLocalListConfirmation(localauth.UpdateStatusAccepted), nil
}

// drainQueue re-sends queued offline messages after reconnecting.
func (b *Bridge) drainQueue() {
	for {
		msg, ok := b.queue.Peek()
		if !ok {
			return
		}
		slog.Info("draining queued message", "type", msg.Type, "id", msg.ID)
		b.queue.Dequeue(msg.ID)
	}
}

// --- SmartCharging inbound handlers ---

func (b *Bridge) OnSetChargingProfile(request *smartcharging.SetChargingProfileRequest) (*smartcharging.SetChargingProfileConfirmation, error) {
	profile := convertChargingProfile(request.ChargingProfile, request.ConnectorId)
	if profile == nil {
		return smartcharging.NewSetChargingProfileConfirmation(smartcharging.ChargingProfileStatusRejected), nil
	}
	if err := b.profileManager.SetChargingProfile(request.ConnectorId, *profile); err != nil {
		return smartcharging.NewSetChargingProfileConfirmation(smartcharging.ChargingProfileStatusRejected), nil
	}
	b.hub.BroadcastMessage(wsapi.Message{
		Type: "charging_profile_changed",
		Data: map[string]interface{}{"action": "set", "profile_id": profile.ProfileID},
	})
	return smartcharging.NewSetChargingProfileConfirmation(smartcharging.ChargingProfileStatusAccepted), nil
}

func (b *Bridge) OnClearChargingProfile(request *smartcharging.ClearChargingProfileRequest) (*smartcharging.ClearChargingProfileConfirmation, error) {
	var connID, profileID *int
	var purpose *string
	if request.Id != nil {
		id := *request.Id
		profileID = &id
	}
	if request.ConnectorId != nil {
		cid := *request.ConnectorId
		connID = &cid
	}
	if request.ChargingProfilePurpose != "" {
		p := string(request.ChargingProfilePurpose)
		purpose = &p
	}
	_ = b.profileManager.ClearChargingProfile(connID, profileID, purpose, nil)
	b.hub.BroadcastMessage(wsapi.Message{
		Type: "charging_profile_changed",
		Data: map[string]interface{}{"action": "cleared"},
	})
	return smartcharging.NewClearChargingProfileConfirmation(smartcharging.ClearChargingProfileStatusAccepted), nil
}

func (b *Bridge) OnGetCompositeSchedule(request *smartcharging.GetCompositeScheduleRequest) (*smartcharging.GetCompositeScheduleConfirmation, error) {
	connID := request.ConnectorId
	duration := request.Duration
	now := time.Now()

	c := b.engine.GetConnector(connID)
	if c == nil {
		return smartcharging.NewGetCompositeScheduleConfirmation(smartcharging.GetCompositeScheduleStatusRejected), nil
	}

	session := b.engine.GetSession(connID)
	var txStart *time.Time
	var txID int
	if session != nil {
		t := session.StartTime
		txStart = &t
		txID = session.TransactionID
	}

	periods, err := b.profileManager.GetCompositeSchedule(connID, txID, now, duration, c.Voltage, txStart, c.Phase)
	if err != nil {
		return smartcharging.NewGetCompositeScheduleConfirmation(smartcharging.GetCompositeScheduleStatusRejected), nil
	}

	ocppPeriods := make([]types.ChargingSchedulePeriod, 0, len(periods))
	for _, p := range periods {
		ocppPeriods = append(ocppPeriods, types.ChargingSchedulePeriod{
			StartPeriod: p.StartPeriod,
			Limit:       p.Limit,
		})
	}

	resp := smartcharging.NewGetCompositeScheduleConfirmation(smartcharging.GetCompositeScheduleStatusAccepted)
	resp.ConnectorId = &connID
	resp.ScheduleStart = types.NewDateTime(now)
	resp.ChargingSchedule = &types.ChargingSchedule{
		Duration:               &duration,
		ChargingRateUnit:       types.ChargingRateUnitAmperes,
		ChargingSchedulePeriod: ocppPeriods,
	}
	return resp, nil
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
