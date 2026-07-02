package v16

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
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

func parseSampledMeasurands(raw string) []types.Measurand {
	if strings.TrimSpace(raw) == "" {
		return []types.Measurand{types.MeasurandEnergyActiveImportRegister}
	}
	measurands := make([]types.Measurand, 0)
	for _, part := range strings.Split(raw, ",") {
		candidate := types.Measurand(strings.TrimSpace(part))
		switch candidate {
		case types.MeasurandEnergyActiveImportRegister,
			types.MeasurandVoltage,
			types.MeasurandCurrentImport,
			types.MeasurandCurrentOffered,
			types.MeasurandPowerActiveImport,
			types.MeasurandPowerOffered:
			measurands = append(measurands, candidate)
		}
	}
	if len(measurands) == 0 {
		return []types.Measurand{types.MeasurandEnergyActiveImportRegister}
	}
	return measurands
}

func (b *Bridge16) configuredSampledMeasurands() []types.Measurand {
	if b.configKeys == nil {
		return []types.Measurand{types.MeasurandEnergyActiveImportRegister}
	}
	return parseSampledMeasurands(b.configKeys.GetConfigValue("MeterValuesSampledData"))
}

// buildSampledValues renders MeterValues.SampledValue entries. offeredCurrent
// is always the connector's rated current (what's available); actualCurrent
// should be the meter's EffectiveCurrent (what's actually flowing, capped by
// any active charging-profile limit) — the two differ whenever a profile is
// throttling the session below the connector's rated current.
func buildSampledValues(conn *engine.Connector, meterWh, effectiveCurrent float64, transactionID int, meterContext types.ReadingContext, measurands []types.Measurand) []types.SampledValue {
	voltage := 0.0
	offeredCurrent := 0.0
	actualCurrent := 0.0
	phases := 0.0
	if conn != nil {
		voltage = conn.Voltage
		offeredCurrent = conn.Current
		phases = float64(conn.Phase)
		if transactionID != 0 && conn.Status == engine.StateCharging {
			actualCurrent = effectiveCurrent
		}
	}
	activePower := voltage * actualCurrent * phases
	offeredPower := voltage * offeredCurrent * phases

	sampled := make([]types.SampledValue, 0, len(measurands))
	appendValue := func(value string, measurand types.Measurand, unit types.UnitOfMeasure) {
		sampled = append(sampled, types.SampledValue{
			Value:     value,
			Context:   meterContext,
			Format:    types.ValueFormatRaw,
			Measurand: measurand,
			Unit:      unit,
		})
	}

	for _, measurand := range measurands {
		switch measurand {
		case types.MeasurandEnergyActiveImportRegister:
			appendValue(fmt.Sprintf("%.2f", meterWh), measurand, types.UnitOfMeasureWh)
		case types.MeasurandVoltage:
			appendValue(fmt.Sprintf("%.1f", voltage), measurand, types.UnitOfMeasureV)
		case types.MeasurandCurrentImport:
			appendValue(fmt.Sprintf("%.2f", actualCurrent), measurand, types.UnitOfMeasureA)
		case types.MeasurandCurrentOffered:
			appendValue(fmt.Sprintf("%.2f", offeredCurrent), measurand, types.UnitOfMeasureA)
		case types.MeasurandPowerActiveImport:
			appendValue(fmt.Sprintf("%.2f", activePower), measurand, types.UnitOfMeasureW)
		case types.MeasurandPowerOffered:
			appendValue(fmt.Sprintf("%.2f", offeredPower), measurand, types.UnitOfMeasureW)
		}
	}
	if len(sampled) == 0 {
		appendValue(fmt.Sprintf("%.2f", meterWh), types.MeasurandEnergyActiveImportRegister, types.UnitOfMeasureWh)
	}
	return sampled
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
		return nil
	}

	// Per OCPP 1.6 §4.2.1: on Pending or Rejected, the Charge Point SHOULD
	// periodically retry BootNotification, using the interval returned by
	// the CSMS as the minimum wait before the next attempt. Without this,
	// a Pending/Rejected response left the station permanently unregistered
	// with no further attempt to boot. Scheduled via time.AfterFunc (not a
	// blocking sleep) so the dispatcher goroutine keeps draining other
	// outbound commands while the retry is pending.
	retryInterval := bootResp.Interval
	if retryInterval <= 0 {
		retryInterval = 30
	}
	slog.Warn("BootNotification not accepted, will retry", "status", bootResp.Status, "retryIntervalSec", retryInterval)
	time.AfterFunc(time.Duration(retryInterval)*time.Second, func() {
		b.dispatcher.Enqueue(ocpp.OCPPCommand{
			Description: "BootNotification (retry)",
			Execute:     b.SendBootNotification,
		})
	})
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
	errorCode = string(mapErrorCode16(errorCode))
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

// SendTransactionEventUpdated is a no-op for OCPP 1.6 because v1.6 reports
// charging state changes via StatusNotification and MeterValues, not via a
// dedicated TransactionEvent message. The engine callback exists for
// version-agnostic symmetry; only the v2.0.1 bridge actually emits a message.
func (b *Bridge16) SendTransactionEventUpdated(connectorID int, chargingState, trigger string) error {
	_ = connectorID
	_ = chargingState
	_ = trigger
	return nil
}

// SendConnectorEventNotification is a no-op for OCPP 1.6 because v1.6 has no
// NotifyEvent equivalent. Status changes are conveyed through StatusNotification
// in v1.6.
func (b *Bridge16) SendConnectorEventNotification(connectorID int, component, instance, variable, actualValue string, evseComponent bool) error {
	_ = connectorID
	_ = component
	_ = instance
	_ = variable
	_ = actualValue
	_ = evseComponent
	return nil
}

