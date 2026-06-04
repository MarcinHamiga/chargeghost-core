package ocpp

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// helper: capture slog output into a string builder
func captureSlog(t *testing.T) (*strings.Builder, func()) {
	t.Helper()
	var buf strings.Builder
	prev := slog.Default()
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(handler))
	restore := func() { slog.SetDefault(prev) }
	return &buf, restore
}

func TestStartHealthTicker_EmitsStructuredHealthLine(t *testing.T) {
	buf, restore := captureSlog(t)
	defer restore()

	tr := NewStatusTracker("wss://csms.example.com/ocpp", "CP_1", "2.0.1")
	tr.OnConnect()
	tr.SetQueueDepth(7)
	tr.SetQueueExhausted(2)
	tr.SetDrainInProgress(true)
	tr.OnHeartbeat(123*time.Millisecond, nil)
	tr.OnOutboundError(nil) // not exercised but should not panic

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var called atomic.Int32
	done := make(chan struct{})
	go func() {
		// Run for ~250ms with 50ms interval; expect ≥ 1 line.
		StartHealthTicker(ctx, tr, 50*time.Millisecond)
		called.Store(1)
		close(done)
	}()

	// Wait a bit for ≥ 1 tick to fire.
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	out := buf.String()
	assert.Contains(t, out, "ocpp health", "expected health log line")
	assert.Contains(t, out, "connected=true", "expected connected=true attribute")
	assert.Contains(t, out, "queueDepth=7", "expected queueDepth=7 attribute")
	assert.Contains(t, out, "queueExhausted=2", "expected queueExhausted=2 attribute")
	assert.Contains(t, out, "drainInProgress=true", "expected drainInProgress=true attribute")
	assert.Contains(t, out, "lastHeartbeatRttMs=123", "expected lastHeartbeatRttMs=123 attribute")
}

func TestStartHealthTicker_EmitsWarnLevelWhenDisconnected(t *testing.T) {
	buf, restore := captureSlog(t)
	defer restore()

	tr := NewStatusTracker("wss://csms.example.com/ocpp", "CP_1", "1.6")
	// Do not call OnConnect — leave it disconnected.

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		StartHealthTicker(ctx, tr, 30*time.Millisecond)
		close(done)
	}()

	time.Sleep(120 * time.Millisecond)
	cancel()
	<-done

	out := buf.String()
	assert.Contains(t, out, "level=WARN", "expected WARN log when disconnected")
	assert.Contains(t, out, "connected=false", "expected connected=false attribute")
}

func TestStartHealthTicker_NilTrackerIsNoOp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		StartHealthTicker(ctx, nil, 10*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
		// Returned immediately — good.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected nil tracker to return immediately")
	}
}

func TestStartHealthTicker_NonPositiveIntervalIsNoOp(t *testing.T) {
	tr := NewStatusTracker("wss://csms.example.com/ocpp", "CP_1", "1.6")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		StartHealthTicker(ctx, tr, 0)
		close(done)
	}()
	select {
	case <-done:
		// Returned immediately — good.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected 0 interval to return immediately")
	}
}
