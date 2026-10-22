package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"twitch_room/internal/performance"
	"twitch_room/internal/validate"
)

var ErrInvalidIdentity = errors.New("invalid identity")

const (
	TwitchAuthorizeURL = "https://id.twitch.tv/oauth2/authorize"
	TwitchAuthURL      = "https://id.twitch.tv/oauth2/token"
	TwitchUserInfoURL  = "https://api.twitch.tv/helix/users"
)

type UserProfile struct {
	ID          string
	DisplayName string
	AvatarURL   string
}

type Provider interface {
	ResolveUserID(ctx context.Context, r *http.Request) (string, error)
	// ResolveIdentity resolves the request to a user login and, when the
	// request carries a fresh OAuth authorization code, also returns the
	// Twitch user access token obtained during the exchange. The token is
	// empty when identity was resolved through dev auth or an existing
	// bearer token.
	ResolveIdentity(ctx context.Context, r *http.Request) (userID, accessToken string, err error)
	// ResolveProfile fetches the full profile (name, avatar) for the authenticated user.
	ResolveProfile(ctx context.Context, r *http.Request) (UserProfile, error)
	BuildAuthorizeURL(state string, scopes []string) (string, error)
}

type TwitchProvider struct {
	ClientID      string
	ClientSecret  string
	RedirectURI   string
	EnableDevAuth bool
	httpClient    *http.Client

	// appAccessToken caches a client-credentials token used to call Helix
	// endpoints (such as /users) without a user access token.
	appAccessToken string
	appTokenExpiry time.Time
	appTokenMu     sync.Mutex

	// identityCache caches the result of validating a user access token so
	// repeated API calls do not each hit Helix.
	identityCache   map[string]cachedIdentity
	identityCacheMu sync.Mutex
}

type cachedIdentity struct {
	userID    string
	fetchedAt time.Time
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
		ID           string `json:"id"`
		Login        string `json:"login"`
		DisplayName  string `json:"display_name"`
		ProfileImage string `json:"profile_image_url"`
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
		identityCache: make(map[string]cachedIdentity),
		httpClient: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: transport,
		},
	}
}

// BuildAuthorizeURL returns the Twitch OAuth 2.0 authorization URL that the
// client should be redirected to in order to start the login flow.
func (p *TwitchProvider) BuildAuthorizeURL(state string, scopes []string) (string, error) {
	if p.ClientID == "" || p.RedirectURI == "" {
		return "", fmt.Errorf("%w: missing client credentials", ErrInvalidIdentity)
	}

	params := url.Values{}
	params.Set("client_id", p.ClientID)
	params.Set("redirect_uri", p.RedirectURI)
	params.Set("response_type", "code")
	params.Set("state", state)
	if len(scopes) > 0 {
		params.Set("scope", strings.Join(scopes, " "))
	}

	return TwitchAuthorizeURL + "?" + params.Encode(), nil
}

// httpClientOrDefault returns the provider's HTTP client, or a default client
// if the provider was constructed without one (e.g. in tests).
func (p *TwitchProvider) httpClientOrDefault() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}

// getAppAccessToken returns a cached client-credentials access token, refreshing
// it when it is missing or close to expiry.
func (p *TwitchProvider) getAppAccessToken(ctx context.Context) (string, error) {
	p.appTokenMu.Lock()
	defer p.appTokenMu.Unlock()

	if p.appAccessToken != "" && time.Now().Before(p.appTokenExpiry) {
		return p.appAccessToken, nil
	}

	params := url.Values{}
	params.Set("client_id", p.ClientID)
	params.Set("client_secret", p.ClientSecret)
	params.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TwitchAuthURL, strings.NewReader(params.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClientOrDefault().Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch app access token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("failed to read app token response: %w", err)
	}

	var tokenResp twitchTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse app token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("no access token in app token response")
	}

	p.appAccessToken = tokenResp.AccessToken
	expiry := tokenResp.ExpiresIn
	if expiry <= 0 {
		expiry = 3600
	}
	p.appTokenExpiry = time.Now().Add(time.Duration(expiry-60) * time.Second)
	return p.appAccessToken, nil
}

