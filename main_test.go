package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"twitch_room/internal/auth"
	"twitch_room/internal/metrics"
	"twitch_room/internal/security"
	"twitch_room/internal/service"
)

func TestAuthLoginRateLimitedByIP(t *testing.T) {
	s := &server{
		identityProvider: &stubProvider{
			authorizeURL: "https://id.twitch.tv/oauth2/authorize?client_id=stub",
		},
		authLimiter: security.NewRateLimiter(2, time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/twitch", s.handleTwitchLogin)
	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	statuses := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		resp, err := http.Get(testServer.URL + "/auth/twitch?state=abc")
		if err != nil {
			t.Fatalf("request %d failed: %v", i+1, err)
		}
		statuses = append(statuses, resp.StatusCode)
		resp.Body.Close()
	}

	if statuses[0] == http.StatusTooManyRequests || statuses[1] == http.StatusTooManyRequests {
		t.Fatalf("expected first requests to pass the rate limit, got %v", statuses)
	}
	if statuses[2] != http.StatusTooManyRequests {
		t.Fatalf("expected third request to be rate limited, got %v", statuses)
	}
}

func TestSessionCookieSecureWhenEnabled(t *testing.T) {
	s := newSessionStore(24 * time.Hour)
	s.enableSecureCookies()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if err := s.setSessionCookie(rec, req, "user-1", ""); err != nil {
		t.Fatalf("setSessionCookie failed: %v", err)
	}

	cookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, "Secure") {
		t.Fatalf("expected Secure flag on cookie, got %q", cookie)
	}
	if !strings.Contains(cookie, "HttpOnly") {
		t.Fatalf("expected HttpOnly flag on cookie, got %q", cookie)
	}
}

func TestSessionCookieNotSecureByDefaultOverPlainHTTP(t *testing.T) {
	s := newSessionStore(24 * time.Hour)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	if err := s.setSessionCookie(rec, req, "user-1", ""); err != nil {
		t.Fatalf("setSessionCookie failed: %v", err)
	}

	if strings.Contains(rec.Header().Get("Set-Cookie"), "Secure") {
		t.Fatal("expected no Secure flag over plain HTTP without explicit opt-in")
	}
}

