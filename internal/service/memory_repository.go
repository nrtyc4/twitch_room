package service

import (
	"sync"
	"time"
)

type MemoryRepository struct {
	mu    sync.RWMutex
	items map[string]UserWatchState
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{items: make(map[string]UserWatchState)}
}

func (r *MemoryRepository) UpsertUserWatchState(userID string, streamIDs []string, updatedAt time.Time) (UserWatchState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	state := UserWatchState{
		UserID:    userID,
		StreamIDs: append([]string(nil), streamIDs...),
		UpdatedAt: updatedAt,
	}
	r.items[userID] = state
	return state, nil
}

func (r *MemoryRepository) GetUserWatchState(userID string) (UserWatchState, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, ok := r.items[userID]
	if !ok {
		return UserWatchState{}, false
	}
	return UserWatchState{
		UserID:    state.UserID,
		StreamIDs: append([]string(nil), state.StreamIDs...),
		UpdatedAt: state.UpdatedAt,
	}, true
}

func (r *MemoryRepository) ListActiveUsers(limit int) []UserWatchState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	states := make([]UserWatchState, 0, len(r.items))
	for _, state := range r.items {
		states = append(states, UserWatchState{
			UserID:    state.UserID,
			StreamIDs: append([]string(nil), state.StreamIDs...),
			UpdatedAt: state.UpdatedAt,
		})
	}

	if len(states) > limit {
		states = states[:limit]
	}

	return states
}

func (r *MemoryRepository) DeleteUserWatchState(userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, userID)
}
