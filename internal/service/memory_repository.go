package service

import (
	"sort"
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

func (r *MemoryRepository) UpsertUserWatchStates(states []UserWatchState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, st := range states {
		r.items[st.UserID] = UserWatchState{
			UserID:    st.UserID,
			StreamIDs: append([]string(nil), st.StreamIDs...),
			Streams:   append([]Stream(nil), st.Streams...),
			UpdatedAt: st.UpdatedAt,
		}
	}
	return nil
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
	state, ok, _ := r.GetUserWatchStateWithError(userID)
	return state, ok
}

func (r *MemoryRepository) GetUserWatchStateWithError(userID string) (UserWatchState, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, ok := r.items[userID]
	if !ok {
		return UserWatchState{}, false, nil
	}
	return UserWatchState{
		UserID:    state.UserID,
		StreamIDs: append([]string(nil), state.StreamIDs...),
		Streams:   append([]Stream(nil), state.Streams...),
		UpdatedAt: state.UpdatedAt,
	}, true, nil
}

func (r *MemoryRepository) ListActiveUsers(limit, offset int) []UserWatchState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if limit <= 0 {
		limit = len(r.items)
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

	sort.Slice(states, func(i, j int) bool {
		return states[i].UpdatedAt.After(states[j].UpdatedAt)
	})

	if offset < 0 {
		offset = 0
	}
	start := offset
	if start > len(states) {
		return []UserWatchState{}
	}

	end := min(start+limit, len(states))

	return states[start:end]
}

func (r *MemoryRepository) DeleteExpiredWatchStates(updatedBefore time.Time) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	removed := 0
	for id, state := range r.items {
		if state.UpdatedAt.Before(updatedBefore) {
			delete(r.items, id)
			removed++
		}
	}
	return removed
}

func (r *MemoryRepository) DeleteUserWatchState(userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, userID)
	return nil
}

func (r *MemoryRepository) DeleteUserData(userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, userID)
	delete(r.settings, userID)
	for key, sub := range r.subscriptions {
		if sub.SubscriberID == userID || sub.PublisherID == userID {
			delete(r.subscriptions, key)
		}
	}
	return nil
}

// CountActiveWatchStates returns the number of stored states last updated at
// or after the given time, mirroring how the service treats non-expired ones.
func (r *MemoryRepository) CountActiveWatchStates(updatedAfter time.Time) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, state := range r.items {
		if !state.UpdatedAt.Before(updatedAfter) {
			count++
		}
	}
	return count
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

func (r *MemoryRepository) DeleteSubscription(subscriberID, publisherID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subscriptions, subscriptionKey(subscriberID, publisherID))
	return nil
}

// CountSubscriptionsByStatus tallies every stored subscription by status.
func (r *MemoryRepository) CountSubscriptionsByStatus() map[SubscriptionStatus]int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	counts := make(map[SubscriptionStatus]int)
	for _, sub := range r.subscriptions {
		counts[sub.Status]++
	}
	return counts
}
