package persistence

import (
	"context"
	"log/slog"
	"time"
)

// Coordinator periodically saves a set of Persistable targets to disk.
type Coordinator struct {
	dir      string
	interval time.Duration
	targets  []Persistable
}

// NewCoordinator creates a Coordinator that saves targets every interval.
func NewCoordinator(dir string, interval time.Duration, targets ...Persistable) *Coordinator {
	return &Coordinator{
		dir:      dir,
		interval: interval,
		targets:  targets,
	}
}

// Run saves all targets on a periodic timer until ctx is cancelled.
func (c *Coordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.saveAll()
		}
	}
}

// SaveAll performs a synchronous save of all targets. Call on shutdown.
func (c *Coordinator) SaveAll() {
	c.saveAll()
}

func (c *Coordinator) saveAll() {
	for _, t := range c.targets {
		if err := t.SaveState(c.dir); err != nil {
			slog.Error("periodic save failed", "err", err)
		}
	}
}
