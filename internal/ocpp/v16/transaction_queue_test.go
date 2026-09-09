package v16

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
	"github.com/lorenzodonini/ocpp-go/ocppj"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
	"github.com/chargeghost/engine/internal/timeline"
)

// TestEnqueueTransactionStart_PersistsAndTriggersDrain verifies the full
// durable path: EnqueueTransactionStart persists the record with its
// occurrence timestamp, kicks a drain pass through the dispatcher, and the
// drain sends it with the original timestamp and resolves the CSMS-assigned
// transaction ID into the engine.
func TestEnqueueTransactionStart_PersistsAndTriggersDrain(t *testing.T) {
	q := queue.NewInMemoryQueue(10)
	dispatcher := ocpppkg.NewCommandDispatcher()
	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 16, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 0, nil, 0))
	b := &Bridge16{
		queue:      q,
		dispatcher: dispatcher,
		engine:     e,
		configKeys: NewConfigKeyManager(),
	}
	b.registered.Store(true)
	b.connected.Store(true)

	startTS := time.Unix(1714348800, 123456789).UTC()
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.StartTransactionRequest)
		require.True(t, ok)
		assert.Equal(t, 1, req.ConnectorId)
		assert.Equal(t, "TAG-1", req.IdTag)
		assert.True(t, startTS.Equal(req.Timestamp.Time), "replay must preserve the occurrence timestamp")
		return core.NewStartTransactionConfirmation(types.NewIdTagInfo(types.AuthorizationStatusAccepted), 77), nil
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go dispatcher.Run(ctx)

	txID, err := b.EnqueueTransactionStart(1, "TAG-1", 100.0, startTS, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, txID, "v1.6 assigns the transaction ID at send time")

	require.Eventually(t, func() bool {
		return q.Len() == 0
	}, 2*time.Second, 10*time.Millisecond, "drain should send the queued StartTransaction")

	txIDPtr := b.engine.GetActiveTransactionID(1)
	require.NotNil(t, txIDPtr)
	assert.Equal(t, 77, *txIDPtr)
}

// TestEnqueueTransactionStart_RejectsWhenNotRegistered mirrors the Send
// guard: nothing is queued before BootNotification is Accepted.
func TestEnqueueTransactionStart_RejectsWhenNotRegistered(t *testing.T) {
	q := queue.NewInMemoryQueue(10)
	b := &Bridge16{queue: q, dispatcher: ocpppkg.NewCommandDispatcher()}

	_, err := b.EnqueueTransactionStart(1, "TAG-1", 100.0, time.Now(), nil)
	require.Error(t, err)
	assert.Equal(t, 0, q.Len())
}

// TestEnqueueTransactionStop_PersistsMappedReason verifies the stop record
// is persisted with its occurrence timestamp and a spec-valid reason.
func TestEnqueueTransactionStop_PersistsMappedReason(t *testing.T) {
	q := queue.NewInMemoryQueue(10)
	b := &Bridge16{queue: q, dispatcher: ocpppkg.NewCommandDispatcher()}
	b.connected.Store(false)

	stopTS := time.Unix(1714349800, 987654321).UTC()
	require.NoError(t, b.EnqueueTransactionStop(4567.89, stopTS, 77, "user_requested", nil, nil))

	msg, ok := q.Peek()
	require.True(t, ok)
	assert.Equal(t, "StopTransaction", msg.Type)
	payload, ok := msg.Payload.(queuedStopTransaction16)
	require.True(t, ok)
	assert.Equal(t, 77, payload.TransactionID)
	assert.True(t, stopTS.Equal(payload.Timestamp))
	assert.Equal(t, string(core.ReasonLocal), payload.Reason)
}

