package service

import (
	"testing"
	"time"
)

func TestSetUserWatchState(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	state, err := svc.SetUserWatchState("user-1", []string{"stream-1", "stream-2"})
	if err != nil {
		t.Fatalf("SetUserWatchState returned error: %v", err)
	}

	if state.UserID != "user-1" {
		t.Fatalf("expected user id user-1, got %s", state.UserID)
	}

	if len(state.StreamIDs) != 2 {
		t.Fatalf("expected 2 streams, got %d", len(state.StreamIDs))
	}
}

func TestGetUserWatchState(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	_, err := svc.SetUserWatchState("user-2", []string{"stream-9"})
	if err != nil {
		t.Fatalf("SetUserWatchState returned error: %v", err)
	}

	state, err := svc.GetUserWatchState("user-2")
	if err != nil {
		t.Fatalf("GetUserWatchState returned error: %v", err)
	}

	if len(state.StreamIDs) != 1 || state.StreamIDs[0] != "stream-9" {
		t.Fatalf("unexpected state: %+v", state)
	}
}

func TestListFeed(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	_, _ = svc.SetUserWatchState("user-1", []string{"stream-1"})
	_, _ = svc.SetUserWatchState("user-2", []string{"stream-2"})

	feed := svc.ListFeed(10)
	if len(feed) != 2 {
		t.Fatalf("expected 2 feed entries, got %d", len(feed))
	}
}

func TestClearUserWatchState(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	_, _ = svc.SetUserWatchState("user-3", []string{"stream-7"})

	if err := svc.ClearUserWatchState("user-3"); err != nil {
		t.Fatalf("ClearUserWatchState returned error: %v", err)
	}

	_, err := svc.GetUserWatchState("user-3")
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestExpiredStateIsRemoved(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, 10*time.Millisecond)

	_, _ = svc.SetUserWatchState("user-4", []string{"stream-5"})
	time.Sleep(50 * time.Millisecond)

	_, err := svc.GetUserWatchState("user-4")
	if err != ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound for expired state, got %v", err)
	}
}
