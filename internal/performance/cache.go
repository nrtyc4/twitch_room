package performance

import (
	"sync"
	"time"

	"twitch_room/internal/service"
)

// CacheEntry represents a cached watch state with expiration.
type CacheEntry struct {
	Data      service.UserWatchState
	ExpiresAt time.Time
}

// WatchStateCache provides in-memory caching for frequently accessed watch states.
// This reduces database queries and improves response times.
type WatchStateCache struct {
	mu    sync.RWMutex
	cache map[string]CacheEntry
	ttl   time.Duration
}

// NewWatchStateCache creates a new watch state cache with the given TTL.
func NewWatchStateCache(ttl time.Duration) *WatchStateCache {
	return &WatchStateCache{
		cache: make(map[string]CacheEntry),
		ttl:   ttl,
	}
}

// Get retrieves a cached watch state if it exists and hasn't expired.
func (c *WatchStateCache) Get(userID string) (service.UserWatchState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.cache[userID]
	if !exists {
		return service.UserWatchState{}, false
	}

	// Check if entry has expired
	now := time.Now()
	if now.After(entry.ExpiresAt) {
		return service.UserWatchState{}, false
	}
	// Also honor the watch state's own TTL so a state that was already
	// close to expiry when cached is not served past its lifetime.
	if !entry.Data.UpdatedAt.IsZero() && now.Sub(entry.Data.UpdatedAt) > c.ttl {
		return service.UserWatchState{}, false
	}

	return entry.Data, true
}

// Set stores a watch state in the cache.
func (c *WatchStateCache) Set(userID string, state service.UserWatchState) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache[userID] = CacheEntry{
		Data:      state,
		ExpiresAt: time.Now().Add(c.ttl),
	}
}

// Invalidate removes a specific entry from the cache.
func (c *WatchStateCache) Invalidate(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.cache, userID)
}

// InvalidateAll clears the entire cache.
func (c *WatchStateCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]CacheEntry)
}

// Cleanup removes expired entries from the cache.
// This should be called periodically to prevent unbounded memory growth.
func (c *WatchStateCache) Cleanup() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	removed := 0
	now := time.Now()

	for userID, entry := range c.cache {
		if now.After(entry.ExpiresAt) {
			delete(c.cache, userID)
			removed++
		}
	}

	return removed
}

// Size returns the current number of entries in the cache.
func (c *WatchStateCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.cache)
}

// ConnectionPoolConfig holds configuration for connection pools.
type ConnectionPoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// DefaultConnectionPoolConfig returns sensible defaults for connection pooling.
func DefaultConnectionPoolConfig() ConnectionPoolConfig {
	return ConnectionPoolConfig{
		MaxOpenConns:    25,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
		ConnMaxIdleTime: time.Minute * 10,
	}
}

// HTTPClientConfig holds configuration for HTTP client optimization.
type HTTPClientConfig struct {
	Timeout             time.Duration
	MaxIdleConns        int
	MaxIdleConnsPerHost int
	MaxConnsPerHost     int
	IdleConnTimeout     time.Duration
}

// DefaultHTTPClientConfig returns sensible defaults for HTTP client pooling.
func DefaultHTTPClientConfig() HTTPClientConfig {
	return HTTPClientConfig{
		Timeout:             10 * time.Second,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 5,
		MaxConnsPerHost:     10,
		IdleConnTimeout:     90 * time.Second,
	}
}
