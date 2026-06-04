package ocpp

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"
)

// OCPPCommand is a single OCPP send operation to be executed sequentially.
type OCPPCommand struct {
	Description string
	Execute     func() error
	// OnSuccess, when set, is invoked with the measured Execute() wall
	// clock duration. Senders that have a timeline correlation ID can
	// populate it via the closure to log a call_result with RTT.
	OnSuccess func(latency time.Duration)
	// CorrelationID, when set, is forwarded to the timeline logger so
	// the call/call_result events share a correlation key.
	CorrelationID string
	// OnDropped, when set, is invoked from Enqueue when the channel
	// is full and this command is dropped. Used by the dispatcher's
	// hub broadcast hook to surface the overflow to dashboards.
	OnDropped func(queueDepth, queueCap, droppedTotal int)
}

// CommandDispatcher guarantees FIFO execution of OCPP commands.
// Engine callbacks enqueue via Enqueue (non-blocking); a single goroutine
// running Run drains the channel sequentially.
type CommandDispatcher struct {
	commands chan OCPPCommand
	tracker  atomic.Pointer[StatusTracker]
	timeline atomic.Pointer[TimelineLogger]
	hub      atomic.Pointer[HubBroadcaster]

	// linkUp, when set, is consulted before executing a command. If it
	// returns false, the command is re-queued at the back and the
	// dispatcher sleeps briefly. This prevents a single down-link call
	// from holding the goroutine while the rest of the queue stalls.
	linkUp atomic.Pointer[func() bool]

	executed          atomic.Uint64
	failed            atomic.Uint64
	dropped           atomic.Uint64
	linkDownRequeues  atomic.Uint64
}

// DispatcherStats is a snapshot of the dispatcher's throughput counters
// and current channel occupancy. Returned by CommandDispatcher.Stats().
type DispatcherStats struct {
	Depth            int    `json:"depth"`
	Capacity         int    `json:"capacity"`
	Executed         uint64 `json:"executed"`
	Failed           uint64 `json:"failed"`
	Dropped          uint64 `json:"dropped"`
	LinkDownRequeues uint64 `json:"linkDownRequeues"`
}

// NewCommandDispatcher creates a dispatcher with a 256-command buffer.
func NewCommandDispatcher() *CommandDispatcher {
	return &CommandDispatcher{
		commands: make(chan OCPPCommand, 256),
	}
}

// SetLinkUpFunc installs a callback that the dispatcher consults before
// executing a command. When the callback returns false, the command is
// re-queued at the back of the channel and the dispatcher sleeps briefly
// to avoid head-of-line blocking. Pass nil to disable the check.
func (d *CommandDispatcher) SetLinkUpFunc(fn func() bool) {
	if fn == nil {
		d.linkUp.Store(nil)
		return
	}
	d.linkUp.Store(&fn)
}

// linkUpIsDown reports whether the link-up callback is set and the link
// is currently down.
func (d *CommandDispatcher) linkUpIsDown() bool {
	p := d.linkUp.Load()
	if p == nil {
		return false
	}
	return !(*p)()
}

// SetStatusTracker installs a tracker that will be notified of command
// execution errors. Pass nil to detach.
func (d *CommandDispatcher) SetStatusTracker(t *StatusTracker) {
	if t == nil {
		d.tracker.Store(nil)
		return
	}
	d.tracker.Store(t)
}

// SetTimelineLogger installs a timeline logger that will receive a structured
// entry for every command-execution failure. Pass nil to detach.
func (d *CommandDispatcher) SetTimelineLogger(tl *TimelineLogger) {
	if tl == nil {
		d.timeline.Store(nil)
		return
	}
	d.timeline.Store(tl)
}

// Stats returns a snapshot of the dispatcher's current occupancy and
// lifetime counters.
func (d *CommandDispatcher) Stats() DispatcherStats {
	return DispatcherStats{
		Depth:            len(d.commands),
		Capacity:         cap(d.commands),
		Executed:         d.executed.Load(),
		Failed:           d.failed.Load(),
		Dropped:          d.dropped.Load(),
		LinkDownRequeues: d.linkDownRequeues.Load(),
	}
}

// linkDownBackoff is how long the dispatcher sleeps when the link-up
// callback reports down before re-queuing the command.
const linkDownBackoff = 200 * time.Millisecond

// Run drains commands sequentially. Call in a dedicated goroutine.
func (d *CommandDispatcher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-d.commands:
			// Defensive link-up check. If the CSMS is down, requeue
			// at the back of the queue and briefly back off so we
			// don't tight-loop on a closed link.
			if d.linkUpIsDown() {
				d.linkDownRequeues.Add(1)
				select {
				case d.commands <- cmd:
				default:
					// Channel is full; treat as a drop with full
					// context so the operator can correlate.
					d.dropped.Add(1)
					depth := len(d.commands)
					capacity := cap(d.commands)
					total := d.dropped.Load()
					slog.Warn("OCPP command dropped while link down",
						"description", cmd.Description,
						"queueDepth", depth,
						"queueCap", capacity,
						"droppedTotal", total,
					)
					if h := d.hub.Load(); h != nil && *h != nil {
						(*h).BroadcastOCPPQueueOverflow(cmd.Description, depth, capacity, int(total))
					}
					if cmd.OnDropped != nil {
						cmd.OnDropped(depth, capacity, int(total))
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(linkDownBackoff):
				}
				continue
			}
			d.executed.Add(1)
			start := time.Now()
			err := cmd.Execute()
			latency := time.Since(start)
			if err != nil {
				slog.Error("OCPP command failed",
					"description", cmd.Description,
					"latencyMs", latency.Milliseconds(),
					"error", err,
				)
				d.failed.Add(1)
				if tl := d.timeline.Load(); tl != nil {
					tl.LogError(cmd.Description, "outbound", nil, err.Error(), nil, cmd.CorrelationID)
				}
				if t := d.tracker.Load(); t != nil {
					t.OnOutboundError(err)
				}
				continue
			}
			if cmd.OnSuccess != nil {
				cmd.OnSuccess(latency)
			}
		}
	}
}

// Enqueue adds a command to the channel without blocking.
// If the channel is full, the command is dropped with a warning log that
// includes the current queue depth, capacity, and lifetime drop count.
func (d *CommandDispatcher) Enqueue(cmd OCPPCommand) {
	select {
	case d.commands <- cmd:
	default:
		d.dropped.Add(1)
		depth := len(d.commands)
		capacity := cap(d.commands)
		total := d.dropped.Load()
		slog.Warn("OCPP command channel full, dropping",
			"description", cmd.Description,
			"queueDepth", depth,
			"queueCap", capacity,
			"droppedTotal", total,
		)
		if h := d.hub.Load(); h != nil && *h != nil {
			(*h).BroadcastOCPPQueueOverflow(cmd.Description, depth, capacity, int(total))
		}
		if cmd.OnDropped != nil {
			cmd.OnDropped(depth, capacity, int(total))
		}
	}
}
