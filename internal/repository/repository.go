package repository

import (
	"sync"
	"time"

	"twitch_room/internal/service"
)

type WatchStateStore interface {
	Upsert(userID string, streamIDs []string, updatedAt time.Time) (service.UserWatchState, error)
	Get(userID string) (service.UserWatchState, bool)
	List(limit int) []service.UserWatchState
	Delete(userID string)
}

type MemoryStore struct {
	mu    sync.RWMutex
	items map[string]service.UserWatchState
}

type ServiceAdapter struct {
	store WatchStateStore
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: make(map[string]service.UserWatchState)}
}

func NewServiceAdapter(store WatchStateStore) *ServiceAdapter {
	return &ServiceAdapter{store: store}
}

func (s *MemoryStore) Upsert(userID string, streamIDs []string, updatedAt time.Time) (service.UserWatchState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := service.UserWatchState{
		UserID:    userID,
		StreamIDs: append([]string(nil), streamIDs...),
		UpdatedAt: updatedAt,
	}
	s.items[userID] = state
	return state, nil
}

func (s *MemoryStore) Get(userID string) (service.UserWatchState, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	state, ok := s.items[userID]
	if !ok {
		return service.UserWatchState{}, false
	}
	return service.UserWatchState{
		UserID:    state.UserID,
		StreamIDs: append([]string(nil), state.StreamIDs...),
		UpdatedAt: state.UpdatedAt,
	}, true
}

func (s *MemoryStore) List(limit int) []service.UserWatchState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	states := make([]service.UserWatchState, 0, len(s.items))
	for _, state := range s.items {
		states = append(states, service.UserWatchState{
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

func (s *MemoryStore) Delete(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, userID)
}

func (a *ServiceAdapter) UpsertUserWatchState(userID string, streamIDs []string, updatedAt time.Time) (service.UserWatchState, error) {
	return a.store.Upsert(userID, streamIDs, updatedAt)
}

func (a *ServiceAdapter) GetUserWatchState(userID string) (service.UserWatchState, bool) {
	return a.store.Get(userID)
}

func (a *ServiceAdapter) ListActiveUsers(limit int) []service.UserWatchState {
	return a.store.List(limit)
}

func (a *ServiceAdapter) DeleteUserWatchState(userID string) {
	a.store.Delete(userID)
}