// TestEnqueueMeterValues_PersistsOccurrenceTimestamp verifies the meter
// record keeps the sample timestamp for chronological replay.
func TestEnqueueMeterValues_PersistsOccurrenceTimestamp(t *testing.T) {
	q := queue.NewInMemoryQueue(10)
	b := &Bridge16{queue: q, dispatcher: ocpppkg.NewCommandDispatcher()}
	b.connected.Store(false)

	meterTS := time.Unix(1714351800, 444555666).UTC()
	require.NoError(t, b.EnqueueMeterValues(1, 1500.25, 77, "Sample.Clock", meterTS))

	msg, ok := q.Peek()
	require.True(t, ok)
	payload, ok := msg.Payload.(queuedMeterValues16)
	require.True(t, ok)
	assert.True(t, meterTS.Equal(payload.Timestamp))
	assert.Equal(t, "Sample.Clock", payload.Context)
}

// TestEnqueueTransactionEventUpdated_NoOp verifies the v1.6 symmetry stub:
// charging-state changes travel via StatusNotification/MeterValues, so
// nothing is queued and no error is reported.
func TestEnqueueTransactionEventUpdated_NoOp(t *testing.T) {
	q := queue.NewInMemoryQueue(10)
	b := &Bridge16{queue: q, dispatcher: ocpppkg.NewCommandDispatcher()}

	require.NoError(t, b.EnqueueTransactionEventUpdated(1, "Charging", "ChargingStateChanged"))
	assert.Equal(t, 0, q.Len())
}

// TestDrainQueue_FatalCallErrorDeadLettersAndContinues verifies the
// protocol-compliant reaction to a deterministic CSMS rejection: the
// rejected message is dead-lettered immediately and the drain continues
// with the messages behind it instead of stopping.
func TestDrainQueue_FatalCallErrorDeadLettersAndContinues(t *testing.T) {
	dlPath := filepath.Join(t.TempDir(), "dead_letter.jsonl")
	q := queue.NewInMemoryQueueWithConfig(10, queue.Config{DeadLetterPath: dlPath})

	e := engine.NewEngine(true, 55000)
	e.AddConnector(230, 16, 1)
	e.AddConnector(230, 16, 1)
	e.PlugIn(1)
	e.PlugIn(2)
	require.NoError(t, e.StartSession(1, 0, nil, 0))
	require.NoError(t, e.StartSession(2, 0, nil, 0))

	b := &Bridge16{
		queue:      q,
		dispatcher: ocpppkg.NewCommandDispatcher(),
		engine:     e,
		configKeys: NewConfigKeyManager(),
	}
	b.registered.Store(true)
	b.connected.Store(true)

	startTS := time.Unix(1714348800, 0).UTC()
	_, err := b.EnqueueTransactionStart(1, "REJECTED-TAG", 100.0, startTS, nil)
	require.NoError(t, err)
	_, err = b.EnqueueTransactionStart(2, "GOOD-TAG", 200.0, startTS, nil)
	require.NoError(t, err)
	require.Equal(t, 2, q.Len())

	var sentTags []string
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.StartTransactionRequest)
		require.True(t, ok)
		sentTags = append(sentTags, req.IdTag)
		if req.IdTag == "REJECTED-TAG" {
			return nil, ocpp.NewError(ocppj.NotSupported, "StartTransaction not supported", "msg-1")
		}
		return core.NewStartTransactionConfirmation(types.NewIdTagInfo(types.AuthorizationStatusAccepted), 77), nil
	}}

	b.drainQueue()

	// Both messages were attempted; the rejected one was dead-lettered and
	// the drain continued to the good one.
	assert.Equal(t, []string{"REJECTED-TAG", "GOOD-TAG"}, sentTags)
	assert.Equal(t, 0, q.Len())
	assert.Equal(t, 1, q.Dropped())
	data, err := os.ReadFile(dlPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "rejected:NotSupported")

	// The confirmed start still resolved its CSMS transaction ID.
	txID := e.GetActiveTransactionID(2)
	require.NotNil(t, txID)
	assert.Equal(t, 77, *txID)
}

