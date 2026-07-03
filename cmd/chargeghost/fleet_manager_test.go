package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chargeghost/engine/internal/api"
	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp/queue"
	"github.com/chargeghost/engine/internal/timeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFleetManager_CreateStation(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	cfg.OCPPID = "CP_1"
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	req := api.CreateStationRequest{
		ID:            "station-2",
		OCPPID:        "CP_2",
		ConnectionURL: "wss://example.com/CP_2",
		OCPPVersion:   "1.6",
		Enabled:       false,
		Save:          true,
	}
	snapshot, opID, err := fm.CreateStation(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "station-2", snapshot.StationID)
	assert.Equal(t, "CP_2", snapshot.OCPPID)
	assert.False(t, snapshot.Enabled)
	assert.NotEmpty(t, opID)

	loaded, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Len(t, loaded.Stations, 1)
	assert.Equal(t, "station-2", loaded.Stations[0].StationID())
}

func TestFleetManager_UpdateStation(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	cfg.OCPPID = "CP_1"
	id := "station-1"
	ocppID := "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	capacity := 75.0
	req := api.PatchStationConfigRequest{
		PatchConfigRequest: api.PatchConfigRequest{
			EVBatteryCapacity: &capacity,
		},
		Save: true,
	}
	result, err := fm.UpdateStation(context.Background(), "station-1", req)
	require.NoError(t, err)
	assert.NotEmpty(t, result.OperationID)
	assert.Equal(t, "station-1", result.Snapshot.StationID)
	assert.False(t, result.RestartRequired)
	assert.Contains(t, result.ChangedFields, "ev_battery_capacity")

	loaded, err := config.Load(cfgPath)
	require.NoError(t, err)
	require.Len(t, loaded.Stations, 1)
	assert.Equal(t, 75.0, *loaded.Stations[0].EVBatteryCapacity)
}

func TestFleetManager_UpdateStation_RestartRequired(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id := "station-1"
	ocppID := "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	url := "wss://example.com/CP_2"
	req := api.PatchStationConfigRequest{
		PatchConfigRequest: api.PatchConfigRequest{
			ConnectionURL: &url,
		},
		Save: true,
	}
	result, err := fm.UpdateStation(context.Background(), "station-1", req)
	require.NoError(t, err)
	assert.True(t, result.RestartRequired)
}

// TestFleetManager_UpdateStation_InvalidRollsBack is a regression test:
// UpdateStation used to mutate fm.cfg's live station entry directly and only
// validate afterward, so a rejected patch left an invalid config mutated in
// memory — a later, unrelated Save() call would then persist it. The patch
// must be applied to a clone and validated before anything is committed, and
// side effects (keyring writes) must not happen either.
func TestFleetManager_UpdateStation_InvalidRollsBack(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id1, ocppID1 := "station-1", "CP_1"
	id2, ocppID2 := "station-2", "CP_2"
	cfg.Stations = []config.StationConfig{
		{ID: &id1, OCPPID: &ocppID1},
		{ID: &id2, OCPPID: &ocppID2},
	}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	dupOCPPID := "CP_2" // collides with station-2
	pw := "should-not-be-stored"
	req := api.PatchStationConfigRequest{
		PatchConfigRequest: api.PatchConfigRequest{
			OCPPID:       &dupOCPPID,
			OCPPPassword: &pw,
		},
		Save: true,
	}
	_, err = fm.UpdateStation(context.Background(), "station-1", req)
	require.Error(t, err)

	afterCfg := fm.Config()
	require.Len(t, afterCfg.Stations, 2)
	assert.Equal(t, "CP_1", *afterCfg.Stations[0].OCPPID, "a rejected patch must not mutate the live config")

	loaded, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "CP_1", *loaded.Stations[0].OCPPID, "a rejected patch must not be persisted")

	t.Setenv("CHARGEGHOST_PASSWORD", "")
	assert.Error(t, fm.TestCredentials("station-1"), "the keyring write must not happen when validation fails")
}

