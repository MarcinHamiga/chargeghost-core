package v201

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	"github.com/lorenzodonini/ocpp-go/ocppj"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
	"github.com/chargeghost/engine/internal/timeline"
)

// TestEnqueueTransactionStart_QueuesStartedAndReturnsTxInt verifies the
// durable path: while offline, EnqueueTransactionStart persists a
// TransactionEvent(Started) record, returns the synthetic transaction int
// for the engine, and kicks the drain loop.
func TestEnqueueTransactionStart_QueuesStartedAndReturnsTxInt(t *testing.T) {
	b := newTestBridge(t)
	b.connected.Store(false)
	triggered := captureEnqueuedCommands(b)

	startTS := time.Unix(1714348800, 123456789).UTC()
	txInt, err := b.EnqueueTransactionStart(1, "TAG-1", 100.0, startTS, nil)
	require.NoError(t, err)
	require.NotZero(t, txInt)

	require.Equal(t, 1, b.queue.Len())
	msg, ok := b.queue.Peek()
	require.True(t, ok)
	assert.Equal(t, "TransactionEvent", msg.Type)
	req, err := queuedTransactionEventRequest(msg.Payload)
	require.NoError(t, err)
	assert.Equal(t, transactions.TransactionEventStarted, req.EventType)
	assert.Equal(t, 0, req.SequenceNo)
	assert.True(t, startTS.Equal(req.Timestamp.Time), "record must keep the occurrence timestamp")

	require.Len(t, *triggered, 1, "enqueue must kick the drain loop")
}

// TestEnqueueTransactionStart_RejectsWhenNotRegistered mirrors the Send
// guard: nothing is queued before BootNotification is Accepted.
func TestEnqueueTransactionStart_RejectsWhenNotRegistered(t *testing.T) {
	b := newTestBridge(t)
	b.registered.Store(false)

	_, err := b.EnqueueTransactionStart(1, "TAG-1", 100.0, time.Now(), nil)
	require.Error(t, err)
	assert.Equal(t, 0, b.queue.Len())
}

// TestEnqueueTransactionEventUpdated_NoBuilderIsNoOp verifies the quiet
// skip: with no active transaction there is nothing to report.
func TestEnqueueTransactionEventUpdated_NoBuilderIsNoOp(t *testing.T) {
	b := newTestBridge(t)
	b.connected.Store(false)

	require.NoError(t, b.EnqueueTransactionEventUpdated(1, "Charging", "ChargingStateChanged"))
	assert.Equal(t, 0, b.queue.Len())
}

// TestEnqueueMeterValues_NoBuilderErrors verifies meter samples without an
// active transaction builder are reported, not silently queued.
func TestEnqueueMeterValues_NoBuilderErrors(t *testing.T) {
	b := newTestBridge(t)
	b.connected.Store(false)

	err := b.EnqueueMeterValues(1, 321.0, 7, "Sample.Clock", time.Now())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no active transaction builder")
}

