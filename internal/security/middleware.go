package security

import (
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
)

// SecurityHeaders returns middleware that sets security-related response
// headers. enableHSTS additionally emits a Strict-Transport-Security header,
// which should only be sent over HTTPS.
func SecurityHeaders(enableHSTS bool, allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")

			directives := []string{
				"default-src 'self'",
				"img-src 'self' https://static-cdn.jtvnw.net",
				"style-src 'self'",
				"script-src 'self'",
				"connect-src 'self'",
				"frame-ancestors 'none'",
				"base-uri 'self'",
				"form-action 'self'",
			}

			if len(allowedOrigins) > 0 {
				// Modify connect-src to allow extension communication
				for i, d := range directives {
					if d == "connect-src 'self'" {
						directives[i] = "connect-src 'self' " + strings.Join(allowedOrigins, " ")
						break
					}
				}
			}

			h.Set("Content-Security-Policy", strings.Join(directives, "; "))

			if enableHSTS {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CORS returns middleware that handles Cross-Origin Resource Sharing.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	type originMatcher struct {
		exact string
		re    *regexp.Regexp
	}
	matchers := make([]originMatcher, 0, len(allowedOrigins))
	for _, o := range allowedOrigins {
		if strings.Contains(o, "*") {
			pattern := regexp.QuoteMeta(o)
			pattern = strings.ReplaceAll(pattern, "\\*", ".*")
			re, err := regexp.Compile("^" + pattern + "$")
			if err == nil {
				matchers = append(matchers, originMatcher{re: re})
			}
		} else {
			matchers = append(matchers, originMatcher{exact: o})
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed := false
			for _, m := range matchers {
				if m.exact != "" && m.exact == origin {
					allowed = true
					break
				}
				if m.re != nil && m.re.MatchString(origin) {
					allowed = true
					break
				}
			}

			if !allowed {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-User-ID")
			w.Header().Set("Access-Control-Allow-Credentials", "true")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

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
// https:// equivalent. Requests isHTTPS reports as already-secure pass through.
// Redirect targets are built from the request Host, which is only used when it
// is present in allowedHosts (an empty list accepts any host); otherwise the
// request is rejected with 400 to prevent host-header injection.
func ForceHTTPS(allowedHosts []string, isHTTPS func(r *http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isHTTPS(r) {
				next.ServeHTTP(w, r)
				return
			}
			if !AllowedHost(r.Host, allowedHosts) {
				http.Error(w, "invalid host", http.StatusBadRequest)
				return
			}
			u := *r.URL
			u.Scheme = "https"
			u.Host = r.Host
			http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
		})
	}
}

// HTTPSRequest reports whether a request arrived securely: over a real TLS
// connection, or via an X-Forwarded-Proto: https header when that header is
// only trusted from a proxy source (trusted).
func HTTPSRequest(r *http.Request, trusted bool) bool {
	if trusted && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return r.TLS != nil
}

// TrustedRequest reports whether the request's remote address is inside one of
// the trusted proxy networks. With no CIDRs configured, no source is trusted.
func TrustedRequest(r *http.Request, cidrs []netip.Prefix) bool {
	if len(cidrs) == 0 {
		return false
	}
	host := ClientIP(r, false)
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, prefix := range cidrs {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// AllowedHost reports whether a Host header value is acceptable as a redirect
// target. An empty allowlist accepts anything. Comparison ignores ports so both
// "example.com" and "example.com:8443" match the entry "example.com".
func AllowedHost(host string, allowedHosts []string) bool {
	if len(allowedHosts) == 0 {
		return true
	}
	expected := hostnameOnly(host)
	for _, allowed := range allowedHosts {
		if hostnameOnly(allowed) == expected {
			return true
		}
	}
	return false
}

func hostnameOnly(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.ToLower(h)
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

// ClientIP returns the caller's IP address, honouring X-Forwarded-For only
// when the request came from a trusted proxy.
func ClientIP(r *http.Request, trusted bool) string {
	if trusted {
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
