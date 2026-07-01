package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/chargeghost/engine/internal/api"
	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
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
	snapshot, opID, err := fm.UpdateStation(context.Background(), "station-1", req)
	require.NoError(t, err)
	assert.NotEmpty(t, opID)
	assert.Equal(t, "station-1", snapshot.StationID)
	assert.False(t, snapshot.RestartRequired)

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
	snapshot, _, err := fm.UpdateStation(context.Background(), "station-1", req)
	require.NoError(t, err)
	assert.True(t, snapshot.RestartRequired)
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

func TestFleetManager_SnapshotAndRegistry(t *testing.T) {
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

	reg := fm.Registry()
	assert.Equal(t, "station-1", reg.DefaultID)
	assert.Len(t, reg.Stations, 0)
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
