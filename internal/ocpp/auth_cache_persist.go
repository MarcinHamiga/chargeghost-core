package ocpp

import (
	"log/slog"
	"time"

	"github.com/chargeghost/engine/internal/persistence"
)

// authCacheSaveDebounce coalesces rapid cache mutations into one delayed
// write so a burst of Authorize responses does not fsync per tag.
const authCacheSaveDebounce = time.Second

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
	c.mu.Lock()
	dir := c.persistDir
	if dir == "" || c.saveScheduled {
		c.mu.Unlock()
		return
	}
	c.saveScheduled = true
	c.mu.Unlock()
	time.AfterFunc(authCacheSaveDebounce, func() {
		c.mu.Lock()
		c.saveScheduled = false
		dir := c.persistDir
		c.mu.Unlock()
		if dir == "" {
			return
		}
		if err := c.SaveState(dir); err != nil {
			slog.Warn("auth cache auto-save failed", "error", err)
		}
	})
}