// TestFleetManager_UpdateStation_RefreshesEffectiveConfig is a regression
// test for finding 8: EnableStation used to rebuild the runtime from a
// ManagedStation.Config cache that UpdateStation never refreshed, so
// patching a disabled station's connection URL and then enabling it
// connected to the OLD URL. ms.Config must be refreshed on every commit.
func TestFleetManager_UpdateStation_RefreshesEffectiveConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id, ocppID := "station-1", "CP_1"
	disabled := false
	oldURL := unroutablePort1URL
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID, Enabled: &disabled, ConnectionURL: &oldURL}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)
	require.NoError(t, fm.Start(context.Background()))
	defer fm.Shutdown(context.Background())

	newURL := "ws://127.0.0.1:2/CP_1"
	req := api.PatchStationConfigRequest{
		PatchConfigRequest: api.PatchConfigRequest{ConnectionURL: &newURL},
	}
	_, err = fm.UpdateStation(context.Background(), "station-1", req)
	require.NoError(t, err)

	_, err = fm.EnableStation(context.Background(), "station-1")
	require.NoError(t, err)

	fm.mu.RLock()
	ms := fm.stations["station-1"]
	fm.mu.RUnlock()
	require.NotNil(t, ms.Runtime)
	assert.Equal(t, newURL, ms.Runtime.Config.ConnectionURL, "enabling after a patch must use the new URL, not a stale cached one")
}

func TestFleetManager_DeleteStation(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id := "station-1"
	ocppID := "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	err = fm.DeleteStation(context.Background(), "station-1", api.DeleteStationOptions{AllowEmpty: true})
	require.NoError(t, err)

	loaded, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Empty(t, loaded.Stations)
}

// TestFleetManager_DeleteStation_StopsNonRunning is a regression test:
// DeleteStation used to stop the runtime only when its state was exactly
// StationRunning, so a Starting/Stopping/Failed runtime was deleted from the
// fleet's map with its goroutines (sim loop, dispatcher, persistence
// coordinator) still live and now unreachable — a goroutine leak. Any live
// (non-Stopped) runtime must be stopped, without requiring force=true (that
// gate exists only to protect a station that's actually serving traffic).
func TestFleetManager_DeleteStation_StopsNonRunning(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id, ocppID := "station-1", "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	cancelled := false
	sr := &StationRuntime{
		ID:             "station-1",
		Config:         config.DefaultConfig(),
		Engine:         engine.NewEngine(false, 0),
		Timeline:       timeline.NewStore(10),
		PersistDir:     t.TempDir(),
		lifecycleState: StationFailed, // not Running
		done:           make(chan struct{}),
	}
	sr.cancel = func() {
		cancelled = true
		close(sr.done)
	}

	fm.mu.Lock()
	fm.stations["station-1"] = &ManagedStation{Runtime: sr}
	fm.mu.Unlock()

	err = fm.DeleteStation(context.Background(), "station-1", api.DeleteStationOptions{AllowEmpty: true})
	require.NoError(t, err, "deleting a Failed (non-Running) station must not require force")
	assert.True(t, cancelled, "DeleteStation must stop a non-Running runtime, not only a Running one")
}

// TestFleetManager_DeleteStation_NewDefaultIDRespected is a regression test:
// after deleting the default station, ensureDefaultIDLocked used to always
// pick the first configured ID, clobbering an explicit NewDefaultID the
// caller had just set moments earlier.
func TestFleetManager_DeleteStation_NewDefaultIDRespected(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id1, ocppID1 := "station-1", "CP_1"
	id2, ocppID2 := "station-2", "CP_2"
	id3, ocppID3 := "station-3", "CP_3"
	cfg.Stations = []config.StationConfig{
		{ID: &id1, OCPPID: &ocppID1},
		{ID: &id2, OCPPID: &ocppID2},
		{ID: &id3, OCPPID: &ocppID3},
	}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)
	require.Equal(t, "station-1", fm.DefaultStationID())

	err = fm.DeleteStation(context.Background(), "station-1", api.DeleteStationOptions{NewDefaultID: "station-3"})
	require.NoError(t, err)

	assert.Equal(t, "station-3", fm.DefaultStationID(), "an explicit NewDefaultID must not be clobbered by the first-station fallback")
}

