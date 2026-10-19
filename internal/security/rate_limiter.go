package security

import (
	"sync"
	"time"
)

type RateLimiter struct {
	mu           sync.Mutex
	requests     map[string][]time.Time
	limit        int
	window       time.Duration
	lastCleanup  time.Time
	cleanupEvery time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit <= 0 {
		limit = 30
	}
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{
		requests:     make(map[string][]time.Time),
		limit:        limit,
		window:       window,
		cleanupEvery: window,
	}
}

func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Periodically reap stale keys to bound memory growth.
	if rl.lastCleanup.IsZero() || now.Sub(rl.lastCleanup) >= rl.cleanupEvery {
		rl.reapLocked(now, cutoff)
		rl.lastCleanup = now
	}

	entries := rl.requests[key]
	filtered := entries[:0]
	for _, t := range entries {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) >= rl.limit {
		rl.requests[key] = filtered
		return false
	}
	filtered = append(filtered, now)
	if len(filtered) == 0 {
		delete(rl.requests, key)
	} else {
		rl.requests[key] = filtered
	}
	return true
}

// Cleanup removes expired entries and empty keys. Safe for concurrent use.
func (rl *RateLimiter) Cleanup() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.reapLocked(time.Now(), time.Now().Add(-rl.window))
}

func (rl *RateLimiter) reapLocked(now time.Time, cutoff time.Time) int {
	removed := 0
	for key, entries := range rl.requests {
		filtered := entries[:0]
		for _, t := range entries {
			if t.After(cutoff) {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) == 0 {
			delete(rl.requests, key)
			removed++
		} else {
			rl.requests[key] = filtered
		}
	}
	return removed
}
