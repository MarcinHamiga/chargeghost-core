package v201

import (
	"log/slog"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/authorization"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/availability"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/remotecontrol"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/smartcharging"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"

	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
)

// -- provisioning.ChargingStationHandler --

func (b *Bridge201) OnGetBaseReport(request *provisioning.GetBaseReportRequest) (*provisioning.GetBaseReportResponse, error) {
	slog.Info("OCPP 2.0.1 GetBaseReport received", "requestId", request.RequestID)

	// Send NotifyReport asynchronously
	go func() {
		reportData := b.deviceModel.BuildNotifyReportData()
		now := types.NewDateTime(time.Now())
		req := provisioning.NewNotifyReportRequest(request.RequestID, now, 0)
		req.ReportData = reportData
		req.Tbc = false

		cb := func(resp ocpp.Response, err error) {
			if err != nil {
				slog.Error("NotifyReport failed", "error", err)
			}
		}
		if err := b.cs.SendRequestAsync(req, cb); err != nil {
			slog.Error("failed to send NotifyReport", "error", err)
		}
	}()

	return &provisioning.GetBaseReportResponse{Status: types.GenericDeviceModelStatusAccepted}, nil
}

func (b *Bridge201) OnGetReport(request *provisioning.GetReportRequest) (*provisioning.GetReportResponse, error) {
	slog.Info("OCPP 2.0.1 GetReport received", "requestId", request.RequestID)
	return &provisioning.GetReportResponse{Status: types.GenericDeviceModelStatusAccepted}, nil
}

func (b *Bridge201) OnGetVariables(request *provisioning.GetVariablesRequest) (*provisioning.GetVariablesResponse, error) {
	slog.Info("OCPP 2.0.1 GetVariables received", "count", len(request.GetVariableData))
	results := b.deviceModel.BuildGetVariablesResponse(request.GetVariableData)
	return &provisioning.GetVariablesResponse{GetVariableResult: results}, nil
}

func (b *Bridge201) OnSetVariables(request *provisioning.SetVariablesRequest) (*provisioning.SetVariablesResponse, error) {
	slog.Info("OCPP 2.0.1 SetVariables received", "count", len(request.SetVariableData))
	results := b.deviceModel.BuildSetVariablesResponse(request.SetVariableData)
	return &provisioning.SetVariablesResponse{SetVariableResult: results}, nil
}

func (b *Bridge201) OnReset(request *provisioning.ResetRequest) (*provisioning.ResetResponse, error) {
	slog.Info("OCPP 2.0.1 Reset received", "type", request.Type)
	return &provisioning.ResetResponse{Status: provisioning.ResetStatusAccepted}, nil
}

func (b *Bridge201) OnSetNetworkProfile(request *provisioning.SetNetworkProfileRequest) (*provisioning.SetNetworkProfileResponse, error) {
	slog.Info("OCPP 2.0.1 SetNetworkProfile received", "slot", request.ConfigurationSlot)
	return &provisioning.SetNetworkProfileResponse{Status: provisioning.SetNetworkProfileStatusRejected}, nil
}

// -- availability.ChargingStationHandler --

func (b *Bridge201) OnChangeAvailability(request *availability.ChangeAvailabilityRequest) (*availability.ChangeAvailabilityResponse, error) {
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

	// Station-level: evse omitted (nil) or evseId == 0.
	if request.Evse == nil || request.Evse.ID == 0 {
		b.deviceModel.SetVariable("ChargingStation", "", 0, "AvailabilityState", newState, MutabilityReadOnly)
		for _, id := range b.engine.GetConnectorIDs() {
			connID := id
			b.engine.SetConnectorAvailability(connID, availType)
			// EVSE/Connector AvailabilityState updated via SendStatusNotification callback.
			slog.Info("OCPP 2.0.1 ChangeAvailability applied", "connector", connID, "state", newState)
		}
		return &availability.ChangeAvailabilityResponse{Status: availability.ChangeAvailabilityStatusAccepted}, nil
	}

	// EVSE-level targeting (evseId == connectorID in our single-connector-per-EVSE model).
	connID := request.Evse.ID
	b.engine.SetConnectorAvailability(connID, availType)
	// EVSE/Connector AvailabilityState updated via SendStatusNotification callback.
	slog.Info("OCPP 2.0.1 ChangeAvailability applied", "connector", connID, "state", newState)
	return &availability.ChangeAvailabilityResponse{Status: availability.ChangeAvailabilityStatusAccepted}, nil
}

// -- authorization.ChargingStationHandler --

func (b *Bridge201) OnClearCache(request *authorization.ClearCacheRequest) (*authorization.ClearCacheResponse, error) {
	slog.Info("OCPP 2.0.1 ClearCache received")
	if b.authCache != nil {
		b.authCache.Clear()
	}
	return authorization.NewClearCacheResponse(authorization.ClearCacheStatusAccepted), nil
}

