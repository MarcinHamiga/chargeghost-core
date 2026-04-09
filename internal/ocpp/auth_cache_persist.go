package ocpp

import (
	"time"

	"github.com/chargeghost/engine/internal/persistence"
)

const authCacheFile = "auth_cache.json"

type authCacheEntryJSON struct {
	IDTag  string     `json:"id_tag"`
	Status string     `json:"status"`
	Expiry *time.Time `json:"expiry,omitempty"`
}

// SetPersistDir enables auto-save. Pass "" to disable.
func (c *AuthorizationCache) SetPersistDir(dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.persistDir = dir
}

func (c *AuthorizationCache) SaveState(dir string) error {
	c.mu.RLock()
	entries := make([]authCacheEntryJSON, 0, len(c.entries))
	for tag, e := range c.entries {
		entries = append(entries, authCacheEntryJSON{IDTag: tag, Status: e.status, Expiry: e.expiry})
	}
	c.mu.RUnlock()
	return persistence.WriteJSON(dir, authCacheFile, entries)
}

func (c *AuthorizationCache) LoadState(dir string) error {
	var entries []authCacheEntryJSON
	if err := persistence.ReadJSON(dir, authCacheFile, &entries); err != nil {
		return err
	}
	if entries == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cacheEntry, len(entries))
	for _, e := range entries {
		c.entries[e.IDTag] = cacheEntry{status: e.Status, expiry: e.Expiry}
	}
	return nil
}

func (c *AuthorizationCache) autoSave() {
	if c.persistDir != "" {
		_ = c.SaveState(c.persistDir)
	}
}
