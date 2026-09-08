package config

import (
	"net/netip"
	"os"
	"testing"
)

func TestLoadSetsDefaults(t *testing.T) {
	_ = os.Unsetenv("PORT")
	_ = os.Unsetenv("TWITCH_ROOM_AUTH_TOKEN")
	_ = os.Unsetenv("ALLOW_DEV_AUTH")
	_ = os.Unsetenv("WATCH_STATE_TTL_MINUTES")
	_ = os.Unsetenv("WATCH_REFRESH_SECONDS")
	_ = os.Unsetenv("APP_REDIRECT_AFTER_LOGIN")

	cfg := Load()
	if cfg.Port != "8080" {
		t.Fatalf("expected default port 8080, got %s", cfg.Port)
	}
	if cfg.WatchStateTTLMin != 5 {
		t.Fatalf("expected default TTL 5, got %d", cfg.WatchStateTTLMin)
	}
	if cfg.WatchRefreshSeconds != 60 {
		t.Fatalf("expected default refresh 60, got %d", cfg.WatchRefreshSeconds)
	}
	if cfg.RedirectAfterLogin != "/" {
		t.Fatalf("expected default redirect after login '/', got %s", cfg.RedirectAfterLogin)
	}
}

func TestLoadReadsEnvironment(t *testing.T) {
	_ = os.Setenv("PORT", "9090")
	_ = os.Setenv("TWITCH_ROOM_AUTH_TOKEN", "secret")
	_ = os.Setenv("ALLOW_DEV_AUTH", "true")
	_ = os.Setenv("WATCH_STATE_TTL_MINUTES", "12")
	_ = os.Setenv("WATCH_REFRESH_SECONDS", "30")
	_ = os.Setenv("APP_REDIRECT_AFTER_LOGIN", "/app")
	defer func() {
		_ = os.Unsetenv("PORT")
		_ = os.Unsetenv("TWITCH_ROOM_AUTH_TOKEN")
		_ = os.Unsetenv("ALLOW_DEV_AUTH")
		_ = os.Unsetenv("WATCH_STATE_TTL_MINUTES")
		_ = os.Unsetenv("WATCH_REFRESH_SECONDS")
		_ = os.Unsetenv("APP_REDIRECT_AFTER_LOGIN")
	}()

	cfg := Load()
	if cfg.Port != "9090" {
		t.Fatalf("expected port 9090, got %s", cfg.Port)
	}
	if cfg.AuthToken != "secret" {
		t.Fatalf("expected auth token secret, got %s", cfg.AuthToken)
	}
	if !cfg.AllowDevAuth {
		t.Fatal("expected AllowDevAuth to be true")
	}
	if cfg.WatchStateTTLMin != 12 {
		t.Fatalf("expected TTL 12, got %d", cfg.WatchStateTTLMin)
	}
	if cfg.WatchRefreshSeconds != 30 {
		t.Fatalf("expected refresh 30, got %d", cfg.WatchRefreshSeconds)
	}
	if cfg.RedirectAfterLogin != "/app" {
		t.Fatalf("expected redirect after login '/app', got %s", cfg.RedirectAfterLogin)
	}
}

func TestLoadHardeningDefaults(t *testing.T) {
	_ = os.Unsetenv("APP_ENV")
	_ = os.Unsetenv("TLS_ENABLED")
	_ = os.Unsetenv("TLS_CERT_FILE")
	_ = os.Unsetenv("TLS_KEY_FILE")
	_ = os.Unsetenv("TLS_REDIRECT_PORT")
	_ = os.Unsetenv("COOKIE_SECURE")
	_ = os.Unsetenv("ALLOWED_HOSTS")
	_ = os.Unsetenv("TRUSTED_PROXY_CIDRS")

	cfg := Load()
	if cfg.AppEnv != "" {
		t.Fatalf("expected empty AppEnv, got %q", cfg.AppEnv)
	}
	if cfg.TLSEnabled {
		t.Fatal("expected TLS disabled by default")
	}
	if cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" || cfg.TLSPort != "" {
		t.Fatalf("expected empty TLS paths, got cert=%q key=%q port=%q", cfg.TLSCertFile, cfg.TLSKeyFile, cfg.TLSPort)
	}
	if cfg.SecureCookies {
		t.Fatal("expected SecureCookies false by default")
	}
	if len(cfg.AllowedHosts) != 0 {
		t.Fatalf("expected empty AllowedHosts, got %v", cfg.AllowedHosts)
	}
	if len(cfg.TrustedProxyCIDRs) != 0 {
		t.Fatalf("expected empty TrustedProxyCIDRs, got %v", cfg.TrustedProxyCIDRs)
	}
}

