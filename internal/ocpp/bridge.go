package ocpp

import (
	"context"
	"fmt"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
)

// OCPPBridge is the version-agnostic interface for OCPP communication.
// Implemented by v16.Bridge16 (OCPP 1.6J) and v201.Bridge201 (future).
type OCPPBridge interface {
	Start(ctx context.Context) error
	Stop()
	IsConnected() bool
	GetHeartbeatInterval() int
	Dispatcher() *CommandDispatcher
	Status() Status

	// Outbound messages
	SendBootNotification() error
	SendHeartbeat() error
	SendStatusNotification(connectorID int, errorCode, status string) error
	SendMeterValues(connectorID int, value float64, transactionID int, context string) error
	SendAuthorize(idTag string) error

	// Transaction lifecycle.
	// Returns transaction ID: server-assigned int for 1.6, synthetic int for 2.0.1.
	SendTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error)
	SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error

	// SendTransactionEventUpdated is called when a connector's charging state
	// changes (e.g. Charging → SuspendedEV → Charging). The v1.6 bridge is a
	// no-op since v1.6 reports state via StatusNotification / MeterValues;
	// the v2.0.1 bridge uses it to emit a TransactionEvent(Updated) with the
	// supplied chargingState and trigger reason. The trigger is the OCPP 2.0.1
	// trigger reason (e.g. "ChargingStateChanged", "EnergyLimitReached");
	// version-specific bridges should validate and fall back to a safe default
	// when unknown.
	SendTransactionEventUpdated(connectorID int, chargingState, trigger string) error

	// Firmware/Diagnostics
	SendFirmwareStatusNotification(status string) error
	SendDiagnosticsStatusNotification(status string) error

	// Data transfer
	SendDataTransfer(vendorID, messageID, data string) (string, string, error)

	// SendConnectorEventNotification emits a status-change event to the CSMS
	// (OCPP 2.0.1 NotifyEvent; v1.6 is a no-op since v1.6 has no equivalent
	// built-in monitoring/alerting message). The component/instance/variable
	// values identify the device-model element whose state changed, and the
	// actualValue is the new value.
	SendConnectorEventNotification(connectorID int, component, instance, variable, actualValue string, evseComponent bool) error

	// SendReservationStatusUpdate reports a Charging-Station-initiated
	// reservation status change (currently only "Expired") to the CSMS.
	// v1.6 has no equivalent message and is a no-op; v2.0.1 sends
	// ReservationStatusUpdateRequest per §3.30. status must be "Expired" or
	// "Removed" (matching ocpp-go's ReservationUpdateStatus values).
	SendReservationStatusUpdate(reservationID int, status string) error

	// MaybeCompleteReset checks if a soft reset is pending and all sessions have
	// stopped, then completes the reset sequence (NormalizeAfterReset + BootNotification).
	MaybeCompleteReset()
}

// NewBridgeForVersion validates the requested OCPP version string.
// Construction of the concrete bridge is done directly in main.go via the v16/v201 packages.
func NewBridgeForVersion(version string) error {
	switch version {
	case "1.6", "":
		return nil
	case "2.0.1":
		return nil
	default:
		return fmt.Errorf("unsupported OCPP version: %s", version)
	}
}
