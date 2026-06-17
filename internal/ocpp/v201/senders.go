package v201

import (
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/authorization"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/availability"
	data201 "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/data"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/diagnostics"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/firmware"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"

	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

// SendBootNotification sends a BootNotification to the CSMS.
// Per OCPP 2.0.1 §3.1 the request MUST include a chargingStation object
// with at least model and vendorName.  We additionally populate
// serialNumber, firmwareVersion, and modem (when configured) so the CSMS
// has enough context to provision the station.
func (b *Bridge201) SendBootNotification() error {
	b.tl.LogOutbound("BootNotification", nil, nil, fmt.Sprintf("model=%s vendor=%s", b.cfg.ChargePointModel, b.cfg.ChargePointVendor), nil)
	resp, err := b.cs.BootNotification(
		provisioning.BootReasonPowerUp,
		b.cfg.ChargePointModel,
		b.cfg.ChargePointVendor,
		func(req *provisioning.BootNotificationRequest) {
			req.ChargingStation.SerialNumber = b.cfg.ChargePointSerial
			req.ChargingStation.FirmwareVersion = b.cfg.FirmwareVersion
			if b.cfg.ModemICCID != "" || b.cfg.ModemIMSI != "" {
				req.ChargingStation.Modem = &provisioning.ModemType{
					Iccid: b.cfg.ModemICCID,
					Imsi:  b.cfg.ModemIMSI,
				}
			}
		},
	)
	if err != nil {
		b.tl.LogError("BootNotification", "outbound", nil, err.Error(), nil, "")
		return fmt.Errorf("BootNotification send: %w", err)
	}
	slog.Info("BootNotification 2.0.1 response", "status", resp.Status, "interval", resp.Interval)

	if resp.Status == provisioning.RegistrationStatusAccepted {
		b.heartbeatInt = resp.Interval
		b.deviceModel.SetVariable("OCPPCommCtrlr", "", 0, "HeartbeatInterval", fmt.Sprintf("%d", resp.Interval), MutabilityReadWrite)
		if b.queue != nil {
			// Queue replay uses SendRequestAsync and must not block the dispatcher,
			// so replay can interleave with the post-boot status refresh below.
			go b.drainQueue()
		}
		// Send StatusNotification for each connector.
		for _, id := range b.engine.GetConnectorIDs() {
			connID := id
			b.dispatcher.Enqueue(ocpppkg.OCPPCommand{
				Description: fmt.Sprintf("StatusNotification connector %d", connID),
				Execute: func() error {
					return b.SendStatusNotification(connID, "NoError", b.engine.GetConnectorStatus(connID))
				},
			})
		}
		// Start heartbeat loop (cancels any previously running loop).
		b.restartHeartbeat()
	}
	return nil
}

// SendHeartbeat sends a Heartbeat to the CSMS.
func (b *Bridge201) SendHeartbeat() error {
	b.tl.LogOutbound("Heartbeat", nil, nil, "Heartbeat", nil)
	_, err := b.cs.Heartbeat()
	if err != nil {
		b.tl.LogError("Heartbeat", "outbound", nil, err.Error(), nil, "")
	}
	return err
}

// SendStatusNotification sends StatusNotification for a connector.
// In OCPP 2.0.1, evseID == connectorID (1-based single connector per EVSE).
func (b *Bridge201) SendStatusNotification(connectorID int, _ string, status string) error {
	b.tl.LogOutbound("StatusNotification", ocpppkg.IntPtr(connectorID), nil, fmt.Sprintf("evse=%d status=%s", connectorID, status), nil)
	cs := mapConnectorStatus(status)

	// Update device model state
	b.deviceModel.SetVariable("EVSE", "", connectorID, "AvailabilityState", string(cs), MutabilityReadOnly)
	b.deviceModel.SetVariable("Connector", "", connectorID, "AvailabilityState", string(cs), MutabilityReadOnly)

	_, err := b.cs.StatusNotification(
		types.NewDateTime(time.Now()),
		cs,
		connectorID, // evseID
		1,           // connectorID within EVSE is always 1 in our model
	)
	return err
}

// mapConnectorStatus maps engine ConnectorState strings to OCPP 2.0.1 ConnectorStatus.
func mapConnectorStatus(status string) availability.ConnectorStatus {
	switch engine.ConnectorState(status) {
	case engine.StateAvailable:
		return availability.ConnectorStatusAvailable
	case engine.StatePreparing, engine.StateCharging, engine.StateSuspendedEV, engine.StateSuspendedEVSE, engine.StateFinishing:
		return availability.ConnectorStatusOccupied
	case engine.StateReserved:
		return availability.ConnectorStatusReserved
	case engine.StateUnavailable:
		return availability.ConnectorStatusUnavailable
	case engine.StateFaulted:
		return availability.ConnectorStatusFaulted
	default:
		return availability.ConnectorStatusAvailable
	}
}

// SendTransactionStart sends a TransactionEvent(Started) to the CSMS.
func (b *Bridge201) SendTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	b.tl.LogOutbound("TransactionEvent", ocpppkg.IntPtr(connectorID), nil, fmt.Sprintf("Started evse=%d idTag=%s meter=%s", connectorID, idTag, ocpppkg.FormatMeter(meterStart)), nil)
	evseID := connectorID
	connID := 1

	builder := NewTransactionEventBuilder(evseID, connID)

	b.mu.Lock()
	b.nextTxInt++
	txInt := b.nextTxInt
	b.txBuilders[evseID] = builder
	b.txIntToEVSE[txInt] = evseID
	b.txStringToEVSE[builder.TransactionID()] = evseID
	b.mu.Unlock()

	idToken := types.IdToken{
		IdToken: idTag,
		Type:    types.IdTokenTypeISO14443,
	}

	// Update device model with starting meter reading
	b.deviceModel.SetVariable("EVSE", "", evseID, "Energy.Active.Import.Register", fmt.Sprintf("%.2f", meterStart), MutabilityReadOnly)

	meter := makeMeterValue(meterStart, timestamp, string(types.ReadingContextTransactionBegin))
	req := builder.Started(idToken, &meter, timestamp)

	if reservationID != nil {
		req.ReservationID = reservationID
	}

	if !b.IsConnected() && b.queue != nil {
		_, err := b.queue.Enqueue(queue.QueuedMessage{
			Type:    "TransactionEvent",
			Payload: req,
		})
		if err != nil {
			return 0, fmt.Errorf("enqueue TransactionEvent(Started): %w", err)
		}
		slog.Info("queued TransactionEvent(Started)", "txId", builder.TransactionID())
		return txInt, nil
	}

	cb := func(response ocpp.Response, err error) {
		if err != nil {
			slog.Error("TransactionEvent(Started) failed", "error", err)
			return
		}
		slog.Info("TransactionEvent(Started) accepted", "txId", builder.TransactionID())
	}

	if err := b.cs.SendRequestAsync(req, cb); err != nil {
		return 0, err
	}
	return txInt, nil
}