// TestEnqueueStop_DrainsStartedThenEndedInOrder exercises the full durable
// lifecycle: Started and Ended records queued while offline are delivered
// in FIFO order once connected, and the queue ends up empty.
func TestEnqueueStop_DrainsStartedThenEndedInOrder(t *testing.T) {
	b := newTestBridge(t)
	b.connected.Store(false)

	startTS := time.Unix(1714348800, 0).UTC()
	txInt, err := b.EnqueueTransactionStart(1, "TAG-1", 100.0, startTS, nil)
	require.NoError(t, err)

	stopTS := time.Unix(1714349800, 0).UTC()
	require.NoError(t, b.EnqueueTransactionStop(4567.89, stopTS, txInt, "EVDisconnected", nil, nil))
	require.Equal(t, 2, b.queue.Len())

	var eventTypes []transactions.TransactionEvent
	b.cs = &stubChargingStation{
		sendRequestAsync: func(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error {
			txReq, ok := request.(*transactions.TransactionEventRequest)
			require.True(t, ok)
			eventTypes = append(eventTypes, txReq.EventType)
			if callback != nil {
				callback(transactions.NewTransactionEventResponse(), nil)
			}
			return nil
		},
	}

	b.connected.Store(true)
	b.DrainOfflineQueue()

	assert.Equal(t,
		[]transactions.TransactionEvent{transactions.TransactionEventStarted, transactions.TransactionEventEnded},
		eventTypes)
	assert.Equal(t, 0, b.queue.Len())
}

// TestTransactionState_RestartContinuesUUIDAndSequence verifies that a
// restart mid-transaction does not split it: after LoadState, stopping the
// pre-restart transaction reuses its UUID with the next sequence number,
// and fresh transactions never reuse synthetic IDs.
func TestTransactionState_RestartContinuesUUIDAndSequence(t *testing.T) {
	dir := t.TempDir()

	before := newTestBridge(t)
	// Set only the transaction persist dir directly: a full SetPersistDir
	// would also reconfigure the device model, whose async auto-save has
	// a pre-existing race with SetPersistDir outside this task's scope.
	before.txPersistDir = dir
	before.connected.Store(false)

	startTS := time.Unix(1714348800, 0).UTC()
	txInt, err := before.EnqueueTransactionStart(1, "TAG-1", 100.0, startTS, nil)
	require.NoError(t, err)

	msg, ok := before.queue.Peek()
	require.True(t, ok)
	started, err := queuedTransactionEventRequest(msg.Payload)
	require.NoError(t, err)
	require.NotEmpty(t, started.TransactionInfo.TransactionID)

	// Simulate a restart: a fresh bridge loads the persisted state.
	after := newTestBridge(t)
	require.NoError(t, after.LoadState(dir))
	after.connected.Store(false)

	stopTS := time.Unix(1714349800, 0).UTC()
	require.NoError(t, after.EnqueueTransactionStop(4567.89, stopTS, txInt, "EVDisconnected", nil, nil))

	endedMsg, ok := after.queue.Peek()
	require.True(t, ok)
	ended, err := queuedTransactionEventRequest(endedMsg.Payload)
	require.NoError(t, err)
	assert.Equal(t, started.TransactionInfo.TransactionID, ended.TransactionInfo.TransactionID,
		"restart must reuse the transaction UUID")
	assert.Equal(t, transactions.TransactionEventEnded, ended.EventType)
	assert.Equal(t, started.SequenceNo+1, ended.SequenceNo,
		"sequence must continue where the persisted state left off")

	// Synthetic IDs keep moving forward instead of colliding with the
	// restored engine transaction.
	txInt2, err := after.EnqueueTransactionStart(2, "TAG-2", 200.0, stopTS, nil)
	require.NoError(t, err)
	assert.Greater(t, txInt2, txInt)
}

// TestEnqueueTransactionEvent_OfflineFlagReflectsLinkState verifies
// E11.FR.02/E12.FR.02: events created while disconnected carry
// offline=true; events created while connected do not.
func TestEnqueueTransactionEvent_OfflineFlagReflectsLinkState(t *testing.T) {
	b := newTestBridge(t)

	b.connected.Store(false)
	_, err := b.EnqueueTransactionStart(1, "TAG-1", 100.0, time.Now(), nil)
	require.NoError(t, err)

	b.connected.Store(true)
	_, err = b.EnqueueTransactionStart(2, "TAG-2", 200.0, time.Now(), nil)
	require.NoError(t, err)

	msgs := b.queue.All()
	require.Len(t, msgs, 2)
	offlineReq, err := queuedTransactionEventRequest(msgs[0].Payload)
	require.NoError(t, err)
	assert.True(t, offlineReq.Offline)
	onlineReq, err := queuedTransactionEventRequest(msgs[1].Payload)
	require.NoError(t, err)
	assert.False(t, onlineReq.Offline)
}

// TestMarkQueuedEventsOffline_FlipsUnsentMessages verifies E11.FR.07:
// messages created while online but still queued at link loss replay as
// offline events.
func TestMarkQueuedEventsOffline_FlipsUnsentMessages(t *testing.T) {
	b := newTestBridge(t)
	b.connected.Store(true)
	_, err := b.EnqueueTransactionStart(1, "TAG-1", 100.0, time.Now(), nil)
	require.NoError(t, err)

	b.markQueuedEventsOffline()

	msg, ok := b.queue.Peek()
	require.True(t, ok)
	req, err := queuedTransactionEventRequest(msg.Payload)
	require.NoError(t, err)
	assert.True(t, req.Offline)
}

// TestDrainQueue_FatalStartedCascadesEnded verifies UUID gating: when the
// CSMS deterministically rejects a Started event, the transaction's Ended
// event is cascaded to dead-letter without ever hitting the wire.
func TestDrainQueue_FatalStartedCascadesEnded(t *testing.T) {
	dlPath := filepath.Join(t.TempDir(), "dead_letter.jsonl")
	b := newTestBridge(t)
	b.queue = queue.NewInMemoryQueueWithConfig(10, queue.Config{DeadLetterPath: dlPath})
	b.connected.Store(false)

	txInt, err := b.EnqueueTransactionStart(1, "REJECTED-TAG", 100.0, time.Now(), nil)
	require.NoError(t, err)
	require.NoError(t, b.EnqueueTransactionStop(4567.89, time.Now(), txInt, "EVDisconnected", nil, nil))
	require.Equal(t, 2, b.queue.Len())

	var sentTypes []transactions.TransactionEvent
	b.cs = &stubChargingStation{
		sendRequestAsync: func(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error {
			txReq, ok := request.(*transactions.TransactionEventRequest)
			require.True(t, ok)
			sentTypes = append(sentTypes, txReq.EventType)
			return ocpp.NewError(ocppj.NotSupported, "TransactionEvent not supported", "msg-1")
		},
	}

	b.connected.Store(true)
	b.DrainOfflineQueue()

	assert.Equal(t, []transactions.TransactionEvent{transactions.TransactionEventStarted}, sentTypes,
		"the orphaned Ended must never be sent")
	assert.Equal(t, 0, b.queue.Len())
	data, err := os.ReadFile(dlPath)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, "rejected:NotSupported")
	assert.Contains(t, body, "start-unconfirmed")
}

// TestDrainQueue_CallErrorMatrix is the conformance regression net: for
// each wire outcome, it pins whether the drain dead-letters and continues
// (deterministic rejection, success) or keeps the message and stops
// (transient failure), verified by whether the sentinel event behind the
// probe is attempted.
func TestDrainQueue_CallErrorMatrix(t *testing.T) {
	cases := []struct {
		name          string
		probeErr      error
		wantSentinel  bool
		wantQueueLen  int
		wantDLReasons []string
	}{
		{"success", nil, true, 0, nil},
		{"fatal/NotSupported", ocpp.NewError(ocppj.NotSupported, "nope", "m1"), true, 0, []string{"rejected:NotSupported"}},
		{"fatal/NotImplemented", ocpp.NewError(ocppj.NotImplemented, "nope", "m1"), true, 0, []string{"rejected:NotImplemented"}},
		{"fatal/FormatViolation", ocpp.NewError(ocppj.FormatViolationV2, "nope", "m1"), true, 0, []string{"rejected:FormatViolation"}},
		{"fatal/PropertyConstraintViolation", ocpp.NewError(ocppj.PropertyConstraintViolation, "nope", "m1"), true, 0, []string{"rejected:PropertyConstraintViolation"}},
		{"retryable/InternalError", ocpp.NewError(ocppj.InternalError, "busy", "m1"), false, 2, nil},
		{"retryable/GenericError", ocpp.NewError(ocppj.GenericError, "busy", "m1"), false, 2, nil},
		{"retryable/transport", fmt.Errorf("connection reset"), false, 2, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dlPath := filepath.Join(t.TempDir(), "dead_letter.jsonl")
			b := newTestBridge(t)
			b.queue = queue.NewInMemoryQueueWithConfig(10, queue.Config{DeadLetterPath: dlPath})
			b.connected.Store(false)

			_, err := b.EnqueueTransactionStart(1, "PROBE", 100.0, time.Now(), nil)
			require.NoError(t, err)
			_, err = b.EnqueueTransactionStart(2, "SENTINEL", 200.0, time.Now(), nil)
			require.NoError(t, err)

			var sent []string
			b.cs = &stubChargingStation{
				sendRequestAsync: func(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error {
					txReq, ok := request.(*transactions.TransactionEventRequest)
					require.True(t, ok)
					sent = append(sent, string(txReq.EventType))
					if len(sent) == 1 && tc.probeErr != nil {
						return tc.probeErr
					}
					if callback != nil {
						callback(transactions.NewTransactionEventResponse(), nil)
					}
					return nil
				},
			}

			b.connected.Store(true)
			b.DrainOfflineQueue()

			if tc.wantSentinel {
				assert.Equal(t, []string{"Started", "Started"}, sent)
			} else {
				assert.Equal(t, []string{"Started"}, sent)
			}
			assert.Equal(t, tc.wantQueueLen, b.queue.Len())
			if len(tc.wantDLReasons) > 0 {
				data, err := os.ReadFile(dlPath)
				require.NoError(t, err)
				for _, reason := range tc.wantDLReasons {
					assert.Contains(t, string(data), `"reason":"`+reason+`"`)
				}
			}
		})
	}
}

// TestDrainQueue_ObservabilityFeedsTimelineAndTracker verifies that a
// deterministically rejected event surfaces in the timeline and the
// status tracker: the tracker's last error names the CALLERROR code and
// the end-of-pass snapshot reports the drained depth and drop count.
func TestDrainQueue_ObservabilityFeedsTimelineAndTracker(t *testing.T) {
	dlPath := filepath.Join(t.TempDir(), "dead_letter.jsonl")
	b := newTestBridge(t)
	b.queue = queue.NewInMemoryQueueWithConfig(10, queue.Config{DeadLetterPath: dlPath})
	store := timeline.NewStore(16)
	b.tl = ocpppkg.NewTimelineLogger(store)
	b.connected.Store(false)

	_, err := b.EnqueueTransactionStart(1, "REJECTED-TAG", 100.0, time.Now(), nil)
	require.NoError(t, err)

	b.cs = &stubChargingStation{
		sendRequestAsync: func(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error {
			return ocpp.NewError(ocppj.NotSupported, "nope", "msg-1")
		},
	}
	b.connected.Store(true)
	b.DrainOfflineQueue()

	assert.Equal(t, 0, b.queue.Len())
	snap := b.statusTracker.Snapshot("", "", "")
	assert.Contains(t, snap.LastError, "NotSupported")
	assert.False(t, snap.LastErrorAt.IsZero())
	assert.Equal(t, 0, snap.QueueDepth)
	assert.Equal(t, 1, snap.QueueDropped)

	events, _ := store.Query(timeline.TimelineFilter{Limit: 16})
	found := false
	for _, evt := range events {
		if evt.Action == "TransactionEvent" && evt.Level == "error" {
			found = true
			assert.Contains(t, evt.Summary, "NotSupported")
		}
	}
	assert.True(t, found, "timeline must record the rejected TransactionEvent")
}

// TestDrainQueue_RetryableErrorStopsDrainAndRetries verifies transient
// failures keep the message queued with retry metadata and stop the pass
// so order is preserved.
func TestDrainQueue_RetryableErrorStopsDrainAndRetries(t *testing.T) {
	b := newTestBridge(t)
	b.connected.Store(false)

	_, err := b.EnqueueTransactionStart(1, "TAG-1", 100.0, time.Now(), nil)
	require.NoError(t, err)
	_, err = b.EnqueueTransactionStart(2, "TAG-2", 200.0, time.Now(), nil)
	require.NoError(t, err)

	attempts := 0
	b.cs = &stubChargingStation{
		sendRequestAsync: func(request ocpp.Request, callback func(confirmation ocpp.Response, protoError error)) error {
			attempts++
			return ocpp.NewError(ocppj.GenericError, "try again later", "msg-1")
		},
	}

	b.connected.Store(true)
	b.DrainOfflineQueue()

	assert.Equal(t, 1, attempts, "drain must stop at the first transient failure")
	require.Equal(t, 2, b.queue.Len())
	msg, ok := b.queue.Peek()
	require.True(t, ok)
	assert.Equal(t, 1, msg.RetryCount)
	assert.NotEmpty(t, msg.LastError)
}
