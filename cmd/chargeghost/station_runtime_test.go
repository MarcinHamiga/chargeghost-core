package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/timeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unroutablePort1URL is a WebSocket URL that fails to connect immediately
// (connection refused) rather than hanging on a DNS lookup or TLS handshake,
// keeping tests that actually call Start() fast and deterministic.
const unroutablePort1URL = "ws://127.0.0.1:1/CP_1"

func TestBuildStationRuntime_IsolatesEnginesAndQueues(t *testing.T) {
	hub := ws.NewHub()
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	cfg1 := config.DefaultConfig()
	cfg1.OCPPID = "CP_1"
	cfg1.ConnectionURL = "wss://example.com/CP_1"

	cfg2 := config.DefaultConfig()
	cfg2.OCPPID = "CP_2"
	cfg2.ConnectionURL = "wss://example.com/CP_2"

	sr1, err := buildStationRuntime("CP_1", cfg1, hub, dir1, dir1)
	require.NoError(t, err)
	sr2, err := buildStationRuntime("CP_2", cfg2, hub, dir2, dir2)
	require.NoError(t, err)

	assert.NotEqual(t, sr1.Engine, sr2.Engine)
	assert.NotEqual(t, sr1.Dispatcher, sr2.Dispatcher)
	assert.Equal(t, sr1.Hub, sr2.Hub)
	assert.Equal(t, "CP_1", sr1.ID)
	assert.Equal(t, "CP_2", sr2.ID)
	assert.Len(t, sr1.Engine.GetConnectorIDs(), 1)
	assert.Len(t, sr2.Engine.GetConnectorIDs(), 1)

	// Modifying one engine should not affect the other.
	sr1.Engine.AddConnector(400, 32, 3)
	assert.Len(t, sr1.Engine.GetConnectorIDs(), 2)
	assert.Len(t, sr2.Engine.GetConnectorIDs(), 1)
}

func TestBuildStationRuntime_StationScopedPersistDir(t *testing.T) {
	hub := ws.NewHub()
	baseDir := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.OCPPID = "CP_1"
	persistDir := config.StationPersistDir(baseDir, cfg.OCPPID)
	queueDir := persistDir

	sr, err := buildStationRuntime("CP_1", cfg, hub, persistDir, queueDir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(baseDir, "stations", config.StationSafeKey("CP_1")), sr.PersistDir)

	require.NoError(t, sr.Engine.SaveState(sr.PersistDir))
	_, err = os.Stat(filepath.Join(persistDir, "engine.json"))
	assert.NoError(t, err, "engine state should be saved to station dir")
}

func TestBuildStationRuntime_LegacyPersistDir(t *testing.T) {
	hub := ws.NewHub()
	baseDir := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.OCPPID = "CP_1"
	persistDir := baseDir
	queueDir := baseDir

	sr, err := buildStationRuntime("CP_1", cfg, hub, persistDir, queueDir)
	require.NoError(t, err)
	assert.Equal(t, baseDir, sr.PersistDir)

	require.NoError(t, sr.Engine.SaveState(sr.PersistDir))
	_, err = os.Stat(filepath.Join(persistDir, "engine.json"))
	assert.NoError(t, err, "engine state should be saved to legacy dir")
}

// TestStationRuntime_Snapshot_AlwaysEnabled is a regression test: Snapshot
// never set the Enabled field, so any running station reported enabled:false
// regardless of its actual config — a StationRuntime only ever exists for a
// station whose config said to run it, so Enabled must always be true here.
func TestStationRuntime_Snapshot_AlwaysEnabled(t *testing.T) {
	hub := ws.NewHub()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.OCPPID = "CP_1"
	cfg.ConnectionURL = unroutablePort1URL

	sr, err := buildStationRuntime("CP_1", cfg, hub, dir, dir)
	require.NoError(t, err)
	assert.True(t, sr.Snapshot().Enabled, "a freshly built (not-yet-started) runtime is still for an enabled station")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sr.Start(ctx))
	require.Eventually(t, func() bool {
		return sr.LifecycleState() == StationRunning
	}, 2*time.Second, 5*time.Millisecond)
	assert.True(t, sr.Snapshot().Enabled)
}

