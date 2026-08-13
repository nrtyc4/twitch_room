package performance

import (
	"testing"
	"time"

	"twitch_room/internal/service"
)

func TestWatchStateCacheGetAndSet(t *testing.T) {
	cache := NewWatchStateCache(time.Second * 5)
	userID := "test-user"
	state := service.UserWatchState{
		UserID:    userID,
		StreamIDs: []string{"stream-1", "stream-2"},
		UpdatedAt: time.Now(),
	}

	// Initially cache should be empty
	_, exists := cache.Get(userID)
	if exists {
		t.Fatal("expected cache miss, got hit")
	}

	// Set the value
	cache.Set(userID, state)

	// Should now be in cache
	retrieved, exists := cache.Get(userID)
	if !exists {
		t.Fatal("expected cache hit, got miss")
	}

	if retrieved.UserID != userID {
		t.Fatalf("expected user ID %s, got %s", userID, retrieved.UserID)
	}

	if len(retrieved.StreamIDs) != 2 {
		t.Fatalf("expected 2 streams, got %d", len(retrieved.StreamIDs))
	}
}

func TestWatchStateCacheExpiration(t *testing.T) {
	cache := NewWatchStateCache(time.Millisecond * 100)
	userID := "test-user"
	state := service.UserWatchState{
		UserID:    userID,
		StreamIDs: []string{"stream-1"},
		UpdatedAt: time.Now(),
	}

	cache.Set(userID, state)

	// Should exist immediately
	_, exists := cache.Get(userID)
	if !exists {
		t.Fatal("expected cache hit immediately after set")
	}

	// Wait for expiration
	time.Sleep(time.Millisecond * 150)

	// Should now be expired
	_, exists = cache.Get(userID)
	if exists {
		t.Fatal("expected cache miss after expiration")
	}
}

func TestWatchStateCacheInvalidate(t *testing.T) {
	cache := NewWatchStateCache(time.Second * 5)
	userID := "test-user"
	state := service.UserWatchState{
		UserID:    userID,
		StreamIDs: []string{"stream-1"},
		UpdatedAt: time.Now(),
	}

	cache.Set(userID, state)

	// Verify it's cached
	_, exists := cache.Get(userID)
	if !exists {
		t.Fatal("expected cache hit")
	}

	// Invalidate
	cache.Invalidate(userID)

	// Should now be gone
	_, exists = cache.Get(userID)
	if exists {
		t.Fatal("expected cache miss after invalidation")
	}
}

func TestWatchStateCacheCleanup(t *testing.T) {
	cache := NewWatchStateCache(time.Millisecond * 50)

	// Add multiple entries
	for i := 1; i <= 5; i++ {
		userID := "user-" + string(rune(48+i))
		state := service.UserWatchState{
			UserID:    userID,
			StreamIDs: []string{"stream-1"},
			UpdatedAt: time.Now(),
		}
		cache.Set(userID, state)
	}

	if cache.Size() != 5 {
		t.Fatalf("expected 5 entries, got %d", cache.Size())
	}

	// Wait for expiration
	time.Sleep(time.Millisecond * 100)

	// Run cleanup
	removed := cache.Cleanup()

	if removed != 5 {
		t.Fatalf("expected 5 entries removed, got %d", removed)
	}

	if cache.Size() != 0 {
		t.Fatalf("expected 0 entries after cleanup, got %d", cache.Size())
	}
}

func TestWatchStateCacheSize(t *testing.T) {
	cache := NewWatchStateCache(time.Second * 5)

	if cache.Size() != 0 {
		t.Fatal("expected cache to start empty")
	}

	state := service.UserWatchState{
		UserID:    "user-1",
		StreamIDs: []string{"stream-1"},
		UpdatedAt: time.Now(),
	}

	cache.Set("user-1", state)
	cache.Set("user-2", state)
	cache.Set("user-3", state)

	if cache.Size() != 3 {
		t.Fatalf("expected 3 entries, got %d", cache.Size())
	}

	cache.InvalidateAll()

	if cache.Size() != 0 {
		t.Fatalf("expected 0 entries after InvalidateAll, got %d", cache.Size())
	}
}

func TestDefaultConnectionPoolConfig(t *testing.T) {
	cfg := DefaultConnectionPoolConfig()

	if cfg.MaxOpenConns != 25 {
		t.Fatalf("expected MaxOpenConns=25, got %d", cfg.MaxOpenConns)
	}

	if cfg.MaxIdleConns != 5 {
		t.Fatalf("expected MaxIdleConns=5, got %d", cfg.MaxIdleConns)
	}
}

func TestDefaultHTTPClientConfig(t *testing.T) {
	cfg := DefaultHTTPClientConfig()

	if cfg.Timeout != 10*time.Second {
		t.Fatalf("expected Timeout=10s, got %v", cfg.Timeout)
	}

	if cfg.MaxIdleConns != 100 {
		t.Fatalf("expected MaxIdleConns=100, got %d", cfg.MaxIdleConns)
	}
}