func TestLoadReadsHardeningEnvironment(t *testing.T) {
	_ = os.Setenv("APP_ENV", "production")
	_ = os.Setenv("TLS_ENABLED", "true")
	_ = os.Setenv("TLS_CERT_FILE", "/certs/fullchain.pem")
	_ = os.Setenv("TLS_KEY_FILE", "/certs/privkey.pem")
	_ = os.Setenv("TLS_REDIRECT_PORT", "80")
	_ = os.Setenv("COOKIE_SECURE", "true")
	_ = os.Setenv("ALLOWED_HOSTS", "App.example.com, app.example.com:8443, evil.invalid")
	_ = os.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 127.0.0.1/32, not-a-cidr")
	defer func() {
		_ = os.Unsetenv("APP_ENV")
		_ = os.Unsetenv("TLS_ENABLED")
		_ = os.Unsetenv("TLS_CERT_FILE")
		_ = os.Unsetenv("TLS_KEY_FILE")
		_ = os.Unsetenv("TLS_REDIRECT_PORT")
		_ = os.Unsetenv("COOKIE_SECURE")
		_ = os.Unsetenv("ALLOWED_HOSTS")
		_ = os.Unsetenv("TRUSTED_PROXY_CIDRS")
	}()

	cfg := Load()
	if cfg.AppEnv != "production" {
		t.Fatalf("expected AppEnv production, got %q", cfg.AppEnv)
	}
	if !cfg.TLSEnabled {
		t.Fatal("expected TLSEnabled true")
	}
	if cfg.TLSCertFile != "/certs/fullchain.pem" || cfg.TLSKeyFile != "/certs/privkey.pem" {
		t.Fatalf("expected cert/key paths, got %q / %q", cfg.TLSCertFile, cfg.TLSKeyFile)
	}
	if cfg.TLSPort != "80" {
		t.Fatalf("expected TLS redirect port 80, got %q", cfg.TLSPort)
	}
	if !cfg.SecureCookies {
		t.Fatal("expected SecureCookies true")
	}
	if len(cfg.AllowedHosts) != 3 || cfg.AllowedHosts[0] != "app.example.com" {
		t.Fatalf("expected normalised allowed hosts, got %v", cfg.AllowedHosts)
	}
	if len(cfg.TrustedProxyCIDRs) != 2 ||
		cfg.TrustedProxyCIDRs[0] != netip.MustParsePrefix("10.0.0.0/8") ||
		cfg.TrustedProxyCIDRs[1] != netip.MustParsePrefix("127.0.0.1/32") {
		t.Fatalf("expected parsed proxy CIDRs, got %v", cfg.TrustedProxyCIDRs)
	}
}

func TestLoadIgnoresMalformedBools(t *testing.T) {
	_ = os.Setenv("COOKIE_SECURE", "not-a-bool")
	_ = os.Setenv("TLS_ENABLED", "maybe")
	defer func() {
		_ = os.Unsetenv("COOKIE_SECURE")
		_ = os.Unsetenv("TLS_ENABLED")
	}()

	cfg := Load()
	if cfg.SecureCookies || cfg.TLSEnabled {
		t.Fatal("expected malformed bools to parse as false")
	}
}
