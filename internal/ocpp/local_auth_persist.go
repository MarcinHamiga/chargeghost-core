package ocpp

import (
	"time"

	"github.com/chargeghost/engine/internal/persistence"
)

const localAuthFile = "local_auth.json"

type localAuthSnapshot struct {
	Version int                  `json:"version"`
	Entries []localAuthEntryJSON `json:"entries"`
	Enabled bool                 `json:"enabled"`
}

type localAuthEntryJSON struct {
	IDTag       string     `json:"id_tag"`
	Status      string     `json:"status"`
	Expiry      *time.Time `json:"expiry,omitempty"`
	ParentIDTag *string    `json:"parent_id_tag,omitempty"`
}

// SetPersistDir enables auto-save. Pass "" to disable.
func (m *LocalAuthListManager) SetPersistDir(dir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.persistDir = dir
}

func (m *LocalAuthListManager) SaveState(dir string) error {
	m.mu.RLock()
	snap := localAuthSnapshot{
		Version: m.version,
		Enabled: m.enabled,
		Entries: make([]localAuthEntryJSON, 0, len(m.entries)),
	}
	for _, e := range m.entries {
		snap.Entries = append(snap.Entries, localAuthEntryJSON{
			IDTag: e.IDTag, Status: e.Status, Expiry: e.Expiry, ParentIDTag: e.ParentIDTag,
		})
	}
	m.mu.RUnlock()
	return persistence.WriteJSON(dir, localAuthFile, snap)
}

func (m *LocalAuthListManager) LoadState(dir string) error {
	var snap localAuthSnapshot
	if err := persistence.ReadJSON(dir, localAuthFile, &snap); err != nil {
		return err
	}
	if snap.Entries == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.version = snap.Version
	m.enabled = snap.Enabled
	m.entries = make(map[string]LocalAuthEntry, len(snap.Entries))
	for _, e := range snap.Entries {
		m.entries[e.IDTag] = LocalAuthEntry{
			IDTag: e.IDTag, Status: e.Status, Expiry: e.Expiry, ParentIDTag: e.ParentIDTag,
		}
	}
	return nil
}

func (m *LocalAuthListManager) autoSave() {
	if m.persistDir != "" {
		_ = m.SaveState(m.persistDir)
	}
}
