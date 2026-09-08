package secrets

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Secrets holds all sensitive configuration values.
// These should never be logged or exposed.
type Secrets struct {
	TwitchClientID     string
	TwitchClientSecret string
	TwitchRedirectURI  string
	DatabaseDSN        string
	AuthToken          string
}

// Load loads secrets from environment variables and validates required secrets are present.
// For local development, secrets can be loaded from a .env file by calling LoadDotEnv first.
func Load() (*Secrets, error) {
	s := &Secrets{
		TwitchClientID:     os.Getenv("TWITCH_CLIENT_ID"),
		TwitchClientSecret: os.Getenv("TWITCH_CLIENT_SECRET"),
		TwitchRedirectURI:  os.Getenv("TWITCH_REDIRECT_URI"),
		DatabaseDSN:        os.Getenv("DATABASE_DSN"),
		AuthToken:          os.Getenv("TWITCH_ROOM_AUTH_TOKEN"),
	}

	// Validate required secrets for OAuth
	if err := s.validateOAuth(); err != nil {
		return nil, err
	}

	return s, nil
}

// validateOAuth ensures that either all OAuth secrets are present or none are.
// This allows the service to run with dev auth fallback if OAuth is not configured.
func (s *Secrets) validateOAuth() error {
	hasClientID := s.TwitchClientID != ""
	hasClientSecret := s.TwitchClientSecret != ""
	hasRedirectURI := s.TwitchRedirectURI != ""

	// All or none: if any are present, all must be present
	if (hasClientID || hasClientSecret || hasRedirectURI) &&
		!(hasClientID && hasClientSecret && hasRedirectURI) {
		return fmt.Errorf("oauth requires all three: TWITCH_CLIENT_ID, TWITCH_CLIENT_SECRET, TWITCH_REDIRECT_URI")
	}

	return nil
}

// IsOAuthConfigured returns true if all OAuth secrets are present.
func (s *Secrets) IsOAuthConfigured() bool {
	return s.TwitchClientID != "" && s.TwitchClientSecret != "" && s.TwitchRedirectURI != ""
}

// LoadDotEnv loads secrets from a .env file for local development.
// It should be called before Load().
// For production, environment variables should be set directly.
func LoadDotEnv(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		// .env file is optional; if it doesn't exist, that's okay
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	// Parse the .env file and set environment variables
	scanner := NewDotEnvScanner(file)
	for scanner.Scan() {
		key, value, ok := scanner.Line()
		if !ok {
			continue
		}

		// Only set if not already set by environment
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}

	return scanner.Err()
}

// DotEnvScanner parses .env files line by line
type DotEnvScanner struct {
	scanner  *bufio.Scanner
	line     string
	key      string
	value    string
	hasError bool
	err      error
}

// NewDotEnvScanner creates a new .env file scanner
func NewDotEnvScanner(file *os.File) *DotEnvScanner {
	return &DotEnvScanner{scanner: bufio.NewScanner(file)}
}

// Scan reads the next line from the .env file
func (s *DotEnvScanner) Scan() bool {
	if s.scanner.Scan() {
		s.line = s.scanner.Text()
		return true
	}
	if err := s.scanner.Err(); err != nil {
		s.hasError = true
		s.err = err
	}
	return false
}

// Line returns the parsed key-value pair from the current line
func (s *DotEnvScanner) Line() (string, string, bool) {
	line := strings.TrimSpace(s.line)

	// Skip empty lines and comments
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}

	// Parse KEY=VALUE format
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}

	key := strings.TrimSpace(parts[0])
	value := strings.TrimSpace(parts[1])

	// Remove quotes if present
	if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
		(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
		value = value[1 : len(value)-1]
	}

	return key, value, key != ""
}

// Err returns any error encountered during scanning
func (s *DotEnvScanner) Err() error {
	return s.err
}

// String returns a safe string representation without exposing secrets.
// This is useful for logging configuration state without leaking sensitive data.
func (s *Secrets) String() string {
	parts := []string{}

	if s.IsOAuthConfigured() {
		parts = append(parts, "OAuth: configured")
	} else {
		parts = append(parts, "OAuth: not configured (dev auth fallback available)")
	}

	if s.DatabaseDSN != "" {
		parts = append(parts, "Database: configured")
	} else {
		parts = append(parts, "Database: not configured (in-memory fallback available)")
	}

	if s.AuthToken != "" {
		parts = append(parts, "Auth Token: configured")
	}

	return "[" + strings.Join(parts, ", ") + "]"
}
