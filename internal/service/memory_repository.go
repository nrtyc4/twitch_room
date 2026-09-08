package service

import (
	"sync"
	"time"
)

type MemoryRepository struct {
	mu            sync.RWMutex
	items         map[string]UserWatchState
	settings      map[string]PublisherSettings
	subscriptions map[string]Subscription
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		items:         make(map[string]UserWatchState),
		settings:      make(map[string]PublisherSettings),
		subscriptions: make(map[string]Subscription),
	}
}

func subscriptionKey(subscriberID, publisherID string) string {
	return subscriberID + "\x00" + publisherID
}

func (r *MemoryRepository) UpsertUserWatchState(userID string, streams []Stream, updatedAt time.Time) (UserWatchState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	state := UserWatchState{
		UserID:    userID,
		StreamIDs: streamIDsFromStreams(streams),
		Streams:   append([]Stream(nil), streams...),
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
		Streams:   append([]Stream(nil), state.Streams...),
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
			Streams:   append([]Stream(nil), state.Streams...),
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

func (r *MemoryRepository) SetPublisherSettings(settings PublisherSettings) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settings[settings.UserID] = settings
	return nil
}

func (r *MemoryRepository) GetPublisherSettings(userID string) (PublisherSettings, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	settings, ok := r.settings[userID]
	return settings, ok
}

func (r *MemoryRepository) UpsertSubscription(sub Subscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subscriptions[subscriptionKey(sub.SubscriberID, sub.PublisherID)] = sub
	return nil
}

func (r *MemoryRepository) GetSubscription(subscriberID, publisherID string) (Subscription, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sub, ok := r.subscriptions[subscriptionKey(subscriberID, publisherID)]
	return sub, ok
}

func (r *MemoryRepository) ListSubscriptionsForSubscriber(subscriberID string) []Subscription {
	r.mu.RLock()
	defer r.mu.RUnlock()

	subs := make([]Subscription, 0)
	for _, sub := range r.subscriptions {
		if sub.SubscriberID == subscriberID {
			subs = append(subs, sub)
		}
	}
	return subs
}

func (r *MemoryRepository) ListSubscriptionsForPublisher(publisherID string, status SubscriptionStatus) []Subscription {
	r.mu.RLock()
	defer r.mu.RUnlock()

	subs := make([]Subscription, 0)
	for _, sub := range r.subscriptions {
		if sub.PublisherID == publisherID && sub.Status == status {
			subs = append(subs, sub)
		}
	}
	return subs
}

func (r *MemoryRepository) SetSubscriptionStatus(subscriberID, publisherID string, status SubscriptionStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := subscriptionKey(subscriberID, publisherID)
	sub, ok := r.subscriptions[key]
	if !ok {
		return ErrNotSubscribed
	}
	sub.Status = status
	r.subscriptions[key] = sub
	return nil
}

func (r *MemoryRepository) DeleteSubscription(subscriberID, publisherID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subscriptions, subscriptionKey(subscriberID, publisherID))
}
