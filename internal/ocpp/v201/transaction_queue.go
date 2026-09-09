package v201

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"

	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

// This file is the OCPP 2.0.1 durable enqueue path for transaction messages.
// Engine callbacks and the meter ticker append TransactionEvent records here
// instead of sending through dispatcher closures; Bridge201.drainQueue is
// the sole online sender. Request construction is shared with the synchronous
// Send* methods (used by the raw debug API) via the allocate* helpers below.

// kickDrain triggers an immediate drain pass through the dispatcher. The
// drain itself is single-flighted, so concurrent triggers are cheap; if the
// trigger is dropped, the periodic drain loop and the reconnect handler
// still pick the record up.
func (b *Bridge201) kickDrain(description string) {
	if b.enqueueCommand == nil && b.dispatcher == nil {
		return
	}
	b.enqueue(ocpppkg.OCPPCommand{
		Description: description,
		Execute: func() error {
			b.DrainOfflineQueue()
			return nil
		},
	})
}

// allocateStartedRequest creates the builder state and the
// TransactionEvent(Started) request for a new transaction. Shared by the
// enqueue path and the synchronous send path.
func (b *Bridge201) allocateStartedRequest(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (*transactions.TransactionEventRequest, int) {
	evseID := connectorID
	connID := 1

	builder := NewTransactionEventBuilder(evseID, connID)

	b.mu.Lock()
	b.nextTxInt++
	txInt := b.nextTxInt
	b.txBuilders[evseID] = builder
	b.txIntToEVSE[txInt] = evseID
	b.txStringToEVSE[builder.TransactionID()] = evseID
	b.mu.Unlock()

	// A RequestStartTransaction charging profile can't declare a
	// TransactionID up front (the transaction doesn't exist yet), so
	// OnRequestStartTransaction stashed it (and, if present, the
	// remoteStartId) on the session instead. Now that the real string
	// transaction id exists, stamp it onto the profile and register it —
	// this is what makes TxProfile scoping (§3.20) accept it.
	var remoteStartID *int
	if session := b.engine.GetSession(connectorID); session != nil {
		if session.RemoteStartChargingProfile != nil {
			profile := *session.RemoteStartChargingProfile
			profile.TransactionID = builder.TransactionID()
			if err := b.profileManager.SetChargingProfile(evseID, profile); err != nil {
				slog.Warn("RequestStartTransaction: failed to register charging profile", "evseId", evseID, "error", err)
			}
		}
		remoteStartID = session.RemoteStartID
	}

	idToken := types.IdToken{
		IdToken: idTag,
		Type:    types.IdTokenTypeISO14443,
	}

	// Update device model with starting meter reading
	b.deviceModel.SetVariable("EVSE", "", evseID, "Energy.Active.Import.Register", fmt.Sprintf("%.2f", meterStart), MutabilityReadOnly)

	meter := makeMeterValue(meterStart, timestamp, string(types.ReadingContextTransactionBegin))
	req := builder.Started(idToken, &meter, timestamp, remoteStartID)

	if reservationID != nil {
		req.ReservationID = reservationID
	}
	b.persistTransactions()
	return req, txInt
}

// allocateEndedRequest resolves the live builder for a transaction, tears
// down its bridge-local state, and builds the TransactionEvent(Ended)
// request. Shared by the enqueue path and the synchronous send path.
func (b *Bridge201) allocateEndedRequest(transactionID int, meterStop float64, timestamp time.Time, reason string, idTag *string, meterHistory []engine.MeterRecord) (*transactions.TransactionEventRequest, error) {
	b.mu.Lock()
	evseID, ok := b.txIntToEVSE[transactionID]
	if !ok {
		b.mu.Unlock()
		return nil, fmt.Errorf("no active transaction for ID %d", transactionID)
	}
	builder, ok := b.txBuilders[evseID]
	if !ok {
		b.mu.Unlock()
		return nil, fmt.Errorf("no active transaction builder for EVSE %d", evseID)
	}
	delete(b.txBuilders, evseID)
	delete(b.txIntToEVSE, transactionID)
	txIDStr := builder.TransactionID()
	delete(b.txStringToEVSE, txIDStr)
	noActiveTx := len(b.txBuilders) == 0
	b.mu.Unlock()

	// Clear any transaction-scoped charging profiles tied to this transaction
	// so they don't leak into subsequent sessions on the same EVSE. Outside the
	// mutex because profile_manager has its own lock.
	if b.profileManager != nil {
		b.profileManager.ClearTxProfilesForTransaction(txIDStr)
	}

	// triggerReset may already have set pendingReset while stop events were still
	// queued. Completing the reset here keeps the post-stop boot flow consistent
	// once the final bridge-local transaction state is gone.
	if noActiveTx && b.pendingReset.CompareAndSwap(true, false) {
		b.completeReset()
	}

	// Update device model with final meter reading - outside b.mu
	b.deviceModel.SetVariable("EVSE", "", evseID, "Energy.Active.Import.Register", fmt.Sprintf("%.2f", meterStop), MutabilityReadOnly)

	stopReason := mapStopReason(reason)
	triggerReason := mapTriggerReasonForStop(reason)
	meter := makeMeterValue(meterStop, timestamp, string(types.ReadingContextTransactionEnd))
	var token *types.IdToken
	if idTag != nil && *idTag != "" {
		token = &types.IdToken{IdToken: *idTag, Type: types.IdTokenTypeISO14443}
	}
	req := builder.Ended(stopReason, triggerReason, &meter, timestamp, token)
	b.persistTransactions()
	return req, nil
}

// allocateUpdatedRequest builds a TransactionEvent(Updated) for a
// charging-state transition. It returns (nil, nil) when there is nothing to
// report: either the engine state has no charging-state counterpart or no
// transaction is active. Shared by the enqueue path and the synchronous
// send path.
func (b *Bridge201) allocateUpdatedRequest(connectorID int, chargingState, trigger string) (*transactions.TransactionEventRequest, error) {
	state, ok := engineStateToChargingState(chargingState)
	if !ok {
		// Engine state does not correspond to a charging state (e.g.
		// Available, Reserved, Unavailable, Faulted). Nothing to report.
		return nil, nil
	}
	evseID := connectorID

	b.mu.Lock()
	builder, hasBuilder := b.txBuilders[evseID]
	b.mu.Unlock()

	if !hasBuilder {
		// No active transaction. The CSMS only cares about charging-state
		// changes within a transaction. Quietly skip.
		return nil, nil
	}

	reason := mapTriggerReason(trigger)
	now := time.Now()
	energyWh, _ := b.engine.GetMeterSnapshot(connectorID)
	meterVal := b.buildTxUpdatedMeterValue(connectorID, energyWh, now, string(types.ReadingContextOther))
	req := builder.Updated(reason, &meterVal, now, state)
	b.persistTransactions()
	return req, nil
}

// allocateMeterUpdatedRequest builds a TransactionEvent(Updated) carrying
// meter data sampled at the given timestamp. Shared by the enqueue path and
// the synchronous send path.
func (b *Bridge201) allocateMeterUpdatedRequest(connectorID int, value float64, meterContext string, timestamp time.Time) (*transactions.TransactionEventRequest, error) {
	evseID := connectorID

	b.mu.Lock()
	builder, ok := b.txBuilders[evseID]
	b.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("no active transaction builder for EVSE %d", evseID)
	}

	// Update device model with latest power/energy reading
	b.deviceModel.SetVariable("EVSE", "", evseID, "Energy.Active.Import.Register", fmt.Sprintf("%.2f", value), MutabilityReadOnly)

	meter := b.buildTxUpdatedMeterValue(evseID, value, timestamp, meterContext)
	req := builder.Updated(triggerReasonForMeterContext(meterContext), &meter, timestamp)
	b.persistTransactions()
	return req, nil
}