func TestFleetManager_Operations(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	cfg.OCPPID = "CP_1"
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	op := fm.ops.Start("test.op", "station-1")
	assert.Equal(t, "running", op.State)
	fm.ops.Succeed(op.ID)

	loaded, ok := fm.Operation(op.ID)
	require.True(t, ok)
	assert.Equal(t, "succeeded", loaded.State)

	ops := fm.Operations()
	assert.Len(t, ops, 1)
}

func TestFleetManager_SavePreservesStations(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id := "station-1"
	ocppID := "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	require.NoError(t, fm.Save())

	loaded, err := config.Load(cfgPath)
	require.NoError(t, err)
	assert.Len(t, loaded.Stations, 1)
	assert.Equal(t, "station-1", loaded.Stations[0].StationID())
}

func TestFleetManager_Snapshot(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id := "station-1"
	ocppID := "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	snapshot, ok := fm.Snapshot("station-1")
	require.True(t, ok)
	assert.Equal(t, "station-1", snapshot.StationID)
	assert.Equal(t, "CP_1", snapshot.OCPPID)
}

func TestFleetManager_Credentials(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id := "station-cred"
	ocppID := "CP_CRED_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	t.Setenv("CHARGEGHOST_PASSWORD", "")
	assert.Error(t, fm.TestCredentials("station-cred"))

	require.NoError(t, fm.SetOCPPPassword("station-cred", "secret"))
	assert.NoError(t, fm.TestCredentials("station-cred"))

	require.NoError(t, fm.ClearOCPPPassword("station-cred"))
	assert.Error(t, fm.TestCredentials("station-cred"))
}

// TestFleetManager_QueueDrainDoesNotDropMessages is a regression test:
// QueueDrain used to loop over the queue itself, dequeuing every
// StartTransaction/StopTransaction/MeterValues message WITHOUT ever sending
// it — silently discarding offline transactions the CSMS was waiting for.
// It must now delegate a single replay pass to the bridge
// (Bridge.DrainOfflineQueue) and leave the queue's actual contents to that
// call, not destroy them itself.
func TestFleetManager_QueueDrainDoesNotDropMessages(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id, ocppID := "station-1", "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	q := queue.NewInMemoryQueue(3)
	_, err = q.Enqueue(queue.QueuedMessage{Type: "StartTransaction", Payload: map[string]interface{}{}})
	require.NoError(t, err)
	_, err = q.Enqueue(queue.QueuedMessage{Type: "StopTransaction", Payload: map[string]interface{}{}})
	require.NoError(t, err)

	bridge := newTestBridge()
	bridge.connected = true

	sr := &StationRuntime{ID: "station-1", Bridge: bridge, Queue: q, lifecycleState: StationRunning}
	fm.mu.Lock()
	fm.stations["station-1"] = &ManagedStation{Runtime: sr}
	fm.mu.Unlock()

	opID, err := fm.QueueDrain("station-1")
	require.NoError(t, err)
	assert.NotEmpty(t, opID)

	require.Eventually(t, func() bool { return bridge.drainCalls.Load() > 0 }, time.Second, 5*time.Millisecond,
		"QueueDrain must delegate to Bridge.DrainOfflineQueue")
	assert.Equal(t, 2, q.Len(), "FleetManager must not itself dequeue messages — this stub's DrainOfflineQueue is a no-op")
}

func TestFleetManager_QueueStatusNotRunning(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id := "station-1"
	ocppID := "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	_, err = fm.QueueStatus("station-1")
	assert.Error(t, err)
}

