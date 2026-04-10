package main

import (
	"fmt"
	"time"

	ws "github.com/chargeghost/engine/internal/api/ws"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
)

func newConnectorStatusChangedCallback(hub *ws.Hub, bridge ocpp.OCPPBridge, dispatcher *ocpp.CommandDispatcher) func(int, engine.ConnectorState) {
	return func(connectorID int, status engine.ConnectorState) {
		hub.BroadcastMessage(ws.Message{
			Type: "connector_status_changed",
			Data: map[string]interface{}{
				"connector_id": connectorID,
				"status":       string(status),
			},
		})
		if bridge.IsConnected() {
			connID := connectorID
			statusStr := string(status)
			dispatcher.Enqueue(ocpp.OCPPCommand{
				Description: fmt.Sprintf("StatusNotification connector %d", connID),
				Execute: func() error {
					return bridge.SendStatusNotification(connID, "NoError", statusStr)
				},
			})
		}
	}
}

func newSessionStartedCallback(e *engine.Engine, hub *ws.Hub, bridge ocpp.OCPPBridge, dispatcher *ocpp.CommandDispatcher) func(int, *string, float64, *int) {
	return func(connectorID int, idTag *string, meterStart float64, reservationID *int) {
		hub.BroadcastMessage(ws.Message{
			Type: "session_started",
			Data: map[string]interface{}{"connector_id": connectorID},
		})

		idTagStr := "UNKNOWN"
		if idTag != nil && *idTag != "" {
			idTagStr = *idTag
		}
		connID := connectorID
		meterStartSnapshot := meterStart
		reservationIDSnapshot := reservationID
		dispatcher.Enqueue(ocpp.OCPPCommand{
			Description: fmt.Sprintf("StartTransaction connector %d", connID),
			Execute: func() error {
				txID, err := bridge.SendTransactionStart(connID, idTagStr, meterStartSnapshot, time.Now(), reservationIDSnapshot)
				if err != nil {
					return err
				}
				e.SetActiveTransaction(connID, txID)
				return nil
			},
		})
	}
}

func newSessionStoppedCallback(hub *ws.Hub, bridge ocpp.OCPPBridge, dispatcher *ocpp.CommandDispatcher) func(int, *engine.StoppedSessionInfo) {
	return func(connectorID int, info *engine.StoppedSessionInfo) {
		if info == nil {
			hub.BroadcastMessage(ws.Message{
				Type: "session_stopped",
				Data: map[string]interface{}{"connector_id": connectorID},
			})
			return
		}

		hub.BroadcastMessage(ws.Message{
			Type: "session_stopped",
			Data: map[string]interface{}{
				"connector_id":      connectorID,
				"transaction_id":    info.TransactionID,
				"energy_charged_wh": info.EnergyCharged,
				"reason":            info.Reason,
			},
		})

		connID := connectorID
		snapshot := *info
		dispatcher.Enqueue(ocpp.OCPPCommand{
			Description: fmt.Sprintf("StopTransaction connector %d tx %d", connID, snapshot.TransactionID),
			Execute: func() error {
				return bridge.SendTransactionStop(snapshot.MeterStop, time.Now(), snapshot.TransactionID, snapshot.Reason, snapshot.MeterHistory)
			},
		})
	}
}