// TestDrainQueue_StopAfterFatalStartCascadesToDeadLetter verifies session
// gating: once a StartTransaction is deterministically rejected, the
// session's StopTransaction references a transaction that never existed,
// so it is cascaded to dead-letter without ever hitting the wire.
func TestDrainQueue_StopAfterFatalStartCascadesToDeadLetter(t *testing.T) {
	dlPath := filepath.Join(t.TempDir(), "dead_letter.jsonl")
	q := queue.NewInMemoryQueueWithConfig(10, queue.Config{DeadLetterPath: dlPath})

	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 16, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 0, nil, 0))
	e.SetActiveTransaction(1, 88)

	b := &Bridge16{
		queue:      q,
		dispatcher: ocpppkg.NewCommandDispatcher(),
		engine:     e,
		configKeys: NewConfigKeyManager(),
	}
	b.registered.Store(true)
	b.connected.Store(true)

	ts := time.Unix(1714348800, 0).UTC()
	_, err := b.EnqueueTransactionStart(1, "REJECTED-TAG", 100.0, ts, nil)
	require.NoError(t, err)
	require.NoError(t, b.EnqueueTransactionStop(4567.89, ts, 88, "EVDisconnected", nil, nil))

	var sentTypes []string
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		switch request.(type) {
		case *core.StartTransactionRequest:
			sentTypes = append(sentTypes, "StartTransaction")
			return nil, ocpp.NewError(ocppj.NotSupported, "nope", "msg-1")
		default:
			sentTypes = append(sentTypes, "other")
			return nil, nil
		}
	}}

	b.drainQueue()

	assert.Equal(t, []string{"StartTransaction"}, sentTypes, "the orphaned Stop must never be sent")
	assert.Equal(t, 0, q.Len())
	assert.Equal(t, 2, q.Dropped())
	data, err := os.ReadFile(dlPath)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, "rejected:NotSupported")
	assert.Contains(t, body, "start-unconfirmed")
}

// TestDrainQueue_ObservabilityFeedsTimelineAndTracker verifies that a
// deterministically rejected message surfaces in the timeline and the
// status tracker: the tracker's last error names the CALLERROR code and
// the end-of-pass snapshot reports the drained depth and drop count.
func TestDrainQueue_ObservabilityFeedsTimelineAndTracker(t *testing.T) {
	dlPath := filepath.Join(t.TempDir(), "dead_letter.jsonl")
	q := queue.NewInMemoryQueueWithConfig(10, queue.Config{DeadLetterPath: dlPath})
	store := timeline.NewStore(16)

	e := engine.NewEngine(false, 55000)
	e.AddConnector(230, 16, 1)
	e.PlugIn(1)
	require.NoError(t, e.StartSession(1, 0, nil, 0))

	b := &Bridge16{
		queue:         q,
		dispatcher:    ocpppkg.NewCommandDispatcher(),
		engine:        e,
		configKeys:    NewConfigKeyManager(),
		tl:            ocpppkg.NewTimelineLogger(store),
		statusTracker: ocpppkg.NewStatusTracker("ws://csms", "CP-1", "1.6"),
	}
	b.registered.Store(true)
	b.connected.Store(true)

	_, err := b.EnqueueTransactionStart(1, "REJECTED-TAG", 100.0, time.Now(), nil)
	require.NoError(t, err)
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		return nil, ocpp.NewError(ocppj.NotSupported, "nope", "msg-1")
	}}

	b.drainQueue()

	assert.Equal(t, 0, q.Len())
	snap := b.statusTracker.Snapshot("", "", "")
	assert.Contains(t, snap.LastError, "NotSupported")
	assert.False(t, snap.LastErrorAt.IsZero())
	assert.Equal(t, 0, snap.QueueDepth)
	assert.Equal(t, 1, snap.QueueDropped)

	events, _ := store.Query(timeline.TimelineFilter{Limit: 16})
	found := false
	for _, evt := range events {
		if evt.Action == "StartTransaction" && evt.Level == "error" {
			found = true
			assert.Contains(t, evt.Summary, "NotSupported")
		}
	}
	assert.True(t, found, "timeline must record the rejected StartTransaction")
}

