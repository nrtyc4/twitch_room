package security

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	middleware := SecurityHeaders(false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	for header, want := range map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "no-referrer",
		"Cross-Origin-Opener-Policy": "same-origin",
		"Content-Security-Policy":    "default-src 'self'",
	} {
		if got := rec.Header().Get(header); !strings.HasPrefix(got, want) {
			t.Errorf("expected %s to start with %q, got %q", header, want, got)
		}
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("expected no HSTS header when disabled")
	}
}

func TestSecurityHeadersHSTSWhenEnabled(t *testing.T) {
	middleware := SecurityHeaders(true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)

	if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
		t.Error("expected HSTS header when enabled")
	}
}

func TestMaxBodyBytesAllowsSmallBodies(t *testing.T) {
	middleware := MaxBodyBytes(16)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello"))
	var handled bool

	middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("unexpected read error: %v", err)
		}
		if string(body) != "hello" {
			t.Errorf("expected body hello, got %q", body)
		}
		handled = true
	})).ServeHTTP(rec, req)

	if !handled {
		t.Fatal("handler was not reached")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMaxBodyBytesRejectsOversizedBodies(t *testing.T) {
	middleware := MaxBodyBytes(4)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("hello world"))

	handled := false
	middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled = true
		_, _ = io.ReadAll(r.Body)
	})).ServeHTTP(rec, req)

	if handled {
		t.Fatal("handler should not be reached for an oversized known-length body")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", rec.Code)
	}
}

func TestMaxBodyBytesRejectsOversizedChunkedBodies(t *testing.T) {
	middleware := MaxBodyBytes(4)
	rec := httptest.NewRecorder()

	// ContentLength -1 simulates a chunked request whose size is unknown
	// up front, so the cap must be enforced while reading.
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = io.NopCloser(strings.NewReader("hello world"))
	req.ContentLength = -1

	readErr := error(nil)
	middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	})).ServeHTTP(rec, req)

	if readErr == nil {
		t.Fatal("expected read to fail past the limit")
	}
}

func TestForceHTTPSRedirectsPlainHTTP(t *testing.T) {
	middleware := ForceHTTPS(false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/feed?x=1", nil)

	middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Fatalf("handler reached over plain HTTP: %s", r.URL)
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://example.com/feed?x=1" {
		t.Fatalf("expected https redirect, got %q", loc)
	}
}

func TestForceHTTPSAllowsTLS(t *testing.T) {
	middleware := ForceHTTPS(false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

	reached := false
	middleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		reached = true
	})).ServeHTTP(rec, req)

	if !reached {
		t.Fatal("handler was not reached over TLS")
	}
}

func TestClientIP(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.1.2.3:5555"
		if got := ClientIP(req, false); got != "10.1.2.3" {
			t.Fatalf("expected 10.1.2.3, got %q", got)
		}
	})

	t.Run("forwarded ignored when not trusted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.1.2.3:5555"
		req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
		if got := ClientIP(req, false); got != "10.1.2.3" {
			t.Fatalf("expected RemoteAddr without trust, got %q", got)
		}
	})

	t.Run("forwarded trusted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.1.2.3:5555"
		req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
		if got := ClientIP(req, true); got != "203.0.113.7" {
			t.Fatalf("expected forwarded IP, got %q", got)
		}
	})
}
