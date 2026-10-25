package config

import (
	"net/netip"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port                string
	AuthToken           string
	AllowDevAuth        bool
	WatchStateTTLMin    int
	WatchRefreshSeconds int
	DatabaseDSN         string
	RedirectAfterLogin  string
	LogLevel            string
	LogFormat           string

	// AppEnv is "production" or ""/"development". It gates warnings about
	// development-only settings.
	AppEnv string

	// TLSEnabled makes the service serve HTTPS directly from TLSCertFile /
	// TLSKeyFile. TLSPort, when non-empty, additionally starts a plain-HTTP
	// redirector on that port that 301s everything to the HTTPS listener.
	TLSEnabled  bool
	TLSCertFile string
	TLSKeyFile  string
	TLSPort     string

	// SecureCookies forces the Secure flag on session cookies even when the
	// request arrived over plain HTTP (needed when TLS terminates at a
	// reverse proxy in front of this service).
	SecureCookies bool

	// AllowedHosts is the whitelist of Host header values the service may
	// redirect to (HTTP->HTTPS and OAuth redirects). Empty accepts any host.
	AllowedHosts []string

	// AllowedOrigins is the whitelist of Cross-Origin Resource Sharing (CORS)
	// origins allowed to access the API. Empty disables CORS (no cross-origin
	// requests are allowed), except in development (dev auth enabled, not
	// production) where it defaults to chrome-extension://* and
	// moz-extension://*.
	AllowedOrigins []string

	// TrustedProxyCIDRs lists the networks whose X-Forwarded-For and
	// X-Forwarded-Proto headers the service honours. Empty trusts nobody,
	// so forwarded headers are ignored.
	TrustedProxyCIDRs []netip.Prefix
}

func Load() Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	allowDevAuth := false
	if value := os.Getenv("ALLOW_DEV_AUTH"); value != "" {
		allowDevAuth, _ = strconv.ParseBool(value)
	}

	ttlMinutes := 5
	if value := os.Getenv("WATCH_STATE_TTL_MINUTES"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err == nil && parsed > 0 {
			ttlMinutes = parsed
		}
	}

	refreshSeconds := 60
	if value := os.Getenv("WATCH_REFRESH_SECONDS"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err == nil && parsed > 0 {
			refreshSeconds = parsed
		}
	}

	redirectAfterLogin := "/"
	if value := os.Getenv("APP_REDIRECT_AFTER_LOGIN"); value != "" {
		redirectAfterLogin = value
	}

	appEnv := os.Getenv("APP_ENV")
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	logFormat := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_FORMAT")))
	if logFormat == "" {
		logFormat = "text"
	}

	tlsEnabled := boolEnv("TLS_ENABLED")
	secureCookies := boolEnv("COOKIE_SECURE")

	allowedOrigins := splitList(os.Getenv("ALLOWED_ORIGINS"))
	// In development (dev auth enabled, not production) default to allowing
	// browser-extension origins so the extension can talk to the API without
	// extra configuration. Production deployments must set ALLOWED_ORIGINS
	// explicitly.
	if len(allowedOrigins) == 0 && allowDevAuth && appEnv != "production" {
		allowedOrigins = []string{"chrome-extension://*", "moz-extension://*"}
	}

	return Config{
		Port:                port,
		AuthToken:           os.Getenv("TWITCH_ROOM_AUTH_TOKEN"),
		AllowDevAuth:        allowDevAuth,
		WatchStateTTLMin:    ttlMinutes,
		WatchRefreshSeconds: refreshSeconds,
		DatabaseDSN:         os.Getenv("DATABASE_DSN"),
		RedirectAfterLogin:  redirectAfterLogin,
		AppEnv:              appEnv,
		LogLevel:            logLevel,
		LogFormat:           logFormat,
		TLSEnabled:          tlsEnabled,
		TLSCertFile:         os.Getenv("TLS_CERT_FILE"),
		TLSKeyFile:          os.Getenv("TLS_KEY_FILE"),
		TLSPort:             os.Getenv("TLS_REDIRECT_PORT"),
		SecureCookies:       secureCookies,
		AllowedHosts:        splitList(os.Getenv("ALLOWED_HOSTS")),
		AllowedOrigins:      allowedOrigins,
		TrustedProxyCIDRs:   parseCIDRs(os.Getenv("TRUSTED_PROXY_CIDRS")),
	}
}

// splitList splits a comma-separated environment value into trimmed,
// lower-cased, non-empty entries.
func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseCIDRs parses a comma-separated list of IPv4/IPv6 CIDR networks,
// silently skipping entries that are not valid prefixes.
func parseCIDRs(value string) []netip.Prefix {
	out := make([]netip.Prefix, 0)
	for _, p := range splitList(value) {
		if prefix, err := netip.ParsePrefix(p); err == nil {
			out = append(out, prefix)
		}
	}
	return out
}

// boolEnv parses an environment variable as a boolean, treating empty values
// as false and ignoring parse errors.
func boolEnv(key string) bool {
	value := os.Getenv(key)
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return parsed
}
