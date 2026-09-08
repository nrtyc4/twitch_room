package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeEnricher struct {
	info  map[string]StreamInfo
	users map[string]string
	err   error
}

func (f *fakeEnricher) LookupStreams(ctx context.Context, logins []string) (map[string]StreamInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]StreamInfo, len(logins))
	for _, login := range logins {
		out[login] = f.info[login]
	}
	return out, nil
}

func (f *fakeEnricher) ResolveUsers(ctx context.Context, userIDs []string) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]string, len(userIDs))
	for _, id := range userIDs {
		if name, ok := f.users[id]; ok {
			out[id] = name
		}
	}
	return out, nil
}

// bareEnricher satisfies StreamEnricher but not UserResolver, modelling an
// enricher that cannot resolve publisher display names.
type bareEnricher struct {
	info map[string]StreamInfo
}

func (e *bareEnricher) LookupStreams(ctx context.Context, logins []string) (map[string]StreamInfo, error) {
	out := make(map[string]StreamInfo, len(logins))
	for _, login := range logins {
		out[login] = e.info[login]
	}
	return out, nil
}

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

	if _, err := svc.Subscribe("viewer", "user-1"); err != nil {
		t.Fatalf("subscribe to user-1 failed: %v", err)
	}
	if _, err := svc.Subscribe("viewer", "user-2"); err != nil {
		t.Fatalf("subscribe to user-2 failed: %v", err)
	}

	feed := svc.ListFeed("viewer", 10)
	if len(feed) != 2 {
		t.Fatalf("expected 2 feed entries, got %d", len(feed))
	}
}

func TestListFeedOnlyShowsSubscriptions(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	_, _ = svc.SetUserWatchState("user-1", []string{"stream-1"})
	_, _ = svc.SetUserWatchState("user-2", []string{"stream-2"})

	// No subscriptions yet - feed should be empty.
	feed := svc.ListFeed("viewer", 10)
	if len(feed) != 0 {
		t.Fatalf("expected empty feed without subscriptions, got %d entries", len(feed))
	}

	// Subscribe to only user-2.
	if _, err := svc.Subscribe("viewer", "user-2"); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	feed = svc.ListFeed("viewer", 10)
	if len(feed) != 1 || feed[0].UserID != "user-2" {
		t.Fatalf("expected only user-2 in feed, got %+v", feed)
	}
}

func TestSubscribeOpenMode(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	sub, err := svc.Subscribe("viewer", "publisher")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if sub.Status != SubscriptionStatusActive {
		t.Fatalf("expected active subscription for open mode, got %s", sub.Status)
	}

	// Re-subscribe should remain active.
	sub, err = svc.Subscribe("viewer", "publisher")
	if err != nil {
		t.Fatalf("re-subscribe failed: %v", err)
	}
	if sub.Status != SubscriptionStatusActive {
		t.Fatalf("expected active subscription after re-subscribe, got %s", sub.Status)
	}
}

func TestSubscribeToSelfFails(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	if _, err := svc.Subscribe("user-1", "user-1"); err != ErrCannotSubscribeToSelf {
		t.Fatalf("expected ErrCannotSubscribeToSelf, got %v", err)
	}
}

func TestSubscribeApprovedMode(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	_, err := svc.UpdatePublisherSettings(PublisherSettings{
		UserID:           "publisher",
		SubscriptionMode: SubscriptionModeApproved,
		Visibility:       VisibilitySubscribers,
	})
	if err != nil {
		t.Fatalf("update settings failed: %v", err)
	}

	sub, err := svc.Subscribe("viewer", "publisher")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if sub.Status != SubscriptionStatusPending {
		t.Fatalf("expected pending subscription for approved mode, got %s", sub.Status)
	}

	// Pending subscribers do not see the publisher in the feed.
	_, _ = svc.SetUserWatchState("publisher", []string{"stream-1"})
	feed := svc.ListFeed("viewer", 10)
	if len(feed) != 0 {
		t.Fatalf("expected empty feed until approved, got %d entries", len(feed))
	}

	// Publisher approves the viewer.
	pending, err := svc.ListPendingRequests("publisher")
	if err != nil {
		t.Fatalf("list pending failed: %v", err)
	}
	if len(pending) != 1 || pending[0].SubscriberID != "viewer" {
		t.Fatalf("expected 1 pending request from viewer, got %+v", pending)
	}

	if err := svc.RespondToSubscriber("publisher", "viewer", true); err != nil {
		t.Fatalf("approve failed: %v", err)
	}

	feed = svc.ListFeed("viewer", 10)
	if len(feed) != 1 || feed[0].UserID != "publisher" {
		t.Fatalf("expected publisher in feed after approval, got %+v", feed)
	}
}

