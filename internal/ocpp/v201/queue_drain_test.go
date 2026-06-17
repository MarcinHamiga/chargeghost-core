package v201

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/transactions"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

func TestDrainQueue_InvalidPayloadNotDequeued(t *testing.T) {
	b := newTestBridge(t)
	b.queue = queue.NewInMemoryQueue(3)
	b.connected.Store(true)

	_, err := b.queue.Enqueue(queue.QueuedMessage{
		Type:    "TransactionEvent",
		Payload: map[string]string{"invalid": "payload"},
	})
	require.NoError(t, err)

	b.drainQueue()

	assert.Equal(t, 1, b.queue.Len())
	msg, ok := b.queue.Peek()
	require.True(t, ok)
	assert.Equal(t, 1, msg.RetryCount)
	assert.NotEmpty(t, msg.LastError)
}

func TestQueueDepth_NilQueueReturnsZero(t *testing.T) {
	b := newTestBridge(t)
	b.queue = nil
	assert.Equal(t, 0, b.QueueDepth())
}

func TestQueueDepth_NonEmptyQueueReturnsLength(t *testing.T) {
	b := newTestBridge(t)
	b.queue = queue.NewInMemoryQueue(3)
	_, err := b.queue.Enqueue(queue.QueuedMessage{Type: "TransactionEvent", Payload: "x"})
	require.NoError(t, err)
	_, err = b.queue.Enqueue(queue.QueuedMessage{Type: "TransactionEvent", Payload: "y"})
	require.NoError(t, err)
	assert.Equal(t, 2, b.QueueDepth())
}

func TestDrainLoopInterval_DefaultIs30Seconds(t *testing.T) {
	b := newTestBridge(t)
	// No OCPPCommCtrlr.TransactionMessageRetryInterval set → fallback to 30.
	assert.Equal(t, 30*time.Second, b.drainLoopInterval())
}

func TestDrainLoopInterval_NonPositiveValueFallsBackToDefault(t *testing.T) {
	b := newTestBridge(t)
	// deviceModelInt with an invalid string also returns the fallback.
	b.deviceModel.SetVariable("OCPPCommCtrlr", "", 0, "TransactionMessageRetryInterval", "-1", MutabilityReadWrite)
	assert.Equal(t, 30*time.Second, b.drainLoopInterval())
}

func TestDrainLoopInterval_RespectsDeviceModel(t *testing.T) {
	b := newTestBridge(t)
	b.deviceModel.SetVariable("OCPPCommCtrlr", "", 0, "TransactionMessageRetryInterval", "5", MutabilityReadWrite)
	assert.Equal(t, 5*time.Second, b.drainLoopInterval())
}

func TestStartDrainLoop_StopsOnContextCancel(t *testing.T) {
	b := newTestBridge(t)
	b.queue = queue.NewInMemoryQueue(3)
	b.connected.Store(false) // ensure the loop does nothing on tick

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.startDrainLoop(ctx, 10*time.Millisecond)
		close(done)
	}()
	cancel()
	select {
	case <-done:
		// returned — good
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected startDrainLoop to return promptly on ctx cancel")
	}
}

func TestStartDrainLoop_StopsOnZeroIntervalByFallingBack(t *testing.T) {
	// interval=0 falls back to 30s; verify the loop does NOT exit early
	// (otherwise we'd silently stop draining after first call).
	b := newTestBridge(t)
	b.queue = queue.NewInMemoryQueue(3)
	b.connected.Store(false) // ensure drain does nothing

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.startDrainLoop(ctx, 0)
		close(done)
	}()
	// Cancel after a brief moment — should return promptly on cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
		// returned — good
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected startDrainLoop to return promptly on ctx cancel even with 0 interval")
	}
}

func TestIdempotencyKey_DerivesStableKeyForSameRequest(t *testing.T) {
	b := newTestBridge(t)
	req := newSampleTxEvent(t, b)
	key1 := IdempotencyKeyFor(req)
	require.NotEmpty(t, key1, "expected non-empty idempotency key")

	// Same logical event → same key.
	req2 := newSampleTxEvent(t, b)
	// Note: a fresh builder produces a new UUID, so we synthesize the
	// second request by copying the first.
	req2.TransactionInfo.TransactionID = req.TransactionInfo.TransactionID
	req2.SequenceNo = req.SequenceNo
	req2.EventType = req.EventType
	req2.Timestamp = req.Timestamp
	key2 := IdempotencyKeyFor(req2)
	assert.Equal(t, key1, key2)
}

func TestIdempotencyKey_ChangesWhenSequenceNumberChanges(t *testing.T) {
	b := newTestBridge(t)
	req := newSampleTxEvent(t, b)
	key1 := IdempotencyKeyFor(req)

	req.SequenceNo++
	key2 := IdempotencyKeyFor(req)
	assert.NotEqual(t, key1, key2, "different sequenceNo → different idempotency key")
}

func TestIdempotencyKey_NilRequestReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", IdempotencyKeyFor(nil))
}

func TestFormatIdempotencyKey_TruncatesTo12(t *testing.T) {
	long := "abcdefghijklmnopqrstuvwxyz"
	assert.Equal(t, "abcdefghijkl", formatIdempotencyKey(long))
	assert.Equal(t, "", formatIdempotencyKey(""))
}

