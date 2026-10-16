package twitchapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeHelix struct {
	mu            sync.Mutex
	tokenCalls    int32
	usersCalls    int32
	usersData     []helixUser
	selfData      []helixUser
	streams       []helixStream
	followed      []FollowedStream
	followedQuery string
	lastAuth      string
}

func (f *fakeHelix) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth2/token":
			f.mu.Lock()
			f.tokenCalls++
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"access_token": "test-token", "expires_in": 3600})
		case r.URL.Path == "/users":
			f.mu.Lock()
			f.usersCalls++
			f.lastAuth = r.Header.Get("Authorization")
			want := loginSet(r.URL.Query()["login"])
			data := make([]helixUser, 0, len(f.usersData))
			if len(want) == 0 {
				// Authenticated self-lookup returns the requesting user.
				data = append(data, f.selfData...)
			} else {
				for _, u := range f.usersData {
					if want[strings.ToLower(u.Login)] {
						data = append(data, u)
					}
				}
			}
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"data": data})
		case r.URL.Path == "/streams":
			w.Header().Set("Content-Type", "application/json")
			want := loginSet(r.URL.Query()["user_login"])
			data := make([]helixStream, 0, len(f.streams))
			for _, st := range f.streams {
				if want[strings.ToLower(st.UserLogin)] {
					data = append(data, st)
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
		case r.URL.Path == "/streams/followed":
			f.mu.Lock()
			f.followedQuery = r.URL.Query().Get("user_id")
			f.lastAuth = r.Header.Get("Authorization")
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"data": f.followed})
		default:
			http.NotFound(w, r)
		}
	})
}

// loginSet normalizes a list of query filter values into a lowercase lookup set.
func loginSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, v := range values {
		set[strings.ToLower(strings.TrimSpace(v))] = true
	}
	return set
}

func newTestClient(t *testing.T, f *fakeHelix) (*Client, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(f.handler(t))
	t.Cleanup(ts.Close)

	c := NewClient("client-id", "client-secret")
	c.httpClient = ts.Client()
	c.tokenEndpoint = ts.URL + "/oauth2/token"
	c.apiBase = ts.URL
	return c, ts
}

func TestLookupStreams(t *testing.T) {
	f := &fakeHelix{
		usersData: []helixUser{
			{ID: "1", Login: "shroud", DisplayName: "shroud"},
			{ID: "2", Login: "xqc", DisplayName: "xQc"},
		},
		streams: []helixStream{
			{UserID: "1", UserLogin: "shroud", UserName: "shroud", Title: "valorant grind", GameName: "VALORANT", ViewerCount: 42, ThumbnailURL: "https://static-cdn.jtvnw.net/stream_defaults/{width}x{height}.jpg"},
		},
	}
	c, _ := newTestClient(t, f)

	info, err := c.LookupStreams(context.Background(), []string{"shroud", "xqc", "ghost"})
	if err != nil {
		t.Fatalf("LookupStreams returned error: %v", err)
	}

	shroud, ok := info["shroud"]
	if !ok || !shroud.Exists {
		t.Fatalf("expected shroud to exist, got %+v", shroud)
	}
	if !shroud.IsLive {
		t.Fatal("expected shroud to be live")
	}
	if shroud.ChannelName != "shroud" {
		t.Fatalf("expected channel name shroud, got %q", shroud.ChannelName)
	}
	if shroud.Title != "valorant grind" {
		t.Fatalf("expected title, got %q", shroud.Title)
	}
	if shroud.GameName != "VALORANT" {
		t.Fatalf("expected game VALORANT, got %q", shroud.GameName)
	}
	if shroud.ViewerCount != 42 {
		t.Fatalf("expected 42 viewers, got %d", shroud.ViewerCount)
	}
	if !strings.Contains(shroud.ThumbnailURL, "320x180") {
		t.Fatalf("expected sized thumbnail, got %q", shroud.ThumbnailURL)
	}

	xqc, ok := info["xqc"]
	if !ok || !xqc.Exists {
		t.Fatalf("expected xqc to exist, got %+v", xqc)
	}
	if xqc.IsLive {
		t.Fatal("expected xqc to be offline")
	}
	if xqc.ChannelName != "xQc" {
		t.Fatalf("expected display name xQc, got %q", xqc.ChannelName)
	}

	ghost, ok := info["ghost"]
	if !ok {
		t.Fatal("expected ghost key present")
	}
	if ghost.Exists {
		t.Fatal("expected ghost to not exist")
	}
}

