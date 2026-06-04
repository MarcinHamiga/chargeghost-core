package v16

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/firmware"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"

	engine "github.com/chargeghost/engine/internal/engine"
	ocpp "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

func normalizeMeterContext(meterContext string) types.ReadingContext {
	switch types.ReadingContext(meterContext) {
	case types.ReadingContextInterruptionBegin,
		types.ReadingContextInterruptionEnd,
		types.ReadingContextOther,
		types.ReadingContextSampleClock,
		types.ReadingContextSamplePeriodic,
		types.ReadingContextTransactionBegin,
		types.ReadingContextTransactionEnd,
		types.ReadingContextTrigger:
		return types.ReadingContext(meterContext)
	default:
		return types.ReadingContextOther
	}
}

// SendBootNotification sends a BootNotification to the CSMS.
func (b *Bridge16) SendBootNotification() error {
	b.tl.LogOutbound("BootNotification", nil, nil, fmt.Sprintf("model=%s vendor=%s", b.cfg.ChargePointModel, b.cfg.ChargePointVendor), nil)
	resp, err := b.cp.SendRequest(core.NewBootNotificationRequest(b.cfg.ChargePointModel, b.cfg.ChargePointVendor))
	if err != nil {
		b.tl.LogError("BootNotification", "outbound", nil, err.Error(), nil, "")
		return fmt.Errorf("BootNotification send: %w", err)
	}
	bootResp, ok := resp.(*core.BootNotificationConfirmation)
	if !ok {
		return fmt.Errorf("unexpected BootNotification response type")
	}
	slog.Info("BootNotification response", "status", bootResp.Status, "interval", bootResp.Interval)

	if bootResp.Status == core.RegistrationStatusAccepted {
		b.heartbeatInt = bootResp.Interval
		_ = b.configKeys.SetConfigValue("HeartbeatInterval", strconv.Itoa(bootResp.Interval))
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
	b.tl.LogOutbound("Heartbeat", nil, nil, "Heartbeat", nil)
	_, err := b.cp.SendRequest(core.NewHeartbeatRequest())
	if err != nil {
		b.tl.LogError("Heartbeat", "outbound", nil, err.Error(), nil, "")
	}
	return err
}

// SendStatusNotification sends StatusNotification for a connector.
func (b *Bridge16) SendStatusNotification(connectorID int, errorCode, status string) error {
	b.tl.LogOutbound("StatusNotification", ocpp.IntPtr(connectorID), nil, fmt.Sprintf("connector=%d status=%s error=%s", connectorID, status, errorCode), nil)
	req := core.NewStatusNotificationRequest(
		connectorID,
		core.ChargePointErrorCode(errorCode),
		core.ChargePointStatus(status),
	)
	req.Timestamp = types.NewDateTime(time.Now())
	_, err := b.cp.SendRequest(req)
	if err != nil {
		b.tl.LogError("StatusNotification", "outbound", ocpp.IntPtr(connectorID), err.Error(), nil, "")
	}
	return err
}

// SendStartTransaction sends a StartTransaction request and returns the CSMS-assigned transaction ID.
func (b *Bridge16) SendStartTransaction(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	b.tl.LogOutbound("StartTransaction", ocpp.IntPtr(connectorID), nil, fmt.Sprintf("connector=%d idTag=%s meter=%s", connectorID, idTag, ocpp.FormatMeter(meterStart)), nil)
	if !b.IsConnected() {
		_, _ = b.queue.Enqueue(queue.QueuedMessage{
			Type: "StartTransaction",
			Payload: queuedStartTransaction16{
				ConnectorID:   connectorID,
				IDTag:         idTag,
				MeterStart:    meterStart,
				Timestamp:     timestamp,
				ReservationID: reservationID,
			},
		})
		return 0, nil
	}
	req := core.NewStartTransactionRequest(connectorID, idTag, int(math.Round(meterStart)), types.NewDateTime(timestamp))
	if reservationID != nil {
		req.ReservationId = reservationID
	}
	resp, err := b.cp.SendRequest(req)
	if err != nil {
		b.tl.LogError("StartTransaction", "outbound", ocpp.IntPtr(connectorID), err.Error(), nil, "")
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
	txID := transactionID
	b.tl.LogOutbound("StopTransaction", nil, &txID, fmt.Sprintf("txId=%d meter=%s reason=%s", transactionID, ocpp.FormatMeter(meterStop), reason), nil)
	if !b.IsConnected() {
		_, _ = b.queue.Enqueue(queue.QueuedMessage{
			Type: "StopTransaction",
			Payload: queuedStopTransaction16{
				TransactionID: transactionID,
				MeterStop:     meterStop,
				Timestamp:     timestamp,
				Reason:        reason,
				MeterHistory:  meterHistory,
			},
		})
		return nil
	}
	req := core.NewStopTransactionRequest(int(math.Round(meterStop)), types.NewDateTime(timestamp), transactionID)
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
	if err != nil {
		b.tl.LogError("StopTransaction", "outbound", nil, err.Error(), nil, "")
	}
	return err
}

// SendMeterValues sends a MeterValues message.
func (b *Bridge16) SendMeterValues(connectorID int, value float64, transactionID int, meterContext string) error {
	txPtr := &transactionID
	if transactionID == 0 {
		txPtr = nil
	}
	timestamp := time.Now()
	b.tl.LogOutbound("MeterValues", ocpp.IntPtr(connectorID), txPtr, fmt.Sprintf("connector=%d meter=%s context=%s", connectorID, ocpp.FormatMeter(value), meterContext), nil)
	if !b.IsConnected() {
		_, _ = b.queue.Enqueue(queue.QueuedMessage{
			Type: "MeterValues",
			Payload: queuedMeterValues16{
				ConnectorID:   connectorID,
				Value:         value,
				TransactionID: transactionID,
				Context:       meterContext,
				Timestamp:     timestamp,
			},
		})
		return nil
	}
	return b.sendMeterValuesAt(connectorID, value, transactionID, meterContext, timestamp)
}

func (b *Bridge16) sendMeterValuesAt(connectorID int, value float64, transactionID int, meterContext string, timestamp time.Time) error {
	readingContext := normalizeMeterContext(meterContext)
	req := core.NewMeterValuesRequest(connectorID, []types.MeterValue{
		{
			Timestamp: types.NewDateTime(timestamp),
			SampledValue: []types.SampledValue{
				{
					Value:     fmt.Sprintf("%.2f", value),
					Context:   readingContext,
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
	b.tl.LogOutbound("Authorize", nil, nil, fmt.Sprintf("idTag=%s", idTag), nil)
	resp, err := b.cp.SendRequest(core.NewAuthorizationRequest(idTag))
	if err != nil {
		b.tl.LogError("Authorize", "outbound", nil, err.Error(), nil, "")
		return err
	}
	authorizeResp, ok := resp.(*core.AuthorizeConfirmation)
	if !ok {
		return fmt.Errorf("unexpected Authorize response type: %T", resp)
	}
	if authorizeResp.IdTagInfo == nil {
		return fmt.Errorf("authorize response missing idTagInfo")
	}

	status := string(authorizeResp.IdTagInfo.Status)
	if normalized, ok := ocpp.NormalizeAuthorizationStatus(status); ok {
		status = normalized
	}

	var expiry *time.Time
	if authorizeResp.IdTagInfo.ExpiryDate != nil {
		t := authorizeResp.IdTagInfo.ExpiryDate.Time
		expiry = &t
	}

	if b.authCache != nil {
		b.authCache.Put(idTag, status, expiry)
	}

	if status != string(types.AuthorizationStatusAccepted) {
		return fmt.Errorf("authorize rejected: status=%s", status)
	}

	return nil
}

// SendDataTransfer sends a DataTransfer request.
func (b *Bridge16) SendDataTransfer(vendorID, messageID, data string) (string, string, error) {
	b.tl.LogOutbound("DataTransfer", nil, nil, fmt.Sprintf("vendor=%s messageId=%s", vendorID, messageID), nil)
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
	respData := ocpp.DataTransferDataString(dtResp.Data)
	return string(dtResp.Status), respData, nil
}

// SendDiagnosticsStatusNotification sends DiagnosticsStatusNotification.
func (b *Bridge16) SendDiagnosticsStatusNotification(status string) error {
	b.tl.LogOutbound("DiagnosticsStatusNotification", nil, nil, fmt.Sprintf("status=%s", status), nil)
	req := firmware.NewDiagnosticsStatusNotificationRequest(firmware.DiagnosticsStatus(status))
	_, err := b.cp.SendRequest(req)
	if err != nil {
		b.tl.LogError("DiagnosticsStatusNotification", "outbound", nil, err.Error(), nil, "")
	}
	return err
}

// SendFirmwareStatusNotification sends FirmwareStatusNotification.
func (b *Bridge16) SendFirmwareStatusNotification(status string) error {
	b.tl.LogOutbound("FirmwareStatusNotification", nil, nil, fmt.Sprintf("status=%s", status), nil)
	req := firmware.NewFirmwareStatusNotificationRequest(firmware.FirmwareStatus(status))
	_, err := b.cp.SendRequest(req)
	if err != nil {
		b.tl.LogError("FirmwareStatusNotification", "outbound", nil, err.Error(), nil, "")
	}
	return err
}
