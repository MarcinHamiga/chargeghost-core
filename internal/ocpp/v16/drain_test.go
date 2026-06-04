package v16

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/chargeghost/engine/internal/ocpp/queue"
)

func TestQueueDepth_NilQueueReturnsZero(t *testing.T) {
	b := &Bridge16{}
	assert.Equal(t, 0, b.QueueDepth())
}

func TestQueueDepth_NonEmptyQueueReturnsLength(t *testing.T) {
	b := &Bridge16{queue: queue.NewInMemoryQueue(3)}
	_, _ = b.queue.Enqueue(queue.QueuedMessage{Type: "StartTransaction", Payload: "x"})
	_, _ = b.queue.Enqueue(queue.QueuedMessage{Type: "StopTransaction", Payload: "y"})
	assert.Equal(t, 2, b.QueueDepth())
}

func TestDrainLoopInterval_DefaultIs30Seconds(t *testing.T) {
	b := &Bridge16{}
	assert.Equal(t, 30*time.Second, b.drainLoopInterval())
}

func TestStartDrainLoop_StopsOnContextCancel(t *testing.T) {
	b := &Bridge16{queue: queue.NewInMemoryQueue(3)}
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