func TestRespondToSubscriberRejectsRemovesRequest(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	_, _ = svc.UpdatePublisherSettings(PublisherSettings{
		UserID:           "publisher",
		SubscriptionMode: SubscriptionModeApproved,
		Visibility:       VisibilitySubscribers,
	})

	if _, err := svc.Subscribe("viewer", "publisher"); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	if err := svc.RespondToSubscriber("publisher", "viewer", false); err != nil {
		t.Fatalf("reject failed: %v", err)
	}

	// Approving again should fail since the request was removed.
	if err := svc.RespondToSubscriber("publisher", "viewer", true); err != ErrNotSubscribed {
		t.Fatalf("expected ErrNotSubscribed after rejection, got %v", err)
	}
}

func TestCanViewVisibility(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	// Self always allowed.
	if ok, _ := svc.CanView("user-1", "user-1"); !ok {
		t.Fatalf("expected self to be allowed")
	}

	// Default is subscribers-only: not allowed without a subscription.
	if ok, _ := svc.CanView("viewer", "publisher"); ok {
		t.Fatalf("expected no access to subscribers-only without subscription")
	}

	// Public allows anyone.
	_, _ = svc.UpdatePublisherSettings(PublisherSettings{
		UserID:           "publisher",
		SubscriptionMode: SubscriptionModeOpen,
		Visibility:       VisibilityPublic,
	})
	if ok, _ := svc.CanView("viewer", "publisher"); !ok {
		t.Fatalf("expected public visibility to allow access")
	}

	// Private denies even subscribed viewers.
	_, _ = svc.UpdatePublisherSettings(PublisherSettings{
		UserID:           "publisher",
		SubscriptionMode: SubscriptionModeOpen,
		Visibility:       VisibilityPrivate,
	})
	if _, err := svc.Subscribe("viewer", "publisher"); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}
	if ok, _ := svc.CanView("viewer", "publisher"); ok {
		t.Fatalf("expected private visibility to deny access")
	}

	// Subscribers-only allows active subscribers only.
	_, _ = svc.UpdatePublisherSettings(PublisherSettings{
		UserID:           "publisher",
		SubscriptionMode: SubscriptionModeOpen,
		Visibility:       VisibilitySubscribers,
	})
	if ok, _ := svc.CanView("viewer", "publisher"); !ok {
		t.Fatalf("expected active subscriber to have access")
	}
	if ok, _ := svc.CanView("stranger", "publisher"); ok {
		t.Fatalf("expected non-subscriber to be denied")
	}
}

func TestPrivatePublisherNotInFeed(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	_, _ = svc.UpdatePublisherSettings(PublisherSettings{
		UserID:           "publisher",
		SubscriptionMode: SubscriptionModeOpen,
		Visibility:       VisibilityPrivate,
	})
	_, _ = svc.SetUserWatchState("publisher", []string{"stream-1"})
	if _, err := svc.Subscribe("viewer", "publisher"); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	feed := svc.ListFeed("viewer", 10)
	if len(feed) != 0 {
		t.Fatalf("expected private publisher to be excluded from feed, got %d entries", len(feed))
	}
}

func TestUnsubscribe(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	if _, err := svc.Subscribe("viewer", "publisher"); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	if err := svc.Unsubscribe("viewer", "publisher"); err != nil {
		t.Fatalf("unsubscribe failed: %v", err)
	}

	if err := svc.Unsubscribe("viewer", "publisher"); err != ErrNotSubscribed {
		t.Fatalf("expected ErrNotSubscribed, got %v", err)
	}

	if _, err := svc.ListSubscriptions("viewer"); err != nil {
		t.Fatalf("list subscriptions failed: %v", err)
	}
	subs, _ := svc.ListSubscriptions("viewer")
	if len(subs) != 0 {
		t.Fatalf("expected no subscriptions after unsubscribe, got %d", len(subs))
	}
}