// SendTransactionStop sends a TransactionEvent(Ended) to the CSMS.
func (b *Bridge201) SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error {
	b.tl.LogOutbound("TransactionEvent", nil, &transactionID, fmt.Sprintf("Ended txId=%d meter=%s reason=%s", transactionID, ocpppkg.FormatMeter(meterStop), reason), nil)
	b.mu.Lock()
	evseID, ok := b.txIntToEVSE[transactionID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("no active transaction for ID %d", transactionID)
	}
	builder, ok := b.txBuilders[evseID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("no active transaction builder for EVSE %d", evseID)
	}
	delete(b.txBuilders, evseID)
	delete(b.txIntToEVSE, transactionID)
	txIDStr := builder.TransactionID()
	delete(b.txStringToEVSE, txIDStr)
	noActiveTx := len(b.txBuilders) == 0
	b.mu.Unlock()

	// Clear any transaction-scoped charging profiles tied to this transaction
	// so they don't leak into subsequent sessions on the same EVSE. Outside the
	// mutex because profile_manager has its own lock.
	if b.profileManager != nil {
		b.profileManager.ClearTxProfilesForTransaction(txIDStr)
	}

	// triggerReset may already have set pendingReset while stop events were still
	// queued. Completing the reset here keeps the post-stop boot flow consistent
	// once the final bridge-local transaction state is gone.
	if noActiveTx && b.pendingReset.CompareAndSwap(true, false) {
		b.completeReset()
	}

	// Update device model with final meter reading - outside b.mu
	b.deviceModel.SetVariable("EVSE", "", evseID, "Energy.Active.Import.Register", fmt.Sprintf("%.2f", meterStop), MutabilityReadOnly)

	stopReason := mapStopReason(reason)
	meter := makeMeterValue(meterStop, timestamp, string(types.ReadingContextTransactionEnd))
	var idToken *types.IdToken
	if idTag != nil && *idTag != "" {
		idToken = &types.IdToken{IdToken: *idTag, Type: types.IdTokenTypeISO14443}
	}
	req := builder.Ended(stopReason, &meter, timestamp, idToken)

	if !b.IsConnected() && b.queue != nil {
		_, err := b.queue.Enqueue(queue.QueuedMessage{
			Type:    "TransactionEvent",
			Payload: req,
		})
		if err != nil {
			return fmt.Errorf("enqueue TransactionEvent(Ended): %w", err)
		}
		slog.Info("queued TransactionEvent(Ended)", "txId", builder.TransactionID())
		return nil
	}

	cb := func(response ocpp.Response, err error) {
		if err != nil {
			slog.Error("TransactionEvent(Ended) failed", "error", err)
			return
		}
		slog.Info("TransactionEvent(Ended) accepted", "txId", builder.TransactionID())
	}

	return b.cs.SendRequestAsync(req, cb)
}

