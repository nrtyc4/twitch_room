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

	return Config{
		Port:                port,
		AuthToken:           os.Getenv("TWITCH_ROOM_AUTH_TOKEN"),
		AllowDevAuth:        allowDevAuth,
		WatchStateTTLMin:    ttlMinutes,
		WatchRefreshSeconds: refreshSeconds,
		DatabaseDSN:         os.Getenv("DATABASE_DSN"),
		RedirectAfterLogin:  redirectAfterLogin,
	}
}