func TestUpdatePublisherSettingsValidation(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewService(repo, time.Minute)

	_, err := svc.UpdatePublisherSettings(PublisherSettings{
		UserID:           "user-1",
		SubscriptionMode: "banana",
		Visibility:       VisibilityPublic,
	})
	if err != ErrInvalidSettings {
		t.Fatalf("expected ErrInvalidSettings for bad mode, got %v", err)
	}

	_, err = svc.UpdatePublisherSettings(PublisherSettings{
		UserID:           "user-1",
		SubscriptionMode: SubscriptionModeOpen,
		Visibility:       "banana",
	})
	if err != ErrInvalidSettings {
		t.Fatalf("expected ErrInvalidSettings for bad visibility, got %v", err)
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

func TestSetUserWatchStateEnriches(t *testing.T) {
	repo := NewMemoryRepository()
	enricher := &fakeEnricher{
		info: map[string]StreamInfo{
			"stream-1": {Exists: true, IsLive: true, ChannelName: "Stream One", Title: "great content", GameName: "Chess", ThumbnailURL: "https://thumb", ViewerCount: 7},
			"stream-2": {Exists: true, IsLive: false, ChannelName: "Stream Two"},
		},
	}
	svc := NewServiceWithEnricher(repo, time.Minute, enricher)

	state, err := svc.SetUserWatchState("user-1", []string{"stream-1", "stream-2"})
	if err != nil {
		t.Fatalf("SetUserWatchState returned error: %v", err)
	}

	if len(state.Streams) != 2 {
		t.Fatalf("expected 2 enriched streams, got %d", len(state.Streams))
	}
	byID := map[string]Stream{}
	for _, st := range state.Streams {
		byID[st.ID] = st
	}

	s1 := byID["stream-1"]
	if !s1.IsLive || s1.Title != "great content" || s1.GameName != "Chess" || s1.ViewerCount != 7 {
		t.Fatalf("unexpected enriched stream-1: %+v", s1)
	}
	if s1.ThumbnailURL != "https://thumb" {
		t.Fatalf("unexpected thumbnail: %q", s1.ThumbnailURL)
	}

	s2 := byID["stream-2"]
	if s2.IsLive || s2.ChannelName != "Stream Two" {
		t.Fatalf("unexpected enriched stream-2: %+v", s2)
	}

	if len(state.StreamIDs) != 2 {
		t.Fatalf("expected stream_ids populated too, got %v", state.StreamIDs)
	}
}

func TestSetUserWatchStateRejectsUnknownChannel(t *testing.T) {
	repo := NewMemoryRepository()
	enricher := &fakeEnricher{
		info: map[string]StreamInfo{
			"stream-1": {Exists: true, ChannelName: "Stream One"},
		},
	}
	svc := NewServiceWithEnricher(repo, time.Minute, enricher)

	_, err := svc.SetUserWatchState("user-1", []string{"stream-1", "does-not-exist"})
	if !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound, got %v", err)
	}

	if _, ok := repo.GetUserWatchState("user-1"); ok {
		t.Fatal("expected watch state to not be stored on rejection")
	}
}

func TestSetUserWatchStateDegradesOnEnricherError(t *testing.T) {
	repo := NewMemoryRepository()
	enricher := &fakeEnricher{err: errors.New("twitch down")}
	svc := NewServiceWithEnricher(repo, time.Minute, enricher)

	state, err := svc.SetUserWatchState("user-1", []string{"stream-1"})
	if err != nil {
		t.Fatalf("expected graceful fallback, got error: %v", err)
	}

	if len(state.Streams) != 1 || state.Streams[0].ID != "stream-1" {
		t.Fatalf("unexpected fallback state: %+v", state)
	}
	if state.Streams[0].IsLive {
		t.Fatal("expected no live metadata on fallback")
	}
}

func TestListFeedUsesEnrichedStreams(t *testing.T) {
	repo := NewMemoryRepository()
	enricher := &fakeEnricher{
		info: map[string]StreamInfo{
			"stream-1": {Exists: true, IsLive: true, ChannelName: "Stream One", Title: "title-1", GameName: "GameX", ViewerCount: 3},
		},
	}
	svc := NewServiceWithEnricher(repo, time.Minute, enricher)

	_, _ = svc.SetUserWatchState("user-1", []string{"stream-1"})
	if _, err := svc.Subscribe("viewer", "user-1"); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	feed := svc.ListFeed("viewer", 10)
	if len(feed) != 1 {
		t.Fatalf("expected 1 feed entry, got %d", len(feed))
	}
	entry := feed[0]
	if len(entry.Streams) != 1 {
		t.Fatalf("expected 1 stream in feed entry, got %d", len(entry.Streams))
	}
	if entry.Streams[0].Title != "title-1" || entry.Streams[0].GameName != "GameX" {
		t.Fatalf("unexpected enriched feed stream: %+v", entry.Streams[0])
	}
}

func TestListFeedUsesPublisherDisplayNames(t *testing.T) {
	repo := NewMemoryRepository()
	enricher := &fakeEnricher{
		info: map[string]StreamInfo{
			"stream-1": {Exists: true, ChannelName: "Stream One"},
		},
		users: map[string]string{"publisher-a": "Publisher A"},
	}
	svc := NewServiceWithEnricher(repo, time.Minute, enricher)

	_, _ = svc.SetUserWatchState("publisher-a", []string{"stream-1"})
	if _, err := svc.Subscribe("viewer", "publisher-a"); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	feed := svc.ListFeed("viewer", 10)
	if len(feed) != 1 {
		t.Fatalf("expected 1 feed entry, got %d", len(feed))
	}
	if feed[0].DisplayName != "Publisher A" {
		t.Fatalf("expected resolved display name, got %q", feed[0].DisplayName)
	}
}

func TestListFeedFallsBackToRawUserID(t *testing.T) {
	repo := NewMemoryRepository()
	// Resolver returns an empty map for this publisher.
	enricher := &fakeEnricher{
		info:  map[string]StreamInfo{"stream-1": {Exists: true, ChannelName: "Stream One"}},
		users: map[string]string{},
	}
	svc := NewServiceWithEnricher(repo, time.Minute, enricher)

	_, _ = svc.SetUserWatchState("publisher-a", []string{"stream-1"})
	if _, err := svc.Subscribe("viewer", "publisher-a"); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	feed := svc.ListFeed("viewer", 10)
	if feed[0].DisplayName != "publisher-a" {
		t.Fatalf("expected raw user id fallback, got %q", feed[0].DisplayName)
	}
}

func TestListFeedWithoutUserResolverUsesRawID(t *testing.T) {
	repo := NewMemoryRepository()
	svc := NewServiceWithEnricher(repo, time.Minute, &bareEnricher{
		info: map[string]StreamInfo{"stream-1": {Exists: true}},
	})

	_, _ = svc.SetUserWatchState("publisher-a", []string{"stream-1"})
	if _, err := svc.Subscribe("viewer", "publisher-a"); err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	feed := svc.ListFeed("viewer", 10)
	if feed[0].DisplayName != "publisher-a" {
		t.Fatalf("expected raw user id without resolver, got %q", feed[0].DisplayName)
	}
}

func TestNormalizeStreamIDs(t *testing.T) {
	got := normalizeStreamIDs([]string{"Shroud", "shroud", "  ", "https://twitch.tv/xqc", "xqc", "https://www.twitch.tv/lirik"})
	expected := []string{"shroud", "xqc", "lirik"}
	if len(got) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Fatalf("expected %v, got %v", expected, got)
		}
	}
}