// mapStopReason maps v1.6-style stop reason strings to OCPP 2.0.1 Reason constants.
func mapStopReason(v16Reason string) transactions.Reason {
	switch v16Reason {
	case "EVDisconnected":
		return transactions.ReasonEVDisconnected
	case "Remote":
		return transactions.ReasonRemote
	case "Local":
		return transactions.ReasonLocal
	case "PowerLoss":
		return transactions.ReasonPowerLoss
	case "Reboot":
		return transactions.ReasonReboot
	case "EmergencyStop":
		return transactions.ReasonEmergencyStop
	default:
		return transactions.ReasonOther
	}
}

// mapTriggerReason maps a free-form trigger string to the OCPP 2.0.1
// TriggerReason enum. Unknown values fall back to TriggerReasonChargingStateChanged
// because that is the most common reason for an Updated event from a charger
// state transition; callers may pass more specific reasons when appropriate.
func mapTriggerReason(trigger string) transactions.TriggerReason {
	switch transactions.TriggerReason(trigger) {
	case transactions.TriggerReasonAuthorized:
		return transactions.TriggerReasonAuthorized
	case transactions.TriggerReasonCablePluggedIn:
		return transactions.TriggerReasonCablePluggedIn
	case transactions.TriggerReasonChargingRateChanged:
		return transactions.TriggerReasonChargingRateChanged
	case transactions.TriggerReasonChargingStateChanged:
		return transactions.TriggerReasonChargingStateChanged
	case transactions.TriggerReasonDeAuthorized:
		return transactions.TriggerReasonDeAuthorized
	case transactions.TriggerReasonEnergyLimitReached:
		return transactions.TriggerReasonEnergyLimitReached
	case transactions.TriggerReasonEVCommunicationLost:
		return transactions.TriggerReasonEVCommunicationLost
	case transactions.TriggerReasonEVConnectTimeout:
		return transactions.TriggerReasonEVConnectTimeout
	case transactions.TriggerReasonMeterValueClock:
		return transactions.TriggerReasonMeterValueClock
	case transactions.TriggerReasonMeterValuePeriodic:
		return transactions.TriggerReasonMeterValuePeriodic
	case transactions.TriggerReasonTimeLimitReached:
		return transactions.TriggerReasonTimeLimitReached
	case transactions.TriggerReasonTrigger:
		return transactions.TriggerReasonTrigger
	case transactions.TriggerReasonUnlockCommand:
		return transactions.TriggerReasonUnlockCommand
	case transactions.TriggerReasonStopAuthorized:
		return transactions.TriggerReasonStopAuthorized
	case transactions.TriggerReasonEVDeparted:
		return transactions.TriggerReasonEVDeparted
	case transactions.TriggerReasonEVDetected:
		return transactions.TriggerReasonEVDetected
	case transactions.TriggerReasonRemoteStop:
		return transactions.TriggerReasonRemoteStop
	case transactions.TriggerReasonRemoteStart:
		return transactions.TriggerReasonRemoteStart
	case transactions.TriggerReasonAbnormalCondition:
		return transactions.TriggerReasonAbnormalCondition
	case transactions.TriggerReasonSignedDataReceived:
		return transactions.TriggerReasonSignedDataReceived
	case transactions.TriggerReasonResetCommand:
		return transactions.TriggerReasonResetCommand
	default:
		return transactions.TriggerReasonChargingStateChanged
	}
}

