package ocpp

import (
	"sync"
	"time"
)

type cacheEntry struct {
	status string
	expiry *time.Time
}

// AuthorizationCache caches per-tag authorization status received from the CSMS.
// Populated by Authorize.conf responses; consulted for local authorization checks.
type AuthorizationCache struct {
	mu         sync.RWMutex
	entries    map[string]cacheEntry
	persistDir string
}

func NewAuthorizationCache() *AuthorizationCache {
	return &AuthorizationCache{entries: make(map[string]cacheEntry)}
}

func (c *AuthorizationCache) Get(idTag string) (status string, expiry *time.Time, found bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if e, ok := c.entries[idTag]; ok {
		return e.status, e.expiry, true
	}
	return "", nil, false
}

func (c *AuthorizationCache) Put(idTag, status string, expiry *time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[idTag] = cacheEntry{status, expiry}
	go c.autoSave()
}

func (c *AuthorizationCache) Remove(idTag string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, idTag)
	go c.autoSave()
}

func (c *AuthorizationCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cacheEntry)
	go c.autoSave()
}

func (c *AuthorizationCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