func TestLookupStreamsDeduplicatesAndLowercases(t *testing.T) {
	f := &fakeHelix{
		usersData: []helixUser{
			{ID: "1", Login: "shroud", DisplayName: "Shroud"},
		},
	}
	c, _ := newTestClient(t, f)

	info, err := c.LookupStreams(context.Background(), []string{"Shroud", "shroud", " SHROUD "})
	if err != nil {
		t.Fatalf("LookupStreams returned error: %v", err)
	}
	if len(info) != 1 {
		t.Fatalf("expected 1 unique login, got %d", len(info))
	}
	shroud, ok := info["shroud"]
	if !ok || !shroud.Exists {
		t.Fatalf("expected shroud to exist, got %+v", shroud)
	}
}

func TestLookupStreamsRejectsNonexistent(t *testing.T) {
	f := &fakeHelix{}
	c, _ := newTestClient(t, f)

	info, err := c.LookupStreams(context.Background(), []string{"noonehere"})
	if err != nil {
		t.Fatalf("LookupStreams returned error: %v", err)
	}
	if info["noonehere"].Exists {
		t.Fatal("expected login to not exist")
	}
}

func TestResolveUsers(t *testing.T) {
	f := &fakeHelix{
		usersData: []helixUser{
			{ID: "1", Login: "shroud", DisplayName: "Shroud"},
			{ID: "2", Login: "xqc", DisplayName: "xQc"},
		},
	}
	c, _ := newTestClient(t, f)

	names, err := c.ResolveUsers(context.Background(), []string{"Shroud", "shroud", "ghost"})
	if err != nil {
		t.Fatalf("ResolveUsers returned error: %v", err)
	}
	if names["shroud"].DisplayName != "Shroud" {
		t.Fatalf("expected shroud -> Shroud, got %+v", names["shroud"])
	}
	if names["xqc"].DisplayName != "" {
		// xqc wasn't queried: not in this call's input.
		t.Fatalf("expected no xqc entry, got %+v", names["xqc"])
	}
	if _, ok := names["ghost"]; ok {
		t.Fatal("expected unresolvable id to be absent")
	}
}

func TestResolveUsersCachesResults(t *testing.T) {
	f := &fakeHelix{
		usersData: []helixUser{
			{ID: "1", Login: "shroud", DisplayName: "Shroud"},
			{ID: "2", Login: "stronghold", DisplayName: "stronghole"},
		},
	}
	c, _ := newTestClient(t, f)

	if _, err := c.ResolveUsers(context.Background(), []string{"shroud", "stronghold"}); err != nil {
		t.Fatalf("first ResolveUsers: %v", err)
	}
	if _, err := c.ResolveUsers(context.Background(), []string{"shroud", "stronghold"}); err != nil {
		t.Fatalf("second ResolveUsers: %v", err)
	}

	f.mu.Lock()
	calls := f.usersCalls
	f.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 users call (cached), got %d", calls)
	}
}

