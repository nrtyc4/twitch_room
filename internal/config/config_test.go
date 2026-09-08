package config

import (
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
