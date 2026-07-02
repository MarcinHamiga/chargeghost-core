package v16

import (
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/firmware"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/localauth"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/remotetrigger"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/reservation"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/smartcharging"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"

	wsapi "github.com/chargeghost/engine/internal/api/ws"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpp "github.com/chargeghost/engine/internal/ocpp"
)

func (b *Bridge16) OnChangeAvailability(request *core.ChangeAvailabilityRequest) (*core.ChangeAvailabilityConfirmation, error) {
	b.tl.LogInbound("ChangeAvailability", ocpp.IntPtr(request.ConnectorId), fmt.Sprintf("connector=%d type=%s", request.ConnectorId, request.Type), nil, "")
	availType := string(request.Type)
	if request.ConnectorId == 0 {
		allAccepted := true
		anyScheduled := false
		for _, id := range b.engine.GetConnectorIDs() {
			result := b.engine.SetConnectorAvailability(id, availType)
			if result == "rejected" {
				allAccepted = false
			} else if result == "scheduled" {
				anyScheduled = true
			}
		}
		if allAccepted && !anyScheduled {
			return core.NewChangeAvailabilityConfirmation(core.AvailabilityStatusAccepted), nil
		} else if anyScheduled {
			return core.NewChangeAvailabilityConfirmation(core.AvailabilityStatusScheduled), nil
		}
		return core.NewChangeAvailabilityConfirmation(core.AvailabilityStatusRejected), nil
	}
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
	b.tl.LogInbound("ChangeConfiguration", nil, fmt.Sprintf("key=%s value=%s", request.Key, request.Value), nil, "")
	result := b.configKeys.SetConfigValue(request.Key, request.Value)
	switch result {
	case "Accepted":
		b.broadcastWS(wsapi.Message{
			Type: "ocpp_config_key_changed",
			Data: map[string]string{"key": request.Key, "value": request.Value},
		})
		// If the heartbeat interval changed, apply it immediately to the running loop.
		if request.Key == "HeartbeatInterval" {
			if val, err := strconv.Atoi(request.Value); err == nil && val > 0 {
				b.heartbeatInt = val
				b.restartHeartbeat()
			}
		}
		return core.NewChangeConfigurationConfirmation(core.ConfigurationStatusAccepted), nil
	case "Rejected":
		return core.NewChangeConfigurationConfirmation(core.ConfigurationStatusRejected), nil
	default:
		return core.NewChangeConfigurationConfirmation(core.ConfigurationStatusNotSupported), nil
	}
}

func (b *Bridge16) OnClearCache(request *core.ClearCacheRequest) (*core.ClearCacheConfirmation, error) {
	b.tl.LogInbound("ClearCache", nil, "ClearCache", nil, "")
	b.authCache.Clear()
	return core.NewClearCacheConfirmation(core.ClearCacheStatusAccepted), nil
}

func (b *Bridge16) OnDataTransfer(request *core.DataTransferRequest) (*core.DataTransferConfirmation, error) {
	b.tl.LogInbound("DataTransfer", nil, fmt.Sprintf("vendor=%s messageId=%s", request.VendorId, request.MessageId), nil, "")
	messageID := request.MessageId
	data := ocpp.DataTransferDataString(request.Data)
	status, responseData := b.dataTransfer.Dispatch(request.VendorId, messageID, messageID, data)
	resp := core.NewDataTransferConfirmation(core.DataTransferStatus(status))
	if responseData != "" {
		resp.Data = responseData
	}
	return resp, nil
}

func (b *Bridge16) OnUpdateFirmware(request *firmware.UpdateFirmwareRequest) (*firmware.UpdateFirmwareConfirmation, error) {
	b.tl.LogInbound("UpdateFirmware", nil, fmt.Sprintf("location=%s", request.Location), nil, "")
	retrieveDate := request.RetrieveDate.Time
	err := b.fwManager.TriggerUpdate(request.Location, retrieveDate)
	if err != nil {
		slog.Warn("firmware update trigger failed", "error", err)
	}
	return firmware.NewUpdateFirmwareConfirmation(), nil
}