func TestRefreshWatchStatesSkipsUnchanged(t *testing.T) {
	repo := NewMemoryRepository()
	enricher := &fakeEnricher{
		info: map[string]StreamInfo{
			"stream-1": {Exists: true, IsLive: true, ChannelName: "Stream One", Title: "t", ViewerCount: 3},
		},
	}
	svc := NewServiceWithEnricher(repo, time.Hour, enricher)

	if _, err := svc.SetUserWatchState("user-1", []string{"stream-1"}); err != nil {
		t.Fatalf("SetUserWatchState: %v", err)
	}

	refreshed, err := svc.RefreshWatchStates(context.Background())
	if err != nil {
		t.Fatalf("RefreshWatchStates: %v", err)
	}
	if len(refreshed) != 0 {
		t.Fatalf("expected no refreshes when metadata is unchanged, got %v", refreshed)
	}
}

func TestRefreshWatchStatesUpdatesMetadata(t *testing.T) {
	repo := NewMemoryRepository()
	enricher := &fakeEnricher{
		info: map[string]StreamInfo{
			"stream-1": {Exists: true, IsLive: false, ChannelName: "Stream One"},
		},
	}
	svc := NewServiceWithEnricher(repo, time.Hour, enricher)

	state, err := svc.SetUserWatchState("user-1", []string{"stream-1"})
	if err != nil {
		t.Fatalf("SetUserWatchState: %v", err)
	}

	// Channel goes live with a title between publish and refresh.
	enricher.info["stream-1"] = StreamInfo{Exists: true, IsLive: true, ChannelName: "Stream One", Title: "now live", GameName: "Chess", ViewerCount: 9}

	refreshed, err := svc.RefreshWatchStates(context.Background())
	if err != nil {
		t.Fatalf("RefreshWatchStates: %v", err)
	}
	if len(refreshed) != 1 || refreshed[0] != "user-1" {
		t.Fatalf("expected user-1 refreshed, got %v", refreshed)
	}

	got, _ := repo.GetUserWatchState("user-1")
	if len(got.Streams) != 1 {
		t.Fatalf("expected 1 stream, got %d", len(got.Streams))
	}
	s := got.Streams[0]
	if !s.IsLive || s.Title != "now live" || s.GameName != "Chess" || s.ViewerCount != 9 {
		t.Fatalf("expected refreshed live metadata, got %+v", s)
	}
	if !got.UpdatedAt.Equal(state.UpdatedAt) {
		t.Fatal("refresh must not extend the state's lifetime (updated_at preserved)")
	}
}