// TestStationRuntime_SingleUse is a regression test: a StationRuntime used to
// silently reset its WaitGroup and re-run Start on an already-running (or
// already-stopped) instance, which could race a still-draining shutdown from
// a prior Stop call. Start must now refuse a second call outright — callers
// build a fresh StationRuntime to restart a station instead.
// TestStationRuntime_Stop_SavesStateBeforeDraining is a regression test for
// a shutdown data-loss bug: main.go used to pass the same context to
// FleetManager.Start that its own shutdown signal handler cancelled BEFORE
// calling FleetManager.Shutdown. Since a StationRuntime's ctx is derived
// from that root, the ambient cancellation let every station goroutine exit
// on its own — and the supervisor goroutine that watches for that (see
// superviseShutdown) raced ahead and marked the runtime StationStopped
// before Stop() was ever invoked. When Shutdown's orderly Stop() call
// finally ran, it saw StationStopped and short-circuited as a no-op,
// silently skipping SaveAll — every graceful shutdown lost the station's
// in-memory state (meter readings, session history, plug status). Fixed by
// rooting FleetManager.runCtx in a context nothing external ever cancels
// (see main.go), so sr.ctx can only be cancelled via Stop() itself. This
// test locks in the resulting invariant: Stop() must persist state via
// SaveAll before a station's goroutines are allowed to fully drain, for a
// runtime whose context is exclusively controlled by Stop (as it always is
// in production).
func TestStationRuntime_Stop_SavesStateBeforeDraining(t *testing.T) {
	hub := ws.NewHub()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.OCPPID = "CP_1"
	cfg.ConnectionURL = unroutablePort1URL

	sr, err := buildStationRuntime("CP_1", cfg, hub, dir, dir)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, sr.Start(ctx))
	require.Eventually(t, func() bool {
		return sr.LifecycleState() == StationRunning
	}, 2*time.Second, 5*time.Millisecond)

	sr.Engine.AddConnector(400, 32, 3)

	require.NoError(t, sr.Stop(context.Background()))
	assert.Equal(t, StationStopped, sr.LifecycleState())

	data, err := os.ReadFile(filepath.Join(dir, "engine.json"))
	require.NoError(t, err, "Stop must have persisted engine state via SaveAll")
	assert.Contains(t, string(data), `"phase": 3`, "the connector added before Stop must be in the saved snapshot")
}

func TestStationRuntime_SingleUse(t *testing.T) {
	hub := ws.NewHub()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.OCPPID = "CP_1"
	cfg.ConnectionURL = unroutablePort1URL

	sr, err := buildStationRuntime("CP_1", cfg, hub, dir, dir)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, sr.Start(ctx))
	err = sr.Start(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyStarted)

	cancel()
	select {
	case <-sr.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not stop after ctx cancel")
	}
	assert.Equal(t, StationStopped, sr.LifecycleState())

	// Even after a clean stop, Start must still refuse — a StationRuntime is
	// single-use for its whole lifetime, not just while running.
	err = sr.Start(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyStarted)
}

// TestStationRuntime_Stop_TimeoutLeavesStopping is a regression test: Stop
// used to unconditionally force the lifecycle state to StationStopped even
// when its context expired before the runtime's goroutines actually drained,
// letting a caller believe it was safe to build a replacement runtime while
// the old one's persistence coordinator was still writing to disk. Stop must
// now report the timeout and leave the runtime in StationStopping instead.
//
// This drives Stop() directly against a synthetic "still running" runtime
// (rather than one built via Start()) because every real goroutine in this
// codebase reacts to context cancellation within microseconds — a genuine
// Start()'d runtime drains too fast to reliably observe an in-flight
// StationStopping window from outside. Convergence to StationStopped once a
// real runtime actually finishes draining is covered by
// TestStationRuntime_SingleUse.
func TestStationRuntime_Stop_TimeoutLeavesStopping(t *testing.T) {
	sr := &StationRuntime{
		ID:             "CP_1",
		Config:         config.DefaultConfig(),
		Engine:         engine.NewEngine(false, 0),
		Timeline:       timeline.NewStore(10),
		PersistDir:     t.TempDir(),
		lifecycleState: StationRunning,
		done:           make(chan struct{}),
		cancel:         func() {}, // nothing real to cancel in this synthetic setup
	}

	shortCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := sr.Stop(shortCtx)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStopTimeout)
	assert.Equal(t, StationStopping, sr.LifecycleState(), "a timed-out Stop must not force the state to Stopped")
}
