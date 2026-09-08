package security

import (
	"net"
	"net/http"
	"strings"
)

// SecurityHeaders returns middleware that sets security-related response
// headers. enableHSTS additionally emits a Strict-Transport-Security header,
// which should only be sent over HTTPS.
func SecurityHeaders(enableHSTS bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https://static-cdn.jtvnw.net; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
			if enableHSTS {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MaxBodyBytes returns middleware that caps the request body size, answering
// with http.StatusRequestEntityTooLarge when the limit is exceeded. Reading is
// left to the handler; only the Content-Length is checked eagerly.
func MaxBodyBytes(limit int64) func(http.Handler) http.Handler {
	if limit <= 0 {
		limit = 1 << 20
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ForceHTTPS returns middleware that redirects plain-HTTP requests to their
// https:// equivalent. Requests already served over TLS pass through. The
// scheme is taken from X-Forwarded-Proto when trustProxy is set, otherwise an
// actual TLS connection is required.
func ForceHTTPS(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isHTTPS(r, trustProxy) {
				next.ServeHTTP(w, r)
				return
			}
			u := *r.URL
			u.Scheme = "https"
			u.Host = r.Host
			http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
		})
	}
}

func isHTTPS(r *http.Request, trustProxy bool) bool {
	if trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return r.TLS != nil
}

// ClientIP returns the caller's IP address, honouring X-Forwarded-For only
// when trustProxy is enabled (a proxy you control sets it).
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
			if first != "" {
				return first
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
