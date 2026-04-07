package v201

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/authorization"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/availability"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"

	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

// SendBootNotification sends a BootNotification to the CSMS.
func (b *Bridge201) SendBootNotification() error {
	resp, err := b.cs.BootNotification(
		provisioning.BootReasonPowerUp,
		b.cfg.ChargePointModel,
		b.cfg.ChargePointVendor,
	)
	if err != nil {
		return fmt.Errorf("BootNotification send: %w", err)
	}
	slog.Info("BootNotification 2.0.1 response", "status", resp.Status, "interval", resp.Interval)

	if resp.Status == provisioning.RegistrationStatusAccepted {
		b.heartbeatInt = resp.Interval
		b.deviceModel.SetVariable("OCPPCommCtrlr", "", 0, "HeartbeatInterval", fmt.Sprintf("%d", resp.Interval), MutabilityReadWrite)
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
		go b.heartbeatLoop()
	}
	return nil
}

// SendHeartbeat sends a Heartbeat to the CSMS.
func (b *Bridge201) SendHeartbeat() error {
	_, err := b.cs.Heartbeat()
	return err
}

// SendStatusNotification sends StatusNotification for a connector.
// In OCPP 2.0.1, evseID == connectorID (1-based single connector per EVSE).
func (b *Bridge201) SendStatusNotification(connectorID int, _ string, status string) error {
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
	evseID := connectorID
	connID := 1

	builder := NewTransactionEventBuilder(evseID, connID)

	b.mu.Lock()
	b.nextTxInt++
	txInt := b.nextTxInt
	b.txBuilders[evseID] = builder
	b.txIntToEVSE[txInt] = evseID
	b.mu.Unlock()

	idToken := types.IdToken{
		IdToken: idTag,
		Type:    types.IdTokenTypeISO14443,
	}

	// Update device model with starting meter reading
	b.deviceModel.SetVariable("EVSE", "", evseID, "Energy.Active.Import.Register", fmt.Sprintf("%.2f", meterStart), MutabilityReadOnly)

	meter := makeMeterValue(meterStart, timestamp)
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
func (b *Bridge201) SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error {
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
	b.mu.Unlock()

	// Update device model with final meter reading - outside b.mu
	b.deviceModel.SetVariable("EVSE", "", evseID, "Energy.Active.Import.Register", fmt.Sprintf("%.2f", meterStop), MutabilityReadOnly)

	stopReason := mapStopReason(reason)
	meter := makeMeterValue(meterStop, timestamp)
	req := builder.Ended(stopReason, &meter, timestamp)

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
// SendMeterValues sends a TransactionEvent(Updated) with meter data to the CSMS.
func (b *Bridge201) SendMeterValues(connectorID int, value float64, transactionID int, meterContext string) error {
	evseID := connectorID

	b.mu.Lock()
	builder, ok := b.txBuilders[evseID]
	b.mu.Unlock()

	if !ok {
		return fmt.Errorf("no active transaction builder for EVSE %d", evseID)
	}

	// Update device model with latest power/energy reading
	b.deviceModel.SetVariable("EVSE", "", evseID, "Energy.Active.Import.Register", fmt.Sprintf("%.2f", value), MutabilityReadOnly)

	now := time.Now()
	meter := makeMeterValue(value, now)

	req := builder.Updated(transactions.TriggerReasonMeterValuePeriodic, &meter, now)

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

// SendAuthorize sends an Authorize request to the CSMS.
func (b *Bridge201) SendAuthorize(idTag string) error {
	cb := func(response ocpp.Response, err error) {
		if err != nil {
			slog.Error("Authorize failed", "error", err)
			return
		}
		resp, ok := response.(*authorization.AuthorizeResponse)
		if !ok {
			slog.Error("Authorize: unexpected response type")
			return
		}
		slog.Info("Authorize response", "status", resp.IdTokenInfo.Status)
	}

	return b.cs.SendRequestAsync(
		authorization.NewAuthorizationRequest(idTag, types.IdTokenTypeISO14443),
		cb,
	)
}

// SendFirmwareStatusNotification sends a FirmwareStatusNotification to the CSMS.
func (b *Bridge201) SendFirmwareStatusNotification(status string) error {
	return fmt.Errorf("SendFirmwareStatusNotification not implemented for OCPP 2.0.1")
}

// SendDiagnosticsStatusNotification is a stub — diagnostics upload uses LogStatusNotification in 2.0.1.
func (b *Bridge201) SendDiagnosticsStatusNotification(status string) error {
	return fmt.Errorf("SendDiagnosticsStatusNotification not implemented for OCPP 2.0.1")
}

// SendDataTransfer passes a vendor-specific message to the CSMS.
func (b *Bridge201) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
	return "", "", fmt.Errorf("SendDataTransfer not implemented for OCPP 2.0.1")
}
