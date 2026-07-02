package v201

import (
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/authorization"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/availability"
	data201 "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/data"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/diagnostics"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/display"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/firmware"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/iso15118"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/localauth"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/remotecontrol"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/reservation"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/smartcharging"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/tariffcost"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"

	wsapi "github.com/chargeghost/engine/internal/api/ws"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
)

// -- provisioning.ChargingStationHandler --

func (b *Bridge201) OnGetBaseReport(request *provisioning.GetBaseReportRequest) (*provisioning.GetBaseReportResponse, error) {
	b.tl.LogInbound("GetBaseReport", nil, fmt.Sprintf("requestId=%d", request.RequestID), nil, "")
	slog.Info("OCPP 2.0.1 GetBaseReport received", "requestId", request.RequestID, "reportBase", request.ReportBase)
	// Per OCPP 2.0.1 §3.21: respond Accepted synchronously, then send
	// the report asynchronously in one or more NotifyReportRequest
	// messages (one per part). We chunk by maxParts entries.
	go b.sendFullReport(request.RequestID, provisioning.ReportTypeFullInventory, nil, nil)
	return provisioning.NewGetBaseReportResponse(types.GenericDeviceModelStatusAccepted), nil
}

func (b *Bridge201) OnGetReport(request *provisioning.GetReportRequest) (*provisioning.GetReportResponse, error) {
	b.tl.LogInbound("GetReport", nil, fmt.Sprintf("count=%d", len(request.ComponentVariable)), nil, "")
	var reqID int
	if request.RequestID != nil {
		reqID = *request.RequestID
	}
	slog.Info("OCPP 2.0.1 GetReport received", "requestId", reqID, "criteria", request.ComponentCriteria, "vars", len(request.ComponentVariable))
	// Per OCPP 2.0.1 §3.21: respond Accepted synchronously, then send
	// the report asynchronously via NotifyReport. We respect the
	// ComponentVariable filter (if any) and the ComponentCriteria
	// (if any).
	var reqIDPtr *int
	if request.RequestID != nil {
		reqIDPtr = request.RequestID
	}
	go b.sendFullReport(reqID, provisioning.ReportTypeFullInventory, request.ComponentCriteria, request.ComponentVariable)
	_ = reqIDPtr // requestID is forwarded to NotifyReport below
	return provisioning.NewGetReportResponse(types.GenericDeviceModelStatusAccepted), nil
}

func (b *Bridge201) OnGetVariables(request *provisioning.GetVariablesRequest) (*provisioning.GetVariablesResponse, error) {
	b.tl.LogInbound("GetVariables", nil, fmt.Sprintf("count=%d", len(request.GetVariableData)), nil, "")
	slog.Info("OCPP 2.0.1 GetVariables received", "count", len(request.GetVariableData))
	results := b.deviceModel.BuildGetVariablesResponse(request.GetVariableData)
	return &provisioning.GetVariablesResponse{GetVariableResult: results}, nil
}

func (b *Bridge201) OnSetVariables(request *provisioning.SetVariablesRequest) (*provisioning.SetVariablesResponse, error) {
	b.tl.LogInbound("SetVariables", nil, fmt.Sprintf("count=%d", len(request.SetVariableData)), nil, "")
	slog.Info("OCPP 2.0.1 SetVariables received", "count", len(request.SetVariableData))
	results := b.deviceModel.BuildSetVariablesResponse(request.SetVariableData)

	// Apply runtime effects for known writable variables.
	for i, item := range request.SetVariableData {
		if item.Component.Name == "OCPPCommCtrlr" && item.Variable.Name == "HeartbeatInterval" {
			if val, err := strconv.Atoi(item.AttributeValue); err == nil && val > 0 {
				b.heartbeatInt = val
				b.restartHeartbeat()
			}
		}
		if i < len(results) && results[i].AttributeStatus == provisioning.SetVariableStatusAccepted {
			b.broadcastWS(wsapi.Message{
				Type: "ocpp_variable_changed",
				Data: map[string]interface{}{
					"component": item.Component.Name,
					"variable":  item.Variable.Name,
					"value":     item.AttributeValue,
					"evse_id":   getEVSEID(item.Component),
				},
			})
		}
	}

	return &provisioning.SetVariablesResponse{SetVariableResult: results}, nil
}

func (b *Bridge201) OnReset(request *provisioning.ResetRequest) (*provisioning.ResetResponse, error) {
	b.tl.LogInbound("Reset", nil, fmt.Sprintf("type=%s", request.Type), nil, "")
	slog.Info("OCPP 2.0.1 Reset received", "type", request.Type)

	if request.Type == provisioning.ResetTypeImmediate {
		b.triggerReset("Reboot")
		return &provisioning.ResetResponse{Status: provisioning.ResetStatusAccepted}, nil
	}

	// OnIdle: schedule reset after last active transaction ends.
	hasActive := len(b.engine.GetSessionInfo()) > 0

	if hasActive {
		b.pendingReset.Store(true)
		return &provisioning.ResetResponse{Status: provisioning.ResetStatusScheduled}, nil
	}

	b.completeReset()
	return &provisioning.ResetResponse{Status: provisioning.ResetStatusAccepted}, nil
}

func (b *Bridge201) OnSetNetworkProfile(request *provisioning.SetNetworkProfileRequest) (*provisioning.SetNetworkProfileResponse, error) {
	slog.Info("OCPP 2.0.1 SetNetworkProfile received", "slot", request.ConfigurationSlot)
	return &provisioning.SetNetworkProfileResponse{Status: provisioning.SetNetworkProfileStatusRejected}, nil
}

// -- availability.ChargingStationHandler --

