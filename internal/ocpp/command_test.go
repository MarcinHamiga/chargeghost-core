package ocpp_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/timeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandDispatcher_ExecutesInOrder(t *testing.T) {
	d := ocpp.NewCommandDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	var results []int
	var mu sync.Mutex

	for i := 0; i < 5; i++ {
		n := i
		d.Enqueue(ocpp.OCPPCommand{
			Description: fmt.Sprintf("cmd %d", n),
			Execute: func() error {
				mu.Lock()
				results = append(results, n)
				mu.Unlock()
				return nil
			},
		})
	}

	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	assert.Equal(t, []int{0, 1, 2, 3, 4}, results)
	mu.Unlock()
}

func TestCommandDispatcher_NonBlockingEnqueue(t *testing.T) {
	d := ocpp.NewCommandDispatcher()
	// Don't start Run — channel fills up.
	// Enqueue should not block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 300; i++ {
			d.Enqueue(ocpp.OCPPCommand{
				Description: "overflow",
				Execute:     func() error { return nil },
			})
		}
		close(done)
	}()

	select {
	case <-done:
		// good — did not block
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Enqueue blocked when channel was full")
	}
}

// TestCommandDispatcher_StatsTrackExecutedAndFailed verifies the
// dispatcher increments Executed for every command and Failed for every
// error, regardless of whether a status tracker or timeline logger is set.
func TestCommandDispatcher_StatsTrackExecutedAndFailed(t *testing.T) {
	d := ocpp.NewCommandDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	for i := 0; i < 3; i++ {
		d.Enqueue(ocpp.OCPPCommand{
			Description: "ok",
			Execute:     func() error { return nil },
		})
	}
	for i := 0; i < 2; i++ {
		d.Enqueue(ocpp.OCPPCommand{
			Description: "fail",
			Execute:     func() error { return errors.New("boom") },
		})
	}

	// Wait for all commands to drain.
	require.Eventually(t, func() bool {
		s := d.Stats()
		return s.Executed == 5 && s.Failed == 2
	}, 2*time.Second, 10*time.Millisecond, "Executed and Failed counts")

	s := d.Stats()
	assert.Equal(t, uint64(5), s.Executed)
	assert.Equal(t, uint64(2), s.Failed)
	assert.Equal(t, uint64(0), s.Dropped)
	assert.Equal(t, 0, s.Depth)
	assert.Equal(t, 256, s.Capacity)
}

// TestCommandDispatcher_StatsCountDrops verifies the drop counter
// increments when the channel is full and Enqueue falls through.
func TestCommandDispatcher_StatsCountDrops(t *testing.T) {
	d := ocpp.NewCommandDispatcher()
	// Don't start Run — channel fills up at capacity 256.
	for i := 0; i < 300; i++ {
		d.Enqueue(ocpp.OCPPCommand{
			Description: "overflow",
			Execute:     func() error { return nil },
		})
	}
	s := d.Stats()
	assert.Equal(t, 256, s.Depth)
	assert.Equal(t, 256, s.Capacity)
	assert.Equal(t, uint64(44), s.Dropped)
}

// TestCommandDispatcher_LinkDownRequeuesCommand verifies that when the
// link-up callback reports down, the dispatcher holds the dequeued command
// (pausing the drain) instead of executing it, preserving FIFO order. The
// old requeue-to-back behavior reordered Stop-behind-Start sequences and
// broke the transaction retry cascade.
func TestCommandDispatcher_LinkDownRequeuesCommand(t *testing.T) {
	d := ocpp.NewCommandDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var linkUp atomic.Bool
	d.SetLinkUpFunc(linkUp.Load)

	go d.Run(ctx)

	var executed atomic.Bool
	d.Enqueue(ocpp.OCPPCommand{
		Description: "stays-pending",
		Execute: func() error {
			executed.Store(true)
			return nil
		},
	})

	// Give the dispatcher a moment to dequeue, see link is down, requeue,
	// and sleep. The command must not have executed.
	time.Sleep(500 * time.Millisecond)
	assert.False(t, executed.Load(), "command must not execute while link is down")
	s := d.Stats()
	assert.Equal(t, uint64(0), s.Executed, "Executed counter must remain 0")
	assert.Greater(t, s.LinkDownRequeues, uint64(0), "LinkDownRequeues must increment")

	// Now flip the link up — the command should drain.
	linkUp.Store(true)
	require.Eventually(t, func() bool { return d.Stats().Executed == 1 },
		2*time.Second, 10*time.Millisecond, "command to execute after link up")
	assert.True(t, executed.Load())
}

