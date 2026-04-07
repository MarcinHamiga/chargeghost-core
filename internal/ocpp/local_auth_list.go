package ocpp

import (
	"errors"
	"sync"
	"time"
)

const maxLocalAuthListEntries = 1000

// LocalAuthListManager is the real implementation that replaces StubLocalAuthManager (Plan 3b).
// It implements the LocalAuthManager interface.
type LocalAuthListManager struct {
	mu      sync.RWMutex
	version int
	entries map[string]LocalAuthEntry
	enabled bool
}

func NewLocalAuthListManager() *LocalAuthListManager {
	return &LocalAuthListManager{
		entries: make(map[string]LocalAuthEntry),
		enabled: true,
	}
}

func (m *LocalAuthListManager) GetVersion() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.version
}

func (m *LocalAuthListManager) GetEntry(idTag string) *LocalAuthEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.entries[idTag]; ok {
		if e.Expiry != nil && time.Now().After(*e.Expiry) {
			return nil // expired
		}
		return &e
	}
	return nil
}

func (m *LocalAuthListManager) GetAllEntries() []LocalAuthEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]LocalAuthEntry, 0, len(m.entries))
	for _, e := range m.entries {
		result = append(result, e)
	}
	return result
}

func (m *LocalAuthListManager) UpdateList(version int, entries []LocalAuthEntry, updateType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if updateType == "Full" {
		if len(entries) > maxLocalAuthListEntries {
			return errors.New("exceeds max local auth list entries (1000)")
		}
		m.entries = make(map[string]LocalAuthEntry, len(entries))
	}

	// Check capacity for differential update.
	if updateType != "Full" {
		if len(m.entries)+len(entries) > maxLocalAuthListEntries {
			return errors.New("would exceed max local auth list entries (1000)")
		}
	}

	for _, e := range entries {
		m.entries[e.IDTag] = e
	}
	m.version = version
	return nil
}

func (m *LocalAuthListManager) RemoveEntry(idTag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[idTag]; !ok {
		return errors.New("entry not found")
	}
	delete(m.entries, idTag)
	return nil
}

func (m *LocalAuthListManager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = make(map[string]LocalAuthEntry)
	m.version = 0
}

func (m *LocalAuthListManager) GetStats() (version, count, maxEntries int, enabled bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.version, len(m.entries), maxLocalAuthListEntries, m.enabled
}