// ResolveProfile fetches the full profile (name, avatar) for the authenticated user.
func (p *TwitchProvider) ResolveProfile(ctx context.Context, r *http.Request) (UserProfile, error) {
	userID, err := p.ResolveUserID(ctx, r)
	if err != nil {
		return UserProfile{}, err
	}

	// Dev auth / no credentials: return a local profile so /auth/me still
	// responds without hitting the Twitch API.
	if p.ClientID == "" || p.ClientSecret == "" {
		return UserProfile{ID: userID, DisplayName: userID}, nil
	}

	appToken, err := p.getAppAccessToken(ctx)
	if err != nil {
		slog.Warn("could not obtain app access token for profile resolution", "error", err)
		return UserProfile{ID: userID, DisplayName: userID}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, TwitchUserInfoURL, nil)
	if err != nil {
		return UserProfile{}, fmt.Errorf("failed to create user info request: %w", err)
	}

	q := req.URL.Query()
	// Twitch Helix /users accepts "id" (numeric user IDs) and "login" (logins).
	// "user_id" is not a valid parameter.
	if isNumeric(userID) {
		q.Add("id", userID)
	} else {
		q.Add("login", userID)
	}
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Client-ID", p.ClientID)
	req.Header.Set("Authorization", "Bearer "+appToken)

	resp, err := p.httpClientOrDefault().Do(req)
	if err != nil {
		return UserProfile{}, fmt.Errorf("failed to fetch user info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return UserProfile{}, fmt.Errorf("received status %d from Twitch API: %s", resp.StatusCode, string(bodySnippet))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return UserProfile{}, fmt.Errorf("failed to read user info response: %w", err)
	}

	var userResp twitchUserResponse
	if err := json.Unmarshal(body, &userResp); err != nil {
		return UserProfile{}, fmt.Errorf("failed to parse user info response: %w", err)
	}

	if len(userResp.Data) == 0 {
		return UserProfile{}, fmt.Errorf("no user data in response")
	}

	data := userResp.Data[0]
	displayName := data.DisplayName
	if displayName == "" {
		displayName = data.Login
	}
	return UserProfile{
		ID:          data.ID,
		DisplayName: displayName,
		AvatarURL:   data.ProfileImage,
	}, nil
}

// isNumeric reports whether s consists solely of ASCII digits.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

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

	resp, err := p.httpClientOrDefault().Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to exchange code for token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
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
	const ttl = 60 * time.Second
	p.identityCacheMu.Lock()
	if c, ok := p.identityCache[accessToken]; ok && time.Since(c.fetchedAt) < ttl {
		p.identityCacheMu.Unlock()
		return c.userID, nil
	}
	p.identityCacheMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, TwitchUserInfoURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create user info request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	req.Header.Set("Client-ID", p.ClientID)

	resp, err := p.httpClientOrDefault().Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch user info: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
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

	login := userResp.Data[0].Login
	p.identityCacheMu.Lock()
	// Bound the cache: drop expired entries, and if still too large, clear it.
	if len(p.identityCache) >= 1024 {
		now := time.Now()
		for k, v := range p.identityCache {
			if now.Sub(v.fetchedAt) >= ttl {
				delete(p.identityCache, k)
			}
		}
		if len(p.identityCache) >= 1024 {
			p.identityCache = make(map[string]cachedIdentity)
		}
	}
	p.identityCache[accessToken] = cachedIdentity{userID: login, fetchedAt: time.Now()}
	p.identityCacheMu.Unlock()
	return login, nil
}

// ResolveIdentity resolves the caller identity, attaching the freshly issued
// user access token when the request completes the OAuth code exchange. In
// every other path (bearer header, dev auth) the token slot is empty. A
// present-but-invalid code fails the request rather than falling back to dev
// auth, so a failed Twitch login never impersonates a header-supplied user.
func (p *TwitchProvider) ResolveIdentity(ctx context.Context, r *http.Request) (string, string, error) {
	if p.ClientID != "" && p.ClientSecret != "" && p.RedirectURI != "" {
		code := r.URL.Query().Get("code")
		if code != "" {
			token, err := p.exchangeCodeForToken(ctx, code)
			if err != nil {
				return "", "", err
			}
			userID, err := p.getUserIDFromToken(ctx, token)
			if err != nil {
				slog.Warn("failed to get user info from token", "error", err)
				return "", "", err
			}
			return userID, token, nil
		}
	}

	userID, err := p.ResolveUserID(ctx, r)
	return userID, "", err
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
				slog.Warn("failed to get user info from token", "error", err)
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
			if err := validate.UserID(userID); err != nil {
				return "", err
			}
			return userID, nil
		}
	}

	return "", ErrInvalidIdentity
}