// markQueuedEventsOffline sets the offline flag on every TransactionEvent
// still in the queue. Called on link loss per E11.FR.07: messages created
// while online but never delivered must replay as offline events.
func (b *Bridge201) markQueuedEventsOffline() {
	if b.queue == nil {
		return
	}
	for _, msg := range b.queue.All() {
		if msg.Type != "TransactionEvent" {
			continue
		}
		req, err := queuedTransactionEventRequest(msg.Payload)
		if err != nil {
			continue
		}
		if req.Offline {
			continue
		}
		req.Offline = true
		msg.Payload = req
		if err := b.queue.Update(msg); err != nil {
			slog.Warn("failed to mark queued message offline", "id", msg.ID, "error", err)
		}
	}
}

// enqueueTransactionEvent appends a built request to the durable queue and
// kicks the drain loop. The offline flag reflects link state at occurrence
// time (E11.FR.02/E12.FR.02): events created while disconnected replay with
// offline=true so the CSMS can tell a complete story that arrived late.
func (b *Bridge201) enqueueTransactionEvent(req *transactions.TransactionEventRequest, description string) error {
	if b.queue == nil {
		return fmt.Errorf("cannot enqueue %s: no offline queue configured", description)
	}
	req.Offline = !b.IsConnected()
	if _, err := b.queue.Enqueue(queue.QueuedMessage{
		Type:    "TransactionEvent",
		Payload: req,
	}); err != nil {
		return fmt.Errorf("enqueue %s: %w", description, err)
	}
	b.kickDrain(description)
	return nil
}