func (b *Bridge201) OnChangeAvailability(request *availability.ChangeAvailabilityRequest) (*availability.ChangeAvailabilityResponse, error) {
	b.tl.LogInbound("ChangeAvailability", nil, fmt.Sprintf("status=%s", request.OperationalStatus), nil, "")
	slog.Info("OCPP 2.0.1 ChangeAvailability received", "status", request.OperationalStatus)

	targetInoperative := request.OperationalStatus == availability.OperationalStatusInoperative
	newState := "Available"
	if targetInoperative {
		newState = "Unavailable"
	}

	availType := "Operative"
	if targetInoperative {
		availType = "Inoperative"
	}

	mapResult := func(result string) availability.ChangeAvailabilityStatus {
		switch result {
		case "accepted":
			return availability.ChangeAvailabilityStatusAccepted
		case "scheduled":
			return availability.ChangeAvailabilityStatusScheduled
		default:
			return availability.ChangeAvailabilityStatusRejected
		}
	}

	// Station-level: evse omitted (nil) or evseId == 0.
	if request.Evse == nil || request.Evse.ID == 0 {
		b.deviceModel.SetVariable("ChargingStation", "", 0, "AvailabilityState", newState, MutabilityReadOnly)
		// If any connector is currently in a charging session, the change can only
		// be applied after the session ends.  Per OCPP 2.0.1 we report
		// `Scheduled` and persist the desired state in the engine's
		// pending-availability map, so it is applied automatically when the
		// session ends.
		anyScheduled := false
		for _, id := range b.engine.GetConnectorIDs() {
			connID := id
			result := b.engine.SetConnectorAvailability(connID, availType)
			if result == "scheduled" {
				anyScheduled = true
			}
			// EVSE/Connector AvailabilityState updated via SendStatusNotification callback.
			slog.Info("OCPP 2.0.1 ChangeAvailability applied", "connector", connID, "state", newState, "result", result)
		}
		status := availability.ChangeAvailabilityStatusAccepted
		if anyScheduled {
			status = availability.ChangeAvailabilityStatusScheduled
		}
		return &availability.ChangeAvailabilityResponse{Status: status}, nil
	}

	// EVSE-level targeting (evseId == connectorID in our single-connector-per-EVSE model).
	connID := request.Evse.ID
	result := b.engine.SetConnectorAvailability(connID, availType)
	// EVSE/Connector AvailabilityState updated via SendStatusNotification callback.
	slog.Info("OCPP 2.0.1 ChangeAvailability applied", "connector", connID, "state", newState, "result", result)
	return &availability.ChangeAvailabilityResponse{Status: mapResult(result)}, nil
}

// -- authorization.ChargingStationHandler --

func (b *Bridge201) OnClearCache(request *authorization.ClearCacheRequest) (*authorization.ClearCacheResponse, error) {
	b.tl.LogInbound("ClearCache", nil, "ClearCache", nil, "")
	slog.Info("OCPP 2.0.1 ClearCache received")
	if b.authCache != nil {
		b.authCache.Clear()
	}
	return authorization.NewClearCacheResponse(authorization.ClearCacheStatusAccepted), nil
}

// -- transactions.ChargingStationHandler --

func (b *Bridge201) OnGetTransactionStatus(request *transactions.GetTransactionStatusRequest) (*transactions.GetTransactionStatusResponse, error) {
	slog.Info("OCPP 2.0.1 GetTransactionStatus received", "transactionId", request.TransactionID)

	// Determine whether any transaction is still ongoing.  Per OCPP 2.0.1:
	//   - When a transactionId is supplied, OngoingIndicator reflects only that tx
	//   - When no transactionId is supplied, it reflects whether any tx is active
	hasQueued := b.queue != nil && b.queue.Len() > 0

	ongoing := false
	if request.TransactionID != "" {
		// Look up the supplied transaction id in the bridge's bookkeeping.
		b.mu.Lock()
		_, found := b.findEVSEByTxIDLocked(request.TransactionID)
		b.mu.Unlock()
		ongoing = found
		// Also cross-check the engine: a session may still be alive even if the
		// bridge lost its txIntToEVSE entry (e.g. after restart).
		if !ongoing {
			ongoing = b.engineHasSessionForTxID(request.TransactionID)
		}
	} else {
		// No specific transaction id: report whether *any* session is active.
		b.mu.Lock()
		ongoing = len(b.txBuilders) > 0
		b.mu.Unlock()
		if !ongoing {
			ongoing = len(b.engine.GetSessionInfo()) > 0
		}
	}

	resp := transactions.NewGetTransactionStatusResponse(hasQueued)
	resp.OngoingIndicator = &ongoing
	return resp, nil
}

// -- remotecontrol.ChargingStationHandler --

