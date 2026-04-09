package v16

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/firmware"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"

	engine "github.com/chargeghost/engine/internal/engine"
	ocpp "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

// SendBootNotification sends a BootNotification to the CSMS.
func (b *Bridge16) SendBootNotification() error {
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
			b.dispatcher.Enqueue(ocpp.OCPPCommand{
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
func (b *Bridge16) SendHeartbeat() error {
	_, err := b.cp.SendRequest(core.NewHeartbeatRequest())
	return err
}

// SendStatusNotification sends StatusNotification for a connector.
func (b *Bridge16) SendStatusNotification(connectorID int, errorCode, status string) error {
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
func (b *Bridge16) SendStartTransaction(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
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
func (b *Bridge16) SendStopTransaction(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error {
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
func (b *Bridge16) SendMeterValues(connectorID int, value float64, transactionID int, meterContext string) error {
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
func (b *Bridge16) SendAuthorize(idTag string) error {
	_, err := b.cp.SendRequest(core.NewAuthorizationRequest(idTag))
	return err
}

// SendDataTransfer sends a DataTransfer request.
func (b *Bridge16) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
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
func (b *Bridge16) SendDiagnosticsStatusNotification(status string) error {
	req := firmware.NewDiagnosticsStatusNotificationRequest(firmware.DiagnosticsStatus(status))
	_, err := b.cp.SendRequest(req)
	return err
}

// SendFirmwareStatusNotification sends FirmwareStatusNotification.
func (b *Bridge16) SendFirmwareStatusNotification(status string) error {
	req := firmware.NewFirmwareStatusNotificationRequest(firmware.FirmwareStatus(status))
	_, err := b.cp.SendRequest(req)
	return err
}
