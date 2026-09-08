package secrets

import (
	"os"
	"strings"
	"testing"
)

func TestSecretsValidateOAuth(t *testing.T) {
	tests := []struct {
		name               string
		clientID           string
		clientSecret       string
		redirectURI        string
		shouldErr          bool
		expectedConfigured bool
	}{
		{
			name:               "all OAuth secrets present",
			clientID:           "test-client-id",
			clientSecret:       "test-client-secret",
			redirectURI:        "http://localhost/callback",
			shouldErr:          false,
			expectedConfigured: true,
		},
		{
			name:               "no OAuth secrets present",
			clientID:           "",
			clientSecret:       "",
			redirectURI:        "",
			shouldErr:          false,
			expectedConfigured: false,
		},
		{
			name:               "only client ID present",
			clientID:           "test-client-id",
			clientSecret:       "",
			redirectURI:        "",
			shouldErr:          true,
			expectedConfigured: false,
		},
		{
			name:               "only client secret present",
			clientID:           "",
			clientSecret:       "test-client-secret",
			redirectURI:        "",
			shouldErr:          true,
			expectedConfigured: false,
		},
		{
			name:               "only redirect URI present",
			clientID:           "",
			clientSecret:       "",
			redirectURI:        "http://localhost/callback",
			shouldErr:          true,
			expectedConfigured: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Secrets{
				TwitchClientID:     tt.clientID,
				TwitchClientSecret: tt.clientSecret,
				TwitchRedirectURI:  tt.redirectURI,
			}

			err := s.validateOAuth()
			if (err != nil) != tt.shouldErr {
				t.Fatalf("expected error=%v, got=%v", tt.shouldErr, err != nil)
			}

			if s.IsOAuthConfigured() != tt.expectedConfigured {
				t.Fatalf("expected configured=%v, got=%v", tt.expectedConfigured, s.IsOAuthConfigured())
			}
		})
	}
}

func TestSecretsLoad(t *testing.T) {
	// Set environment variables for testing
	os.Setenv("TWITCH_CLIENT_ID", "test-id")
	os.Setenv("TWITCH_CLIENT_SECRET", "test-secret")
	os.Setenv("TWITCH_REDIRECT_URI", "http://localhost/callback")
	os.Setenv("DATABASE_DSN", "postgres://localhost/db")
	os.Setenv("TWITCH_ROOM_AUTH_TOKEN", "test-auth-token")

	defer func() {
		os.Unsetenv("TWITCH_CLIENT_ID")
		os.Unsetenv("TWITCH_CLIENT_SECRET")
		os.Unsetenv("TWITCH_REDIRECT_URI")
		os.Unsetenv("DATABASE_DSN")
		os.Unsetenv("TWITCH_ROOM_AUTH_TOKEN")
	}()

	s, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if s.TwitchClientID != "test-id" {
		t.Fatalf("expected client ID to match")
	}

	if !s.IsOAuthConfigured() {
		t.Fatal("expected OAuth to be configured")
	}
}

func TestLoadDotEnvParsesMultipleLines(t *testing.T) {
	dir := t.TempDir()
	path := dir + string(os.PathSeparator) + ".env"
	content := strings.Join([]string{
		"# comment line",
		"",
		"TWITCH_CLIENT_ID=client-id-123",
		"TWITCH_CLIENT_SECRET=client-secret-456",
		"TWITCH_REDIRECT_URI=http://localhost:8080/auth/twitch/callback",
		"APP_REDIRECT_AFTER_LOGIN=/app",
	}, "\n")

	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write .env file: %v", err)
	}

	for _, key := range []string{
		"TWITCH_CLIENT_ID",
		"TWITCH_CLIENT_SECRET",
		"TWITCH_REDIRECT_URI",
		"APP_REDIRECT_AFTER_LOGIN",
	} {
		_ = os.Unsetenv(key)
	}

	defer func() {
		for _, key := range []string{
			"TWITCH_CLIENT_ID",
			"TWITCH_CLIENT_SECRET",
			"TWITCH_REDIRECT_URI",
			"APP_REDIRECT_AFTER_LOGIN",
		} {
			_ = os.Unsetenv(key)
		}
	}()

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("expected no error loading .env, got: %v", err)
	}

	if got := os.Getenv("TWITCH_CLIENT_ID"); got != "client-id-123" {
		t.Fatalf("expected TWITCH_CLIENT_ID, got %q", got)
	}
	if got := os.Getenv("TWITCH_CLIENT_SECRET"); got != "client-secret-456" {
		t.Fatalf("expected TWITCH_CLIENT_SECRET, got %q", got)
	}
	if got := os.Getenv("TWITCH_REDIRECT_URI"); got != "http://localhost:8080/auth/twitch/callback" {
		t.Fatalf("expected TWITCH_REDIRECT_URI, got %q", got)
	}
	if got := os.Getenv("APP_REDIRECT_AFTER_LOGIN"); got != "/app" {
		t.Fatalf("expected APP_REDIRECT_AFTER_LOGIN, got %q", got)
	}
}

func TestSecretsString(t *testing.T) {
	s := &Secrets{
		TwitchClientID:     "test-id",
		TwitchClientSecret: "test-secret",
		TwitchRedirectURI:  "http://localhost/callback",
		DatabaseDSN:        "postgres://localhost/db",
		AuthToken:          "token",
	}

	str := s.String()
	if str == "" {
		t.Fatal("expected non-empty string representation")
	}

	// Ensure no secrets are exposed in the string representation
	if strings.Contains(str, "test-id") || strings.Contains(str, "test-secret") || strings.Contains(str, "http://localhost/callback") {
		t.Fatal("secrets are exposed in string representation")
	}

	// Should mention configured status
	if !strings.Contains(str, "configured") {
		t.Fatalf("expected 'configured' in string representation, got: %s", str)
	}
}
