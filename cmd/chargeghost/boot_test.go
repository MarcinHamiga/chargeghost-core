package main

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/chargeghost/engine/internal/config"
	"github.com/stretchr/testify/require"
)

func newTestBootConfig(t *testing.T) (cfgPath, baseDir string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath = filepath.Join(dir, "config.json")
	baseDir = filepath.Join(dir, ".chargeghost")
	cfg := config.DefaultConfig()
	// Disabled override for the default station so StartBoot's fleet start
	// never dials a CSMS (same pattern as fleet_manager_test.go).
	id, ocppID := "station-1", "CP_1"
	disabled := false
	cfg.Stations = []config.StationConfig{{ID: &id, OCPPID: &ocppID, Enabled: &disabled}}
	require.NoError(t, cfg.Save(cfgPath))
	return cfgPath, baseDir
}

func TestStartBoot_ServesHealthOnEphemeralPort(t *testing.T) {
	cfgPath, baseDir := newTestBootConfig(t)

	boot, err := StartBoot(cfgPath, baseDir, "127.0.0.1:0", nil)
	require.NoError(t, err)
	require.NotNil(t, boot)
	require.NotContains(t, boot.Addr, ":0", "boot.Addr should carry the resolved ephemeral port")

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + boot.Addr + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "ok", body["status"])

	done := make(chan struct{})
	go func() {
		boot.Shutdown()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return within 5s")
	}
}

func TestStartBoot_PinnedPortServesHealth(t *testing.T) {
	// Reserve a port, release it, and pin StartBoot to it — mirrors the
	// `--listen 127.0.0.1:<port>` acceptance path without ephemeral ports.
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	pinned := reserved.Addr().String()
	require.NoError(t, reserved.Close())

	cfgPath, baseDir := newTestBootConfig(t)
	boot, err := StartBoot(cfgPath, baseDir, pinned, nil)
	require.NoError(t, err)
	require.Equal(t, pinned, boot.Addr)

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + pinned + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	boot.Shutdown()
}

func TestStartBoot_ListenFailureReturnsError(t *testing.T) {
	cfgPath, baseDir := newTestBootConfig(t)

	// Occupy a port, then ask StartBoot to bind the same one.
	boot, err := StartBoot(cfgPath, baseDir, "127.0.0.1:0", nil)
	require.NoError(t, err)
	defer func() { boot.Shutdown() }()

	_, err = StartBoot(cfgPath, baseDir, boot.Addr, nil)
	require.Error(t, err, "StartBoot on an occupied port should fail")

	shutdown := make(chan struct{})
	go func() {
		boot.Shutdown()
		close(shutdown)
	}()
	select {
	case <-shutdown:
	case <-time.After(5 * time.Second):
		t.Fatal("Shutdown did not return within 5s")
	}
}