func TestFleetManager_StartStopDisabled(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id := "station-1"
	ocppID := "CP_1"
	disabled := false
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID, Enabled: &disabled}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = fm.StartStation(ctx, "station-1")
	assert.Error(t, err)
}

// TestFleetManager_ShutdownPersistsStationState is a regression test for a
// shutdown data-loss bug: main.go used to cancel the same context it passed
// to FleetManager.Start before calling Shutdown, which let station
// goroutines exit via that ambient cancellation and get marked Stopped
// before Shutdown's own orderly Stop() call ever ran — Stop() then saw an
// already-Stopped runtime and skipped SaveAll entirely, so every graceful
// shutdown silently discarded in-memory engine state. Exercises the fixed,
// correct calling convention (fm.Start on a context nothing external
// cancels — see main.go) end to end and asserts the station's engine state
// actually lands on disk after Shutdown.
func TestFleetManager_ShutdownPersistsStationState(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id, ocppID := "station-1", "CP_1"
	url := unroutablePort1URL
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID, ConnectionURL: &url}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)
	require.NoError(t, fm.Start(context.Background()))

	fm.mu.RLock()
	runtime := fm.stations["station-1"].Runtime
	fm.mu.RUnlock()
	require.NotNil(t, runtime)
	require.Eventually(t, func() bool {
		return runtime.LifecycleState() == StationRunning
	}, 2*time.Second, 5*time.Millisecond)
	runtime.Engine.AddConnector(400, 32, 3)

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	require.NoError(t, fm.Shutdown(shutCtx))

	persistDir := runtime.PersistDir
	data, err := os.ReadFile(filepath.Join(persistDir, "engine.json"))
	require.NoError(t, err, "Shutdown must have persisted engine state via Stop's SaveAll call")
	assert.Contains(t, string(data), `"phase": 3`)
}

// TestFleetManager_ReplaceRuntimeStopsOld is a regression test: restarting a
// station used to build and assign a fresh runtime without ever stopping the
// previous one, leaving its simulation loop, dispatcher, and persistence
// coordinator running and writing to the same persist directory as the new
// runtime. RestartStation must now fully stop the old runtime before a
// replacement is built.
func TestFleetManager_ReplaceRuntimeStopsOld(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id, ocppID := "station-1", "CP_1"
	url := unroutablePort1URL
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID, ConnectionURL: &url}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)
	require.NoError(t, fm.Start(context.Background()))
	defer fm.Shutdown(context.Background())

	fm.mu.RLock()
	oldMS := fm.stations["station-1"]
	fm.mu.RUnlock()
	require.NotNil(t, oldMS)
	oldRuntime := oldMS.Runtime
	require.NotNil(t, oldRuntime)
	require.Eventually(t, func() bool {
		return oldRuntime.LifecycleState() == StationRunning
	}, 2*time.Second, 5*time.Millisecond)

	opID, err := fm.RestartStation(context.Background(), "station-1")
	require.NoError(t, err)
	assert.NotEmpty(t, opID)

	select {
	case <-oldRuntime.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("old runtime did not stop")
	}
	assert.Equal(t, StationStopped, oldRuntime.LifecycleState())

	fm.mu.RLock()
	newRuntime := fm.stations["station-1"].Runtime
	fm.mu.RUnlock()
	require.NotNil(t, newRuntime)
	assert.NotSame(t, oldRuntime, newRuntime, "restart must build a fresh runtime, not reuse the old one")
}

