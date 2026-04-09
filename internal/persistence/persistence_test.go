package persistence

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteAndReadJSON(t *testing.T) {
	dir := t.TempDir()

	type sample struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	original := sample{Name: "test", Value: 42}
	require.NoError(t, WriteJSON(dir, "test.json", original))

	var loaded sample
	require.NoError(t, ReadJSON(dir, "test.json", &loaded))
	assert.Equal(t, original, loaded)
}

func TestReadJSON_MissingFile(t *testing.T) {
	dir := t.TempDir()

	var data map[string]string
	err := ReadJSON(dir, "nonexistent.json", &data)
	assert.NoError(t, err)
	assert.Nil(t, data)
}

func TestReadJSON_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{invalid"), 0600))

	var data map[string]string
	err := ReadJSON(dir, "bad.json", &data)
	assert.Error(t, err)
}

func TestWriteJSON_CreatesDirectories(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deep")

	err := WriteJSON(dir, "test.json", map[string]int{"a": 1})
	assert.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, "test.json"))
	assert.NoError(t, err)
}