func (b *Bridge201) OnRequestStartTransaction(request *remotecontrol.RequestStartTransactionRequest) (*remotecontrol.RequestStartTransactionResponse, error) {
	evse := 1
	if request.EvseID != nil {
		evse = *request.EvseID
	}
	b.tl.LogInbound("RequestStartTransaction", ocpppkg.IntPtr(evse), fmt.Sprintf("evse=%d idTag=%s", evse, request.IDToken.IdToken), nil, "")
	slog.Info("OCPP 2.0.1 RequestStartTransaction received", "idToken", request.IDToken.IdToken)

	// Per OCPP 2.0.1 §B01/B02: while not registered (BootNotification not
	// yet Accepted), the Charging Station SHALL NOT act on requests that
	// would generate further traffic like TransactionEvent(Started).
	if !b.registered.Load() {
		slog.Warn("OCPP 2.0.1 RequestStartTransaction rejected: not registered with CSMS", "evse", evse)
		return remotecontrol.NewRequestStartTransactionResponse(remotecontrol.RequestStartStopStatusRejected), nil
	}

	evseID := 1
	if request.EvseID != nil {
		evseID = *request.EvseID
	}

	idTag := request.IDToken.IdToken
	if !b.admitRemoteStart(idTag, time.Now()) {
		slog.Warn("OCPP 2.0.1 RequestStartTransaction rejected by authorization", "idToken", idTag)
		return remotecontrol.NewRequestStartTransactionResponse(remotecontrol.RequestStartStopStatusRejected), nil
	}

	// Capture the optional charging profile before StartSession so we can apply
	// it (or stash it on the pending remote start) if the EV is not yet plugged in.
	// Register it with the manager first so the limiter picks it up as soon as
	// the session starts.
	var profile *engine.ChargingProfile
	if request.ChargingProfile != nil {
		b.profileManager.SetProfile(evseID, *request.ChargingProfile)
		profile = convertChargingProfile201(request.ChargingProfile, evseID)
	}

	err := b.engine.StartSession(evseID, 0, &idTag, 30)
	if err != nil {
		slog.Warn("OCPP 2.0.1 RequestStartTransaction rejected", "error", err)
		return remotecontrol.NewRequestStartTransactionResponse(remotecontrol.RequestStartStopStatusRejected), nil
	}

	if profile != nil {
		// If the EV was already connected, StartSession started the session
		// immediately and we can set the profile directly.  If the EV was not
		// yet connected, StartSession stored a PendingRemoteStart — store the
		// profile there so it is applied when the EV plugs in.
		if b.engine.GetSession(evseID) != nil {
			b.engine.SetSessionChargingProfile(evseID, profile)
		} else {
			b.engine.SetPendingRemoteStartChargingProfile(evseID, profile)
		}
		slog.Info("OCPP 2.0.1 RequestStartTransaction applied ChargingProfile",
			"evseId", evseID, "profileId", profile.ProfileID, "purpose", profile.Purpose)
	}

	// Record remoteStartId so TransactionEvent(Started) can echo it back to
	// the CSMS for correlation (OCPP 2.0.1 §F02), mirroring the charging
	// profile's immediate-vs-pending handling above.
	if b.engine.GetSession(evseID) != nil {
		b.engine.SetSessionRemoteStartID(evseID, request.RemoteStartID)
	} else {
		b.engine.SetPendingRemoteStartID(evseID, request.RemoteStartID)
	}

	return remotecontrol.NewRequestStartTransactionResponse(remotecontrol.RequestStartStopStatusAccepted), nil
}

func (b *Bridge201) OnRequestStopTransaction(request *remotecontrol.RequestStopTransactionRequest) (*remotecontrol.RequestStopTransactionResponse, error) {
	b.tl.LogInbound("RequestStopTransaction", nil, fmt.Sprintf("txId=%s", request.TransactionID), nil, "")
	slog.Info("OCPP 2.0.1 RequestStopTransaction received", "txId", request.TransactionID)

	b.mu.Lock()
	var evseID int
	found := false
	for eid, builder := range b.txBuilders {
		if builder.TransactionID() == request.TransactionID {
			evseID = eid
			found = true
			break
		}
	}
	b.mu.Unlock()

	if !found {
		slog.Warn("OCPP 2.0.1 RequestStopTransaction: transaction not found", "txId", request.TransactionID)
		return remotecontrol.NewRequestStopTransactionResponse(remotecontrol.RequestStartStopStatusRejected), nil
	}

	result := b.engine.StopSession(&evseID, "Remote")
	if result == nil {
		slog.Warn("OCPP 2.0.1 RequestStopTransaction: StopSession returned nil", "evseId", evseID)
		return remotecontrol.NewRequestStopTransactionResponse(remotecontrol.RequestStartStopStatusRejected), nil
	}

	return remotecontrol.NewRequestStopTransactionResponse(remotecontrol.RequestStartStopStatusAccepted), nil
}

func (b *Bridge201) OnTriggerMessage(request *remotecontrol.TriggerMessageRequest) (*remotecontrol.TriggerMessageResponse, error) {
	b.tl.LogInbound("TriggerMessage", nil, fmt.Sprintf("requested=%s", request.RequestedMessage), nil, "")
	slog.Info("OCPP 2.0.1 TriggerMessage received", "requestedMessage", request.RequestedMessage)

	switch request.RequestedMessage {
	case remotecontrol.MessageTriggerBootNotification:
		b.dispatcher.Enqueue(ocpppkg.OCPPCommand{
			Description: "BootNotification (triggered)",
			Execute:     b.SendBootNotification,
		})
	case remotecontrol.MessageTriggerHeartbeat:
		b.dispatcher.Enqueue(ocpppkg.OCPPCommand{
			Description: "Heartbeat (triggered)",
			Execute:     b.SendHeartbeat,
		})
	case remotecontrol.MessageTriggerStatusNotification:
		evseID := 1
		if request.Evse != nil {
			evseID = request.Evse.ID
		}
		status := b.engine.GetConnectorStatus(evseID)
		capturedEVSE := evseID
		capturedStatus := status
		b.dispatcher.Enqueue(ocpppkg.OCPPCommand{
			Description: "StatusNotification (triggered)",
			Execute:     func() error { return b.SendStatusNotification(capturedEVSE, "", capturedStatus) },
		})
	case remotecontrol.MessageTriggerTransactionEvent:
		return remotecontrol.NewTriggerMessageResponse(remotecontrol.TriggerMessageStatusNotImplemented), nil
	case remotecontrol.MessageTriggerLogStatusNotification:
		return remotecontrol.NewTriggerMessageResponse(remotecontrol.TriggerMessageStatusNotImplemented), nil
	default:
		return remotecontrol.NewTriggerMessageResponse(remotecontrol.TriggerMessageStatusNotImplemented), nil
	}

	return remotecontrol.NewTriggerMessageResponse(remotecontrol.TriggerMessageStatusAccepted), nil
}

