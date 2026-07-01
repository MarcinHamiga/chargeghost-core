package main

import (
	"os"
	"path/filepath"
	"testing"

	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