// SendTransactionEventUpdated emits a TransactionEvent(Updated) reflecting a
// charging-state transition (e.g. Charging → SuspendedEV → Charging). It is
// invoked from the engine OnChargingStateChanged callback. The chargingState
// argument is the engine ConnectorState value as a string. The trigger
// argument is a free-form string that is mapped to a known OCPP 2.0.1
// TriggerReason (unknown values fall back to ChargingStateChanged).
func (b *Bridge201) SendTransactionEventUpdated(connectorID int, chargingState, trigger string) error {
	state, ok := engineStateToChargingState(chargingState)
	if !ok {
		// Engine state does not correspond to a charging state (e.g.
		// Available, Reserved, Unavailable, Faulted). Nothing to report.
		return nil
	}
	evseID := connectorID

	b.mu.Lock()
	builder, hasBuilder := b.txBuilders[evseID]
	b.mu.Unlock()

	if !hasBuilder {
		// No active transaction. The CSMS only cares about charging-state
		// changes within a transaction. Quietly skip.
		return nil
	}

	reason := mapTriggerReason(trigger)
	now := time.Now()
	meter, _ := b.engine.GetMeterSnapshot(connectorID)
	meterVal := makeMeterValue(meter, now, string(types.ReadingContextOther))

	b.tl.LogOutbound("TransactionEvent", ocpppkg.IntPtr(connectorID), nil,
		fmt.Sprintf("Updated evse=%d state=%s trigger=%s", connectorID, state, reason), nil)

	req := builder.Updated(reason, &meterVal, now, state)

	if !b.IsConnected() && b.queue != nil {
		_, err := b.queue.Enqueue(queue.QueuedMessage{
			Type:    "TransactionEvent",
			Payload: req,
		})
		if err != nil {
			return fmt.Errorf("enqueue TransactionEvent(Updated): %w", err)
		}
		return nil
	}

	cb := func(response ocpp.Response, err error) {
		if err != nil {
			slog.Error("TransactionEvent(Updated) failed", "error", err)
		}
	}

	return b.cs.SendRequestAsync(req, cb)
}

// SendMeterValues sends a TransactionEvent(Updated) with meter data to the CSMS.
func (b *Bridge201) SendMeterValues(connectorID int, value float64, transactionID int, meterContext string) error {
	b.tl.LogOutbound("TransactionEvent", ocpppkg.IntPtr(connectorID), &transactionID, fmt.Sprintf("Updated evse=%d meter=%s context=%s", connectorID, ocpppkg.FormatMeter(value), meterContext), nil)
	evseID := connectorID

	b.mu.Lock()
	builder, ok := b.txBuilders[evseID]
	b.mu.Unlock()

	if !ok {
		return fmt.Errorf("no active transaction builder for EVSE %d", evseID)
	}

	// Update device model with latest power/energy reading
	b.deviceModel.SetVariable("EVSE", "", evseID, "Energy.Active.Import.Register", fmt.Sprintf("%.2f", value), MutabilityReadOnly)

	// Resolve TxUpdatedMeasurands from the device model.  This honors
	// the CSMS-configured subset of measurands (comma-separated).
	rawMeasurands := b.deviceModel.GetVariable("SampledDataCtrlr", "", 0, "TxUpdatedMeasurands")
	measurands := parseMeasurandList(rawMeasurands.Value)

	// Render the meter value honoring the requested measurands.  Use
	// the engine's connector voltage/current/phases if available;
	// otherwise fall back to the defaults used by makeMeterValue().
	voltage, current, phases := 230.0, 32.0, 1
	if c := b.engine.GetConnector(evseID); c != nil {
		voltage = c.Voltage
		current = c.Current
		phases = c.Phase
	}

	now := time.Now()
	meter := makeMeterValueForMeasurands(value, voltage, current, phases, now, meterContext, measurands)

	req := builder.Updated(triggerReasonForMeterContext(meterContext), &meter, now)

	if !b.IsConnected() && b.queue != nil {
		_, err := b.queue.Enqueue(queue.QueuedMessage{
			Type:    "TransactionEvent",
			Payload: req,
		})
		if err != nil {
			return fmt.Errorf("enqueue TransactionEvent(Updated): %w", err)
		}
		return nil
	}

	cb := func(response ocpp.Response, err error) {
		if err != nil {
			slog.Error("TransactionEvent(Updated) failed", "error", err)
		}
	}

	return b.cs.SendRequestAsync(req, cb)
}