func (b *Bridge201) OnUnlockConnector(request *remotecontrol.UnlockConnectorRequest) (*remotecontrol.UnlockConnectorResponse, error) {
	b.tl.LogInbound("UnlockConnector", ocpppkg.IntPtr(request.EvseID), fmt.Sprintf("evse=%d connector=%d", request.EvseID, request.ConnectorID), nil, "")
	slog.Info("OCPP 2.0.1 UnlockConnector received", "evseId", request.EvseID, "connectorId", request.ConnectorID)

	if b.engine.GetConnector(request.EvseID) == nil {
		return remotecontrol.NewUnlockConnectorResponse(remotecontrol.UnlockStatusUnknownConnector), nil
	}
	// Per OCPP 2.0.1 §3.16: SHALL NOT unlock a connector with an ongoing
	// authorized transaction — the CSMS must stop the transaction first.
	// The previous implementation force-unplugged the EVSE here, silently
	// killing any active session and misattributing the stop to the EV
	// driver disconnecting.
	if b.engine.GetSession(request.EvseID) != nil {
		return remotecontrol.NewUnlockConnectorResponse(remotecontrol.UnlockStatusOngoingAuthorizedTransaction), nil
	}
	if err := b.engine.UnlockConnector(request.EvseID); err != nil {
		return remotecontrol.NewUnlockConnectorResponse(remotecontrol.UnlockStatusUnlockFailed), nil
	}
	return remotecontrol.NewUnlockConnectorResponse(remotecontrol.UnlockStatusUnlocked), nil
}

// -- smartcharging.ChargingStationHandler --

func (b *Bridge201) OnSetChargingProfile(request *smartcharging.SetChargingProfileRequest) (*smartcharging.SetChargingProfileResponse, error) {
	b.tl.LogInbound("SetChargingProfile", ocpppkg.IntPtr(request.EvseID), fmt.Sprintf("evse=%d", request.EvseID), nil, "")
	if request.ChargingProfile == nil {
		slog.Error("OCPP 2.0.1 SetChargingProfile received with nil ChargingProfile", "evseId", request.EvseID)
		return smartcharging.NewSetChargingProfileResponse(smartcharging.ChargingProfileStatusRejected), nil
	}
	// Validate TxProfile: per OCPP 2.0.1 §3.21, TxProfile MUST carry a
	// TransactionID.  Accepting one without a txId would mean the profile is
	// silently dead, so reject up-front.  TxDefaultProfile with an empty
	// TransactionID is allowed and means "applies to any transaction on the
	// EVSE".
	if request.ChargingProfile.ChargingProfilePurpose == types.ChargingProfilePurposeTxProfile &&
		request.ChargingProfile.TransactionID == "" {
		slog.Warn("OCPP 2.0.1 SetChargingProfile rejected: TxProfile without transactionId",
			"evseId", request.EvseID, "profileId", request.ChargingProfile.ID,
			"purpose", request.ChargingProfile.ChargingProfilePurpose)
		return smartcharging.NewSetChargingProfileResponse(smartcharging.ChargingProfileStatusRejected), nil
	}
	// If the request's evseId is 0 and the profile is scoped to a transaction,
	// resolve to the EVSE that owns the transaction so limit resolution works.
	evseID := request.EvseID
	if evseID == 0 && request.ChargingProfile.TransactionID != "" {
		b.mu.Lock()
		resolvedEVSE, found := b.findEVSEByTxIDLocked(request.ChargingProfile.TransactionID)
		b.mu.Unlock()
		if found {
			evseID = resolvedEVSE
		}
	}
	slog.Info("OCPP 2.0.1 SetChargingProfile received", "evseId", evseID, "profileId", request.ChargingProfile.ID)
	b.profileManager.SetProfile(evseID, *request.ChargingProfile)
	b.broadcastWS(wsapi.Message{
		Type: "charging_profile_changed",
		Data: map[string]interface{}{"action": "set", "profile_id": request.ChargingProfile.ID, "evse_id": evseID},
	})
	return smartcharging.NewSetChargingProfileResponse(smartcharging.ChargingProfileStatusAccepted), nil
}

func (b *Bridge201) OnClearChargingProfile(request *smartcharging.ClearChargingProfileRequest) (*smartcharging.ClearChargingProfileResponse, error) {
	b.tl.LogInbound("ClearChargingProfile", nil, "ClearChargingProfile", nil, "")
	slog.Info("OCPP 2.0.1 ClearChargingProfile received")
	var evseID *int
	var purpose *types.ChargingProfilePurposeType
	var stackLevel *int

	if request.ChargingProfileCriteria != nil {
		evseID = request.ChargingProfileCriteria.EvseID
		if request.ChargingProfileCriteria.ChargingProfilePurpose != "" {
			p := request.ChargingProfileCriteria.ChargingProfilePurpose
			purpose = &p
		}
		stackLevel = request.ChargingProfileCriteria.StackLevel
	}

	cleared := b.profileManager.ClearProfile(request.ChargingProfileID, evseID, purpose, stackLevel)
	if cleared > 0 {
		b.broadcastWS(wsapi.Message{
			Type: "charging_profile_changed",
			Data: map[string]interface{}{"action": "cleared", "cleared_count": cleared},
		})
		return smartcharging.NewClearChargingProfileResponse(smartcharging.ClearChargingProfileStatusAccepted), nil
	}
	return smartcharging.NewClearChargingProfileResponse(smartcharging.ClearChargingProfileStatusUnknown), nil
}

