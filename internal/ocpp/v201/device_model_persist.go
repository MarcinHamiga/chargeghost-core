package v201

import (
	"github.com/chargeghost/engine/internal/persistence"
)

const deviceModelFile = "device_model.json"

type deviceModelEntryJSON struct {
	Component  string         `json:"component"`
	Instance   string         `json:"instance,omitempty"`
	EVSEID     int            `json:"evse_id"`
	Variable   string         `json:"variable"`
	Value      string         `json:"value"`
	Mutability MutabilityType `json:"mutability"`
}

// SetPersistDir enables auto-save. Pass "" to disable.
func (dm *DeviceModel) SetPersistDir(dir string) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.persistDir = dir
}

func (dm *DeviceModel) SaveState(dir string) error {
	dm.mu.RLock()
	entries := make([]deviceModelEntryJSON, 0, len(dm.variables))
	for key, entry := range dm.variables {
		entries = append(entries, deviceModelEntryJSON{
			Component: key.Component, Instance: key.Instance,
			EVSEID: key.EVSEID, Variable: key.Variable,
			Value: entry.Value, Mutability: entry.Mutability,
		})
	}
	dm.mu.RUnlock()
	return persistence.WriteJSON(dir, deviceModelFile, entries)
}

func (dm *DeviceModel) LoadState(dir string) error {
	var entries []deviceModelEntryJSON
	if err := persistence.ReadJSON(dir, deviceModelFile, &entries); err != nil {
		return err
	}
	if entries == nil {
		return nil
	}

	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.variables = make(map[componentVariableKey]variableEntry, len(entries))
	for _, e := range entries {
		key := componentVariableKey{
			Component: e.Component, Instance: e.Instance,
			EVSEID: e.EVSEID, Variable: e.Variable,
		}
		dm.variables[key] = variableEntry{Value: e.Value, Mutability: e.Mutability}
	}
	return nil
}

func (dm *DeviceModel) autoSave() {
	if dm.persistDir != "" {
		_ = dm.SaveState(dm.persistDir)
	}
}