// SendAuthorize sends an Authorize request to the CSMS and waits for the response.
func (b *Bridge201) SendAuthorize(idTag string) error {
	b.tl.LogOutbound("Authorize", nil, nil, fmt.Sprintf("idTag=%s", idTag), nil)
	done := make(chan error, 1)
	err := b.cs.SendRequestAsync(
		authorization.NewAuthorizationRequest(idTag, types.IdTokenTypeISO14443),
		func(response ocpp.Response, err error) {
			if err != nil {
				b.tl.LogError("Authorize", "outbound", nil, err.Error(), nil, "")
				done <- err
				return
			}
			resp, ok := response.(*authorization.AuthorizeResponse)
			if !ok {
				done <- fmt.Errorf("unexpected Authorize response type: %T", response)
				return
			}
			status := string(resp.IdTokenInfo.Status)
			if normalized, ok := ocpppkg.NormalizeAuthorizationStatus(status); ok {
				status = normalized
			}
			var expiry *time.Time
			if resp.IdTokenInfo.CacheExpiryDateTime != nil {
				t := resp.IdTokenInfo.CacheExpiryDateTime.Time
				expiry = &t
			}
			b.cacheAuthorizationDecision(idTag, status, expiry)
			if status != string(types.AuthorizationStatusAccepted) {
				done <- fmt.Errorf("authorize rejected: status=%s", status)
				return
			}
			done <- nil
		},
	)
	if err != nil {
		return err
	}
	return <-done
}

// mapFirmwareStatus maps shared FirmwareManager status strings to OCPP 2.0.1 FirmwareStatus.
func mapFirmwareStatus(status string) firmware.FirmwareStatus {
	switch status {
	case "Downloading":
		return firmware.FirmwareStatusDownloading
	case "Downloaded":
		return firmware.FirmwareStatusDownloaded
	case "Installing":
		return firmware.FirmwareStatusInstalling
	case "Installed":
		return firmware.FirmwareStatusInstalled
	case "InstallationFailed":
		return firmware.FirmwareStatusInstallationFailed
	default:
		return firmware.FirmwareStatusIdle
	}
}

// SendFirmwareStatusNotification sends a FirmwareStatusNotification to the CSMS.
func (b *Bridge201) SendFirmwareStatusNotification(status string) error {
	b.tl.LogOutbound("FirmwareStatusNotification", nil, nil, fmt.Sprintf("status=%s", status), nil)
	_, err := b.cs.FirmwareStatusNotification(mapFirmwareStatus(status))
	if err != nil {
		b.tl.LogError("FirmwareStatusNotification", "outbound", nil, err.Error(), nil, "")
	}
	return err
}

// mapDiagnosticsStatus maps shared DiagnosticsManager status strings to OCPP 2.0.1 UploadLogStatus.
func mapDiagnosticsStatus(status string) diagnostics.UploadLogStatus {
	switch status {
	case "Uploading":
		return diagnostics.UploadLogStatusUploading
	case "Uploaded":
		return diagnostics.UploadLogStatusUploaded
	case "UploadFailed":
		return diagnostics.UploadLogStatusUploadFailure
	default:
		return diagnostics.UploadLogStatusIdle
	}
}