func (b *Bridge201) OnGetChargingProfiles(request *smartcharging.GetChargingProfilesRequest) (*smartcharging.GetChargingProfilesResponse, error) {
	slog.Info("OCPP 2.0.1 GetChargingProfiles received", "requestId", request.RequestID)

	var purpose *types.ChargingProfilePurposeType
	var stackLevel *int
	var profileIDs []int

	if request.ChargingProfile.ChargingProfilePurpose != "" {
		p := request.ChargingProfile.ChargingProfilePurpose
		purpose = &p
	}
	stackLevel = request.ChargingProfile.StackLevel
	profileIDs = request.ChargingProfile.ChargingProfileID

	profiles := b.profileManager.GetFilteredProfiles(request.EvseID, profileIDs, purpose, stackLevel)
	if len(profiles) == 0 {
		return smartcharging.NewGetChargingProfilesResponse(smartcharging.GetChargingProfileStatusNoProfiles), nil
	}

	// Send ReportChargingProfiles asynchronously
	go func() {
		reportEvseID := 0
		if request.EvseID != nil {
			reportEvseID = *request.EvseID
		}
		req := smartcharging.NewReportChargingProfilesRequest(request.RequestID, types.ChargingLimitSourceCSO, reportEvseID, profiles)
		cb := func(resp ocpp.Response, err error) {
			if err != nil {
				slog.Error("ReportChargingProfiles failed", "error", err)
			}
		}
		if err := b.cs.SendRequestAsync(req, cb); err != nil {
			slog.Error("failed to send ReportChargingProfiles", "error", err)
		}
	}()

	return smartcharging.NewGetChargingProfilesResponse(smartcharging.GetChargingProfileStatusAccepted), nil
}

func (b *Bridge201) OnGetCompositeSchedule(request *smartcharging.GetCompositeScheduleRequest) (*smartcharging.GetCompositeScheduleResponse, error) {
	slog.Info("OCPP 2.0.1 GetCompositeSchedule received", "evseId", request.EvseID, "duration", request.Duration)
	c := b.engine.GetConnector(request.EvseID)
	if c == nil {
		return smartcharging.NewGetCompositeScheduleResponse(smartcharging.GetCompositeScheduleStatusRejected, request.EvseID), nil
	}

	session := b.engine.GetSession(request.EvseID)
	var txStart *time.Time
	if session != nil {
		t := session.StartTime
		txStart = &t
	}

	now := time.Now()
	periods, err := b.profileManager.GetCompositeSchedule(request.EvseID, 0, now, request.Duration, c.Voltage, txStart, c.Phase)
	if err != nil || len(periods) == 0 {
		return smartcharging.NewGetCompositeScheduleResponse(smartcharging.GetCompositeScheduleStatusRejected, request.EvseID), nil
	}

	ocppPeriods := make([]types.ChargingSchedulePeriod, 0, len(periods))
	for _, period := range periods {
		ocppPeriods = append(ocppPeriods, types.ChargingSchedulePeriod{
			StartPeriod: period.StartPeriod,
			Limit:       period.Limit,
		})
	}

	resp := smartcharging.NewGetCompositeScheduleResponse(smartcharging.GetCompositeScheduleStatusAccepted, request.EvseID)
	resp.Schedule = &smartcharging.CompositeSchedule{
		StartDateTime: types.NewDateTime(now),
		ChargingSchedule: &types.ChargingSchedule{
			ChargingRateUnit:       types.ChargingRateUnitAmperes,
			Duration:               &request.Duration,
			ChargingSchedulePeriod: ocppPeriods,
		},
	}
	return resp, nil
}

func (b *Bridge201) OnNotifyEVChargingSchedule(request *smartcharging.NotifyEVChargingScheduleRequest) (*smartcharging.NotifyEVChargingScheduleResponse, error) {
	slog.Info("OCPP 2.0.1 NotifyEVChargingSchedule received (not supported)")
	return smartcharging.NewNotifyEVChargingScheduleResponse(types.GenericStatusRejected), nil
}

func (b *Bridge201) OnNotifyEVChargingNeeds(request *smartcharging.NotifyEVChargingNeedsRequest) (*smartcharging.NotifyEVChargingNeedsResponse, error) {
	slog.Info("OCPP 2.0.1 NotifyEVChargingNeeds received (not supported)")
	return smartcharging.NewNotifyEVChargingNeedsResponse(smartcharging.EVChargingNeedsStatusRejected), nil
}

// -- diagnostics.ChargingStationHandler --

func (b *Bridge201) OnSetVariableMonitoring(request *diagnostics.SetVariableMonitoringRequest) (*diagnostics.SetVariableMonitoringResponse, error) {
	slog.Info("OCPP 2.0.1 SetVariableMonitoring received (not supported)", "count", len(request.MonitoringData))
	var results []diagnostics.SetMonitoringResult
	for _, d := range request.MonitoringData {
		status := diagnostics.SetMonitoringStatusRejected
		results = append(results, diagnostics.SetMonitoringResult{
			Status:    status,
			Component: d.Component,
			Variable:  d.Variable,
			Type:      d.Type,
			Severity:  d.Severity,
		})
	}
	return diagnostics.NewSetVariableMonitoringResponse(results), nil
}

func (b *Bridge201) OnClearVariableMonitoring(request *diagnostics.ClearVariableMonitoringRequest) (*diagnostics.ClearVariableMonitoringResponse, error) {
	slog.Info("OCPP 2.0.1 ClearVariableMonitoring received")
	var results []diagnostics.ClearMonitoringResult
	for _, id := range request.ID {
		status := diagnostics.ClearMonitoringStatusAccepted
		if !b.monitoringManager.ClearMonitor(id) {
			status = diagnostics.ClearMonitoringStatusNotFound
		}
		results = append(results, diagnostics.ClearMonitoringResult{
			Status: status,
			ID:     id,
		})
	}
	return diagnostics.NewClearVariableMonitoringResponse(results), nil
}

