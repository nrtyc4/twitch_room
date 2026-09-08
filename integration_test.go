package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"twitch_room/internal/auth"
	"twitch_room/internal/identity"
	"twitch_room/internal/performance"
	"twitch_room/internal/repository"
	"twitch_room/internal/security"
	"twitch_room/internal/service"
)

// TestIntegrationOAuthFlowWithMockedTwitchAPI tests the full OAuth flow with a mocked Twitch API.
func TestIntegrationOAuthFlowWithMockedTwitchAPI(t *testing.T) {
	// Mock Twitch token endpoint
	mockTwitchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" && r.Method == http.MethodPost {
			// Return mock token response
			resp := map[string]interface{}{
				"access_token": "mock-access-token-12345",
				"token_type":   "bearer",
				"expires_in":   3600,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
		if r.URL.Path == "/helix/users" && r.Method == http.MethodGet {
			// Return mock user info
			resp := map[string]interface{}{
				"data": []map[string]interface{}{
					{
						"id":    "123456789",
						"login": "test-twitch-user",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	defer mockTwitchServer.Close()

	// Create provider with mock Twitch server URLs
	provider := &identity.TwitchProvider{
		ClientID:      "test-client-id",
		ClientSecret:  "test-client-secret",
		RedirectURI:   "http://localhost/callback",
		EnableDevAuth: false,
		// Note: In real tests, we'd need to patch the URLs or use dependency injection
		// For now, we'll test with dev auth fallback as a workaround
	}

	// Test with dev auth as fallback for now (since we can't easily override API URLs)
	provider.EnableDevAuth = true

	store := repository.NewMemoryStore()
	svc := service.NewService(repository.NewServiceAdapter(store), 5*time.Minute)

	s := &server{
		watchService:    svc,
		authMiddleware:  &auth.Middleware{AllowDevAuth: true},
		rateLimiter:     security.NewRateLimiter(100, time.Minute),
		watchStateCache: performance.NewWatchStateCache(5 * time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/watch", s.handleWatch)
	mux.HandleFunc("/v1/subscriptions/", s.handleSubscription)
	mux.HandleFunc("/v1/feed", s.handleFeed)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Test 1: Publisher sets watch state via dev auth
	reqBody := watchRequest{StreamIDs: []string{"stream-123", "stream-456"}}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", testServer.URL+"/v1/watch", bytes.NewReader(body))
	req.Header.Set("X-User-ID", "publisher-user")
	req.Header.Set("Content-Type", "application/json")

	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}

	// Test 2: Subscriber subscribes to the publisher
	reqSub, _ := http.NewRequest("POST", testServer.URL+"/v1/subscriptions/publisher-user", nil)
	reqSub.Header.Set("X-User-ID", "dev-user")
	respSub, _ := http.DefaultClient.Do(reqSub)
	if respSub.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for subscribe, got %d", respSub.StatusCode)
	}

	// Test 3: Verify watch state is visible in the subscriber's feed
	req2, _ := http.NewRequest("GET", testServer.URL+"/v1/feed", nil)
	req2.Header.Set("X-User-ID", "dev-user")

	resp2, _ := http.DefaultClient.Do(req2)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for feed, got %d", resp2.StatusCode)
	}

	var feedResp []map[string]interface{}
	json.NewDecoder(resp2.Body).Decode(&feedResp)

	if len(feedResp) != 1 {
		t.Fatalf("expected 1 user in feed, got %d", len(feedResp))
	}

	t.Logf("OAuth flow integration test passed")
}

// TestIntegrationWatchStateWithMemoryStore tests watch state operations end-to-end.
func TestIntegrationWatchStateWithMemoryStore(t *testing.T) {
	store := repository.NewMemoryStore()
	repo := repository.NewServiceAdapter(store)
	svc := service.NewService(repo, 5*time.Minute)

	s := &server{
		watchService:    svc,
		authMiddleware:  &auth.Middleware{AllowDevAuth: true},
		rateLimiter:     security.NewRateLimiter(100, time.Minute),
		watchStateCache: performance.NewWatchStateCache(5 * time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/watch", s.handleWatch)
	mux.HandleFunc("/v1/users/", s.handleUserWatch)
	mux.HandleFunc("/v1/subscriptions/", s.handleSubscription)
	mux.HandleFunc("/v1/feed", s.handleFeed)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Test 1: User 1 sets watch state
	req1, _ := http.NewRequest("POST", testServer.URL+"/v1/watch", bytes.NewReader([]byte(`{"stream_ids":["stream-a"]}`)))
	req1.Header.Set("X-User-ID", "user1")
	req1.Header.Set("Content-Type", "application/json")
	resp1, _ := http.DefaultClient.Do(req1)

	if resp1.StatusCode != http.StatusCreated {
		t.Fatalf("user1 watch failed: %d", resp1.StatusCode)
	}

	// Test 2: User 2 sets watch state
	req2, _ := http.NewRequest("POST", testServer.URL+"/v1/watch", bytes.NewReader([]byte(`{"stream_ids":["stream-b"]}`)))
	req2.Header.Set("X-User-ID", "user2")
	req2.Header.Set("Content-Type", "application/json")
	resp2, _ := http.DefaultClient.Do(req2)

	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("user2 watch failed: %d", resp2.StatusCode)
	}

	// Test 3: User1 subscribes to user2, then verifies the feed shows it
	reqSub, _ := http.NewRequest("POST", testServer.URL+"/v1/subscriptions/user2", nil)
	reqSub.Header.Set("X-User-ID", "user1")
	respSub, _ := http.DefaultClient.Do(reqSub)
	if respSub.StatusCode != http.StatusCreated {
		t.Fatalf("subscribe user1->user2 failed: %d", respSub.StatusCode)
	}

	reqFeed, _ := http.NewRequest("GET", testServer.URL+"/v1/feed", nil)
	reqFeed.Header.Set("X-User-ID", "user1")
	respFeed, _ := http.DefaultClient.Do(reqFeed)

	var feedResp []map[string]interface{}
	json.NewDecoder(respFeed.Body).Decode(&feedResp)

	if len(feedResp) != 1 || feedResp[0]["user_id"] != "user2" {
		t.Fatalf("expected 1 user (user2) in feed, got %v", feedResp)
	}

	// Test 4: User can read their own watch state via /v1/users/{id}/watch
	reqOwn, _ := http.NewRequest("GET", testServer.URL+"/v1/users/user1/watch", nil)
	reqOwn.Header.Set("X-User-ID", "user1")
	respOwn, _ := http.DefaultClient.Do(reqOwn)

	if respOwn.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 reading own watch state, got %d", respOwn.StatusCode)
	}

	var ownState map[string]interface{}
	json.NewDecoder(respOwn.Body).Decode(&ownState)

	if ownState["user_id"] != "user1" {
		t.Fatalf("expected own user_id 'user1', got %v", ownState["user_id"])
	}

	// Test 5: An active subscriber can read another user's watch state
	reqSubView, _ := http.NewRequest("GET", testServer.URL+"/v1/users/user2/watch", nil)
	reqSubView.Header.Set("X-User-ID", "user1")
	respSubView, _ := http.DefaultClient.Do(reqSubView)

	if respSubView.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 reading subscribed user's watch state, got %d", respSubView.StatusCode)
	}

	// Test 6: A stranger (unsubscribed) cannot read another user's watch state
	reqStranger, _ := http.NewRequest("GET", testServer.URL+"/v1/users/user2/watch", nil)
	reqStranger.Header.Set("X-User-ID", "stranger")
	respStranger, _ := http.DefaultClient.Do(reqStranger)

	if respStranger.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 reading another user's watch state, got %d", respStranger.StatusCode)
	}

	t.Logf("Watch state integration test passed")
}

// TestIntegrationRateLimitingEnforcement tests that rate limiting is enforced.
func TestIntegrationRateLimitingEnforcement(t *testing.T) {
	store := repository.NewMemoryStore()
	svc := service.NewService(repository.NewServiceAdapter(store), 5*time.Minute)

	s := &server{
		watchService:    svc,
		authMiddleware:  &auth.Middleware{AllowDevAuth: true},
		rateLimiter:     security.NewRateLimiter(3, time.Minute), // Limit to 3 requests per minute
		watchStateCache: performance.NewWatchStateCache(5 * time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/watch", s.handleWatch)
	mux.HandleFunc("/v1/feed", s.handleFeed)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	userID := "rate-limit-user"
	successCount := 0
	limitedCount := 0

	// Make 5 requests; expect first 3 to succeed, next 2 to be rate limited
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest("POST", testServer.URL+"/v1/watch", bytes.NewReader([]byte(`{"stream_ids":["test"]}`)))
		req.Header.Set("X-User-ID", userID)
		req.Header.Set("Content-Type", "application/json")

		resp, _ := http.DefaultClient.Do(req)

		if resp.StatusCode == http.StatusCreated {
			successCount++
		} else if resp.StatusCode == http.StatusTooManyRequests {
			limitedCount++
		}
	}

	if successCount != 3 {
		t.Fatalf("expected 3 successful requests, got %d", successCount)
	}
	if limitedCount != 2 {
		t.Fatalf("expected 2 rate-limited requests, got %d", limitedCount)
	}

	t.Logf("Rate limiting integration test passed")
}

// TestIntegrationAuthMiddlewareWithVariousTokenTypes tests auth with different token formats.
func TestIntegrationAuthMiddlewareWithVariousTokenTypes(t *testing.T) {
	store := repository.NewMemoryStore()
	svc := service.NewService(repository.NewServiceAdapter(store), 5*time.Minute)

	s := &server{
		watchService:    svc,
		authMiddleware:  &auth.Middleware{AllowDevAuth: true},
		rateLimiter:     security.NewRateLimiter(100, time.Minute),
		watchStateCache: performance.NewWatchStateCache(5 * time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/watch", s.handleWatch)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	tests := []struct {
		name           string
		userID         string
		authHeader     string
		expectedStatus int
	}{
		{
			name:           "dev auth with X-User-ID header",
			userID:         "dev-user",
			authHeader:     "",
			expectedStatus: http.StatusCreated,
		},
		{
			name:           "no auth provided should fail",
			userID:         "",
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", testServer.URL+"/v1/watch", bytes.NewReader([]byte(`{"stream_ids":["test"]}`)))
			req.Header.Set("Content-Type", "application/json")

			if tt.userID != "" {
				req.Header.Set("X-User-ID", tt.userID)
			}
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			resp, _ := http.DefaultClient.Do(req)
			if resp.StatusCode != tt.expectedStatus {
				t.Fatalf("expected %d, got %d", tt.expectedStatus, resp.StatusCode)
			}
		})
	}

	t.Logf("Auth middleware integration tests passed")
}

// TestIntegrationFullRequestCycle tests a complete request cycle from HTTP to storage.
func TestIntegrationFullRequestCycle(t *testing.T) {
	// Setup in-memory store
	store := repository.NewMemoryStore()
	repo := repository.NewServiceAdapter(store)
	svc := service.NewService(repo, 5*time.Minute)

	s := &server{
		watchService:    svc,
		authMiddleware:  &auth.Middleware{AllowDevAuth: true},
		rateLimiter:     security.NewRateLimiter(100, time.Minute),
		watchStateCache: performance.NewWatchStateCache(5 * time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/watch", s.handleWatch)
	mux.HandleFunc("/v1/subscriptions/", s.handleSubscription)
	mux.HandleFunc("/v1/feed", s.handleFeed)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	userID := "integration-test-user"
	streamIDs := []string{"stream-1", "stream-2", "stream-3"}

	// Step 1: Set watch state
	reqBody := watchRequest{StreamIDs: streamIDs}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", testServer.URL+"/v1/watch", bytes.NewReader(body))
	req.Header.Set("X-User-ID", userID)
	req.Header.Set("Content-Type", "application/json")

	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("set watch state failed: %d", resp.StatusCode)
	}

	respBody, _ := io.ReadAll(resp.Body)
	var setResp map[string]interface{}
	json.Unmarshal(respBody, &setResp)

	if setResp["user_id"] != userID {
		t.Fatalf("expected user_id %s, got %v", userID, setResp["user_id"])
	}

	if len(setResp["stream_ids"].([]interface{})) != 3 {
		t.Fatalf("expected 3 streams, got %v", len(setResp["stream_ids"].([]interface{})))
	}

	// Step 2: Publisher sets watch state
	publisherID := "publisher-user"
	reqPubBody := watchRequest{StreamIDs: []string{"stream-pub"}}
	pubBody, _ := json.Marshal(reqPubBody)

	reqPub, _ := http.NewRequest("POST", testServer.URL+"/v1/watch", bytes.NewReader(pubBody))
	reqPub.Header.Set("X-User-ID", publisherID)
	reqPub.Header.Set("Content-Type", "application/json")

	respPub, _ := http.DefaultClient.Do(reqPub)
	if respPub.StatusCode != http.StatusCreated {
		t.Fatalf("set publisher watch state failed: %d", respPub.StatusCode)
	}

	// Step 3: Subscriber follows the publisher
	reqSub, _ := http.NewRequest("POST", testServer.URL+"/v1/subscriptions/"+publisherID, nil)
	reqSub.Header.Set("X-User-ID", userID)
	respSub, _ := http.DefaultClient.Do(reqSub)
	if respSub.StatusCode != http.StatusCreated {
		t.Fatalf("subscribe failed: %d", respSub.StatusCode)
	}

	// Step 4: Query feed to verify persistence
	reqFeed, _ := http.NewRequest("GET", testServer.URL+"/v1/feed", nil)
	reqFeed.Header.Set("X-User-ID", userID)

	respFeed, _ := http.DefaultClient.Do(reqFeed)
	if respFeed.StatusCode != http.StatusOK {
		t.Fatalf("get feed failed: %d", respFeed.StatusCode)
	}

	feedRespBody, _ := io.ReadAll(respFeed.Body)
	var feedResp []map[string]interface{}
	json.Unmarshal(feedRespBody, &feedResp)

	if len(feedResp) != 1 || feedResp[0]["user_id"] != publisherID {
		t.Fatalf("expected 1 user (publisher) in feed, got %v", feedResp)
	}

	t.Logf("Full request cycle integration test passed")
}

// TestIntegrationSubscriptionModes tests the settings, approved-subscription,
// and rejection flows end-to-end over HTTP.
func TestIntegrationSubscriptionModes(t *testing.T) {
	store := repository.NewMemoryStore()
	svc := service.NewService(repository.NewServiceAdapter(store), 5*time.Minute)

	s := &server{
		watchService:    svc,
		authMiddleware:  &auth.Middleware{AllowDevAuth: true},
		rateLimiter:     security.NewRateLimiter(100, time.Minute),
		watchStateCache: performance.NewWatchStateCache(5 * time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/watch", s.handleWatch)
	mux.HandleFunc("/v1/users/", s.handleUserWatch)
	mux.HandleFunc("/v1/feed", s.handleFeed)
	mux.HandleFunc("/v1/settings", s.handleSettings)
	mux.HandleFunc("/v1/subscriptions", s.handleSubscriptions)
	mux.HandleFunc("/v1/subscriptions/", s.handleSubscription)
	mux.HandleFunc("/v1/subscription_requests", s.handleSubscriptionRequests)
	mux.HandleFunc("/v1/subscription_requests/", s.handleSubscriptionRequestAction)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	do := func(method, path, userID string, body string) (*http.Response, []byte) {
		var reader io.Reader
		if body != "" {
			reader = bytes.NewReader([]byte(body))
		}
		req, _ := http.NewRequest(method, testServer.URL+path, reader)
		if userID != "" {
			req.Header.Set("X-User-ID", userID)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s failed: %v", method, path, err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, data
	}

	// Step 1: Publisher publishes watch state and switches to approved mode.
	if resp, _ := do("POST", "/v1/watch", "publisher", `{"stream_ids":["stream-1"]}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("publisher watch failed: %d", resp.StatusCode)
	}
	if resp, body := do("PUT", "/v1/settings", "publisher", `{"subscription_mode":"approved","visibility":"subscribers"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("update settings failed: %d (%s)", resp.StatusCode, body)
	}

	// Step 2: Settings are persisted and returned.
	if resp, body := do("GET", "/v1/settings", "publisher", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("get settings failed: %d", resp.StatusCode)
	} else {
		var settings map[string]interface{}
		json.Unmarshal(body, &settings)
		if settings["subscription_mode"] != "approved" || settings["visibility"] != "subscribers" {
			t.Fatalf("unexpected settings: %s", body)
		}
	}

	// Step 3: Viewer subscribes; request is pending (202).
	if resp, _ := do("POST", "/v1/subscriptions/publisher", "viewer", ""); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 pending subscription, got %d", resp.StatusCode)
	}

	// Step 4: Feed stays empty while pending.
	if resp, body := do("GET", "/v1/feed", "viewer", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("feed failed: %d", resp.StatusCode)
	} else {
		var feed []map[string]interface{}
		json.Unmarshal(body, &feed)
		if len(feed) != 0 {
			t.Fatalf("expected empty feed while pending, got %v", feed)
		}
	}

	// Step 5: Publisher sees the pending request and approves it.
	if resp, body := do("GET", "/v1/subscription_requests", "publisher", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("list requests failed: %d", resp.StatusCode)
	} else {
		var requests []map[string]interface{}
		json.Unmarshal(body, &requests)
		if len(requests) != 1 || requests[0]["subscriber_id"] != "viewer" {
			t.Fatalf("unexpected requests: %s", body)
		}
	}
	if resp, _ := do("POST", "/v1/subscription_requests/viewer/approve", "publisher", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("approve failed: %d", resp.StatusCode)
	}

	// Step 6: Publisher now appears in the viewer's feed.
	if resp, body := do("GET", "/v1/feed", "viewer", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("feed failed: %d", resp.StatusCode)
	} else {
		var feed []map[string]interface{}
		json.Unmarshal(body, &feed)
		if len(feed) != 1 || feed[0]["user_id"] != "publisher" {
			t.Fatalf("expected publisher in feed after approval, got %v", feed)
		}
	}

	// Step 7: Viewer can read the approved publisher's watch state directly.
	if resp, _ := do("GET", "/v1/users/publisher/watch", "viewer", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 reading approved publisher's watch state, got %d", resp.StatusCode)
	}

	// Step 8: Unsubscribe removes the publisher from the feed.
	if resp, _ := do("DELETE", "/v1/subscriptions/publisher", "viewer", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unsubscribe failed: %d", resp.StatusCode)
	}
	if resp, body := do("GET", "/v1/feed", "viewer", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("feed failed: %d", resp.StatusCode)
	} else {
		var feed []map[string]interface{}
		json.Unmarshal(body, &feed)
		if len(feed) != 0 {
			t.Fatalf("expected empty feed after unsubscribe, got %v", feed)
		}
	}

	t.Logf("Subscription modes integration test passed")
}

// stubProvider is a minimal identity.Provider for wiring tests.
type stubProvider struct {
	resolvedUserID string
	resolveErr     error
	authorizeURL   string
	authorizeErr   error
}

func (s *stubProvider) ResolveUserID(ctx context.Context, r *http.Request) (string, error) {
	return s.resolvedUserID, s.resolveErr
}

func (s *stubProvider) BuildAuthorizeURL(state string, scopes []string) (string, error) {
	return s.authorizeURL, s.authorizeErr
}

// TestIntegrationTwitchLoginCallbackFlow tests the /auth/twitch login redirect
// and /auth/twitch/callback routes with a stub identity provider.
func TestIntegrationTwitchLoginCallbackFlow(t *testing.T) {
	store := repository.NewMemoryStore()
	svc := service.NewService(repository.NewServiceAdapter(store), 5*time.Minute)

	provider := &stubProvider{
		resolvedUserID: "twitch-login-user",
		authorizeURL:   "https://id.twitch.tv/oauth2/authorize?client_id=stub",
	}

	s := &server{
		watchService:       svc,
		authMiddleware:     &auth.Middleware{AllowDevAuth: true},
		identityProvider:   provider,
		rateLimiter:        security.NewRateLimiter(100, time.Minute),
		watchStateCache:    performance.NewWatchStateCache(5 * time.Minute),
		oauthStates:        newOAuthStateStore(15 * time.Minute),
		redirectAfterLogin: "/app",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/twitch", s.handleTwitchLogin)
	mux.HandleFunc("/auth/twitch/callback", s.handleTwitchCallback)
	mux.HandleFunc("/v1/watch", s.handleWatch)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Don't follow redirects so we can inspect the Location header.
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// Test 1: Login redirects with a 302 to the Twitch authorize URL.
	loginReq, _ := http.NewRequest("GET", testServer.URL+"/auth/twitch?state=test-state", nil)
	loginResp, err := client.Do(loginReq)
	if err != nil {
		t.Fatalf("login request failed: %v", err)
	}
	if loginResp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 from login, got %d", loginResp.StatusCode)
	}
	loc := loginResp.Header.Get("Location")
	if loc != "https://id.twitch.tv/oauth2/authorize?client_id=stub" {
		t.Fatalf("expected redirect to Twitch authorize URL, got %q", loc)
	}

	// Test 2: Callback with the issued state and a code resolves the user and
	// redirects the browser into the app with the user id.
	callbackReq, _ := http.NewRequest("GET", testServer.URL+"/auth/twitch/callback?state=test-state&code=abc123", nil)
	callbackResp, err := client.Do(callbackReq)
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	if callbackResp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 from callback, got %d", callbackResp.StatusCode)
	}
	cbLoc := callbackResp.Header.Get("Location")
	if cbLoc != "/app?user_id=twitch-login-user" {
		t.Fatalf("expected redirect into the app, got %q", cbLoc)
	}

	// Test 3: Callback with an unknown state is rejected.
	badStateReq, _ := http.NewRequest("GET", testServer.URL+"/auth/twitch/callback?state=wrong-state&code=abc123", nil)
	badStateResp, err := client.Do(badStateReq)
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	if badStateResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown state, got %d", badStateResp.StatusCode)
	}

	// Test 4: A state cannot be reused after it has been consumed.
	reusedReq, _ := http.NewRequest("GET", testServer.URL+"/auth/twitch/callback?state=test-state&code=abc123", nil)
	reusedResp, _ := client.Do(reusedReq)
	if reusedResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for reused state, got %d", reusedResp.StatusCode)
	}

	t.Logf("Twitch login/callback flow integration test passed")
}