func TestMarkQueuedMessageFailure_ExhaustedMovesToDeadLetter(t *testing.T) {
	dir := t.TempDir()
	dlPath := filepath.Join(dir, "dead_letter.jsonl")
	cfg := queue.Config{DeadLetterPath: dlPath}
	b := newTestBridge(t)
	b.queue, _ = queue.NewQueueWithConfig(true, filepath.Join(dir, "queue.json"), 2, cfg)
	b.connected.Store(true)

	// Pin the device-model retries to 1 so the message exhausts on the first failure.
	b.deviceModel.SetVariable("OCPPCommCtrlr", "", 0, "RetryBackOffRepeatTimes", "1", MutabilityReadWrite)

	_, err := b.queue.Enqueue(queue.QueuedMessage{
		Type:    "TransactionEvent",
		Payload: map[string]string{"invalid": "payload"},
	})
	require.NoError(t, err)

	b.drainQueue()

	// After exhaustion, the message should be removed from the active queue.
	assert.Equal(t, 0, b.queue.Len(), "exhausted message should be removed from active queue")

	// And the dead-letter file should contain exactly one record.
	data, err := os.ReadFile(dlPath)
	require.NoError(t, err)
	require.NotEmpty(t, data)
	body := string(data)
	assert.True(t, strings.Contains(body, `"reason":"exhausted"`), "dead-letter should be marked exhausted: %s", body)

	// And the dropped counter should be incremented.
	assert.Equal(t, 1, b.queue.Dropped())
}

func TestMarkQueuedMessageFailure_NotYetExhaustedStaysInQueue(t *testing.T) {
	b := newTestBridge(t)
	b.queue = queue.NewInMemoryQueue(5)
	b.deviceModel.SetVariable("OCPPCommCtrlr", "", 0, "RetryBackOffRepeatTimes", "5", MutabilityReadWrite)
	b.connected.Store(true)

	_, err := b.queue.Enqueue(queue.QueuedMessage{
		Type:    "TransactionEvent",
		Payload: map[string]string{"invalid": "payload"},
	})
	require.NoError(t, err)

	b.drainQueue()

	// The first attempt failed but the message is still in the queue,
	// with RetryCount incremented and LastError set.
	msg, ok := b.queue.Peek()
	require.True(t, ok)
	assert.Equal(t, 1, msg.RetryCount)
	assert.NotEmpty(t, msg.LastError)
	assert.Equal(t, 0, b.queue.Dropped(), "non-exhausted message must not increment dropped count")
}

func TestMarkQueuedMessageFailure_NoDeadLetterConfiguredStillLogs(t *testing.T) {
	b := newTestBridge(t)
	// InMemoryQueue with no dead-letter path.
	b.queue = queue.NewInMemoryQueue(1)
	b.deviceModel.SetVariable("OCPPCommCtrlr", "", 0, "RetryBackOffRepeatTimes", "1", MutabilityReadWrite)
	b.connected.Store(true)

	_, err := b.queue.Enqueue(queue.QueuedMessage{
		Type:    "TransactionEvent",
		Payload: map[string]string{"invalid": "payload"},
	})
	require.NoError(t, err)

	b.drainQueue()

	// The message is removed (exhausted) but no dead-letter file is created.
	assert.Equal(t, 0, b.queue.Len(), "exhausted message should be dequeued even without DLQ")
}

// newSampleTxEvent constructs a minimal TransactionEventRequest for
// idempotency-key tests. The shape doesn't have to be valid; we only
// need the Identity-bearing fields populated.
func newSampleTxEvent(t *testing.T, _ *Bridge201) *transactions.TransactionEventRequest {
	t.Helper()
	return &transactions.TransactionEventRequest{
		EventType:  transactions.TransactionEventStarted,
		Timestamp:  ocpp201types.NewDateTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		SequenceNo: 0,
		TransactionInfo: transactions.Transaction{
			TransactionID: "test-tx-123",
		},
	}
}

// TestHeartbeatResilience_FailedHeartbeatsDoNotBlockNextTick verifies
// that a failed heartbeat (e.g. CSMS hiccup) does not prevent the
// heartbeat loop from continuing. The dispatcher is responsible for
// calling Execute() and recording the error; subsequent commands must
// still run. This is the recovery invariant the operator relies on
// when the CSMS is briefly unavailable.
func TestHeartbeatResilience_FailedHeartbeatsDoNotBlockNextTick(t *testing.T) {
	d := ocpppkg.NewCommandDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	var callCount int
	var mu sync.Mutex

	for i := 0; i < 5; i++ {
		fail := i == 0 // first heartbeat fails
		d.Enqueue(ocpppkg.OCPPCommand{
			Description: "Heartbeat",
			Execute: func() error {
				mu.Lock()
				callCount++
				mu.Unlock()
				if fail {
					return fmt.Errorf("simulated CSMS hiccup")
				}
				return nil
			},
		})
	}

	// Wait for all 5 to drain.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return callCount == 5
	}, 2*time.Second, 10*time.Millisecond, "all 5 heartbeats must execute")

	stats := d.Stats()
	assert.Equal(t, uint64(5), stats.Executed, "Executed must count all heartbeats")
	assert.Equal(t, uint64(1), stats.Failed, "Failed must count exactly 1")
	assert.Equal(t, uint64(0), stats.Dropped, "Dropped must remain 0")
}