// TestFleetManager_ReplaceRuntimeRefusesOnStopTimeout is a regression test
// for the corollary of the above: if the old runtime's Stop times out,
// replaceRuntime must not build a second runtime for the same station (that
// would leave two persistence coordinators writing the same directory).
//
// This installs a synthetic "stuck" runtime directly (cancel is a no-op, done
// never closes) rather than one built via fm.Start(), because every real
// goroutine in this codebase reacts to cancellation within microseconds —
// racing a real runtime's shutdown against an already-expired context is not
// reliably observable from outside (see TestStationRuntime_Stop_TimeoutLeavesStopping).
func TestFleetManager_ReplaceRuntimeRefusesOnStopTimeout(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id, ocppID := "station-1", "CP_1"
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)

	stuck := &StationRuntime{
		ID:             "station-1",
		Config:         config.DefaultConfig(),
		Engine:         engine.NewEngine(false, 0),
		Timeline:       timeline.NewStore(10),
		PersistDir:     t.TempDir(),
		lifecycleState: StationRunning,
		done:           make(chan struct{}),
		cancel:         func() {},
	}
	fm.mu.Lock()
	fm.stations["station-1"] = &ManagedStation{
		Runtime: stuck,
		Config:  &config.EffectiveStation{ID: "station-1", Enabled: true, Config: cfg},
	}
	fm.mu.Unlock()

	shortCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = fm.RestartStation(shortCtx, "station-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStopTimeout)

	fm.mu.RLock()
	sameRuntime := fm.stations["station-1"].Runtime
	fm.mu.RUnlock()
	assert.Same(t, stuck, sameRuntime, "no replacement runtime should be built while the old one is still stopping")
}

// TestFleetManager_SnapshotsSurviveBuildFailure is a regression test: a
// station whose runtime failed to build used to be recorded as a
// StationRuntime{Config: nil, Engine: nil} placeholder, and both Snapshot and
// AllSnapshots dereferenced those nil fields — a single misconfigured station
// turned GET /stations and /fleet/status into a panic for the whole fleet.
func TestFleetManager_SnapshotsSurviveBuildFailure(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	idBad, ocppIDBad := "station-bad", "CP_BAD"
	badVersion := "9.9.9"
	idGood, ocppIDGood := "station-good", "CP_GOOD"
	goodURL := unroutablePort1URL
	cfg.Stations = []config.StationConfig{
		{ID: &idBad, OCPPID: &ocppIDBad, OCPPVersion: &badVersion},
		{ID: &idGood, OCPPID: &ocppIDGood, ConnectionURL: &goodURL},
	}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)
	require.NoError(t, fm.Start(context.Background()))
	defer fm.Shutdown(context.Background())

	var snapshots []api.StationSnapshot
	require.NotPanics(t, func() {
		snapshots = fm.AllSnapshots()
	})
	assert.Len(t, snapshots, 2)

	badSnap, ok := fm.Snapshot("station-bad")
	require.True(t, ok)
	assert.Equal(t, string(StationFailed), badSnap.LifecycleState)
	assert.NotEmpty(t, badSnap.LastError)

	goodSnap, ok := fm.Snapshot("station-good")
	require.True(t, ok)
	assert.NotEqual(t, string(StationFailed), goodSnap.LifecycleState)
}

// TestFleetManager_StartStationOutlivesRequestCtx is a regression test: REST
// handlers pass a short-lived, per-request context into lifecycle methods;
// StartStation used to start the runtime on that same context, so the
// station's goroutines were killed the moment the HTTP handler returned.
// FleetManager must start runtimes on its own long-lived context instead.
func TestFleetManager_StartStationOutlivesRequestCtx(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	id, ocppID := "station-1", "CP_1"
	url := unroutablePort1URL
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID, ConnectionURL: &url}}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)
	require.NoError(t, fm.Start(context.Background()))
	defer fm.Shutdown(context.Background())

	_, err = fm.StopStation(context.Background(), "station-1")
	require.NoError(t, err)

	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err = fm.StartStation(shortCtx, "station-1")
	cancel()
	require.NoError(t, err)

	// The request context above is long expired by now; the runtime must
	// still be running because it was started on FleetManager's own runCtx.
	time.Sleep(250 * time.Millisecond)
	snap, ok := fm.Snapshot("station-1")
	require.True(t, ok)
	assert.Equal(t, string(StationRunning), snap.LifecycleState)
}