// -- transactions.ChargingStationHandler --

func (b *Bridge201) OnGetTransactionStatus(request *transactions.GetTransactionStatusRequest) (*transactions.GetTransactionStatusResponse, error) {
	slog.Info("OCPP 2.0.1 GetTransactionStatus received", "transactionId", request.TransactionID)
	b.mu.Lock()
	hasActive := len(b.txBuilders) > 0
	b.mu.Unlock()
	hasQueued := b.queue != nil && b.queue.Len() > 0
	resp := transactions.NewGetTransactionStatusResponse(hasQueued)
	resp.OngoingIndicator = &hasActive
	return resp, nil
}

// -- remotecontrol.ChargingStationHandler --

func (b *Bridge201) OnRequestStartTransaction(request *remotecontrol.RequestStartTransactionRequest) (*remotecontrol.RequestStartTransactionResponse, error) {
	slog.Info("OCPP 2.0.1 RequestStartTransaction received", "idToken", request.IDToken.IdToken)

	evseID := 1
	if request.EvseID != nil {
		evseID = *request.EvseID
	}

	idTag := request.IDToken.IdToken
	err := b.engine.StartSession(evseID, 0, 0, &idTag, 0)
	if err != nil {
		slog.Warn("OCPP 2.0.1 RequestStartTransaction rejected", "error", err)
		return remotecontrol.NewRequestStartTransactionResponse(remotecontrol.RequestStartStopStatusRejected), nil
	}

	return remotecontrol.NewRequestStartTransactionResponse(remotecontrol.RequestStartStopStatusAccepted), nil
}

func (b *Bridge201) OnRequestStopTransaction(request *remotecontrol.RequestStopTransactionRequest) (*remotecontrol.RequestStopTransactionResponse, error) {
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
	default:
		return remotecontrol.NewTriggerMessageResponse(remotecontrol.TriggerMessageStatusNotImplemented), nil
	}

	return remotecontrol.NewTriggerMessageResponse(remotecontrol.TriggerMessageStatusAccepted), nil
}

func (b *Bridge201) OnUnlockConnector(request *remotecontrol.UnlockConnectorRequest) (*remotecontrol.UnlockConnectorResponse, error) {
	slog.Info("OCPP 2.0.1 UnlockConnector received", "evseId", request.EvseID, "connectorId", request.ConnectorID)
	b.engine.Unplug(request.EvseID)
	return remotecontrol.NewUnlockConnectorResponse(remotecontrol.UnlockStatusUnlocked), nil
}

// -- smartcharging.ChargingStationHandler --

func (b *Bridge201) OnSetChargingProfile(request *smartcharging.SetChargingProfileRequest) (*smartcharging.SetChargingProfileResponse, error) {
	if request.ChargingProfile == nil {
		slog.Error("OCPP 2.0.1 SetChargingProfile received with nil ChargingProfile", "evseId", request.EvseID)
		return smartcharging.NewSetChargingProfileResponse(smartcharging.ChargingProfileStatusRejected), nil
	}
	slog.Info("OCPP 2.0.1 SetChargingProfile received", "evseId", request.EvseID, "profileId", request.ChargingProfile.ID)
	b.profileManager.SetProfile(request.EvseID, *request.ChargingProfile)
	return smartcharging.NewSetChargingProfileResponse(smartcharging.ChargingProfileStatusAccepted), nil
}

func (b *Bridge201) OnClearChargingProfile(request *smartcharging.ClearChargingProfileRequest) (*smartcharging.ClearChargingProfileResponse, error) {
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
	// Not implemented — return Rejected as per OCPP 2.0.1 requirements when no schedule is provided
	return smartcharging.NewGetCompositeScheduleResponse(smartcharging.GetCompositeScheduleStatusRejected, request.EvseID), nil
}

func (b *Bridge201) OnNotifyEVChargingSchedule(request *smartcharging.NotifyEVChargingScheduleRequest) (*smartcharging.NotifyEVChargingScheduleResponse, error) {
	slog.Info("OCPP 2.0.1 NotifyEVChargingSchedule received")
	return smartcharging.NewNotifyEVChargingScheduleResponse(types.GenericStatusAccepted), nil
}

func (b *Bridge201) OnNotifyEVChargingNeeds(request *smartcharging.NotifyEVChargingNeedsRequest) (*smartcharging.NotifyEVChargingNeedsResponse, error) {
	slog.Info("OCPP 2.0.1 NotifyEVChargingNeeds received")
	return smartcharging.NewNotifyEVChargingNeedsResponse(smartcharging.EVChargingNeedsStatusAccepted), nil
}
