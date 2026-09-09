package v16

import (
	"fmt"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
	ocpp "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

// This file is the OCPP 1.6 durable enqueue path for transaction messages.
// Engine callbacks and the meter ticker append typed records here instead of
// sending through dispatcher closures; Bridge16.drainQueue is the sole
// sender. Records carry their occurrence timestamp so replay after a
// disconnect preserves chronological order per OCPP 1.6 §3.7.

// kickDrain triggers an immediate drain pass through the dispatcher. The
// drain itself is single-flighted, so concurrent triggers are cheap; if the
// dispatcher channel is full and the trigger is dropped, the periodic drain
// loop and the reconnect handler still pick the record up.
func (b *Bridge16) kickDrain(description string) {
	if b.dispatcher == nil {
		return
	}
	b.dispatcher.Enqueue(ocpp.OCPPCommand{
		Description: description,
		Execute: func() error {
			b.DrainOfflineQueue()
			return nil
		},
	})
}

// EnqueueTransactionStart appends a StartTransaction record to the durable
// queue and kicks the drain loop. It always returns 0: the CSMS assigns the
// transaction ID at send time and the drain resolves it into the engine via
// SetActiveTransaction. Like SendStartTransaction, it refuses while the
// station is not registered (BootNotification not Accepted).
func (b *Bridge16) EnqueueTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	if !b.registered.Load() {
		return 0, fmt.Errorf("cannot enqueue StartTransaction: charge point not registered with CSMS (BootNotification not Accepted)")
	}
	if b.queue == nil {
		return 0, fmt.Errorf("cannot enqueue StartTransaction: no offline queue configured")
	}
	if _, err := b.queue.Enqueue(queue.QueuedMessage{
		Type: "StartTransaction",
		Payload: queuedStartTransaction16{
			ConnectorID:   connectorID,
			IDTag:         idTag,
			MeterStart:    meterStart,
			Timestamp:     timestamp,
			ReservationID: reservationID,
		},
	}); err != nil {
		return 0, fmt.Errorf("enqueue StartTransaction: %w", err)
	}
	b.kickDrain(fmt.Sprintf("StartTransaction connector %d", connectorID))
	return 0, nil
}

// EnqueueTransactionStop appends a StopTransaction record to the durable
// queue and kicks the drain loop.
func (b *Bridge16) EnqueueTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error {
	if b.queue == nil {
		return fmt.Errorf("cannot enqueue StopTransaction: no offline queue configured")
	}
	if _, err := b.queue.Enqueue(queue.QueuedMessage{
		Type: "StopTransaction",
		Payload: queuedStopTransaction16{
			TransactionID: transactionID,
			MeterStop:     meterStop,
			Timestamp:     timestamp,
			Reason:        string(mapStopReason16(reason)),
			IDTag:         idTag,
			MeterHistory:  meterHistory,
		},
	}); err != nil {
		return fmt.Errorf("enqueue StopTransaction: %w", err)
	}
	b.kickDrain(fmt.Sprintf("StopTransaction tx %d", transactionID))
	return nil
}

// EnqueueMeterValues appends a MeterValues record with its occurrence
// timestamp to the durable queue and kicks the drain loop.
func (b *Bridge16) EnqueueMeterValues(connectorID int, value float64, transactionID int, meterContext string, timestamp time.Time) error {
	if b.queue == nil {
		return fmt.Errorf("cannot enqueue MeterValues: no offline queue configured")
	}
	if _, err := b.queue.Enqueue(queue.QueuedMessage{
		Type: "MeterValues",
		Payload: queuedMeterValues16{
			ConnectorID:   connectorID,
			Value:         value,
			TransactionID: transactionID,
			Context:       meterContext,
			Timestamp:     timestamp,
		},
	}); err != nil {
		return fmt.Errorf("enqueue MeterValues: %w", err)
	}
	b.kickDrain(fmt.Sprintf("MeterValues connector %d", connectorID))
	return nil
}

// EnqueueTransactionEventUpdated is a no-op for OCPP 1.6, mirroring
// SendTransactionEventUpdated: charging-state changes travel via
// StatusNotification and MeterValues, so there is nothing to queue.
func (b *Bridge16) EnqueueTransactionEventUpdated(connectorID int, chargingState, trigger string) error {
	_ = connectorID
	_ = chargingState
	_ = trigger
	return nil
}