// TestDrainQueue_CallErrorMatrix is the conformance regression net: for
// each wire outcome, it pins whether the drain dead-letters and continues
// (deterministic rejection, success) or keeps the message and stops
// (transient failure), verified by whether the sentinel message behind the
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
		{"fatal/FormationViolation", ocpp.NewError(ocppj.FormatViolationV16, "nope", "m1"), true, 0, []string{"rejected:FormationViolation"}},
		{"fatal/PropertyConstraintViolation", ocpp.NewError(ocppj.PropertyConstraintViolation, "nope", "m1"), true, 0, []string{"rejected:PropertyConstraintViolation"}},
		{"retryable/InternalError", ocpp.NewError(ocppj.InternalError, "busy", "m1"), false, 2, nil},
		{"retryable/GenericError", ocpp.NewError(ocppj.GenericError, "busy", "m1"), false, 2, nil},
		{"retryable/transport", fmt.Errorf("connection reset"), false, 2, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dlPath := filepath.Join(t.TempDir(), "dead_letter.jsonl")
			q := queue.NewInMemoryQueueWithConfig(10, queue.Config{DeadLetterPath: dlPath})

			e := engine.NewEngine(true, 55000)
			e.AddConnector(230, 16, 1)
			e.AddConnector(230, 16, 1)
			e.PlugIn(1)
			e.PlugIn(2)
			require.NoError(t, e.StartSession(1, 0, nil, 0))
			require.NoError(t, e.StartSession(2, 0, nil, 0))

			b := &Bridge16{
				queue:         q,
				dispatcher:    ocpppkg.NewCommandDispatcher(),
				engine:        e,
				configKeys:    NewConfigKeyManager(),
				statusTracker: ocpppkg.NewStatusTracker("ws://csms", "CP-1", "1.6"),
			}
			b.registered.Store(true)
			b.connected.Store(true)

			ts := time.Unix(1714348800, 0).UTC()
			_, err := b.EnqueueTransactionStart(1, "PROBE", 100.0, ts, nil)
			require.NoError(t, err)
			_, err = b.EnqueueTransactionStart(2, "SENTINEL", 200.0, ts, nil)
			require.NoError(t, err)

			var sentTags []string
			b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
				req, ok := request.(*core.StartTransactionRequest)
				require.True(t, ok)
				sentTags = append(sentTags, req.IdTag)
				if req.IdTag == "PROBE" && tc.probeErr != nil {
					return nil, tc.probeErr
				}
				return core.NewStartTransactionConfirmation(types.NewIdTagInfo(types.AuthorizationStatusAccepted), 77), nil
			}}

			b.drainQueue()

			if tc.wantSentinel {
				assert.Equal(t, []string{"PROBE", "SENTINEL"}, sentTags)
			} else {
				assert.Equal(t, []string{"PROBE"}, sentTags)
			}
			assert.Equal(t, tc.wantQueueLen, q.Len())
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

// TestRetryPending_LinearBackoff verifies the OCPP 1.6 §3.7.1 schedule:
// the wait before a retransmission is TransactionMessageRetryInterval
// multiplied by the number of preceding transmissions.
func TestRetryPending_LinearBackoff(t *testing.T) {
	keys := NewConfigKeyManager()
	require.Equal(t, "Accepted", keys.SetConfigValue("TransactionMessageRetryInterval", "60"))
	b := &Bridge16{configKeys: keys}

	ago := func(d time.Duration) *time.Time { t := time.Now().UTC().Add(-d); return &t }
	newMsg := func(retryCount int, last *time.Time) queue.QueuedMessage {
		return queue.QueuedMessage{RetryCount: retryCount, LastAttemptAt: last}
	}

	assert.False(t, b.retryPending(newMsg(0, nil)), "never attempted: no backoff")
	assert.True(t, b.retryPending(newMsg(1, ago(30*time.Second))), "1 preceding attempt: wait 60s")
	assert.False(t, b.retryPending(newMsg(1, ago(61*time.Second))), "1 preceding attempt: 60s elapsed, ready")
	assert.True(t, b.retryPending(newMsg(2, ago(61*time.Second))), "2 preceding attempts: wait 120s")
	assert.False(t, b.retryPending(newMsg(2, ago(121*time.Second))), "2 preceding attempts: 120s elapsed, ready")
}