func (b *Bridge201) OnGetMonitoringReport(request *diagnostics.GetMonitoringReportRequest) (*diagnostics.GetMonitoringReportResponse, error) {
	slog.Info("OCPP 2.0.1 GetMonitoringReport received (not supported)", "requestId", request.RequestID)
	return diagnostics.NewGetMonitoringReportResponse(types.GenericDeviceModelStatusNotSupported), nil
}

func (b *Bridge201) OnSetMonitoringBase(request *diagnostics.SetMonitoringBaseRequest) (*diagnostics.SetMonitoringBaseResponse, error) {
	slog.Info("OCPP 2.0.1 SetMonitoringBase received (not supported)", "base", request.MonitoringBase)
	return diagnostics.NewSetMonitoringBaseResponse(types.GenericDeviceModelStatusRejected), nil
}

func (b *Bridge201) OnSetMonitoringLevel(request *diagnostics.SetMonitoringLevelRequest) (*diagnostics.SetMonitoringLevelResponse, error) {
	slog.Info("OCPP 2.0.1 SetMonitoringLevel received (not supported)", "severity", request.Severity)
	return diagnostics.NewSetMonitoringLevelResponse(types.GenericDeviceModelStatusRejected), nil
}

func (b *Bridge201) OnCustomerInformation(request *diagnostics.CustomerInformationRequest) (*diagnostics.CustomerInformationResponse, error) {
	slog.Info("OCPP 2.0.1 CustomerInformation received (not supported)", "requestId", request.RequestID)
	return diagnostics.NewCustomerInformationResponse(diagnostics.CustomerInformationStatusRejected), nil
}

func (b *Bridge201) OnGetLog(request *diagnostics.GetLogRequest) (*diagnostics.GetLogResponse, error) {
	b.tl.LogInbound("GetLog", nil, fmt.Sprintf("requestId=%d type=%s location=%s", request.RequestID, request.LogType, request.Log.RemoteLocation), nil, "")
	slog.Info("OCPP 2.0.1 GetLog received", "type", request.LogType)
	if request.LogType != diagnostics.LogTypeDiagnostics || b.diagManager == nil {
		return diagnostics.NewGetLogResponse(diagnostics.LogStatusRejected), nil
	}

	retries := 0
	retryInterval := 0
	if request.Retries != nil {
		retries = *request.Retries
	}
	if request.RetryInterval != nil {
		retryInterval = *request.RetryInterval
	}

	if err := b.diagManager.TriggerUpload(request.Log.RemoteLocation, retries, retryInterval); err != nil {
		slog.Warn("OCPP 2.0.1 GetLog trigger failed", "error", err)
		return diagnostics.NewGetLogResponse(diagnostics.LogStatusRejected), nil
	}

	b.diagRequestID.Store(int64(request.RequestID))
	resp := diagnostics.NewGetLogResponse(diagnostics.LogStatusAccepted)
	resp.Filename = "diagnostics.tgz"
	return resp, nil
}

// -- display.ChargingStationHandler --

func (b *Bridge201) OnSetDisplayMessage(request *display.SetDisplayMessageRequest) (*display.SetDisplayMessageResponse, error) {
	slog.Info("OCPP 2.0.1 SetDisplayMessage received", "id", request.Message.ID)
	b.displayStore.Set(DisplayMessage{
		ID:       request.Message.ID,
		Priority: string(request.Message.Priority),
		State:    string(request.Message.State),
		Text:     request.Message.Message.Content,
		Language: request.Message.Message.Language,
	})
	b.broadcastWS(wsapi.Message{
		Type: "display_message_set",
		Data: map[string]interface{}{
			"id":   request.Message.ID,
			"text": request.Message.Message.Content,
		},
	})
	return display.NewSetDisplayMessageResponse(display.DisplayMessageStatusAccepted), nil
}

func (b *Bridge201) OnClearDisplay(request *display.ClearDisplayRequest) (*display.ClearDisplayResponse, error) {
	slog.Info("OCPP 2.0.1 ClearDisplayMessage received", "id", request.ID)
	if b.displayStore.Clear(request.ID) {
		return display.NewClearDisplayResponse(display.ClearMessageStatusAccepted), nil
	}
	return display.NewClearDisplayResponse(display.ClearMessageStatusUnknown), nil
}

