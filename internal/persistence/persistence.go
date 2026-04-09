package persistence

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Persistable is implemented by any component that can save/load its state to disk.
type Persistable interface {
	SaveState(dir string) error
	LoadState(dir string) error
}

// WriteJSON marshals v to JSON and writes it to dir/filename with 0600 permissions.
// Creates parent directories as needed.
func WriteJSON(dir, filename string, v any) error {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, filename), data, 0600)
}

// ReadJSON reads dir/filename and unmarshals JSON into v.
// Returns nil (no error) if the file does not exist, leaving v untouched.
func ReadJSON(dir, filename string, v any) error {
	data, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, v)
}
