package identity
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
	ClientID string
	ClientSecret string
	EnableDevAuth bool
}

func NewTwitchProvider() *TwitchProvider {
	return &TwitchProvider{
		ClientID:      os.Getenv("TWITCH_CLIENT_ID"),
		ClientSecret:  os.Getenv("TWITCH_CLIENT_SECRET"),
		EnableDevAuth: os.Getenv("ALLOW_DEV_AUTH") == "true",
	}
}

func (p *TwitchProvider) ResolveUserID(ctx context.Context, r *http.Request) (string, error) {
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

	if p.ClientID == "" {
		return "", fmt.Errorf("%w: Twitch client ID not configured", ErrInvalidIdentity)
	}

	return "twitch-user", nil
}
