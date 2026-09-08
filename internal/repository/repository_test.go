package repository

import (
	"testing"
	"time"

	"twitch_room/internal/service"
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

func TestMemoryStoreSettings(t *testing.T) {
	store := NewMemoryStore()

	if _, ok := store.GetPublisherSettings("user-1"); ok {
		t.Fatal("expected no settings before being set")
	}

	settings := service.PublisherSettings{
		UserID:           "user-1",
		SubscriptionMode: service.SubscriptionModeApproved,
		Visibility:       service.VisibilityPrivate,
	}
	if err := store.SetPublisherSettings(settings); err != nil {
		t.Fatalf("SetPublisherSettings returned error: %v", err)
	}

	got, ok := store.GetPublisherSettings("user-1")
	if !ok {
		t.Fatal("expected settings to exist")
	}
	if got.SubscriptionMode != service.SubscriptionModeApproved || got.Visibility != service.VisibilityPrivate {
		t.Fatalf("unexpected settings: %+v", got)
	}
}

func TestMemoryStoreSubscriptions(t *testing.T) {
	store := NewMemoryStore()
	createdAt := time.Now().UTC()

	sub := service.Subscription{
		SubscriberID: "viewer",
		PublisherID:  "publisher",
		Status:       service.SubscriptionStatusPending,
		CreatedAt:    createdAt,
	}
	if err := store.UpsertSubscription(sub); err != nil {
		t.Fatalf("UpsertSubscription returned error: %v", err)
	}

	got, ok := store.GetSubscription("viewer", "publisher")
	if !ok {
		t.Fatal("expected subscription to exist")
	}
	if got.Status != service.SubscriptionStatusPending {
		t.Fatalf("expected pending status, got %s", got.Status)
	}

	if err := store.SetSubscriptionStatus("viewer", "publisher", service.SubscriptionStatusActive); err != nil {
		t.Fatalf("SetSubscriptionStatus returned error: %v", err)
	}
	got, _ = store.GetSubscription("viewer", "publisher")
	if got.Status != service.SubscriptionStatusActive {
		t.Fatalf("expected active status, got %s", got.Status)
	}

	subs := store.ListSubscriptionsForSubscriber("viewer")
	if len(subs) != 1 || subs[0].PublisherID != "publisher" {
		t.Fatalf("expected 1 subscription for subscriber, got %+v", subs)
	}

	active := store.ListSubscriptionsForPublisher("publisher", service.SubscriptionStatusActive)
	if len(active) != 1 || active[0].SubscriberID != "viewer" {
		t.Fatalf("expected 1 active subscription for publisher, got %+v", active)
	}
	pending := store.ListSubscriptionsForPublisher("publisher", service.SubscriptionStatusPending)
	if len(pending) != 0 {
		t.Fatalf("expected no pending subscriptions, got %+v", pending)
	}

	// Upserting again overrides the status.
	sub.Status = service.SubscriptionStatusPending
	if err := store.UpsertSubscription(sub); err != nil {
		t.Fatalf("UpsertSubscription returned error: %v", err)
	}
	got, _ = store.GetSubscription("viewer", "publisher")
	if got.Status != service.SubscriptionStatusPending {
		t.Fatalf("expected pending status after upsert, got %s", got.Status)
	}

	store.DeleteSubscription("viewer", "publisher")
	if _, ok := store.GetSubscription("viewer", "publisher"); ok {
		t.Fatal("expected subscription to be deleted")
	}
}
