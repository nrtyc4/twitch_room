package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"twitch_room/internal/auth"
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

	if err := s.setSessionCookie(rec, req, "user-1"); err != nil {
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

	if err := s.setSessionCookie(rec, req, "user-1"); err != nil {
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

func TestHandlerChainSetsSecurityHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	var handler http.Handler = withLogging(mux)
	handler = security.MaxBodyBytes(1 << 20)(handler)
	handler = security.SecurityHeaders(true)(handler)

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
