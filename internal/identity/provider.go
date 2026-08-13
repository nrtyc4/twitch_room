package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"twitch_room/internal/performance"
)

var ErrInvalidIdentity = errors.New("invalid identity")

const (
	TwitchAuthURL     = "https://id.twitch.tv/oauth2/token"
	TwitchUserInfoURL = "https://api.twitch.tv/helix/users"
)

type Provider interface {
	ResolveUserID(ctx context.Context, r *http.Request) (string, error)
}

type TwitchProvider struct {
	ClientID      string
	ClientSecret  string
	RedirectURI   string
	EnableDevAuth bool
	httpClient    *http.Client
}

type twitchTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error,omitempty"`
	ErrorDesc   string `json:"error_description,omitempty"`
}

type twitchUserResponse struct {
	Data []struct {
		ID    string `json:"id"`
		Login string `json:"login"`
	} `json:"data"`
}

func NewTwitchProvider() *TwitchProvider {
	cfg := performance.DefaultHTTPClientConfig()

	// Create a custom transport with connection pooling
	transport := &http.Transport{
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:     cfg.MaxConnsPerHost,
		IdleConnTimeout:     cfg.IdleConnTimeout,
	}

	return &TwitchProvider{
		ClientID:      os.Getenv("TWITCH_CLIENT_ID"),
		ClientSecret:  os.Getenv("TWITCH_CLIENT_SECRET"),
		RedirectURI:   os.Getenv("TWITCH_REDIRECT_URI"),
		EnableDevAuth: os.Getenv("ALLOW_DEV_AUTH") == "true",
		httpClient: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: transport,
		},
	}
}

// exchangeCodeForToken performs the OAuth 2.0 authorization code exchange with Twitch.
func (p *TwitchProvider) exchangeCodeForToken(ctx context.Context, code string) (string, error) {
	if p.ClientID == "" || p.ClientSecret == "" {
		return "", fmt.Errorf("%w: missing client credentials", ErrInvalidIdentity)
	}
	if code == "" {
		return "", fmt.Errorf("%w: authorization code is empty", ErrInvalidIdentity)
	}

	params := url.Values{}
	params.Set("client_id", p.ClientID)
	params.Set("client_secret", p.ClientSecret)
	params.Set("code", code)
	params.Set("grant_type", "authorization_code")
	params.Set("redirect_uri", p.RedirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TwitchAuthURL, strings.NewReader(params.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to exchange code for token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read token response: %w", err)
	}

	var tokenResp twitchTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}

	if tokenResp.Error != "" {
		return "", fmt.Errorf("%w: %s (%s)", ErrInvalidIdentity, tokenResp.Error, tokenResp.ErrorDesc)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("%w: no access token in response", ErrInvalidIdentity)
	}

	return tokenResp.AccessToken, nil
}

// getUserIDFromToken fetches the user ID from Twitch using a valid access token.
func (p *TwitchProvider) getUserIDFromToken(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, TwitchUserInfoURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create user info request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	req.Header.Set("Client-ID", p.ClientID)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch user info: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read user info response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: received status %d from Twitch API", ErrInvalidIdentity, resp.StatusCode)
	}

	var userResp twitchUserResponse
	if err := json.Unmarshal(body, &userResp); err != nil {
		return "", fmt.Errorf("failed to parse user info response: %w", err)
	}

	if len(userResp.Data) == 0 {
		return "", fmt.Errorf("%w: no user data in response", ErrInvalidIdentity)
	}

	return userResp.Data[0].Login, nil
}

func (p *TwitchProvider) ResolveUserID(ctx context.Context, r *http.Request) (string, error) {
	// 1. Try OAuth flow first if we detect a code parameter
	if p.ClientID != "" && p.ClientSecret != "" && p.RedirectURI != "" {
		code := r.URL.Query().Get("code")
		if code != "" {
			// Exchange authorization code for access token
			token, err := p.exchangeCodeForToken(ctx, code)
			if err == nil {
				// Use the access token to fetch user ID from Twitch
				userID, err := p.getUserIDFromToken(ctx, token)
				if err == nil {
					return userID, nil
				}
				// Log error but continue to fallback
				fmt.Printf("WARNING: failed to get user info from token: %v\n", err)
			}
		}
	}

	// 2. Try Bearer token validation with Twitch
	authorization := r.Header.Get("Authorization")
	if authorization != "" {
		parts := strings.SplitN(authorization, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			token := strings.TrimSpace(parts[1])
			if token != "" && p.ClientID != "" {
				// Validate the token with Twitch API
				userID, err := p.getUserIDFromToken(ctx, token)
				if err == nil {
					return userID, nil
				}
			}
		}
	}

	// 3. Fallback to Dev Auth (if enabled)
	if p.EnableDevAuth {
		userID := strings.TrimSpace(r.Header.Get("X-User-ID"))
		if userID != "" {
			return userID, nil
		}
	}

	return "", ErrInvalidIdentity
}