func (b *Bridge16) OnGetDiagnostics(request *firmware.GetDiagnosticsRequest) (*firmware.GetDiagnosticsConfirmation, error) {
	b.tl.LogInbound("GetDiagnostics", nil, fmt.Sprintf("location=%s", request.Location), nil, "")
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
	b.tl.LogInbound("GetConfiguration", nil, fmt.Sprintf("keys=%v", request.Key), nil, "")
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
	connectorID := 1
	if request.ConnectorId != nil {
		connectorID = *request.ConnectorId
	}
	b.tl.LogInbound("RemoteStartTransaction", ocpp.IntPtr(connectorID), fmt.Sprintf("connector=%d idTag=%s", connectorID, request.IdTag), nil, "")

	// Per OCPP 1.6 §4.2.1: while not registered (BootNotification not yet
	// Accepted), the Charge Point SHALL NOT act on requests that would
	// generate further traffic like StartTransaction.
	if !b.registered.Load() {
		slog.Warn("RemoteStartTransaction rejected: not registered with CSMS", "connector", connectorID)
		return core.NewRemoteStartTransactionConfirmation(types.RemoteStartStopStatusRejected), nil
	}

	if !b.admitRemoteStart(request.IdTag, time.Now()) {
		return core.NewRemoteStartTransactionConfirmation(types.RemoteStartStopStatusRejected), nil
	}

	var profile *engine.ChargingProfile
	if request.ChargingProfile != nil {
		profile = convertChargingProfile(request.ChargingProfile, connectorID)
	}

	idTag := request.IdTag
	err := b.engine.StartSession(connectorID, -1, &idTag, 30)
	if err != nil {
		return core.NewRemoteStartTransactionConfirmation(types.RemoteStartStopStatusRejected), nil
	}

	if profile != nil {
		// Register with the profile manager immediately so GetLimit picks it up
		// as soon as charging starts — the v1.6 manager scopes limits by
		// connectorID only (no transaction-id matching), so it is safe to
		// register before the session (or even the plug-in) exists.
		if err := b.profileManager.SetChargingProfile(connectorID, *profile); err != nil {
			slog.Warn("RemoteStartTransaction: failed to register charging profile", "connector", connectorID, "error", err)
		}
		// If the EV was already connected, StartSession started the session immediately
		// and we can set the profile directly on it.  If the EV was not yet connected,
		// StartSession stored a PendingRemoteStart — store the profile there so it is
		// applied when the EV plugs in and the pending entry is consumed.
		if b.engine.GetSession(connectorID) != nil {
			b.engine.SetSessionChargingProfile(connectorID, profile)
		} else {
			b.engine.SetPendingRemoteStartChargingProfile(connectorID, profile)
		}
	}

	return core.NewRemoteStartTransactionConfirmation(types.RemoteStartStopStatusAccepted), nil
}

func (b *Bridge16) OnRemoteStopTransaction(request *core.RemoteStopTransactionRequest) (*core.RemoteStopTransactionConfirmation, error) {
	b.tl.LogInbound("RemoteStopTransaction", nil, fmt.Sprintf("txId=%d", request.TransactionId), nil, "")
	connectorID := b.engine.GetConnectorByTransaction(request.TransactionId)
	if connectorID == nil {
		return core.NewRemoteStopTransactionConfirmation(types.RemoteStartStopStatusRejected), nil
	}
	b.engine.StopSession(connectorID, "Remote")
	return core.NewRemoteStopTransactionConfirmation(types.RemoteStartStopStatusAccepted), nil
}

// OnReset handles both Reset types per OCPP 1.6 §5.15: a Hard reset restarts
// the hardware and a Soft reset restarts the application — but both SHALL
// stop any ongoing transaction (as if StopTransaction were called) before
// restarting; a Soft reset must not merely wait for transactions to end on
// their own, since a driver mid-charge would never let it complete.
func (b *Bridge16) OnReset(request *core.ResetRequest) (*core.ResetConfirmation, error) {
	b.tl.LogInbound("Reset", nil, fmt.Sprintf("type=%s", request.Type), nil, "")

	reason := "HardReset"
	if request.Type == core.ResetTypeSoft {
		reason = "SoftReset"
	}
	for _, id := range b.engine.GetConnectorIDs() {
		cid := id
		b.engine.StopSession(&cid, reason)
	}
	b.engine.NormalizeAfterReset()
	// A reset restarts the application (or hardware), so the Charge Point
	// must re-register with a fresh BootNotification before sending any
	// further traffic — same as after a physical power-on. This is set
	// here (not before StopSession above) so the already-enqueued
	// StopTransaction for the interrupted session is unaffected.
	b.registered.Store(false)
	b.dispatcher.Enqueue(ocpp.OCPPCommand{
		Description: "BootNotification (post-reset)",
		Execute:     b.SendBootNotification,
	})
	return core.NewResetConfirmation(core.ResetStatusAccepted), nil
}