func TestRefreshWatchStatesClearsOfflineMetadata(t *testing.T) {
	repo := NewMemoryRepository()
	enricher := &fakeEnricher{
		info: map[string]StreamInfo{
			"stream-1": {Exists: true, IsLive: true, ChannelName: "Stream One", Title: "old title", ViewerCount: 5},
		},
	}
	svc := NewServiceWithEnricher(repo, time.Hour, enricher)

	if _, err := svc.SetUserWatchState("user-1", []string{"stream-1"}); err != nil {
		t.Fatalf("SetUserWatchState: %v", err)
	}

	// Channel goes offline.
	enricher.info["stream-1"] = StreamInfo{Exists: true, IsLive: false, ChannelName: "Stream One"}

	if _, err := svc.RefreshWatchStates(context.Background()); err != nil {
		t.Fatalf("RefreshWatchStates: %v", err)
	}

	got, _ := repo.GetUserWatchState("user-1")
	s := got.Streams[0]
	if s.IsLive || s.Title != "" || s.ViewerCount != 0 {
		t.Fatalf("expected offline metadata cleared, got %+v", s)
	}
}

func TestRefreshWatchStatesPreservesMissingChannels(t *testing.T) {
	repo := NewMemoryRepository()
	enricher := &fakeEnricher{
		info: map[string]StreamInfo{
			"stream-1": {Exists: true, IsLive: true, ChannelName: "Stream One", Title: "live title", ViewerCount: 3},
		},
	}
	svc := NewServiceWithEnricher(repo, time.Hour, enricher)

	if _, err := svc.SetUserWatchState("user-1", []string{"stream-1"}); err != nil {
		t.Fatalf("SetUserWatchState: %v", err)
	}

	// The channel stops resolving entirely.
	enricher.info = map[string]StreamInfo{}

	if _, err := svc.RefreshWatchStates(context.Background()); err != nil {
		t.Fatalf("RefreshWatchStates: %v", err)
	}

	got, _ := repo.GetUserWatchState("user-1")
	s := got.Streams[0]
	if !s.IsLive || s.Title != "live title" {
		t.Fatalf("expected previous metadata preserved for missing channel, got %+v", s)
	}
}

func TestRefreshWatchStatesDeletesExpired(t *testing.T) {
	repo := NewMemoryRepository()
	enricher := &fakeEnricher{
		info: map[string]StreamInfo{
			"stream-1": {Exists: true},
			"stream-2": {Exists: true},
		},
	}
	svc := NewServiceWithEnricher(repo, 10*time.Millisecond, enricher)

	if _, err := svc.SetUserWatchState("user-1", []string{"stream-1"}); err != nil {
		t.Fatalf("SetUserWatchState: %v", err)
	}
	if _, err := svc.SetUserWatchState("user-2", []string{"stream-2"}); err != nil {
		t.Fatalf("SetUserWatchState: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	refreshed, err := svc.RefreshWatchStates(context.Background())
	if err != nil {
		t.Fatalf("RefreshWatchStates: %v", err)
	}
	if len(refreshed) != 0 {
		t.Fatalf("expected no refreshed states, got %v", refreshed)
	}
	if _, ok := repo.GetUserWatchState("user-1"); ok {
		t.Fatal("expected expired user-1 to be removed")
	}
	if _, ok := repo.GetUserWatchState("user-2"); ok {
		t.Fatal("expected expired user-2 to be removed")
	}
}

func TestRefreshWatchStatesDelegatesLookupError(t *testing.T) {
	repo := NewMemoryRepository()
	enricher := &fakeEnricher{err: errors.New("twitch down")}
	svc := NewServiceWithEnricher(repo, time.Hour, enricher)

	if _, err := svc.SetUserWatchState("user-1", []string{"stream-1"}); err != nil {
		t.Fatalf("SetUserWatchState: %v", err)
	}

	if _, err := svc.RefreshWatchStates(context.Background()); err == nil {
		t.Fatal("expected lookup error to propagate")
	}
	// State must be untouched on failure.
	got, ok := repo.GetUserWatchState("user-1")
	if !ok {
		t.Fatal("expected state to remain")
	}
	for _, s := range got.Streams {
		if s.IsLive {
			t.Fatal("expected no metadata change on refresh error")
		}
	}
}
