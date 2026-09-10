package main

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	ws "github.com/chargeghost/engine/internal/api/ws"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
)

func broadcastHub(hub *ws.Hub, stationID string, msg ws.Message) {
	if hub != nil {
		msg.StationID = stationID
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

func newConnectorStatusChangedCallback(stationID string, e *engine.Engine, hub *ws.Hub, bridge ocpp.OCPPBridge, dispatcher *ocpp.CommandDispatcher) func(int, engine.ConnectorState) {
	var mu sync.Mutex
	// sent tracks per-connector, per-kind notifications already handed to
	// the dispatcher: kind ("status", "event", or "fault:<code>") maps to
	// the status string it was sent for. Tracking each command separately
	// means a queue-full drop of one retries only that one instead of
	// duplicating the rest, and a fault-code change under a steady Faulted
	// status still reports the new code.
	sent := map[int]map[string]string{}
	// markSent records kind as sent for statusStr, reporting whether that
	// exact notification was already sent (duplicate to suppress).
	markSent := func(connectorID int, kind, statusStr string) bool {
		mu.Lock()
		defer mu.Unlock()
		kinds, ok := sent[connectorID]
		if ok && kinds[kind] == statusStr {
			return true
		}
		if !ok {
			kinds = map[string]string{}
			sent[connectorID] = kinds
		}
		if strings.HasPrefix(kind, "fault:") {
			// One fault code is current at a time: forget older codes so
			// a return to a previous code under a steady Faulted status
			// re-reports it instead of being suppressed as already sent.
			for k := range kinds {
				if k != kind && strings.HasPrefix(k, "fault:") {
					delete(kinds, k)
				}
			}
		}
		kinds[kind] = statusStr
		return false
	}
	// unmark forgets a sent notification so the next engine notification
	// for that state retries it instead of being suppressed as a duplicate.
	unmark := func(connectorID int, kind, statusStr string) {
		mu.Lock()
		defer mu.Unlock()
		if kinds, ok := sent[connectorID]; ok && kinds[kind] == statusStr {
			delete(kinds, kind)
			if len(kinds) == 0 {
				delete(sent, connectorID)
			}
		}
	}
	// sendFunc wraps a bridge send so an execute-time failure unmarks the
	// notification: without this a failed send would stay suppressed as
	// "already sent" (the dispatcher only logs execute errors) and the
	// CSMS would never converge to the reported state.
	sendFunc := func(connectorID int, kind, statusStr string, send func() error) func() error {
		return func() error {
			if err := send(); err != nil {
				unmark(connectorID, kind, statusStr)
				return err
			}
			return nil
		}
	}
	// enqueue reports queue-full backpressure without losing the pending
	// state: only the dropped kind is unmarked, so a subsequent change (or
	// a repeated notification) retries that send without duplicating the
	// commands that made it into the queue.
	enqueue := func(connectorID int, kind, statusStr string, cmd ocpp.OCPPCommand) {
		if err := dispatcher.Enqueue(cmd); err != nil {
			slog.Warn("OCPP status queue full, dropping",
				"station", stationID,
				"connector", connectorID,
				"status", statusStr,
				"description", cmd.Description,
				"error", err,
			)
			unmark(connectorID, kind, statusStr)
		}
	}
	return func(connectorID int, status engine.ConnectorState) {
		broadcastHub(hub, stationID, ws.Message{
			Type: "connector_status_changed",
			Data: connectorStatusData(e, connectorID, status),
		})
		// Status notifications always enqueue, even while offline: the
		// dispatcher holds commands until the link recovers, so the CSMS
		// converges to the latest state after reconnect instead of losing
		// intermediate Faulted/Unavailable transitions. Duplicate
		// notifications for an unchanged state are suppressed per kind.
		statusStr := string(status)
		connID := connectorID
		// Report the connector's real fault code instead of a hardcoded
		// NoError — a Faulted StatusNotification claiming NoError is
		// self-contradictory. v16.Bridge16.SendStatusNotification
		// validates/normalizes this against the OCPP 1.6
		// ChargePointErrorCode set before sending.
		errorCode := "NoError"
		faultCode := ""
		if status == engine.StateFaulted {
			if c := e.GetConnector(connectorID); c != nil && c.FaultCode != "" {
				faultCode = c.FaultCode
				errorCode = faultCode
			}
		}
		if !markSent(connID, "status", statusStr) {
			enqueue(connID, "status", statusStr, ocpp.OCPPCommand{
				Description: fmt.Sprintf("StatusNotification connector %d", connID),
				Execute: sendFunc(connID, "status", statusStr, func() error {
					return bridge.SendStatusNotification(connID, errorCode, statusStr)
				}),
			})
		}
		// OCPP 2.0.1 also gets a NotifyEvent for the EVSE AvailabilityState
		// so the CSMS can correlate variable changes to the device model.
		if !markSent(connID, "event", statusStr) {
			enqueue(connID, "event", statusStr, ocpp.OCPPCommand{
				Description: fmt.Sprintf("NotifyEvent connector %d", connID),
				Execute: sendFunc(connID, "event", statusStr, func() error {
					return bridge.SendConnectorEventNotification(connID, "EVSE", "", "AvailabilityState", statusStr, true)
				}),
			})
		}
		// v2.0.1 has no error-code field on StatusNotification; report the
		// fault via a second NotifyEvent instead (no-op on v1.6, which
		// already got the fault code above).
		if faultCode != "" {
			faultKind := "fault:" + faultCode
			if !markSent(connID, faultKind, statusStr) {
				enqueue(connID, faultKind, statusStr, ocpp.OCPPCommand{
					Description: fmt.Sprintf("NotifyEvent fault connector %d", connID),
					Execute: sendFunc(connID, faultKind, statusStr, func() error {
						return bridge.SendConnectorEventNotification(connID, "EVSE", "", "ProblemFaultCode", faultCode, true)
					}),
				})
			}
		}
	}
}

func newConnectorPlugChangedCallback(stationID string, hub *ws.Hub) func(int, bool) {
	return func(connectorID int, isPluggedIn bool) {
		broadcastHub(hub, stationID, ws.Message{
			Type: "connector_plug_changed",
			Data: map[string]interface{}{
				"connector_id":  connectorID,
				"is_plugged_in": isPluggedIn,
			},
		})
	}
}

func newConnectorIDTagChangedCallback(stationID string, hub *ws.Hub) func(int, *string) {
	return func(connectorID int, idTag *string) {
		broadcastHub(hub, stationID, ws.Message{
			Type: "connector_id_tag_changed",
			Data: map[string]interface{}{
				"connector_id": connectorID,
				"id_tag":       idTag,
			},
		})
	}
}

func newTransactionIDChangedCallback(stationID string, hub *ws.Hub) func(int, int) {
	return func(connectorID, transactionID int) {
		broadcastHub(hub, stationID, ws.Message{
			Type: "transaction_id_changed",
			Data: map[string]interface{}{
				"connector_id":   connectorID,
				"transaction_id": transactionID,
			},
		})
	}
}

func newSessionStartedCallback(stationID string, e *engine.Engine, hub *ws.Hub, bridge ocpp.OCPPBridge) func(int, *string, float64, *int) {
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
		broadcastHub(hub, stationID, ws.Message{
			Type: "session_started",
			Data: data,
		})

		idTagStr := "UNKNOWN"
		if idTag != nil && *idTag != "" {
			idTagStr = *idTag
		}
		// Transaction delivery is durable: the record is persisted to the
		// version queue with its occurrence timestamp and the drain loop
		// sends it. v2.0.1 assigns the engine transaction ID at enqueue
		// time; v1.6 resolves the CSMS-assigned ID at send time in the
		// drain (see Bridge16.drainQueue).
		txID, err := bridge.EnqueueTransactionStart(connectorID, idTagStr, meterStart, time.Now(), reservationID)
		if err != nil {
			slog.Warn("failed to enqueue transaction start", "connector", connectorID, "error", err)
			return
		}
		if txID != 0 {
			e.SetActiveTransaction(connectorID, txID)
		}
	}
}

func newSessionStoppedCallback(stationID string, hub *ws.Hub, bridge ocpp.OCPPBridge) func(int, *engine.StoppedSessionInfo) {
	return func(connectorID int, info *engine.StoppedSessionInfo) {
		if info == nil {
			broadcastHub(hub, stationID, ws.Message{
				Type: "session_stopped",
				Data: map[string]interface{}{"connector_id": connectorID},
			})
			return
		}

		broadcastHub(hub, stationID, ws.Message{
			Type: "session_stopped",
			Data: map[string]interface{}{
				"connector_id":      connectorID,
				"transaction_id":    info.TransactionID,
				"energy_charged_wh": info.EnergyCharged,
				"reason":            info.Reason,
			},
		})

		// Durable delivery: persist the stop record with its occurrence
		// timestamp; the drain loop sends it in order behind the start.
		if err := bridge.EnqueueTransactionStop(info.MeterStop, time.Now(), info.TransactionID, info.Reason, info.IDTag, info.MeterHistory); err != nil {
			slog.Warn("failed to enqueue transaction stop", "connector", connectorID, "error", err)
		}
		bridge.MaybeCompleteReset()
	}
}

// newReservationExpiredCallback builds a callback that broadcasts a
// reservation's autonomous expiry over the WebSocket hub and — for OCPP
// 2.0.1 — reports it to the CSMS via ReservationStatusUpdate (§3.30). v1.6
// has no equivalent message; SendReservationStatusUpdate is a no-op there.
// Not queue-backed: like StatusNotification, this reflects point-in-time
// state, so it's only sent while connected.
func newReservationExpiredCallback(stationID string, hub *ws.Hub, bridge ocpp.OCPPBridge, dispatcher *ocpp.CommandDispatcher) func(int, int) {
	return func(reservationID, connectorID int) {
		broadcastHub(hub, stationID, ws.Message{
			Type: "reservation_changed",
			Data: map[string]interface{}{
				"action":         "expired",
				"reservation_id": reservationID,
				"connector_id":   connectorID,
			},
		})

		if bridge.IsConnected() {
			resID := reservationID
			dispatcher.Enqueue(ocpp.OCPPCommand{
				Description: fmt.Sprintf("ReservationStatusUpdate reservation %d", resID),
				Execute: func() error {
					return bridge.SendReservationStatusUpdate(resID, "Expired")
				},
			})
		}
	}
}

// newChargingStateChangedCallback builds a callback that bridges engine
// charging-state transitions to OCPP. The v1.6 bridge ignores this (v1.6
// reports state changes via StatusNotification and MeterValues); the v2.0.1
// bridge uses it to emit a TransactionEvent(Updated) with the new charging
// state, satisfying OCPP 2.0.1's event-driven model. The callback also
// broadcasts the change over the WebSocket hub for UI consumers.
func newChargingStateChangedCallback(stationID string, hub *ws.Hub, bridge ocpp.OCPPBridge) func(int, engine.ConnectorState) {
	return func(connectorID int, chargingState engine.ConnectorState) {
		broadcastHub(hub, stationID, ws.Message{
			Type: "charging_state_changed",
			Data: map[string]interface{}{
				"connector_id":   connectorID,
				"charging_state": string(chargingState),
			},
		})

		// Durable delivery (v2.0.1 TransactionEvent(Updated); no-op on
		// v1.6 which reports state via StatusNotification/MeterValues).
		if err := bridge.EnqueueTransactionEventUpdated(connectorID, string(chargingState), "ChargingStateChanged"); err != nil {
			slog.Warn("failed to enqueue transaction event update", "connector", connectorID, "error", err)
		}
	}
}
