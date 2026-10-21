package repository

import (
	"sort"
	"sync"
	"time"

	"twitch_room/internal/service"
)

type WatchStateStore interface {
	Upsert(userID string, streams []service.Stream, updatedAt time.Time) (service.UserWatchState, error)
	Get(userID string) (service.UserWatchState, bool)
	List(limit, offset int) []service.UserWatchState
	Delete(userID string) error
	DeleteUserData(userID string) error
	CountActiveWatchStates(updatedAfter time.Time) int

	SetPublisherSettings(settings service.PublisherSettings) error
	GetPublisherSettings(userID string) (service.PublisherSettings, bool)

	UpsertSubscription(sub service.Subscription) error
	GetSubscription(subscriberID, publisherID string) (service.Subscription, bool)
	ListSubscriptionsForSubscriber(subscriberID string) []service.Subscription
	ListSubscriptionsForPublisher(publisherID string, status service.SubscriptionStatus) []service.Subscription
	SetSubscriptionStatus(subscriberID, publisherID string, status service.SubscriptionStatus) error
	DeleteSubscription(subscriberID, publisherID string) error
	CountSubscriptionsByStatus() map[service.SubscriptionStatus]int
}

type MemoryStore struct {
	mu            sync.RWMutex
	items         map[string]service.UserWatchState
	settings      map[string]service.PublisherSettings
	subscriptions map[string]service.Subscription
}

type ServiceAdapter struct {
	store WatchStateStore
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		items:         make(map[string]service.UserWatchState),
		settings:      make(map[string]service.PublisherSettings),
		subscriptions: make(map[string]service.Subscription),
	}
}

func subscriptionKey(subscriberID, publisherID string) string {
	return subscriberID + "\x00" + publisherID
}

func cloneStreams(streams []service.Stream) []service.Stream {
	if len(streams) == 0 {
		return nil
	}
	return append([]service.Stream(nil), streams...)
}

func streamIDs(streams []service.Stream) []string {
	if len(streams) == 0 {
		return []string{}
	}
	ids := make([]string, 0, len(streams))
	for _, s := range streams {
		ids = append(ids, s.ID)
	}
	return ids
}

func NewServiceAdapter(store WatchStateStore) *ServiceAdapter {
	return &ServiceAdapter{store: store}
}

func (s *MemoryStore) Upsert(userID string, streams []service.Stream, updatedAt time.Time) (service.UserWatchState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := service.UserWatchState{
		UserID:    userID,
		StreamIDs: streamIDs(streams),
		Streams:   cloneStreams(streams),
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
		Streams:   cloneStreams(state.Streams),
		UpdatedAt: state.UpdatedAt,
	}, true
}

func (s *MemoryStore) List(limit, offset int) []service.UserWatchState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = len(s.items)
	}
	if offset < 0 {
		offset = 0
	}

	allStates := make([]service.UserWatchState, 0, len(s.items))
	for _, state := range s.items {
		allStates = append(allStates, service.UserWatchState{
			UserID:    state.UserID,
			StreamIDs: append([]string(nil), state.StreamIDs...),
			Streams:   cloneStreams(state.Streams),
			UpdatedAt: state.UpdatedAt,
		})
	}

	sort.Slice(allStates, func(i, j int) bool {
		return allStates[i].UpdatedAt.After(allStates[j].UpdatedAt)
	})

	start := offset
	if start > len(allStates) {
		return []service.UserWatchState{}
	}

	end := start + limit
	if end > len(allStates) {
		end = len(allStates)
	}

	return allStates[start:end]
}

func (s *MemoryStore) Delete(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, userID)
	return nil
}

// DeleteUserData removes all data associated with a user (watch state, settings,
// and any subscriptions where they participate as subscriber or publisher).
func (s *MemoryStore) DeleteUserData(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, userID)
	delete(s.settings, userID)
	for key, sub := range s.subscriptions {
		if sub.SubscriberID == userID || sub.PublisherID == userID {
			delete(s.subscriptions, key)
		}
	}
	return nil
}

// CountActiveWatchStates returns the number of stored states last updated at
// or after the given time, mirroring how the service treats non-expired ones.
func (s *MemoryStore) CountActiveWatchStates(updatedAfter time.Time) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, state := range s.items {
		if !state.UpdatedAt.Before(updatedAfter) {
			count++
		}
	}
	return count
}

func (s *MemoryStore) SetPublisherSettings(settings service.PublisherSettings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[settings.UserID] = settings
	return nil
}

