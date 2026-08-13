package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

var ErrInvalidIdentity = errors.New("invalid identity")

type Provider interface {
	ResolveUserID(ctx context.Context, r *http.Request) (string, error)
}

type TwitchProvider struct {
	ClientID      string
	ClientSecret  string
	EnableDevAuth bool
	// Add HTTP client for API calls here later
}

func NewTwitchProvider() *TwitchProvider {
	return &TwitchProvider{
		ClientID:      os.Getenv("TWITCH_CLIENT_ID"),
		ClientSecret:  os.Getenv("TWITCH_CLIENT_SECRET"),
		EnableDevAuth: os.Getenv("ALLOW_DEV_AUTH") == "true",
	}
}

// Authenticate exchanges an authorization code for a Twitch access token.
func (p *TwitchProvider) Authenticate(ctx context.Context, code string) (string, error) {
	if p.ClientID == "" || p.ClientSecret == "" {
		return "", fmt.Errorf("%w: missing client credentials", ErrInvalidIdentity)
	}
	// TODO: Implement actual HTTP POST request to Twitch token endpoint using 'code' and credentials.
	// This requires a proper HTTP client setup (e.g., net/http client with context).
	fmt.Printf("--- MOCK AUTH: Attempting to exchange code %s for token ---\n", code)
	return "mock-twitch-access-token-12345", nil // Mock success for now
}

func (p *TwitchProvider) ResolveUserID(ctx context.Context, r *http.Request) (string, error) {
	// 1. Try OAuth flow first if we detect a code parameter or need to authenticate
	if p.ClientID != "" && !p.EnableDevAuth {
		code := r.URL.Query().Get("code")
		if code != "" {
			token, err := p.Authenticate(ctx, code)
			if err == nil {
				// In a real app, we would store this token and use it to fetch the user ID.
				fmt.Printf("Successfully obtained mock token: %s\n", token)
				return "twitch-user-from-oauth", nil // Return a placeholder ID based on successful auth
			}
		}
	}

	// 2. Fallback to Dev Auth (if enabled) or Bearer Token check
	if p.EnableDevAuth {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID != "" {
			return userID, nil
		}
	}

	authorization := r.Header.Get("Authorization")
	if authorization == "" {
		return "", ErrInvalidIdentity
	}

	parts := strings.SplitN(authorization, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", ErrInvalidIdentity
	}
	if strings.TrimSpace(parts[1]) == "" {
		return "", ErrInvalidIdentity
	}

	// For now, return a placeholder user ID. In production, validate the token with Twitch.
	return "twitch-user", nil
}
