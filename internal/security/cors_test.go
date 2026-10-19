package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORS(t *testing.T) {
	t.Run("Exact origin matching", func(t *testing.T) {
		allowedOrigins := []string{"https://example.com"}
		middleware := CORS(allowedOrigins)
		handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://example.com")

		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
			t.Errorf("expected Access-Control-Allow-Origin %q, got %q", "https://example.com", got)
		}
	})

	t.Run("Wildcard origin matching", func(t *testing.T) {
		// Note: The current implementation uses a simple prefix match if the pattern ends with '*'
		// We test if it works as intended.
		allowedOrigins := []string{"https://example.*"}
		middleware := CORS(allowedOrigins)
		handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://example.com")

		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
			t.Errorf("expected Access-Control-Allow-Origin %q, got %q (wildcard pattern %q failed)", "https://example.com", got, allowedOrigins[0])
		}
	})

	t.Run("Wildcard origin matching - user requested pattern", func(t *testing.T) {
		// Testing the specific pattern requested by the user: https://*.example.com
		// Based on the current implementation, this is expected to FAIL unless it ends with '*'
		// and even then, strings.HasPrefix will treat '*' literally.
		allowedOrigins := []string{"https://*.example.com"}
		middleware := CORS(allowedOrigins)
		handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://sub.example.com")

		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://sub.example.com" {
			t.Errorf("expected Access-Control-Allow-Origin %q, got %q (wildcard pattern %q failed)", "https://sub.example.com", got, allowedOrigins[0])
		}
	})

	t.Run("OPTIONS preflight request", func(t *testing.T) {
		allowedOrigins := []string{"https://example.com"}
		middleware := CORS(allowedOrigins)
		handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodOptions, "/", nil)
		req.Header.Set("Origin", "https://example.com")

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("expected status 204 No Content, got %d", rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
			t.Errorf("expected Access-Control-Allow-Origin %q, got %q", "https://example.com", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, PUT, DELETE, OPTIONS" {
			t.Errorf("expected Access-Control-Allow-Methods %q, got %q", "GET, POST, PUT, DELETE, OPTIONS", got)
		}
	})

	t.Run("Access-Control-Allow-Origin header is present when appropriate", func(t *testing.T) {
		allowedOrigins := []string{"https://example.com"}
		middleware := CORS(allowedOrigins)
		handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Origin", "https://example.com")

		handler.ServeHTTP(rec, req)

		if rec.Header().Get("Access-Control-Allow-Origin") == "" {
			t.Error("expected Access-Control-Allow-Origin header to be present")
		}
	})
}

func TestCSP(t *testing.T) {
	t.Run("Content-Security-Policy header contains the whitelisted origins in the connect-src directive", func(t *testing.T) {
		allowedOrigins := []string{"https://extension.example.com", "https://another.example.com"}
		middleware := SecurityHeaders(false, allowedOrigins)
		handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)

		handler.ServeHTTP(rec, req)

		csp := rec.Header().Get("Content-Security-Policy")
		if csp == "" {
			t.Fatal("expected Content-Security-Policy header to be present")
		}

		// Check if connect-src contains the whitelisted origins
		if !strings.Contains(csp, "connect-src 'self' https://extension.example.com https://another.example.com") {
			t.Errorf("CSP connect-src directive does not contain expected origins. Got: %q", csp)
		}
	})
}

func TestCORSEmptyOriginsDisablesCORS(t *testing.T) {
	middleware := CORS(nil)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://example.com")

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no CORS header with empty origins, got %q", got)
	}
}
