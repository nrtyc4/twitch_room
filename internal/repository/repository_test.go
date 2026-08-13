package repository

import (
	"testing"
	"time"
)

func TestMemoryStoreUpsertAndGet(t *testing.T) {
	store := NewMemoryStore()
	updatedAt := time.Now().UTC()

	state, err := store.Upsert("user-1", []string{"stream-1", "stream-2"}, updatedAt)
	if err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}
	if state.UserID != "user-1" {
		t.Fatalf("expected user-1, got %s", state.UserID)
	}

	got, ok := store.Get("user-1")
	if !ok {
		t.Fatal("expected user-1 to exist")
	}
	if len(got.StreamIDs) != 2 {
		t.Fatalf("expected 2 streams, got %d", len(got.StreamIDs))
	}
}

func TestMemoryStoreList(t *testing.T) {
	store := NewMemoryStore()
	// Upsert user-1 and check for success/failure
	_, err1 := store.Upsert("user-1", []string{"stream-1"}, time.Now().UTC())
	if err1 != nil {
		t.Fatalf("expected user-1 to be upserted: %v", err1)
	}

	// Upsert user-2 and check for success/failure
	_, err2 := store.Upsert("user-2", []string{"stream-2"}, time.Now().UTC())
	if err2 != nil {
		t.Fatalf("expected user-2 to be upserted: %v", err2)
	}

	states := store.List(10)
	if len(states) != 2 {
		t.Fatalf("expected 2 states, got %d", len(states))
	}
}
