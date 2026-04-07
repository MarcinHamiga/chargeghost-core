package runtime

import (
	"context"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
)

const (
	tickInterval = 50 * time.Millisecond // 20 Hz wake-up
	stepInterval = 0.1                   // 100 ms simulation step in seconds
	maxSteps     = 5                     // spiral-of-death guard
)

// Runtime drives the fixed-timestep simulation loop.
type Runtime struct {
	engine      *engine.Engine
	lastTick    time.Time
	accumulator float64
}

// NewRuntime creates a Runtime that will drive the given engine.
func NewRuntime(e *engine.Engine) *Runtime {
	return &Runtime{
		engine:   e,
		lastTick: time.Now(),
	}
}

// Run blocks, calling engine.Simulate on a fixed timestep until ctx is cancelled.
// Call in a dedicated goroutine.
func (r *Runtime) Run(ctx context.Context) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	r.lastTick = time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			delta := now.Sub(r.lastTick).Seconds()
			r.lastTick = now
			r.accumulator += delta

			steps := 0
			for r.accumulator >= stepInterval && steps < maxSteps {
				r.engine.Simulate(stepInterval)
				r.accumulator -= stepInterval
				steps++
			}
		}
	}
}
