package security

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	middleware := SecurityHeaders(false, nil)
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
	middleware := SecurityHeaders(true, nil)
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

func plainHTTPSDetector() func(*http.Request) bool {
	return func(r *http.Request) bool {
		return r.TLS != nil
	}
}

func TestForceHTTPSRedirectsPlainHTTP(t *testing.T) {
	middleware := ForceHTTPS(nil, plainHTTPSDetector())
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
	middleware := ForceHTTPS(nil, plainHTTPSDetector())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	req.TLS = &tls.ConnectionState{}

	reached := false
	middleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		reached = true
	})).ServeHTTP(rec, req)

	if !reached {
		t.Fatal("handler was not reached over TLS")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestForceHTTPSHonoursTrustedForwardedProto(t *testing.T) {
	base := plainHTTPSDetector()
	isHTTPS := func(r *http.Request) bool {
		if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			return true
		}
		return base(r)
	}
	middleware := ForceHTTPS(nil, isHTTPS)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")

	reached := false
	middleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		reached = true
	})).ServeHTTP(rec, req)

	if !reached {
		t.Fatal("expected forwarded https request to pass through")
	}
}

func TestForceHTTPSRejectsUnexpectedHost(t *testing.T) {
	middleware := ForceHTTPS([]string{"app.example.com"}, plainHTTPSDetector())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/feed", nil)
	req.Host = "evil.example.net"

	middleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler should not be reached for a disallowed host")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for disallowed host, got %d", rec.Code)
	}
}

func TestForceHTTPSRedirectsOnlyAllowedHosts(t *testing.T) {
	middleware := ForceHTTPS([]string{"app.example.com"}, plainHTTPSDetector())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://app.example.com/feed", nil)
	req.Host = "app.example.com:443"

	middleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler should not be reached")
	})).ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("expected 301, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://app.example.com:443/feed" {
		t.Fatalf("unexpected redirect location %q", loc)
	}
}

func TestAllowedHost(t *testing.T) {
	for _, test := range []struct {
		host    string
		allowed []string
		want    bool
	}{
		{"app.example.com", []string{"app.example.com"}, true},
		{"app.example.com:8443", []string{"app.example.com"}, true},
		{"app.example.com", []string{"other.example.com"}, false},
		{"EVIL.example.com", []string{"app.example.com"}, false},
		{"app.example.com", nil, true},
		{"anything.dev", []string{}, true},
	} {
		if got := AllowedHost(test.host, test.allowed); got != test.want {
			t.Errorf("AllowedHost(%q, %v) = %v, want %v", test.host, test.allowed, got, test.want)
		}
	}
}

func TestTrustedRequest(t *testing.T) {
	cidrs := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("127.0.0.1/32")}

	inside := httptest.NewRequest(http.MethodGet, "/", nil)
	inside.RemoteAddr = "10.1.2.3:5555"
	if !TrustedRequest(inside, cidrs) {
		t.Error("expected 10.1.2.3 to be trusted")
	}

	outside := httptest.NewRequest(http.MethodGet, "/", nil)
	outside.RemoteAddr = "203.0.113.7:5555"
	if TrustedRequest(outside, cidrs) {
		t.Error("expected 203.0.113.7 to be untrusted")
	}

	if TrustedRequest(inside, nil) {
		t.Error("expected no trust with empty CIDR list")
	}
}

func TestHTTPSRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	if HTTPSRequest(req, false) {
		t.Error("expected plain request to be non-HTTPS")
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	if HTTPSRequest(req, false) {
		t.Error("expected forwarded https to be ignored when untrusted")
	}
	if !HTTPSRequest(req, true) {
		t.Error("expected forwarded https to count when trusted")
	}

	tlsReq := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	tlsReq.Header.Set("X-Forwarded-Proto", "http")
	if !HTTPSRequest(tlsReq, false) {
		t.Error("expected a real TLS connection to count regardless of headers")
	}
}


