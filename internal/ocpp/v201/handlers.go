package v201

import (
	"log/slog"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/authorization"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/availability"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/remotecontrol"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"

	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
)

// -- provisioning.ChargingStationHandler --

func (b *Bridge201) OnGetBaseReport(request *provisioning.GetBaseReportRequest) (*provisioning.GetBaseReportResponse, error) {
	slog.Info("OCPP 2.0.1 GetBaseReport received", "requestId", request.RequestID)
	return &provisioning.GetBaseReportResponse{Status: types.GenericDeviceModelStatusAccepted}, nil
}

func (b *Bridge201) OnGetReport(request *provisioning.GetReportRequest) (*provisioning.GetReportResponse, error) {
	slog.Info("OCPP 2.0.1 GetReport received", "requestId", request.RequestID)
	return &provisioning.GetReportResponse{Status: types.GenericDeviceModelStatusAccepted}, nil
}

func (b *Bridge201) OnGetVariables(request *provisioning.GetVariablesRequest) (*provisioning.GetVariablesResponse, error) {
	slog.Info("OCPP 2.0.1 GetVariables received", "count", len(request.GetVariableData))
	results := make([]provisioning.GetVariableResult, len(request.GetVariableData))
	for i, d := range request.GetVariableData {
		results[i] = provisioning.GetVariableResult{
			AttributeStatus: provisioning.GetVariableStatusUnknownComponent,
			Component:       d.Component,
			Variable:        d.Variable,
		}
	}
	return &provisioning.GetVariablesResponse{GetVariableResult: results}, nil
}

func (b *Bridge201) OnSetVariables(request *provisioning.SetVariablesRequest) (*provisioning.SetVariablesResponse, error) {
	slog.Info("OCPP 2.0.1 SetVariables received", "count", len(request.SetVariableData))
	results := make([]provisioning.SetVariableResult, len(request.SetVariableData))
	for i, d := range request.SetVariableData {
		results[i] = provisioning.SetVariableResult{
			AttributeStatus: provisioning.SetVariableStatusUnknownComponent,
			Component:       d.Component,
			Variable:        d.Variable,
		}
	}
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
		for _, id := range b.engine.GetConnectorIDs() {
			connID := id
			b.engine.SetConnectorAvailability(connID, availType)
			slog.Info("OCPP 2.0.1 ChangeAvailability applied", "connector", connID, "state", newState)
		}
		return &availability.ChangeAvailabilityResponse{Status: availability.ChangeAvailabilityStatusAccepted}, nil
	}

	// EVSE-level targeting (evseId == connectorID in our single-connector-per-EVSE model).
	connID := request.Evse.ID
	b.engine.SetConnectorAvailability(connID, availType)
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