// SendDiagnosticsStatusNotification sends a LogStatusNotification to the CSMS.
func (b *Bridge201) SendDiagnosticsStatusNotification(status string) error {
	requestID := int(b.diagRequestID.Load())
	if requestID <= 0 {
		return nil
	}
	b.tl.LogOutbound("LogStatusNotification", nil, nil, fmt.Sprintf("status=%s requestId=%d", status, requestID), nil)
	_, err := b.cs.LogStatusNotification(mapDiagnosticsStatus(status), requestID)
	if err != nil {
		b.tl.LogError("LogStatusNotification", "outbound", nil, err.Error(), nil, "")
		return err
	}
	if status == "Uploaded" || status == "UploadFailed" || status == "Idle" {
		b.diagRequestID.Store(0)
	}
	return nil
}

// SendDataTransfer passes a vendor-specific message to the CSMS.
func (b *Bridge201) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
	b.tl.LogOutbound("DataTransfer", nil, nil, fmt.Sprintf("vendor=%s messageId=%s", vendorID, messageID), nil)
	resp, err := b.cs.DataTransfer(vendorID, func(req *data201.DataTransferRequest) {
		req.MessageID = messageID
		if data != "" {
			req.Data = data
		}
	})
	if err != nil {
		return "", "", err
	}
	responseData := ocpppkg.DataTransferDataString(resp.Data)
	return string(resp.Status), responseData, nil
}

// notifyEventSeqNo is the per-bridge counter for NotifyEvent messages.
// A real charger would use the variable-monitoring IDs to track events;
// we use a simple monotonic counter starting at 0 because OCPP 2.0.1
// NotifyEvent does not require it to be unique per CSMS event stream.
var notifyEventSeqNo atomic.Int64

// SendConnectorEventNotification emits a NotifyEvent to the CSMS for a
// status change on the given device-model variable. Per OCPP 2.0.1 §3.21,
// this is a hardwired "PreconfiguredMonitor" event with Alerting trigger.
// connectorID is the EVSE id; if evseComponent is true the Component
// references the EVSE; otherwise the Component is station-level.
func (b *Bridge201) SendConnectorEventNotification(connectorID int, component, instance, variable, actualValue string, evseComponent bool) error {
	comp := types.Component{Name: component, Instance: instance}
	if evseComponent && connectorID > 0 {
		comp.EVSE = &types.EVSE{ID: connectorID}
	}
	var txID string
	if evseComponent {
		txID = b.ActiveTxIDForEVSE(connectorID)
	}

	ev := diagnostics.EventData{
		EventID:               0,
		Timestamp:             types.NewDateTime(time.Now()),
		Trigger:               diagnostics.EventTriggerAlerting,
		ActualValue:           actualValue,
		Cleared:               false,
		TransactionID:         txID,
		EventNotificationType: diagnostics.EventHardWiredNotification,
		Component:             comp,
		Variable:              types.Variable{Name: variable},
	}

	seqNo := int(notifyEventSeqNo.Add(1) - 1)
	req := diagnostics.NewNotifyEventRequest(types.NewDateTime(time.Now()), seqNo, []diagnostics.EventData{ev})

	b.tl.LogOutbound("NotifyEvent", ocpppkg.IntPtr(connectorID), nil,
		fmt.Sprintf("%s.%s=%s evseScope=%t", component, variable, actualValue, evseComponent), nil)

	if !b.IsConnected() {
		// We don't persist NotifyEvents across reconnects (the OCPP 2.0.1
		// spec only requires CSMS-side persistence of monitoring reports,
		// not the events themselves).
		slog.Debug("NotifyEvent dropped: OCPP not connected", "component", component, "variable", variable)
		return nil
	}

	cb := func(response ocpp.Response, err error) {
		if err != nil {
			slog.Error("NotifyEvent failed", "error", err)
		}
	}
	return b.cs.SendRequestAsync(req, cb)
}
