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
	SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error

	// Firmware/Diagnostics
	SendFirmwareStatusNotification(status string) error
	SendDiagnosticsStatusNotification(status string) error

	// Data transfer
	SendDataTransfer(vendorID, messageID, data string) (string, string, error)
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
