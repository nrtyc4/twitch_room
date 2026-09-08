package repository

import (
	"sync"
	"time"

	"twitch_room/internal/service"
)

type WatchStateStore interface {
	Upsert(userID string, streams []service.Stream, updatedAt time.Time) (service.UserWatchState, error)
	Get(userID string) (service.UserWatchState, bool)
	List(limit int) []service.UserWatchState
	Delete(userID string)

	SetPublisherSettings(settings service.PublisherSettings) error
	GetPublisherSettings(userID string) (service.PublisherSettings, bool)

	UpsertSubscription(sub service.Subscription) error
	GetSubscription(subscriberID, publisherID string) (service.Subscription, bool)
	ListSubscriptionsForSubscriber(subscriberID string) []service.Subscription
	ListSubscriptionsForPublisher(publisherID string, status service.SubscriptionStatus) []service.Subscription
	SetSubscriptionStatus(subscriberID, publisherID string, status service.SubscriptionStatus) error
	DeleteSubscription(subscriberID, publisherID string)
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
		return nil
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
			Streams:   cloneStreams(state.Streams),
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

func (s *MemoryStore) DeleteSubscription(subscriberID, publisherID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subscriptions, subscriptionKey(subscriberID, publisherID))
}

func (a *ServiceAdapter) UpsertUserWatchState(userID string, streams []service.Stream, updatedAt time.Time) (service.UserWatchState, error) {
	return a.store.Upsert(userID, streams, updatedAt)
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

func (a *ServiceAdapter) DeleteSubscription(subscriberID, publisherID string) {
	a.store.DeleteSubscription(subscriberID, publisherID)
}
