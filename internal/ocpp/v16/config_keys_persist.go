package v16

import (
	"github.com/chargeghost/engine/internal/persistence"
)

const configKeysFile = "config_keys.json"

type configKeyJSON struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// SetPersistDir enables auto-save. Pass "" to disable.
func (m *ConfigKeyManager) SetPersistDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.persistDir = dir
}

func (m *ConfigKeyManager) SaveState(dir string) error {
	m.mu.RLock()
	entries := make([]configKeyJSON, 0, len(m.keys))
	for _, k := range m.keys {
		entries = append(entries, configKeyJSON{Key: k.Key, Value: k.Value})
	}
	m.mu.RUnlock()
	return persistence.WriteJSON(dir, configKeysFile, entries)
}

func (m *ConfigKeyManager) LoadState(dir string) error {
	var entries []configKeyJSON
	if err := persistence.ReadJSON(dir, configKeysFile, &entries); err != nil {
		return err
	}
	if entries == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Overlay saved values onto defaults (already populated by NewConfigKeyManager).
	for _, e := range entries {
		if k, ok := m.keys[e.Key]; ok {
			k.Value = e.Value
		}
	}
	return nil
}

func (m *ConfigKeyManager) autoSave() {
	if m.persistDir != "" {
		_ = m.SaveState(m.persistDir)
	}
}
