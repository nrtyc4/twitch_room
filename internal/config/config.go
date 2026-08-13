package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port             string
	AuthToken        string
	AllowDevAuth     bool
	WatchStateTTLMin int
	DatabaseDSN      string
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

	return Config{
		Port:             port,
		AuthToken:        os.Getenv("TWITCH_ROOM_AUTH_TOKEN"),
		AllowDevAuth:     allowDevAuth,
		WatchStateTTLMin: ttlMinutes,
		DatabaseDSN:      os.Getenv("DATABASE_DSN"),
	}
}