func (b *Bridge16) OnUnlockConnector(request *core.UnlockConnectorRequest) (*core.UnlockConnectorConfirmation, error) {
	b.tl.LogInbound("UnlockConnector", ocpp.IntPtr(request.ConnectorId), fmt.Sprintf("connector=%d", request.ConnectorId), nil, "")
	c := b.engine.GetConnector(request.ConnectorId)
	if c == nil {
		return core.NewUnlockConnectorConfirmation(core.UnlockStatusNotSupported), nil
	}
	if !c.IsLocked {
		return core.NewUnlockConnectorConfirmation(core.UnlockStatusNotSupported), nil
	}
	// Per OCPP 1.6 §5.19: unlocking a connector that has an ongoing
	// transaction stops that transaction first (as StopTransaction, reason
	// UnlockCommand) rather than leaving it dangling.
	if b.engine.GetSession(request.ConnectorId) != nil {
		cid := request.ConnectorId
		b.engine.StopSession(&cid, "UnlockCommand")
	}
	if err := b.engine.UnlockConnector(request.ConnectorId); err != nil {
		return core.NewUnlockConnectorConfirmation(core.UnlockStatusNotSupported), nil
	}
	return core.NewUnlockConnectorConfirmation(core.UnlockStatusUnlocked), nil
}

// OnTriggerMessage handles TriggerMessage requests from the CSMS.
func (b *Bridge16) OnTriggerMessage(request *remotetrigger.TriggerMessageRequest) (*remotetrigger.TriggerMessageConfirmation, error) {
	b.tl.LogInbound("TriggerMessage", nil, fmt.Sprintf("requested=%s", request.RequestedMessage), nil, "")
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
	b.tl.LogInbound("GetLocalListVersion", nil, "GetLocalListVersion", nil, "")
	version := b.localAuth.GetVersion()
	return localauth.NewGetLocalListVersionConfirmation(version), nil
}

