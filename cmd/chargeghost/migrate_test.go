package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}

func TestMigrateLegacySingleStationState_MovesFiles(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "engine", "engine.json"), `{"connectors":[]}`)
	writeFile(t, filepath.Join(baseDir, "message_queue.json"), `[]`)
	writeFile(t, filepath.Join(baseDir, "message_dead_letter.jsonl"), `{}`)

	stationDir := config.StationPersistDirByID(baseDir, "CP_A")

	migrated, err := migrateLegacySingleStationState(baseDir, stationDir)
	require.NoError(t, err)
	assert.True(t, migrated)

	assert.FileExists(t, filepath.Join(stationDir, "engine.json"))
	assert.FileExists(t, filepath.Join(stationDir, "message_queue.json"))
	assert.FileExists(t, filepath.Join(stationDir, "message_dead_letter.jsonl"))

	assert.NoDirExists(t, filepath.Join(baseDir, "engine"))
	assert.NoFileExists(t, filepath.Join(baseDir, "message_queue.json"))
	assert.NoFileExists(t, filepath.Join(baseDir, "message_dead_letter.jsonl"))
}

func TestMigrateLegacySingleStationState_SecondRunIsNoOp(t *testing.T) {
	baseDir := t.TempDir()
	writeFile(t, filepath.Join(baseDir, "engine", "engine.json"), `{"connectors":[]}`)
	stationDir := config.StationPersistDirByID(baseDir, "CP_A")

	migrated, err := migrateLegacySingleStationState(baseDir, stationDir)
	require.NoError(t, err)
	require.True(t, migrated)

	// A stray legacy dir reappearing (e.g. a different, unrelated run)
	// must not be moved again once stationDir already exists — that's the
	// "already migrated" marker.
	writeFile(t, filepath.Join(baseDir, "engine", "engine.json"), `{"connectors":["should not move"]}`)
	migrated, err = migrateLegacySingleStationState(baseDir, stationDir)
	require.NoError(t, err)
	assert.False(t, migrated)

	data, err := os.ReadFile(filepath.Join(stationDir, "engine.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"connectors":[]}`, string(data), "the already-migrated file must not be overwritten by a second run")
}

func TestMigrateLegacySingleStationState_NoLegacyDirIsNoOp(t *testing.T) {
	baseDir := t.TempDir()
	stationDir := config.StationPersistDirByID(baseDir, "CP_A")

	migrated, err := migrateLegacySingleStationState(baseDir, stationDir)
	require.NoError(t, err)
	assert.False(t, migrated)
	assert.NoDirExists(t, stationDir)
}

// TestFleetManager_MigratesLegacyStateForMatchingOCPPIDOnly is an
// integration-level regression test: becoming multi-station used to
// silently abandon the original single station's persisted meter/session
// history and offline queue, since the new per-station directory started
// empty. Only the station whose OCPP ID matches the pre-fleet top-level
// identity should receive the migrated state — a different new station must
// start with nothing (there's no "its" legacy state to inherit).
func TestFleetManager_MigratesLegacyStateForMatchingOCPPIDOnly(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	baseDir := filepath.Join(dir, ".chargeghost")

	// Legacy single-station state, as if this had been running standalone
	// under OCPP ID "CP_A" before the config gained a stations array.
	writeFile(t, filepath.Join(baseDir, "engine", "engine.json"), `{"connectors":[]}`)
	writeFile(t, filepath.Join(baseDir, "message_queue.json"), `[]`)

	cfg := config.DefaultConfig()
	cfg.OCPPID = "CP_A"
	idA, ocppA := "station-a", "CP_A"
	urlA := unroutablePort1URL
	idB, ocppB := "station-b", "CP_B"
	urlB := "ws://127.0.0.1:2/CP_B"
	cfg.Stations = []config.StationConfig{
		{ID: &idA, OCPPID: &ocppA, ConnectionURL: &urlA},
		{ID: &idB, OCPPID: &ocppB, ConnectionURL: &urlB},
	}
	require.NoError(t, cfg.Save(cfgPath))

	hub := ws.NewHub()
	fm, err := NewFleetManager(cfgPath, baseDir, hub)
	require.NoError(t, err)
	require.NoError(t, fm.Start(context.Background()))
	defer fm.Shutdown(context.Background())

	stationADir := config.StationPersistDirByID(baseDir, "station-a")
	stationBDir := config.StationPersistDirByID(baseDir, "station-b")

	assert.FileExists(t, filepath.Join(stationADir, "engine.json"), "the station continuing CP_A's identity must inherit its legacy state")
	assert.FileExists(t, filepath.Join(stationADir, "message_queue.json"))
	assert.NoFileExists(t, filepath.Join(stationBDir, "engine.json"), "an unrelated new station must not receive CP_A's legacy state")
}
