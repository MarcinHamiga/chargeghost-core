package main

import (
	"fmt"
	"time"

	ws "github.com/chargeghost/engine/internal/api/ws"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
)

func broadcastHub(hub *ws.Hub, msg ws.Message) {
	if hub != nil {
		hub.BroadcastMessage(msg)
	}
}

func connectorStatusData(e *engine.Engine, connectorID int, status engine.ConnectorState) map[string]interface{} {
	data := map[string]interface{}{
		"connector_id": connectorID,
		"status":       string(status),
	}
	if c := e.GetConnector(connectorID); c != nil {
		data["is_plugged_in"] = c.IsPluggedIn
	}
	return data
}

func newConnectorStatusChangedCallback(e *engine.Engine, hub *ws.Hub, bridge ocpp.OCPPBridge, dispatcher *ocpp.CommandDispatcher) func(int, engine.ConnectorState) {
	return func(connectorID int, status engine.ConnectorState) {
		broadcastHub(hub, ws.Message{
			Type: "connector_status_changed",
			Data: connectorStatusData(e, connectorID, status),
		})
		// Status notifications reflect point-in-time connector state and are not
		// replayed from the offline queue. Transaction start/stop is queue-backed,
		// so those callbacks enqueue regardless of connection state.
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

func newConnectorPlugChangedCallback(hub *ws.Hub) func(int, bool) {
	return func(connectorID int, isPluggedIn bool) {
		broadcastHub(hub, ws.Message{
			Type: "connector_plug_changed",
			Data: map[string]interface{}{
				"connector_id":  connectorID,
				"is_plugged_in": isPluggedIn,
			},
		})
	}
}

func newConnectorIDTagChangedCallback(hub *ws.Hub) func(int, *string) {
	return func(connectorID int, idTag *string) {
		broadcastHub(hub, ws.Message{
			Type: "connector_id_tag_changed",
			Data: map[string]interface{}{
				"connector_id": connectorID,
				"id_tag":       idTag,
			},
		})
	}
}

func newTransactionIDChangedCallback(hub *ws.Hub) func(int, int) {
	return func(connectorID, transactionID int) {
		broadcastHub(hub, ws.Message{
			Type: "transaction_id_changed",
			Data: map[string]interface{}{
				"connector_id":   connectorID,
				"transaction_id": transactionID,
			},
		})
	}
}

func newSessionStartedCallback(e *engine.Engine, hub *ws.Hub, bridge ocpp.OCPPBridge, dispatcher *ocpp.CommandDispatcher) func(int, *string, float64, *int) {
	return func(connectorID int, idTag *string, meterStart float64, reservationID *int) {
		data := map[string]interface{}{
			"connector_id": connectorID,
			"meter_start":  meterStart,
			"id_tag":       idTag,
		}
		if reservationID != nil {
			data["reservation_id"] = *reservationID
		}
		if s := e.GetSession(connectorID); s != nil {
			data["transaction_id"] = s.TransactionID
		}
		broadcastHub(hub, ws.Message{
			Type: "session_started",
			Data: data,
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
				if txID != 0 {
					e.SetActiveTransaction(connID, txID)
				}
				return nil
			},
		})
	}
}

func newSessionStoppedCallback(hub *ws.Hub, bridge ocpp.OCPPBridge, dispatcher *ocpp.CommandDispatcher) func(int, *engine.StoppedSessionInfo) {
	return func(connectorID int, info *engine.StoppedSessionInfo) {
		if info == nil {
			broadcastHub(hub, ws.Message{
				Type: "session_stopped",
				Data: map[string]interface{}{"connector_id": connectorID},
			})
			return
		}

		broadcastHub(hub, ws.Message{
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
