//go:build integration

package v201

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	ocpp2 "github.com/lorenzodonini/ocpp-go/ocpp2.0.1"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/availability"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

// freePort finds an available TCP port on localhost.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// mockCSMSHandler implements the provisioning and transactions CSMS handler interfaces.
type mockCSMSHandler struct {
	bootReceived      chan *provisioning.BootNotificationRequest
	transactionEvents chan *transactions.TransactionEventRequest
}

func newMockCSMSHandler() *mockCSMSHandler {
	return &mockCSMSHandler{
		bootReceived:      make(chan *provisioning.BootNotificationRequest, 10),
		transactionEvents: make(chan *transactions.TransactionEventRequest, 10),
	}
}

// provisioning.CSMSHandler

func (h *mockCSMSHandler) OnBootNotification(_ string, request *provisioning.BootNotificationRequest) (*provisioning.BootNotificationResponse, error) {
	h.bootReceived <- request
	return provisioning.NewBootNotificationResponse(types.NewDateTime(time.Now()), 300, provisioning.RegistrationStatusAccepted), nil
}

func (h *mockCSMSHandler) OnNotifyReport(_ string, _ *provisioning.NotifyReportRequest) (*provisioning.NotifyReportResponse, error) {
	return provisioning.NewNotifyReportResponse(), nil
}

// transactions.CSMSHandler

func (h *mockCSMSHandler) OnTransactionEvent(_ string, request *transactions.TransactionEventRequest) (*transactions.TransactionEventResponse, error) {
	h.transactionEvents <- request
	return transactions.NewTransactionEventResponse(), nil
}

// availability.CSMSHandler

func (h *mockCSMSHandler) OnHeartbeat(_ string, _ *availability.HeartbeatRequest) (*availability.HeartbeatResponse, error) {
	return availability.NewHeartbeatResponse(*types.NewDateTime(time.Now())), nil
}

func (h *mockCSMSHandler) OnStatusNotification(_ string, _ *availability.StatusNotificationRequest) (*availability.StatusNotificationResponse, error) {
	return availability.NewStatusNotificationResponse(), nil
}

// startMockCSMS creates and starts an in-process CSMS on the given port. Returns a stop function.
func startMockCSMS(t *testing.T, port int, handler *mockCSMSHandler) func() {
	t.Helper()
	csms := ocpp2.NewCSMS(nil, nil)
	csms.SetProvisioningHandler(handler)
	csms.SetTransactionsHandler(handler)
	csms.SetAvailabilityHandler(handler)

	go csms.Start(port, "/{ws}")

	// Wait until the server is actually accepting connections.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	return func() { csms.Stop() }
}

// TestIntegration_BootAndHeartbeat verifies that the Bridge201 sends a BootNotification
// to the mock CSMS on Start, and the CSMS receives it with the correct model/vendor.
func TestIntegration_BootAndHeartbeat(t *testing.T) {
	port := freePort(t)
	handler := newMockCSMSHandler()
	stopCSMS := startMockCSMS(t, port, handler)
	defer stopCSMS()

	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	cfg.OCPPID = "test-cp-boot-001"
	cfg.ConnectionURL = fmt.Sprintf("ws://127.0.0.1:%d", port)

	dispatcher := ocpppkg.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)

	bridge := NewBridge(e, nil, cfg, dispatcher, q)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dispCtx, dispCancel := context.WithCancel(context.Background())
	defer dispCancel()
	go dispatcher.Run(dispCtx)

	go func() { _ = bridge.Start(ctx) }()

	// Wait for BootNotification to arrive at the mock CSMS.
	select {
	case req := <-handler.bootReceived:
		assert.Equal(t, cfg.ChargePointModel, req.ChargingStation.Model)
		assert.Equal(t, cfg.ChargePointVendor, req.ChargingStation.VendorName)
		assert.Equal(t, provisioning.BootReasonPowerUp, req.Reason)
	case <-time.After(8 * time.Second):
		t.Fatal("timeout waiting for BootNotification")
	}

	assert.True(t, bridge.IsConnected())
}

// TestIntegration_TransactionEvent verifies that a TransactionEvent(Started) is sent
// to the CSMS when a charging session begins.
func TestIntegration_TransactionEvent(t *testing.T) {
	port := freePort(t)
	handler := newMockCSMSHandler()
	stopCSMS := startMockCSMS(t, port, handler)
	defer stopCSMS()

	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	cfg.OCPPID = "test-cp-tx-001"
	cfg.ConnectionURL = fmt.Sprintf("ws://127.0.0.1:%d", port)

	dispatcher := ocpppkg.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)

	bridge := NewBridge(e, nil, cfg, dispatcher, q)

	// Wire the session-started callback. The callback is invoked while the
	// engine lock is held, so we must dispatch work in a goroutine to avoid
	// deadlocking on GetSession (which needs a read lock).
	e.OnSessionStarted = func(connectorID int) {
		connID := connectorID
		go func() {
			session := e.GetSession(connID)
			if session == nil {
				return
			}
			idTag := ""
			if session.IDTag != nil {
				idTag = *session.IDTag
			}
			meter, _ := e.GetMeterSnapshot(connID)
			dispatcher.Enqueue(ocpppkg.OCPPCommand{
				Description: fmt.Sprintf("StartTransaction connector %d", connID),
				Execute: func() error {
					txID, err := bridge.SendTransactionStart(connID, idTag, meter, time.Now(), nil)
					if err != nil {
						return err
					}
					e.SetActiveTransaction(connID, txID)
					return nil
				},
			})
		}()
	}

	// Add a connector.
	e.AddConnector(230, 16, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	dispCtx, dispCancel := context.WithCancel(context.Background())
	defer dispCancel()
	go dispatcher.Run(dispCtx)

	go func() { _ = bridge.Start(ctx) }()

	// Wait for boot to complete before starting a session.
	select {
	case <-handler.bootReceived:
	case <-time.After(8 * time.Second):
		t.Fatal("timeout waiting for BootNotification")
	}

	// Give the status notifications a moment to clear.
	time.Sleep(100 * time.Millisecond)

	// Simulate plug-in and start a session.
	e.PlugIn(1)
	idTag := "TEST-TAG-001"
	require.NoError(t, e.StartSession(1, 0, 0, &idTag, 0))

	// Wait for TransactionEvent(Started) to arrive at the mock CSMS.
	select {
	case evt := <-handler.transactionEvents:
		assert.Equal(t, transactions.TransactionEventStarted, evt.EventType)
		assert.NotEmpty(t, evt.TransactionInfo.TransactionID)
	case <-time.After(8 * time.Second):
		t.Fatal("timeout waiting for TransactionEvent(Started)")
	}
}
