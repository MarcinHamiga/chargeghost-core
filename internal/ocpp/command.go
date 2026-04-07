package ocpp

import (
	"context"
	"log/slog"
)

// OCPPCommand is a single OCPP send operation to be executed sequentially.
type OCPPCommand struct {
	Description string
	Execute     func() error
}

// CommandDispatcher guarantees FIFO execution of OCPP commands.
// Engine callbacks enqueue via Enqueue (non-blocking); a single goroutine
// running Run drains the channel sequentially.
type CommandDispatcher struct {
	commands chan OCPPCommand
}

// NewCommandDispatcher creates a dispatcher with a 256-command buffer.
func NewCommandDispatcher() *CommandDispatcher {
	return &CommandDispatcher{
		commands: make(chan OCPPCommand, 256),
	}
}

// Run drains commands sequentially. Call in a dedicated goroutine.
func (d *CommandDispatcher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cmd := <-d.commands:
			if err := cmd.Execute(); err != nil {
				slog.Error("OCPP command failed", "description", cmd.Description, "error", err)
			}
		}
	}
}

// Enqueue adds a command to the channel without blocking.
// If the channel is full, the command is dropped with a warning log.
func (d *CommandDispatcher) Enqueue(cmd OCPPCommand) {
	select {
	case d.commands <- cmd:
	default:
		slog.Warn("OCPP command channel full, dropping", "description", cmd.Description)
	}
}