func TestResolveUsersCacheRevalidatesAfterTTL(t *testing.T) {
	f := &fakeHelix{
		usersData: []helixUser{{ID: "1", Login: "shroud", DisplayName: "Shroud"}},
	}
	c, _ := newTestClient(t, f)

	if _, err := c.ResolveUsers(context.Background(), []string{"shroud"}); err != nil {
		t.Fatalf("first ResolveUsers: %v", err)
	}

	c.usersMu.Lock()
	entry := c.userCache["shroud"]
	entry.fetchedAt = entry.fetchedAt.Add(-(userCacheTTL + time.Hour))
	c.userCache["shroud"] = entry
	c.usersMu.Unlock()

	if _, err := c.ResolveUsers(context.Background(), []string{"shroud"}); err != nil {
		t.Fatalf("second ResolveUsers: %v", err)
	}

	f.mu.Lock()
	calls := f.usersCalls
	f.mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected 2 users calls after TTL expiry, got %d", calls)
	}
}

func TestAccessTokenCachedAndRefreshedOnExpiry(t *testing.T) {
	f := &fakeHelix{
		usersData: []helixUser{{ID: "1", Login: "shroud", DisplayName: "shroud"}},
	}
	c, _ := newTestClient(t, f)

	if _, err := c.LookupStreams(context.Background(), []string{"shroud"}); err != nil {
		t.Fatalf("first LookupStreams: %v", err)
	}
	if _, err := c.LookupStreams(context.Background(), []string{"shroud"}); err != nil {
		t.Fatalf("second LookupStreams: %v", err)
	}

	f.mu.Lock()
	calls := f.tokenCalls
	f.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 token fetch (cached), got %d", calls)
	}

	// Force expiry and confirm a refetch.
	c.tokenMu.Lock()
	c.expiresAt = c.expiresAt.Add(-time.Hour)
	c.tokenMu.Unlock()

	if _, err := c.LookupStreams(context.Background(), []string{"shroud"}); err != nil {
		t.Fatalf("LookupStreams after expiry: %v", err)
	}
	f.mu.Lock()
	calls = f.tokenCalls
	f.mu.Unlock()
	if calls != 2 {
		t.Fatalf("expected 2 token fetches after expiry, got %d", calls)
	}
}

func TestFollowedLiveStreams(t *testing.T) {
	f := &fakeHelix{
		selfData: []helixUser{{ID: "123", Login: "viewer1", DisplayName: "viewer1"}},
		followed: []FollowedStream{
			{UserLogin: "small", UserName: "small", Title: "off to a slow start", GameName: "Just Chatting", ViewerCount: 3, ThumbnailURL: "https://static-cdn.jtvnw.net/a/{width}x{height}.jpg"},
			{UserLogin: "big", UserName: "big", Title: "gran finale", GameName: "Chess", ViewerCount: 90210, ThumbnailURL: "https://static-cdn.jtvnw.net/b/{width}x{height}.jpg"},
		},
	}
	c, _ := newTestClient(t, f)

	streams, err := c.FollowedLiveStreams(context.Background(), "user-access-token")
	if err != nil {
		t.Fatalf("FollowedLiveStreams: %v", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.followedQuery != "123" {
		t.Fatalf("expected /streams/followed filtered by numeric user id, got %q", f.followedQuery)
	}
	if f.lastAuth != "Bearer user-access-token" {
		t.Fatalf("expected user access token in Authorization header, got %q", f.lastAuth)
	}

	if len(streams) != 2 {
		t.Fatalf("expected 2 followed streams, got %d", len(streams))
	}
	// Sorted by viewer count, most-watched first.
	if streams[0].UserLogin != "big" || streams[1].UserLogin != "small" {
		t.Fatalf("expected viewer-descending order, got %+v", streams)
	}
	if streams[0].ThumbnailURL != "https://static-cdn.jtvnw.net/b/320x180.jpg" {
		t.Fatalf("expected sized thumbnail, got %q", streams[0].ThumbnailURL)
	}
}

func TestFollowedLiveStreamsEmptyToken(t *testing.T) {
	c, _ := newTestClient(t, &fakeHelix{})
	streams, err := c.FollowedLiveStreams(context.Background(), "")
	if err != nil {
		t.Fatalf("FollowedLiveStreams with empty token: %v", err)
	}
	if len(streams) != 0 {
		t.Fatalf("expected no streams for empty token, got %+v", streams)
	}
}