func TestWatchRejectsInvalidStreamID(t *testing.T) {
	s := &server{
		watchService:   service.NewService(service.NewMemoryRepository(), time.Minute),
		authMiddleware: &auth.Middleware{AllowDevAuth: true},
		rateLimiter:    security.NewRateLimiter(30, time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/watch", s.handleWatch)
	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	for _, body := range []string{
		`{"stream_ids":["bad/id"]}`,
		`{"stream_ids":["a b"]}`,
	} {
		req, _ := http.NewRequest(http.MethodPost, testServer.URL+"/v1/watch", bytes.NewBufferString(body))
		req.Header.Set(auth.HeaderUserID, "dev-watcher")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %s: expected 400, got %d", body, resp.StatusCode)
		}
	}
}

func TestRequireUserRejectsMalformedIDs(t *testing.T) {
	s := &server{
		watchService:   service.NewService(service.NewMemoryRepository(), time.Minute),
		authMiddleware: &auth.Middleware{AllowDevAuth: true},
		rateLimiter:    security.NewRateLimiter(30, time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/watch", s.handleWatch)
	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	req, _ := http.NewRequest(http.MethodGet, testServer.URL+"/v1/watch", nil)
	req.Header.Set(auth.HeaderUserID, "bad/user")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for malformed X-User-ID, got %d", resp.StatusCode)
	}
}

func TestUserPathRejectsMalformedID(t *testing.T) {
	s := &server{
		watchService:   service.NewService(service.NewMemoryRepository(), time.Minute),
		authMiddleware: &auth.Middleware{AllowDevAuth: true},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/users/", s.handleUserWatch)
	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	req, _ := http.NewRequest(http.MethodGet, testServer.URL+"/v1/users/bad~id/watch", nil)
	req.Header.Set(auth.HeaderUserID, "viewer")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed path user id, got %d", resp.StatusCode)
	}
}

func truncate(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}

// fakeShareEnricher enriches streams and resolves every user to a friendly
// display name so share-page rendering can be exercised offline.
type fakeShareEnricher struct{}

func (f *fakeShareEnricher) LookupStreams(ctx context.Context, logins []string) (map[string]service.StreamInfo, error) {
	out := make(map[string]service.StreamInfo, len(logins))
	for _, l := range logins {
		out[l] = service.StreamInfo{
			Exists:       true,
			IsLive:       true,
			ChannelName:  "live channel",
			Title:        "very stream",
			GameName:     "Chess",
			ThumbnailURL: "https://thumb",
		}
	}
	return out, nil
}

func (f *fakeShareEnricher) ResolveUsers(ctx context.Context, userIDs []string) (map[string]service.UserInfo, error) {
	out := make(map[string]service.UserInfo, len(userIDs))
	for _, id := range userIDs {
		out[strings.ToLower(id)] = service.UserInfo{DisplayName: "Best Publisher"}
	}
	return out, nil
}

func newShareServer() (*server, *httptest.Server) {
	svc := service.NewServiceWithEnricher(
		service.NewMemoryRepository(),
		time.Minute,
		&fakeShareEnricher{},
	)
	s := &server{
		watchService:   svc,
		authMiddleware: &auth.Middleware{AllowDevAuth: true},
		rateLimiter:    security.NewRateLimiter(30, time.Minute),
		shareLimiter:   security.NewRateLimiter(100, time.Minute),
	}

	_, _ = svc.SetUserWatchState("shroud", []string{"xqc"})
	_, _ = svc.SetUserWatchState("private-user", []string{"xqc"})
	_, _ = svc.UpdatePublisherSettings(service.PublisherSettings{
		UserID:           "shroud",
		SubscriptionMode: service.SubscriptionModeOpen,
		Visibility:       service.VisibilityPublic,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/u/", s.handleSharePage)
	testServer := httptest.NewServer(mux)
	return s, testServer
}

func TestSharePageRendersPublicState(t *testing.T) {
	_, ts := newShareServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/u/shroud")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	out := body.String()
	if !strings.Contains(out, "Best Publisher") {
		t.Errorf("expected resolved display name in page, got:\n%s", truncate(out))
	}
	if !strings.Contains(out, "xqc") {
		t.Errorf("expected stream id in page, got:\n%s", truncate(out))
	}
	if !strings.Contains(out, "live channel") {
		t.Errorf("expected enriched value in page, got:\n%s", truncate(out))
	}
}

func TestSharePageHidesNonPublicAndMissing(t *testing.T) {
	_, ts := newShareServer()
	defer ts.Close()

	for _, path := range []string{"/u/private-user", "/u/stranger", "/u/bad~id"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d", path, resp.StatusCode)
		}
	}
}

func TestSharePageRejectsNonGet(t *testing.T) {
	_, ts := newShareServer()
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/u/shroud", "application/json", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestHandlerChainSetsSecurityHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	var handler http.Handler = withLogging(mux)
	handler = security.MaxBodyBytes(1 << 20)(handler)
	handler = security.SecurityHeaders(true, nil)(handler)

	testServer := httptest.NewServer(handler)
	defer testServer.Close()

	resp, err := http.Get(testServer.URL + "/health")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("expected nosniff header, got %q", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("expected DENY frame options, got %q", got)
	}
	if got := resp.Header.Get("Strict-Transport-Security"); got == "" {
		t.Error("expected HSTS header when TLS is enabled")
	}
}

// newMetricsServer builds a server whose full middleware chain and /metrics
// route are wired, seeded with one watch state and a mix of subscriptions.
func newMetricsServer(middleware *auth.Middleware) (*server, *httptest.Server) {
	svc := service.NewServiceWithEnricher(
		service.NewMemoryRepository(),
		time.Minute,
		&fakeShareEnricher{},
	)
	_, _ = svc.SetUserWatchState("shroud", []string{"xqc"})
	_, _ = svc.Subscribe("viewer", "shroud")
	_, _ = svc.UpdatePublisherSettings(service.PublisherSettings{
		UserID:           "shroud",
		SubscriptionMode: service.SubscriptionModeOpen,
		Visibility:       service.VisibilityPublic,
	})
	_ = svc.RespondToSubscriber("shroud", "viewer", true)

	s := &server{
		watchService:   svc,
		authMiddleware: middleware,
		rateLimiter:    security.NewRateLimiter(30, time.Minute),
		shareLimiter:   security.NewRateLimiter(100, time.Minute),
		metrics:        metrics.New(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/v1/users/foo", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	var handler http.Handler = withLogging(mux)
	handler = s.metricsMiddleware(handler)
	return s, httptest.NewServer(handler)
}

func TestMetricsPublicWhenNoTokenConfigured(t *testing.T) {
	_, ts := newMetricsServer(&auth.Middleware{AllowDevAuth: true})
	defer ts.Close()

	// The first scrape populates the request counters (which are written
	// after the response body is rendered), so assert on the second scrape.
	first, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	first.Body.Close()
	if first.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", first.StatusCode)
	}

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("expected text/plain content type, got %q", got)
	}

	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	out := body.String()
	for _, want := range []string{
		"# TYPE twitch_room_active_watch_states gauge",
		`twitch_room_active_watch_states 1`,
		`# TYPE twitch_room_subscriptions gauge`,
		`twitch_room_subscriptions{status="active"} 1`,
		`# TYPE twitch_room_http_requests_total counter`,
		`# TYPE twitch_room_http_request_duration_seconds histogram`,
		`twitch_room_app_info{version="dev"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics body missing %q", want)
		}
	}
}

func TestMetricsCountsThrottledRequests(t *testing.T) {
	s := &server{metrics: metrics.New()}
	s.incThrottled("auth_ip")
	s.incThrottled("auth_ip")
	s.incThrottled("share_ip")

	out := string(s.metrics.Prometheus())
	for _, want := range []string{
		"# TYPE twitch_room_throttled_requests_total counter",
		`twitch_room_throttled_requests_total{reason="auth_ip"} 2`,
		`twitch_room_throttled_requests_total{reason="share_ip"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics body missing %q", want)
		}
	}
}

func TestMetricsRequiresTokenWhenConfigured(t *testing.T) {
	_, ts := newMetricsServer(&auth.Middleware{ExpectedToken: "top-secret", AllowDevAuth: true})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", resp.StatusCode)
	}

	req.Header.Set("Authorization", "Bearer top-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with correct token, got %d", resp.StatusCode)
	}
}

func TestMetricsMiddlewareBucketsRoutesByFirstSegment(t *testing.T) {
	_, ts := newMetricsServer(&auth.Middleware{AllowDevAuth: true})
	defer ts.Close()

	// /v1/users/foo counts under v1/users, /health under health, /metrics
	// under metrics.
	urls := []string{ts.URL + "/v1/users/foo", ts.URL + "/health", ts.URL + "/metrics"}
	for _, u := range urls {
		resp, err := http.Get(u)
		if err != nil {
			t.Fatalf("request to %s failed: %v", u, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request to %s returned %d", u, resp.StatusCode)
		}
	}

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	out := body.String()

	// The rendered body never includes the request currently being served
	// (counters are incremented after the body is written), and each route
	// was hit exactly once before this scrape.
	for _, want := range []string{
		`twitch_room_http_requests_total{code="200",method="GET",route="v1/users"} 1`,
		`twitch_room_http_requests_total{code="200",method="GET",route="health"} 1`,
		`twitch_room_http_requests_total{code="200",method="GET",route="metrics"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics body missing %q", want)
		}
	}
}