// EnqueueTransactionStart appends a TransactionEvent(Started) record to the
// durable queue and kicks the drain loop. It returns the synthetic
// transaction int the caller uses as the engine's active transaction ID,
// mirroring the previous offline behavior. Refused while the station is not
// registered (BootNotification not Accepted).
func (b *Bridge201) EnqueueTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error) {
	if !b.registered.Load() {
		return 0, fmt.Errorf("cannot enqueue TransactionEvent(Started): charging station not registered with CSMS (BootNotification not Accepted)")
	}
	b.tl.LogOutbound("TransactionEvent", ocpppkg.IntPtr(connectorID), nil, fmt.Sprintf("Started evse=%d idTag=%s meter=%s", connectorID, idTag, ocpppkg.FormatMeter(meterStart)), nil)
	req, txInt := b.allocateStartedRequest(connectorID, idTag, meterStart, timestamp, reservationID)
	if err := b.enqueueTransactionEvent(req, fmt.Sprintf("TransactionEvent(Started) evse %d", connectorID)); err != nil {
		return 0, err
	}
	slog.Info("queued TransactionEvent(Started)", "txId", req.TransactionInfo.TransactionID)
	return txInt, nil
}

// EnqueueTransactionStop appends a TransactionEvent(Ended) record to the
// durable queue and kicks the drain loop.
func (b *Bridge201) EnqueueTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error {
	b.tl.LogOutbound("TransactionEvent", nil, &transactionID, fmt.Sprintf("Ended txId=%d meter=%s reason=%s", transactionID, ocpppkg.FormatMeter(meterStop), reason), nil)
	req, err := b.allocateEndedRequest(transactionID, meterStop, timestamp, reason, idTag, meterHistory)
	if err != nil {
		return err
	}
	if err := b.enqueueTransactionEvent(req, fmt.Sprintf("TransactionEvent(Ended) tx %d", transactionID)); err != nil {
		return err
	}
	slog.Info("queued TransactionEvent(Ended)", "txId", req.TransactionInfo.TransactionID)
	return nil
}

// EnqueueMeterValues appends a meter-data TransactionEvent(Updated) record
// to the durable queue and kicks the drain loop. The transactionID is used
// for timeline correlation; the live builder is resolved by connector.
func (b *Bridge201) EnqueueMeterValues(connectorID int, value float64, transactionID int, meterContext string, timestamp time.Time) error {
	b.tl.LogOutbound("TransactionEvent", ocpppkg.IntPtr(connectorID), &transactionID, fmt.Sprintf("Updated evse=%d meter=%s context=%s", connectorID, ocpppkg.FormatMeter(value), meterContext), nil)
	req, err := b.allocateMeterUpdatedRequest(connectorID, value, meterContext, timestamp)
	if err != nil {
		return err
	}
	return b.enqueueTransactionEvent(req, fmt.Sprintf("TransactionEvent(Updated) evse %d", connectorID))
}

// EnqueueTransactionEventUpdated appends a charging-state
// TransactionEvent(Updated) record to the durable queue and kicks the drain
// loop. It is a no-op when there is no active transaction to report on.
func (b *Bridge201) EnqueueTransactionEventUpdated(connectorID int, chargingState, trigger string) error {
	req, err := b.allocateUpdatedRequest(connectorID, chargingState, trigger)
	if err != nil {
		return err
	}
	if req == nil {
		return nil
	}
	b.tl.LogOutbound("TransactionEvent", ocpppkg.IntPtr(connectorID), nil,
		fmt.Sprintf("Updated evse=%d state=%s trigger=%s", connectorID, chargingState, trigger), nil)
	return b.enqueueTransactionEvent(req, fmt.Sprintf("TransactionEvent(Updated) evse %d", connectorID))
}
