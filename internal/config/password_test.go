package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/chargeghost/engine/internal/config"
)

// The keyring mock is process-global and per-test-run only; it keeps these
// tests from reading or writing the developer's real OS keyring.
func mockKeyring(t *testing.T) {
	t.Helper()
	keyring.MockInit()
}

func TestGetPassword_EmptyKeyringEntryFallsBackToEnv(t *testing.T) {
	mockKeyring(t)
	// Older builds wrote "" instead of deleting the entry on clear; such an
	// entry must not shadow the CHARGEGHOST_PASSWORD fallback.
	require.NoError(t, keyring.Set("chargeghost", "CP_EMPTY", ""))
	t.Setenv("CHARGEGHOST_PASSWORD", "env-password")

	assert.Equal(t, "env-password", config.GetPassword("CP_EMPTY"))
}

func TestPasswordFromEnvFallback_OnlyWhenEnvActuallySuppliesPassword(t *testing.T) {
	mockKeyring(t)
	// Keyring hit plus env set: the env var sits unused, so there is no
	// fallback to warn about.
	require.NoError(t, config.SetPassword("CP_SRC", "keyring-password"))
	t.Setenv("CHARGEGHOST_PASSWORD", "env-password")
	assert.False(t, config.PasswordFromEnvFallback("CP_SRC"),
		"keyring-supplied password must not report an env fallback")
	assert.Equal(t, "keyring-password", config.GetPassword("CP_SRC"))

	// Keyring cleared: the same env var now supplies the password.
	require.NoError(t, config.DeletePassword("CP_SRC"))
	assert.True(t, config.PasswordFromEnvFallback("CP_SRC"))
	assert.Equal(t, "env-password", config.GetPassword("CP_SRC"))

	// An empty keyring entry counts as unset, so the env fallback applies.
	require.NoError(t, keyring.Set("chargeghost", "CP_SRC_EMPTY", ""))
	assert.True(t, config.PasswordFromEnvFallback("CP_SRC_EMPTY"))
}

func TestDeletePassword_RemovesEntryAndRestoresEnvFallback(t *testing.T) {
	mockKeyring(t)
	require.NoError(t, config.SetPassword("CP_DEL", "stored"))
	require.Equal(t, "stored", config.GetPassword("CP_DEL"))

	require.NoError(t, config.DeletePassword("CP_DEL"))

	t.Setenv("CHARGEGHOST_PASSWORD", "env-password")
	assert.Equal(t, "env-password", config.GetPassword("CP_DEL"))
}

func TestDeletePassword_MissingEntryIsNotAnError(t *testing.T) {
	mockKeyring(t)
	assert.NoError(t, config.DeletePassword("CP_NEVER_STORED"))
}

func TestLoad_EmptyPasswordFieldDoesNotClobberKeyring(t *testing.T) {
	mockKeyring(t)
	require.NoError(t, config.SetPassword("CP_KEEP", "real-password"))

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"ocpp_id":"CP_KEEP","ocpp_password":""}`), 0600))

	cfg, err := config.Load(path)
	require.NoError(t, err)

	assert.Nil(t, cfg.OCPPPassword, "the field must still be stripped after load")
	assert.Equal(t, "real-password", config.GetPassword("CP_KEEP"),
		"an empty ocpp_password field in config.json must not overwrite the stored password")
}

func TestLoad_EmptyStationPasswordFieldDoesNotClobberKeyring(t *testing.T) {
	mockKeyring(t)
	require.NoError(t, config.SetPassword("CP_ST", "station-password"))

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"ocpp_id":"CP_MAIN","stations":[{"id":"st-1","ocpp_id":"CP_ST","ocpp_password":""}]}`
	require.NoError(t, os.WriteFile(path, []byte(body), 0600))

	cfg, err := config.Load(path)
	require.NoError(t, err)

	require.Len(t, cfg.Stations, 1)
	assert.Nil(t, cfg.Stations[0].OCPPPassword)
	assert.Equal(t, "station-password", config.GetPassword("CP_ST"))
}
