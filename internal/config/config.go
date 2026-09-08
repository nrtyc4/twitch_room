package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port                string
	AuthToken           string
	AllowDevAuth        bool
	WatchStateTTLMin    int
	WatchRefreshSeconds int
	DatabaseDSN         string
	RedirectAfterLogin  string

	// AppEnv is "production" or ""/"development". It gates warnings about
	// development-only settings.
	AppEnv string

	// TLSEnabled makes the service serve HTTPS directly from TLSCertFile /
	// TLSKeyFile. TLSPort, when non-empty, additionally starts a plain-HTTP
	// redirector on that port that 301s everything to the HTTPS listener.
	TLSEnabled bool
	TLSCertFile string
	TLSKeyFile  string
	TLSPort     string

	// SecureCookies forces the Secure flag on session cookies even when the
	// request arrived over plain HTTP (needed when TLS terminates at a
	// reverse proxy in front of this service).
	SecureCookies bool

	// TrustProxyHeaders makes the service honour X-Forwarded-For /
	// X-Forwarded-Proto. Only enable when a proxy you control sets them.
	TrustProxyHeaders bool
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

	tlsEnabled := boolEnv("TLS_ENABLED")
	secureCookies := boolEnv("COOKIE_SECURE")
	trustProxyHeaders := boolEnv("TRUST_PROXY_HEADERS")

	return Config{
		Port:                port,
		AuthToken:           os.Getenv("TWITCH_ROOM_AUTH_TOKEN"),
		AllowDevAuth:        allowDevAuth,
		WatchStateTTLMin:    ttlMinutes,
		WatchRefreshSeconds: refreshSeconds,
		DatabaseDSN:         os.Getenv("DATABASE_DSN"),
		RedirectAfterLogin:  redirectAfterLogin,
		AppEnv:              appEnv,
		TLSEnabled:          tlsEnabled,
		TLSCertFile:         os.Getenv("TLS_CERT_FILE"),
		TLSKeyFile:          os.Getenv("TLS_KEY_FILE"),
		TLSPort:             os.Getenv("TLS_REDIRECT_PORT"),
		SecureCookies:       secureCookies,
		TrustProxyHeaders:   trustProxyHeaders,
	}
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
