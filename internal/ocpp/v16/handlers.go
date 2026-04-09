package v16

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/firmware"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/localauth"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/remotetrigger"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/smartcharging"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"

	wsapi "github.com/chargeghost/engine/internal/api/ws"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpp "github.com/chargeghost/engine/internal/ocpp"
)

func (b *Bridge16) OnChangeAvailability(request *core.ChangeAvailabilityRequest) (*core.ChangeAvailabilityConfirmation, error) {
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

func (b *Bridge16) OnChangeConfiguration(request *core.ChangeConfigurationRequest) (*core.ChangeConfigurationConfirmation, error) {
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

func (b *Bridge16) OnClearCache(request *core.ClearCacheRequest) (*core.ClearCacheConfirmation, error) {
	b.authCache.Clear()
	return core.NewClearCacheConfirmation(core.ClearCacheStatusAccepted), nil
}

func (b *Bridge16) OnDataTransfer(request *core.DataTransferRequest) (*core.DataTransferConfirmation, error) {
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

func (b *Bridge16) OnUpdateFirmware(request *firmware.UpdateFirmwareRequest) (*firmware.UpdateFirmwareConfirmation, error) {
	retrieveDate := request.RetrieveDate.Time
	err := b.fwManager.TriggerUpdate(request.Location, retrieveDate)
	if err != nil {
		slog.Warn("firmware update trigger failed", "error", err)
	}
	return firmware.NewUpdateFirmwareConfirmation(), nil
}

func (b *Bridge16) OnGetDiagnostics(request *firmware.GetDiagnosticsRequest) (*firmware.GetDiagnosticsConfirmation, error) {
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

func (b *Bridge16) OnGetConfiguration(request *core.GetConfigurationRequest) (*core.GetConfigurationConfirmation, error) {
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
		keyMap := make(map[string]ocpp.ConfigKeyEntry, len(allKeys))
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

func (b *Bridge16) OnRemoteStartTransaction(request *core.RemoteStartTransactionRequest) (*core.RemoteStartTransactionConfirmation, error) {
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

	if profile != nil {
		b.engine.SetSessionChargingProfile(connectorID, profile)
	}

	return core.NewRemoteStartTransactionConfirmation(types.RemoteStartStopStatusAccepted), nil
}

func (b *Bridge16) OnRemoteStopTransaction(request *core.RemoteStopTransactionRequest) (*core.RemoteStopTransactionConfirmation, error) {
	connectorID := b.engine.GetConnectorByTransaction(request.TransactionId)
	if connectorID == nil {
		return core.NewRemoteStopTransactionConfirmation(types.RemoteStartStopStatusRejected), nil
	}
	b.engine.StopSession(connectorID, "Remote")
	return core.NewRemoteStopTransactionConfirmation(types.RemoteStartStopStatusAccepted), nil
}

func (b *Bridge16) OnReset(request *core.ResetRequest) (*core.ResetConfirmation, error) {
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

func (b *Bridge16) OnUnlockConnector(request *core.UnlockConnectorRequest) (*core.UnlockConnectorConfirmation, error) {
	return core.NewUnlockConnectorConfirmation(core.UnlockStatusUnlocked), nil
}

// OnTriggerMessage handles TriggerMessage requests from the CSMS.
func (b *Bridge16) OnTriggerMessage(request *remotetrigger.TriggerMessageRequest) (*remotetrigger.TriggerMessageConfirmation, error) {
	switch request.RequestedMessage {
	case remotetrigger.MessageTrigger(core.BootNotificationFeatureName):
		b.dispatcher.Enqueue(ocpp.OCPPCommand{Description: "TriggerBootNotification", Execute: b.SendBootNotification})
		return remotetrigger.NewTriggerMessageConfirmation(remotetrigger.TriggerMessageStatusAccepted), nil
	case remotetrigger.MessageTrigger(core.HeartbeatFeatureName):
		b.dispatcher.Enqueue(ocpp.OCPPCommand{Description: "TriggerHeartbeat", Execute: b.SendHeartbeat})
		return remotetrigger.NewTriggerMessageConfirmation(remotetrigger.TriggerMessageStatusAccepted), nil
	case remotetrigger.MessageTrigger(core.StatusNotificationFeatureName):
		connID := 1
		if request.ConnectorId != nil {
			connID = *request.ConnectorId
		}
		b.dispatcher.Enqueue(ocpp.OCPPCommand{
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
		b.dispatcher.Enqueue(ocpp.OCPPCommand{
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

func (b *Bridge16) OnGetLocalListVersion(request *localauth.GetLocalListVersionRequest) (*localauth.GetLocalListVersionConfirmation, error) {
	version := b.localAuth.GetVersion()
	return localauth.NewGetLocalListVersionConfirmation(version), nil
}

func (b *Bridge16) OnSendLocalList(request *localauth.SendLocalListRequest) (*localauth.SendLocalListConfirmation, error) {
	entries := make([]ocpp.LocalAuthEntry, 0, len(request.LocalAuthorizationList))
	for _, e := range request.LocalAuthorizationList {
		entry := ocpp.LocalAuthEntry{IDTag: e.IdTag, Status: "Accepted"}
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

// --- SmartCharging inbound handlers ---

func (b *Bridge16) OnSetChargingProfile(request *smartcharging.SetChargingProfileRequest) (*smartcharging.SetChargingProfileConfirmation, error) {
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

func (b *Bridge16) OnClearChargingProfile(request *smartcharging.ClearChargingProfileRequest) (*smartcharging.ClearChargingProfileConfirmation, error) {
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

func (b *Bridge16) OnGetCompositeSchedule(request *smartcharging.GetCompositeScheduleRequest) (*smartcharging.GetCompositeScheduleConfirmation, error) {
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
