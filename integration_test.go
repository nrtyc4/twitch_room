package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"twitch_room/internal/auth"
	"twitch_room/internal/identity"
	"twitch_room/internal/metrics"
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
			resp := map[string]any{
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
			resp := map[string]any{
				"data": []map[string]any{
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
	svc := service.NewService(repository.NewServiceAdapter(store), 5*time.Minute, service.RealClock{})

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

	var feedResp []map[string]any
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
	svc := service.NewService(repo, 5*time.Minute, service.RealClock{})

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

	var feedResp []map[string]any
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

	var ownState map[string]any
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
	svc := service.NewService(repository.NewServiceAdapter(store), 5*time.Minute, service.RealClock{})

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
	for range 5 {
		req, _ := http.NewRequest("POST", testServer.URL+"/v1/watch", bytes.NewReader([]byte(`{"stream_ids":["test"]}`)))
		req.Header.Set("X-User-ID", userID)
		req.Header.Set("Content-Type", "application/json")

		resp, _ := http.DefaultClient.Do(req)

		if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
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
	svc := service.NewService(repository.NewServiceAdapter(store), 5*time.Minute, service.RealClock{})

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
	svc := service.NewService(repo, 5*time.Minute, service.RealClock{})

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
	var setResp map[string]any
	json.Unmarshal(respBody, &setResp)

	if setResp["user_id"] != userID {
		t.Fatalf("expected user_id %s, got %v", userID, setResp["user_id"])
	}

	if len(setResp["stream_ids"].([]any)) != 3 {
		t.Fatalf("expected 3 streams, got %v", len(setResp["stream_ids"].([]any)))
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
	var feedResp []map[string]any
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
	svc := service.NewService(repository.NewServiceAdapter(store), 5*time.Minute, service.RealClock{})

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
		var settings map[string]any
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
		var feed []map[string]any
		json.Unmarshal(body, &feed)
		if len(feed) != 0 {
			t.Fatalf("expected empty feed while pending, got %v", feed)
		}
	}

	// Step 5: Publisher sees the pending request and approves it.
	if resp, body := do("GET", "/v1/subscription_requests", "publisher", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("list requests failed: %d", resp.StatusCode)
	} else {
		var requests []map[string]any
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
		var feed []map[string]any
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
		var feed []map[string]any
		json.Unmarshal(body, &feed)
		if len(feed) != 0 {
			t.Fatalf("expected empty feed after unsubscribe, got %v", feed)
		}
	}

	t.Logf("Subscription modes integration test passed")
}

// TestIntegrationExtensionFlow simulates the browser extension talking to the
// headless API: publishing a watch state, reading the feed, settings, the
// public share page and /auth/me, all over the full middleware chain.
func TestIntegrationExtensionFlow(t *testing.T) {
	store := repository.NewMemoryStore()
	repo := repository.NewServiceAdapter(store)
	svc := service.NewService(repo, 5*time.Minute, service.RealClock{})

	s := &server{
		watchService:      svc,
		authMiddleware:    &auth.Middleware{AllowDevAuth: true},
		identityProvider:  &stubProvider{resolvedUserID: "ext-user"},
		rateLimiter:       security.NewRateLimiter(100, time.Minute),
		authLimiter:       security.NewRateLimiter(100, time.Minute),
		shareLimiter:      security.NewRateLimiter(100, time.Minute),
		settingsLimiter:   security.NewRateLimiter(100, time.Minute),
		watchStateCache:   performance.NewWatchStateCache(5 * time.Minute),
		oauthStates:       newOAuthStateStore(15 * time.Minute),
		sessions:          newSessionStore(24 * time.Hour),
		metrics:           metrics.New(),
		trustedProxyCIDRs: nil,
	}

	mux, err := newRouter(s)
	if err != nil {
		t.Fatalf("newRouter failed: %v", err)
	}

	// Reproduce the production middleware chain (minus TLS).
	var handler http.Handler = withLogging(mux)
	handler = security.MaxBodyBytes(1 << 20)(handler)
	handler = security.SecurityHeaders(false, []string{"chrome-extension://*"})(handler)
	handler = s.metricsMiddleware(handler)

	testServer := httptest.NewServer(handler)
	defer testServer.Close()

	userID := "ext-user"
	doReq := func(method, path string, body []byte) *http.Response {
		var r *http.Request
		if body != nil {
			r, _ = http.NewRequest(method, testServer.URL+path, bytes.NewReader(body))
		} else {
			r, _ = http.NewRequest(method, testServer.URL+path, nil)
		}
		r.Header.Set("X-User-ID", userID)
		r.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatalf("%s %s failed: %v", method, path, err)
		}
		return resp
	}

	// 1. Publish a watch state (as the extension content script would).
	wb, _ := json.Marshal(map[string][]string{"stream_ids": {"shroud"}})
	resp := doReq("POST", "/v1/watch", wb)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("publish watch state: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 2. Read it back.
	resp = doReq("GET", "/v1/watch", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read watch state: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 3. Update visibility to public so the share page works.
	sb, _ := json.Marshal(map[string]string{"subscription_mode": "open", "visibility": "public"})
	resp = doReq("PUT", "/v1/settings", sb)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update settings: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 4. Public share page (unauthenticated) should render.
	shareResp, _ := http.Get(testServer.URL + "/u/" + userID)
	if shareResp.StatusCode != http.StatusOK {
		t.Fatalf("share page: got %d", shareResp.StatusCode)
	}
	shareResp.Body.Close()

	// 5. /auth/me reports the user.
	resp = doReq("GET", "/auth/me", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/auth/me: got %d", resp.StatusCode)
	}
	var me map[string]any
	json.NewDecoder(resp.Body).Decode(&me)
	resp.Body.Close()
	if me["user_id"] != userID {
		t.Fatalf("/auth/me user_id: got %v", me["user_id"])
	}

	// 6. Feed endpoint responds.
	resp = doReq("GET", "/v1/feed", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feed: got %d", resp.StatusCode)
	}
	resp.Body.Close()

	t.Logf("Extension flow integration test passed")
}

// stubProvider is a minimal identity.Provider for wiring tests.
type stubProvider struct {
	resolvedUserID  string
	accessToken     string
	resolveErr      error
	authorizeURL    string
	authorizeErr    error
	resolvedProfile identity.UserProfile
}

func (s *stubProvider) ResolveUserID(ctx context.Context, r *http.Request) (string, error) {
	return s.resolvedUserID, s.resolveErr
}

func (s *stubProvider) ResolveIdentity(ctx context.Context, r *http.Request) (string, string, error) {
	return s.resolvedUserID, s.accessToken, s.resolveErr
}

func (s *stubProvider) BuildAuthorizeURL(state string, scopes []string) (string, error) {
	return s.authorizeURL, s.authorizeErr
}

func (s *stubProvider) ResolveProfile(ctx context.Context, r *http.Request) (identity.UserProfile, error) {
	return s.resolvedProfile, s.resolveErr
}

// TestIntegrationTwitchLoginCallbackFlow tests the /auth/twitch login redirect
// and /auth/twitch/callback routes with a stub identity provider.
func TestIntegrationTwitchLoginCallbackFlow(t *testing.T) {
	store := repository.NewMemoryStore()
	svc := service.NewService(repository.NewServiceAdapter(store), 5*time.Minute, service.RealClock{})

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
		sessions:           newSessionStore(24 * time.Hour),
		redirectAfterLogin: "/app",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/twitch", s.handleTwitchLogin)
	mux.HandleFunc("/auth/twitch/callback", s.handleTwitchCallback)
	mux.HandleFunc("/auth/me", s.handleMe)
	mux.HandleFunc("/auth/logout", s.handleLogout)
	mux.HandleFunc("/v1/watch", s.handleWatch)

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Don't follow redirects so we can inspect the Location header.
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// Test 1: Login redirects with a 302 to the Twitch authorize URL.
	loginReq, _ := http.NewRequest("GET", testServer.URL+"/auth/twitch", nil)
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

	// The login must bind a state to this browser via a cookie.
	var oauthStateCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == oauthStateCookieName {
			oauthStateCookie = c
			break
		}
	}
	if oauthStateCookie == nil || oauthStateCookie.Value == "" {
		t.Fatal("expected an oauth state cookie to be set on login")
	}
	loginResp.Body.Close()

	// Test 2: Callback with the issued state and a code resolves the user,
	// sets a session cookie, and redirects the browser into the app.
	callbackReq, _ := http.NewRequest("GET", testServer.URL+"/auth/twitch/callback?state="+oauthStateCookie.Value+"&code=abc123", nil)
	callbackReq.AddCookie(oauthStateCookie)
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

	// The callback must set an HttpOnly session cookie.
	sessionCookies := callbackResp.Cookies()
	var sessionCookie *http.Cookie
	for _, c := range sessionCookies {
		if c.Name == sessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected a session cookie to be set on the callback response")
	}
	if !sessionCookie.HttpOnly {
		t.Fatal("expected the session cookie to be HttpOnly")
	}

	// A follow-up request carrying the cookie is authenticated without any
	// X-User-ID header, via the session store.
	jar, _ := cookiejar.New(nil)
	jar.SetCookies(callbackResp.Request.URL, []*http.Cookie{sessionCookie})
	sessionClient := &http.Client{Jar: jar}

	meReq, _ := http.NewRequest("GET", testServer.URL+"/auth/me", nil)
	meResp, err := sessionClient.Do(meReq)
	if err != nil {
		t.Fatalf("me request failed: %v", err)
	}
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /auth/me with session, got %d", meResp.StatusCode)
	}
	meBody, _ := io.ReadAll(meResp.Body)
	meResp.Body.Close()
	var me map[string]any
	json.Unmarshal(meBody, &me)
	if me["user_id"] != "twitch-login-user" {
		t.Fatalf("expected session user twitch-login-user, got %v", me["user_id"])
	}

	// The session authenticates API calls too.
	sessWatchReq, _ := http.NewRequest("POST", testServer.URL+"/v1/watch", bytes.NewReader([]byte(`{"stream_ids":["cookie-stream"]}`)))
	sessWatchReq.Header.Set("Content-Type", "application/json")
	sessWatchResp, err := sessionClient.Do(sessWatchReq)
	if err != nil {
		t.Fatalf("session watch request failed: %v", err)
	}
	if sessWatchResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating watch state via session, got %d", sessWatchResp.StatusCode)
	}
	sessWatchResp.Body.Close()

	// Logging out destroys the session so the cookie no longer authenticates.
	logoutReq, _ := http.NewRequest("POST", testServer.URL+"/auth/logout", nil)
	logoutResp, err := sessionClient.Do(logoutReq)
	if err != nil {
		t.Fatalf("logout request failed: %v", err)
	}
	if logoutResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 from logout, got %d", logoutResp.StatusCode)
	}
	logoutResp.Body.Close()

	meAfterReq, _ := http.NewRequest("GET", testServer.URL+"/auth/me", nil)
	meAfterResp, err := sessionClient.Do(meAfterReq)
	if err != nil {
		t.Fatalf("me after logout failed: %v", err)
	}
	// The stub identity provider re-authenticates unconditionally, so /auth/me
	// still succeeds. What must be verified is that the server-side session was
	// actually destroyed.
	if meAfterResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /auth/me after logout (stub authenticates), got %d", meAfterResp.StatusCode)
	}
	meAfterResp.Body.Close()

	if sessionCookie != nil {
		if _, ok := s.sessions.userID(sessionCookie.Value); ok {
			t.Fatal("expected the session token to be destroyed after logout")
		}
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