func (b *Bridge16) OnSendLocalList(request *localauth.SendLocalListRequest) (*localauth.SendLocalListConfirmation, error) {
	b.tl.LogInbound("SendLocalList", nil, fmt.Sprintf("version=%d type=%s entries=%d", request.ListVersion, request.UpdateType, len(request.LocalAuthorizationList)), nil, "")
	// Per OCPP 1.6 spec section 9.4.1: for Differential updates, the new ListVersion
	// must be strictly greater than the current version; otherwise reject with VersionMismatch.
	if request.UpdateType == localauth.UpdateTypeDifferential {
		if request.ListVersion <= b.localAuth.GetVersion() {
			return localauth.NewSendLocalListConfirmation(localauth.UpdateStatusVersionMismatch), nil
		}
	}

	entries := make([]ocpp.LocalAuthEntry, 0, len(request.LocalAuthorizationList))
	for _, e := range request.LocalAuthorizationList {
		entry := ocpp.LocalAuthEntry{IDTag: e.IdTag, Status: "Accepted"}
		if e.IdTagInfo != nil {
			status := string(e.IdTagInfo.Status)
			if normalized, ok := ocpp.NormalizeAuthorizationStatus(status); ok {
				entry.Status = normalized
			} else {
				entry.Status = status
			}
			if e.IdTagInfo.ExpiryDate != nil {
				t := e.IdTagInfo.ExpiryDate.Time
				entry.Expiry = &t
			}
		} else {
			entry.Delete = true
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
	b.tl.LogInbound("SetChargingProfile", ocpp.IntPtr(request.ConnectorId), fmt.Sprintf("connector=%d profileId=%d", request.ConnectorId, request.ChargingProfile.ChargingProfileId), nil, "")
	profile := convertChargingProfile(request.ChargingProfile, request.ConnectorId)
	if profile == nil {
		return smartcharging.NewSetChargingProfileConfirmation(smartcharging.ChargingProfileStatusRejected), nil
	}
	if err := b.profileManager.SetChargingProfile(request.ConnectorId, *profile); err != nil {
		return smartcharging.NewSetChargingProfileConfirmation(smartcharging.ChargingProfileStatusRejected), nil
	}
	b.broadcastWS(wsapi.Message{
		Type: "charging_profile_changed",
		Data: map[string]interface{}{"action": "set", "profile_id": profile.ProfileID},
	})
	return smartcharging.NewSetChargingProfileConfirmation(smartcharging.ChargingProfileStatusAccepted), nil
}

func (b *Bridge16) OnClearChargingProfile(request *smartcharging.ClearChargingProfileRequest) (*smartcharging.ClearChargingProfileConfirmation, error) {
	b.tl.LogInbound("ClearChargingProfile", nil, "ClearChargingProfile", nil, "")
	var connID, profileID *int
	var purpose, stackLevel *string
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
	if request.StackLevel != nil {
		s := strconv.Itoa(*request.StackLevel)
		stackLevel = &s
	}
	cleared := b.profileManager.clearChargingProfiles(connID, profileID, purpose, stackLevel)
	if cleared == 0 {
		return smartcharging.NewClearChargingProfileConfirmation(smartcharging.ClearChargingProfileStatusUnknown), nil
	}
	b.broadcastWS(wsapi.Message{
		Type: "charging_profile_changed",
		Data: map[string]interface{}{"action": "cleared"},
	})
	return smartcharging.NewClearChargingProfileConfirmation(smartcharging.ClearChargingProfileStatusAccepted), nil
}

func (b *Bridge16) OnGetCompositeSchedule(request *smartcharging.GetCompositeScheduleRequest) (*smartcharging.GetCompositeScheduleConfirmation, error) {
	b.tl.LogInbound("GetCompositeSchedule", ocpp.IntPtr(request.ConnectorId), fmt.Sprintf("connector=%d duration=%d", request.ConnectorId, request.Duration), nil, "")
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
	if err != nil || len(periods) == 0 {
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

// --- Reservation inbound handlers ---

func (b *Bridge16) OnReserveNow(request *reservation.ReserveNowRequest) (*reservation.ReserveNowConfirmation, error) {
	b.tl.LogInbound("ReserveNow", ocpp.IntPtr(request.ConnectorId), fmt.Sprintf("connector=%d reservationId=%d idTag=%s", request.ConnectorId, request.ReservationId, request.IdTag), nil, "")
	expiry := request.ExpiryDate.Time
	var parentIDTag *string
	if request.ParentIdTag != "" {
		p := request.ParentIdTag
		parentIDTag = &p
	}
	result := b.engine.ReserveConnector(
		request.ConnectorId,
		request.ReservationId,
		request.IdTag,
		expiry,
		parentIDTag,
	)
	switch result {
	case "accepted":
		b.broadcastWS(wsapi.Message{
			Type: "reservation_changed",
			Data: map[string]interface{}{
				"action":         "created",
				"reservation_id": request.ReservationId,
				"connector_id":   request.ConnectorId,
				"id_tag":         request.IdTag,
			},
		})
		return reservation.NewReserveNowConfirmation(reservation.ReservationStatusAccepted), nil
	case "occupied":
		return reservation.NewReserveNowConfirmation(reservation.ReservationStatusOccupied), nil
	case "faulted":
		return reservation.NewReserveNowConfirmation(reservation.ReservationStatusFaulted), nil
	case "unavailable":
		return reservation.NewReserveNowConfirmation(reservation.ReservationStatusUnavailable), nil
	default:
		return reservation.NewReserveNowConfirmation(reservation.ReservationStatusRejected), nil
	}
}

func (b *Bridge16) OnCancelReservation(request *reservation.CancelReservationRequest) (*reservation.CancelReservationConfirmation, error) {
	b.tl.LogInbound("CancelReservation", nil, fmt.Sprintf("reservationId=%d", request.ReservationId), nil, "")
	result := b.engine.CancelReservation(request.ReservationId)
	if result == "accepted" {
		b.broadcastWS(wsapi.Message{
			Type: "reservation_changed",
			Data: map[string]interface{}{
				"action":         "cancelled",
				"reservation_id": request.ReservationId,
			},
		})
		return reservation.NewCancelReservationConfirmation(reservation.CancelReservationStatusAccepted), nil
	}
	return reservation.NewCancelReservationConfirmation(reservation.CancelReservationStatusRejected), nil
}