// SendReservationStatusUpdate is a no-op for OCPP 1.6, which has no
// equivalent message — a v1.6 CSMS only learns a reservation ended when it
// next queries or the connector's StatusNotification changes.
func (b *Bridge16) SendReservationStatusUpdate(reservationID int, status string) error {
	_ = reservationID
	_ = status
	return nil
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

	// Per OCPP 1.6 §4.8: the Charge Point SHALL update its authorization
	// cache from idTagInfo, and if the status is not Accepted, SHALL stop
	// the transaction it just optimistically started.
	if startResp.IdTagInfo != nil {
		status := string(startResp.IdTagInfo.Status)
		if normalized, ok := ocpp.NormalizeAuthorizationStatus(status); ok {
			status = normalized
		}
		var expiry *time.Time
		if startResp.IdTagInfo.ExpiryDate != nil {
			t := startResp.IdTagInfo.ExpiryDate.Time
			expiry = &t
		}
		if b.authCache != nil {
			b.authCache.Put(idTag, status, expiry)
		}
		if status != string(types.AuthorizationStatusAccepted) {
			slog.Warn("StartTransaction rejected by CSMS, stopping session", "connector", connectorID, "status", status)
			b.engine.StopSession(&connectorID, "DeAuthorized")
		}
	}

	return startResp.TransactionId, nil
}

// mapStopReason16 maps engine-internal stop reasons to valid OCPP 1.6 Reason
// enum values (DeAuthorized, EmergencyStop, EVDisconnected, HardReset, Local,
// Other, PowerLoss, Reboot, Remote, SoftReset, UnlockCommand). ocpp-go
// validates outgoing StopTransaction requests client-side and silently drops
// (never transmits) any request whose Reason fails the "reason" validator,
// so an unmapped engine reason here means the CSMS never learns the
// transaction ended.
func mapStopReason16(reason string) core.Reason {
	switch core.Reason(reason) {
	case core.ReasonDeAuthorized, core.ReasonEmergencyStop, core.ReasonEVDisconnected,
		core.ReasonHardReset, core.ReasonLocal, core.ReasonOther, core.ReasonPowerLoss,
		core.ReasonReboot, core.ReasonRemote, core.ReasonSoftReset, core.ReasonUnlockCommand:
		return core.Reason(reason)
	case "user_requested":
		return core.ReasonLocal
	case "Faulted":
		return core.ReasonOther
	default:
		return core.ReasonOther
	}
}

// mapErrorCode16 validates/normalizes an errorCode against the OCPP 1.6
// ChargePointErrorCode enum (ConnectorLockFailure, EVCommunicationError,
// GroundFailure, HighTemperature, InternalError, LocalListConflict, NoError,
// OtherError, OverCurrentFailure, OverVoltage, PowerMeterFailure,
// PowerSwitchFailure, ReaderFailure, ResetFailure, UnderVoltage,
// WeakSignal). Like mapStopReason16, this exists because ocpp-go validates
// outgoing requests client-side and silently drops ones that fail — an
// unrecognized engine.Connector.FaultCode here would otherwise mean the
// CSMS never learns the connector faulted at all.
func mapErrorCode16(errorCode string) core.ChargePointErrorCode {
	switch core.ChargePointErrorCode(errorCode) {
	case core.ConnectorLockFailure, core.EVCommunicationError, core.GroundFailure,
		core.HighTemperature, core.InternalError, core.LocalListConflict, core.NoError,
		core.OtherError, core.OverCurrentFailure, core.OverVoltage, core.PowerMeterFailure,
		core.PowerSwitchFailure, core.ReaderFailure, core.ResetFailure, core.UnderVoltage,
		core.WeakSignal:
		return core.ChargePointErrorCode(errorCode)
	default:
		return core.OtherError
	}
}

// SendStopTransaction sends a StopTransaction request.
func (b *Bridge16) SendStopTransaction(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error {
	reason = string(mapStopReason16(reason))
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
				IDTag:         idTag,
				MeterHistory:  meterHistory,
			},
		})
		return nil
	}
	req := core.NewStopTransactionRequest(int(math.Round(meterStop)), types.NewDateTime(timestamp), transactionID)
	req.Reason = core.Reason(reason)
	if idTag != nil {
		req.IdTag = *idTag
	}

	if len(meterHistory) > 0 {
		var transactionData []types.MeterValue
		for _, record := range meterHistory {
			ts, err := time.Parse(time.RFC3339Nano, record.Timestamp)
			if err != nil {
				ts = time.Now()
			}
			transactionData = append(transactionData, types.MeterValue{
				Timestamp: types.NewDateTime(ts),
				SampledValue: []types.SampledValue{
					{
						Value:     fmt.Sprintf("%.2f", record.Value),
						Context:   types.ReadingContextSamplePeriodic,
						Unit:      types.UnitOfMeasureWh,
						Measurand: types.MeasurandEnergyActiveImportRegister,
					},
				},
			})
		}
		req.TransactionData = transactionData
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
	var conn *engine.Connector
	effectiveCurrent := 0.0
	if b.engine != nil {
		conn = b.engine.GetConnector(connectorID)
		if meter := b.engine.GetEnergyMeter(connectorID); meter != nil {
			effectiveCurrent = meter.EffectiveCurrent
		}
	}
	sampledValues := buildSampledValues(conn, value, effectiveCurrent, transactionID, readingContext, b.configuredSampledMeasurands())
	req := core.NewMeterValuesRequest(connectorID, []types.MeterValue{
		{
			Timestamp:    types.NewDateTime(timestamp),
			SampledValue: sampledValues,
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
