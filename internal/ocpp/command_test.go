package ocpp_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
// link-up callback reports down, the dispatcher re-queues the command at
// the back of the channel instead of executing it. This is the contract
// that prevents a single down-link send from head-of-line blocking the
// rest of the queue.
func TestCommandDispatcher_LinkDownRequeuesCommand(t *testing.T) {
	d := ocpp.NewCommandDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	linkUp := false
	d.SetLinkUpFunc(func() bool { return linkUp })

	go d.Run(ctx)

	executed := false
	d.Enqueue(ocpp.OCPPCommand{
		Description: "stays-pending",
		Execute: func() error {
			executed = true
			return nil
		},
	})

	// Give the dispatcher a moment to dequeue, see link is down, requeue,
	// and sleep. The command must not have executed.
	time.Sleep(500 * time.Millisecond)
	assert.False(t, executed, "command must not execute while link is down")
	s := d.Stats()
	assert.Equal(t, uint64(0), s.Executed, "Executed counter must remain 0")
	assert.Greater(t, s.LinkDownRequeues, uint64(0), "LinkDownRequeues must increment")

	// Now flip the link up — the command should drain.
	linkUp = true
	require.Eventually(t, func() bool { return d.Stats().Executed == 1 },
		2*time.Second, 10*time.Millisecond, "command to execute after link up")
	assert.True(t, executed)
}

// TestCommandDispatcher_LinkUpFuncNilDisablesCheck verifies the default
// behavior (no link-up check) is preserved when SetLinkUpFunc(nil) is
// called or never called at all.
func TestCommandDispatcher_LinkUpFuncNilDisablesCheck(t *testing.T) {
	d := ocpp.NewCommandDispatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	executed := false
	d.Enqueue(ocpp.OCPPCommand{
		Description: "ok",
		Execute: func() error {
			executed = true
			return nil
		},
	})
	require.Eventually(t, func() bool { return executed },
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