func (s *MemoryStore) GetPublisherSettings(userID string) (service.PublisherSettings, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	settings, ok := s.settings[userID]
	return settings, ok
}

func (s *MemoryStore) UpsertSubscription(sub service.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscriptions[subscriptionKey(sub.SubscriberID, sub.PublisherID)] = sub
	return nil
}

func (s *MemoryStore) GetSubscription(subscriberID, publisherID string) (service.Subscription, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sub, ok := s.subscriptions[subscriptionKey(subscriberID, publisherID)]
	return sub, ok
}

func (s *MemoryStore) ListSubscriptionsForSubscriber(subscriberID string) []service.Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()

	subs := make([]service.Subscription, 0)
	for _, sub := range s.subscriptions {
		if sub.SubscriberID == subscriberID {
			subs = append(subs, sub)
		}
	}
	return subs
}

func (s *MemoryStore) ListSubscriptionsForPublisher(publisherID string, status service.SubscriptionStatus) []service.Subscription {
	s.mu.RLock()
	defer s.mu.RUnlock()

	subs := make([]service.Subscription, 0)
	for _, sub := range s.subscriptions {
		if sub.PublisherID == publisherID && sub.Status == status {
			subs = append(subs, sub)
		}
	}
	return subs
}

func (s *MemoryStore) SetSubscriptionStatus(subscriberID, publisherID string, status service.SubscriptionStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := subscriptionKey(subscriberID, publisherID)
	sub, ok := s.subscriptions[key]
	if !ok {
		return service.ErrNotSubscribed
	}
	sub.Status = status
	s.subscriptions[key] = sub
	return nil
}

func (s *MemoryStore) DeleteSubscription(subscriberID, publisherID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subscriptions, subscriptionKey(subscriberID, publisherID))
	return nil
}

// CountSubscriptionsByStatus tallies every stored subscription by status.
func (s *MemoryStore) CountSubscriptionsByStatus() map[service.SubscriptionStatus]int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	counts := make(map[service.SubscriptionStatus]int)
	for _, sub := range s.subscriptions {
		counts[sub.Status]++
	}
	return counts
}

func (a *ServiceAdapter) UpsertUserWatchState(userID string, streams []service.Stream, updatedAt time.Time) (service.UserWatchState, error) {
	return a.store.Upsert(userID, streams, updatedAt)
}

func (a *ServiceAdapter) GetUserWatchState(userID string) (service.UserWatchState, bool) {
	return a.store.Get(userID)
}

func (a *ServiceAdapter) ListActiveUsers(limit, offset int) []service.UserWatchState {
	return a.store.List(limit, offset)
}

func (a *ServiceAdapter) DeleteUserWatchState(userID string) error {
	return a.store.Delete(userID)
}

func (a *ServiceAdapter) DeleteUserData(userID string) error {
	return a.store.DeleteUserData(userID)
}

func (a *ServiceAdapter) CountActiveWatchStates(updatedAfter time.Time) int {
	return a.store.CountActiveWatchStates(updatedAfter)
}

func (a *ServiceAdapter) SetPublisherSettings(settings service.PublisherSettings) error {
	return a.store.SetPublisherSettings(settings)
}

func (a *ServiceAdapter) GetPublisherSettings(userID string) (service.PublisherSettings, bool) {
	return a.store.GetPublisherSettings(userID)
}

func (a *ServiceAdapter) UpsertSubscription(sub service.Subscription) error {
	return a.store.UpsertSubscription(sub)
}

func (a *ServiceAdapter) GetSubscription(subscriberID, publisherID string) (service.Subscription, bool) {
	return a.store.GetSubscription(subscriberID, publisherID)
}

func (a *ServiceAdapter) ListSubscriptionsForSubscriber(subscriberID string) []service.Subscription {
	return a.store.ListSubscriptionsForSubscriber(subscriberID)
}

func (a *ServiceAdapter) ListSubscriptionsForPublisher(publisherID string, status service.SubscriptionStatus) []service.Subscription {
	return a.store.ListSubscriptionsForPublisher(publisherID, status)
}

func (a *ServiceAdapter) SetSubscriptionStatus(subscriberID, publisherID string, status service.SubscriptionStatus) error {
	return a.store.SetSubscriptionStatus(subscriberID, publisherID, status)
}

func (a *ServiceAdapter) DeleteSubscription(subscriberID, publisherID string) error {
	return a.store.DeleteSubscription(subscriberID, publisherID)
}

func (a *ServiceAdapter) CountSubscriptionsByStatus() map[service.SubscriptionStatus]int {
	return a.store.CountSubscriptionsByStatus()
}
