package repository

import (
	"testing"
	"time"

	"twitch_room/internal/service"
)

func TestMemoryStoreUpsertAndGet(t *testing.T) {
	store := NewMemoryStore()
	updatedAt := time.Now().UTC()

	state, err := store.Upsert("user-1", []service.Stream{{ID: "stream-1"}, {ID: "stream-2"}}, updatedAt)
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
	if len(got.Streams) != 2 {
		t.Fatalf("expected 2 enriched streams, got %d", len(got.Streams))
	}
}

func TestMemoryStoreList(t *testing.T) {
	store := NewMemoryStore()
	// Upsert user-1 and check for success/failure
	_, err1 := store.Upsert("user-1", []service.Stream{{ID: "stream-1"}}, time.Now().UTC())
	if err1 != nil {
		t.Fatalf("expected user-1 to be upserted: %v", err1)
	}

	// Upsert user-2 and check for success/failure
	_, err2 := store.Upsert("user-2", []service.Stream{{ID: "stream-2"}}, time.Now().UTC())
	if err2 != nil {
		t.Fatalf("expected user-2 to be upserted: %v", err2)
	}

	states := store.List(10, 0)
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

func TestMemoryStoreDeleteUserData(t *testing.T) {
	store := NewMemoryStore()
	userID := "user-1"

	// Setup data
	store.Upsert(userID, []service.Stream{{ID: "s1"}}, time.Now())
	store.SetPublisherSettings(service.PublisherSettings{UserID: userID})
	store.UpsertSubscription(service.Subscription{SubscriberID: userID, PublisherID: "p1"})
	store.UpsertSubscription(service.Subscription{SubscriberID: "p2", PublisherID: userID})

	store.DeleteUserData(userID)

	if _, ok := store.Get(userID); ok {
		t.Error("expected user watch state to be deleted")
	}
	if _, ok := store.GetPublisherSettings(userID); ok {
		t.Error("expected publisher settings to be deleted")
	}
	if len(store.ListSubscriptionsForSubscriber(userID)) != 0 {
		t.Error("expected subscriptions as subscriber to be deleted")
	}
	if len(store.ListSubscriptionsForPublisher(userID, service.SubscriptionStatusActive)) != 0 {
		t.Error("expected subscriptions as publisher to be deleted")
	}
}
