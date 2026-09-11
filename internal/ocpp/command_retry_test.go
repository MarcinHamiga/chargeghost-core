package ocpp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCommandDispatcher_EnqueueWithRetry_RetriesAfterDrain verifies a
// command dropped with ErrQueueFull is re-enqueued after the retry delay
// and executes once the dispatcher drains.
func TestCommandDispatcher_EnqueueWithRetry_RetriesAfterDrain(t *testing.T) {
	oldDelay := queueFullRetryDelay
	queueFullRetryDelay = 50 * time.Millisecond
	defer func() { queueFullRetryDelay = oldDelay }()

	d := NewCommandDispatcher()
	noop := OCPPCommand{Description: "fill", Execute: func() error { return nil }}
	for i := 0; i < 256; i++ {
		require.NoError(t, d.Enqueue(noop))
	}

	var executed atomic.Bool
	d.EnqueueWithRetry(OCPPCommand{
		Description: "retried",
		Execute: func() error {
			executed.Store(true)
			return nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)
	require.Eventually(t, executed.Load, 2*time.Second, 10*time.Millisecond,
		"retried command must execute after drain")
}