// TestCommandDispatcher_EnqueueReturnsErrQueueFull verifies backpressure:
// a full channel reports ErrQueueFull (and counts the drop) instead of
// silently swallowing the command.
func TestCommandDispatcher_EnqueueReturnsErrQueueFull(t *testing.T) {
	d := ocpp.NewCommandDispatcher()
	// Don't start Run — channel fills up at capacity 256.
	for i := 0; i < 256; i++ {
		require.NoError(t, d.Enqueue(ocpp.OCPPCommand{
			Description: "fill",
			Execute:     func() error { return nil },
		}))
	}
	err := d.Enqueue(ocpp.OCPPCommand{
		Description: "overflow",
		Execute:     func() error { return nil },
	})
	require.ErrorIs(t, err, ocpp.ErrQueueFull)
	assert.Equal(t, uint64(1), d.Stats().Dropped)
}

// TestCommandDispatcher_LinkDownPreservesFIFO verifies that commands queued
// while the link is down execute in original order once it recovers —
// Start before Stop — with no drops.
func TestCommandDispatcher_LinkDownPreservesFIFO(t *testing.T) {
	d := ocpp.NewCommandDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var linkUp atomic.Bool // down
	d.SetLinkUpFunc(linkUp.Load)
	go d.Run(ctx)

	var mu sync.Mutex
	var order []string
	for _, name := range []string{"start", "status", "stop"} {
		n := name
		require.NoError(t, d.Enqueue(ocpp.OCPPCommand{
			Description: n,
			Execute: func() error {
				mu.Lock()
				order = append(order, n)
				mu.Unlock()
				return nil
			},
		}))
	}

	// While down, nothing executes but nothing is dropped either.
	time.Sleep(400 * time.Millisecond)
	mu.Lock()
	assert.Empty(t, order)
	mu.Unlock()
	assert.Equal(t, uint64(0), d.Stats().Dropped)

	linkUp.Store(true)
	require.Eventually(t, func() bool { return d.Stats().Executed == 3 },
		2*time.Second, 10*time.Millisecond, "queued commands to drain in order")
	mu.Lock()
	assert.Equal(t, []string{"start", "status", "stop"}, order)
	mu.Unlock()
	assert.Equal(t, uint64(0), d.Stats().Dropped)
}

// TestCommandDispatcher_LinkUpFuncNilDisablesCheck verifies the default
// behavior (no link-up check) is preserved when SetLinkUpFunc(nil) is
// called or never called at all.
func TestCommandDispatcher_LinkUpFuncNilDisablesCheck(t *testing.T) {
	d := ocpp.NewCommandDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	var executed atomic.Bool
	d.Enqueue(ocpp.OCPPCommand{
		Description: "ok",
		Execute: func() error {
			executed.Store(true)
			return nil
		},
	})
	require.Eventually(t, executed.Load,
		1*time.Second, 5*time.Millisecond, "command to execute with no link check")
}

// TestCommandDispatcher_ExecutionFailureWritesToTimeline verifies that
// when a command fails and a timeline logger is attached, an error entry
// is appended to the timeline store.
func TestCommandDispatcher_ExecutionFailureWritesToTimeline(t *testing.T) {
	store := timeline.NewStore(100)
	tl := ocpp.NewTimelineLogger(store)
	d := ocpp.NewCommandDispatcher()
	d.SetTimelineLogger(tl)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	d.Enqueue(ocpp.OCPPCommand{
		Description: "FailingAction",
		Execute:     func() error { return errors.New("kaboom") },
	})

	require.Eventually(t, func() bool { return d.Stats().Failed == 1 },
		2*time.Second, 10*time.Millisecond, "Failed counter to increment")

	require.Eventually(t, func() bool { return store.Count() >= 1 },
		2*time.Second, 10*time.Millisecond, "timeline store to receive the error")

	events, _ := store.Query(timeline.TimelineFilter{Action: "FailingAction", Limit: 10})
	require.NotEmpty(t, events)
	for _, ev := range events {
		assert.Equal(t, "call_error", ev.EventType)
		assert.Equal(t, "outbound", ev.Direction)
		assert.Equal(t, "error", ev.Level)
		assert.Contains(t, ev.Summary, "kaboom")
	}
}