func (b *Bridge201) OnGetDisplayMessages(request *display.GetDisplayMessagesRequest) (*display.GetDisplayMessagesResponse, error) {
	slog.Info("OCPP 2.0.1 GetDisplayMessages received", "requestId", request.RequestID)

	// Send NotifyDisplayMessages asynchronously per OCPP 2.0.1 spec
	go func() {
		all := b.displayStore.GetAll()
		var msgInfos []display.MessageInfo
		for _, m := range all {
			// Apply optional filters from the request
			if len(request.ID) > 0 {
				found := false
				for _, id := range request.ID {
					if id == m.ID {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}
			if request.Priority != "" && string(request.Priority) != m.Priority {
				continue
			}
			if request.State != "" && string(request.State) != m.State {
				continue
			}
			msgInfos = append(msgInfos, display.MessageInfo{
				ID:       m.ID,
				Priority: display.MessagePriority(m.Priority),
				State:    display.MessageState(m.State),
				Message: types.MessageContent{
					Format:   types.MessageFormatUTF8,
					Language: m.Language,
					Content:  m.Text,
				},
			})
		}
		req := display.NewNotifyDisplayMessagesRequest(request.RequestID)
		req.MessageInfo = msgInfos
		req.Tbc = false

		cb := func(resp ocpp.Response, err error) {
			if err != nil {
				slog.Error("NotifyDisplayMessages failed", "error", err)
			}
		}
		if err := b.cs.SendRequestAsync(req, cb); err != nil {
			slog.Error("failed to send NotifyDisplayMessages", "error", err)
		}
	}()

	return display.NewGetDisplayMessagesResponse(display.MessageStatusAccepted), nil
}

// -- tariffcost.ChargingStationHandler --

func (b *Bridge201) OnCostUpdated(request *tariffcost.CostUpdatedRequest) (*tariffcost.CostUpdatedResponse, error) {
	slog.Info("OCPP 2.0.1 CostUpdated received", "txId", request.TransactionID, "cost", request.TotalCost)
	b.costStore.Update(request.TransactionID, request.TotalCost)
	// Look up the EVSE for this transaction and update the builder's cached
	// cost so the next TransactionEvent(Updated) / Ended can read the latest
	// value.  Also surface it through the device model so external consumers
	// (REST API, monitoring) can observe the running cost.
	b.mu.Lock()
	evseID, hasEVSE := b.findEVSEByTxIDLocked(request.TransactionID)
	var builder *TransactionEventBuilder
	if hasEVSE {
		builder = b.txBuilders[evseID]
	}
	b.mu.Unlock()
	if builder != nil {
		builder.SetLastCost(request.TotalCost)
	}
	b.deviceModel.SetVariable("TariffCostCtrlr", "", 0, "TotalCost", fmt.Sprintf("%.4f", request.TotalCost), MutabilityReadOnly)
	if hasEVSE {
		b.deviceModel.SetVariable("TariffCostCtrlr", "", evseID, "TotalCost", fmt.Sprintf("%.4f", request.TotalCost), MutabilityReadOnly)
	}
	b.broadcastWS(wsapi.Message{
		Type: "cost_updated",
		Data: map[string]interface{}{
			"transaction_id": request.TransactionID,
			"total_cost":     request.TotalCost,
		},
	})
	return tariffcost.NewCostUpdatedResponse(), nil
}

// -- firmware.ChargingStationHandler --

func (b *Bridge201) OnUpdateFirmware(request *firmware.UpdateFirmwareRequest) (*firmware.UpdateFirmwareResponse, error) {
	b.tl.LogInbound("UpdateFirmware", nil, fmt.Sprintf("location=%s", request.Firmware.Location), nil, "")
	slog.Info("OCPP 2.0.1 UpdateFirmware received", "location", request.Firmware.Location, "requestId", request.RequestID)
	if b.fwManager == nil || request.Firmware.RetrieveDateTime == nil {
		return firmware.NewUpdateFirmwareResponse(firmware.UpdateFirmwareStatusRejected), nil
	}
	retrieveDate := request.Firmware.RetrieveDateTime.Time
	if err := b.fwManager.TriggerUpdate(request.Firmware.Location, retrieveDate); err != nil {
		slog.Warn("OCPP 2.0.1 UpdateFirmware trigger failed", "error", err)
		return firmware.NewUpdateFirmwareResponse(firmware.UpdateFirmwareStatusRejected), nil
	}
	return firmware.NewUpdateFirmwareResponse(firmware.UpdateFirmwareStatusAccepted), nil
}

func (b *Bridge201) OnPublishFirmware(request *firmware.PublishFirmwareRequest) (*firmware.PublishFirmwareResponse, error) {
	slog.Info("OCPP 2.0.1 PublishFirmware received (not supported)", "location", request.Location)
	return firmware.NewPublishFirmwareResponse(types.GenericStatusRejected), nil
}

func (b *Bridge201) OnUnpublishFirmware(request *firmware.UnpublishFirmwareRequest) (*firmware.UnpublishFirmwareResponse, error) {
	slog.Info("OCPP 2.0.1 UnpublishFirmware received (no published firmware)", "checksum", request.Checksum)
	return firmware.NewUnpublishFirmwareResponse(firmware.UnpublishFirmwareStatusNoFirmware), nil
}

// -- localauth.ChargingStationHandler --

func (b *Bridge201) OnGetLocalListVersion(request *localauth.GetLocalListVersionRequest) (*localauth.GetLocalListVersionResponse, error) {
	slog.Info("OCPP 2.0.1 GetLocalListVersion received")
	version := 0
	if b.localAuth != nil {
		version = b.localAuth.GetVersion()
	}
	return localauth.NewGetLocalListVersionResponse(version), nil
}

func (b *Bridge201) OnSendLocalList(request *localauth.SendLocalListRequest) (*localauth.SendLocalListResponse, error) {
	b.tl.LogInbound("SendLocalList", nil, fmt.Sprintf("version=%d type=%s entries=%d", request.VersionNumber, request.UpdateType, len(request.LocalAuthorizationList)), nil, "")
	slog.Info("OCPP 2.0.1 SendLocalList received", "version", request.VersionNumber, "updateType", request.UpdateType)
	if b.localAuth == nil {
		return localauth.NewSendLocalListResponse(localauth.SendLocalListStatusFailed), nil
	}
	if request.UpdateType == localauth.UpdateTypeDifferential && request.VersionNumber <= b.localAuth.GetVersion() {
		return localauth.NewSendLocalListResponse(localauth.SendLocalListStatusVersionMismatch), nil
	}

	entries := make([]ocpppkg.LocalAuthEntry, 0, len(request.LocalAuthorizationList))
	for _, d := range request.LocalAuthorizationList {
		entry := ocpppkg.LocalAuthEntry{
			IDTag: d.IdToken.IdToken,
		}
		if d.IdTokenInfo != nil {
			status := string(d.IdTokenInfo.Status)
			if normalized, ok := ocpppkg.NormalizeAuthorizationStatus(status); ok {
				entry.Status = normalized
			} else {
				entry.Status = status
			}
			if d.IdTokenInfo.CacheExpiryDateTime != nil {
				t := d.IdTokenInfo.CacheExpiryDateTime.Time
				entry.Expiry = &t
			}
			if d.IdTokenInfo.GroupIdToken != nil {
				s := d.IdTokenInfo.GroupIdToken.IdToken
				entry.ParentIDTag = &s
			}
		} else {
			entry.Delete = true
		}
		entries = append(entries, entry)
	}

	if err := b.localAuth.UpdateList(request.VersionNumber, entries, string(request.UpdateType)); err != nil {
		slog.Warn("OCPP 2.0.1 SendLocalList update failed", "error", err)
		return localauth.NewSendLocalListResponse(localauth.SendLocalListStatusFailed), nil
	}
	return localauth.NewSendLocalListResponse(localauth.SendLocalListStatusAccepted), nil
}

// -- data201.ChargingStationHandler --

func (b *Bridge201) OnDataTransfer(request *data201.DataTransferRequest) (*data201.DataTransferResponse, error) {
	b.tl.LogInbound("DataTransfer", nil, fmt.Sprintf("vendor=%s messageId=%s", request.VendorID, request.MessageID), nil, "")
	messageID := request.MessageID
	dataStr := ocpppkg.DataTransferDataString(request.Data)
	slog.Info("OCPP 2.0.1 DataTransfer received", "vendorId", request.VendorID, "messageId", messageID)

	if b.dataTransfer == nil {
		return data201.NewDataTransferResponse(data201.DataTransferStatusUnknownVendorId), nil
	}

	status, responseData := b.dataTransfer.Dispatch(request.VendorID, messageID, messageID, dataStr)
	resp := data201.NewDataTransferResponse(data201.DataTransferStatus(status))
	if responseData != "" {
		resp.Data = responseData
	}
	return resp, nil
}

// -- reservation.ChargingStationHandler --

func (b *Bridge201) OnReserveNow(request *reservation.ReserveNowRequest) (*reservation.ReserveNowResponse, error) {
	b.tl.LogInbound("ReserveNow", nil, fmt.Sprintf("reservationId=%d", request.ID), nil, "")
	slog.Info("OCPP 2.0.1 ReserveNow received", "reservationId", request.ID, "evseId", request.EvseID)
	if request.EvseID == nil {
		return &reservation.ReserveNowResponse{Status: reservation.ReserveNowStatusRejected}, nil
	}
	expiry := request.ExpiryDateTime.Time
	result := b.engine.ReserveConnector(*request.EvseID, request.ID, request.IdToken.IdToken, expiry, nil)
	switch result {
	case "accepted":
		b.broadcastWS(wsapi.Message{
			Type: "reservation_changed",
			Data: map[string]interface{}{
				"action":         "created",
				"reservation_id": request.ID,
				"connector_id":   *request.EvseID,
				"id_tag":         request.IdToken.IdToken,
			},
		})
		return &reservation.ReserveNowResponse{Status: reservation.ReserveNowStatusAccepted}, nil
	case "occupied":
		return &reservation.ReserveNowResponse{Status: reservation.ReserveNowStatusOccupied}, nil
	case "faulted":
		return &reservation.ReserveNowResponse{Status: reservation.ReserveNowStatusFaulted}, nil
	case "unavailable":
		return &reservation.ReserveNowResponse{Status: reservation.ReserveNowStatusUnavailable}, nil
	default:
		return &reservation.ReserveNowResponse{Status: reservation.ReserveNowStatusRejected}, nil
	}
}

func (b *Bridge201) OnCancelReservation(request *reservation.CancelReservationRequest) (*reservation.CancelReservationResponse, error) {
	b.tl.LogInbound("CancelReservation", nil, fmt.Sprintf("reservationId=%d", request.ReservationID), nil, "")
	slog.Info("OCPP 2.0.1 CancelReservation received", "reservationId", request.ReservationID)
	result := b.engine.CancelReservation(request.ReservationID)
	if result == "accepted" {
		b.broadcastWS(wsapi.Message{
			Type: "reservation_changed",
			Data: map[string]interface{}{
				"action":         "cancelled",
				"reservation_id": request.ReservationID,
			},
		})
		return &reservation.CancelReservationResponse{Status: reservation.CancelReservationStatusAccepted}, nil
	}
	return &reservation.CancelReservationResponse{Status: reservation.CancelReservationStatusRejected}, nil
}

// -- iso15118.ChargingStationHandler --

func (b *Bridge201) OnDeleteCertificate(request *iso15118.DeleteCertificateRequest) (*iso15118.DeleteCertificateResponse, error) {
	slog.Info("OCPP 2.0.1 DeleteCertificate received (not supported)")
	return &iso15118.DeleteCertificateResponse{Status: iso15118.DeleteCertificateStatusFailed}, nil
}

func (b *Bridge201) OnGetInstalledCertificateIds(request *iso15118.GetInstalledCertificateIdsRequest) (*iso15118.GetInstalledCertificateIdsResponse, error) {
	slog.Info("OCPP 2.0.1 GetInstalledCertificateIds received (not supported)")
	return &iso15118.GetInstalledCertificateIdsResponse{Status: iso15118.GetInstalledCertificateStatusNotFound}, nil
}

func (b *Bridge201) OnInstallCertificate(request *iso15118.InstallCertificateRequest) (*iso15118.InstallCertificateResponse, error) {
	slog.Info("OCPP 2.0.1 InstallCertificate received (not supported)")
	return &iso15118.InstallCertificateResponse{Status: iso15118.CertificateStatusRejected}, nil
}
