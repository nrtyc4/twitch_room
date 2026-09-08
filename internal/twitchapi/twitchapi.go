package twitchapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"twitch_room/internal/service"
)

const (
	apiBase        = "https://api.twitch.tv/helix"
	tokenEndpoint  = "https://id.twitch.tv/oauth2/token"
	tokenBufferSec = 60
)

// Client is a Twitch Helix API client used to enrich published streams.
// It authenticates as the app itself via the client credentials flow.
type Client struct {
	clientID      string
	clientSecret  string
	httpClient    *http.Client
	apiBase       string
	tokenEndpoint string

	tokenMu   sync.Mutex
	token     string
	expiresAt time.Time
}

// NewClient creates a Helix client for app-level (client credentials) auth.
func NewClient(clientID, clientSecret string) *Client {
	return &Client{
		clientID:      clientID,
		clientSecret:  clientSecret,
		httpClient:    &http.Client{Timeout: 10 * time.Second},
		apiBase:       apiBase,
		tokenEndpoint: tokenEndpoint,
	}
}

// LookupStreams resolves channel logins against the Twitch API. Logins that do
// not correspond to an existing channel are reported with Exists=false. The
// returned map is keyed by the input login (lowercase).
func (c *Client) LookupStreams(ctx context.Context, logins []string) (map[string]service.StreamInfo, error) {
	if len(logins) == 0 {
		return map[string]service.StreamInfo{}, nil
	}

	result := make(map[string]service.StreamInfo, len(logins))
	unique := dedupeLogins(logins)
	for _, login := range unique {
		result[login] = service.StreamInfo{}
	}

	users, err := c.fetchUsers(ctx, unique)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		info := service.StreamInfo{
			Exists:      true,
			ChannelName: u.DisplayName,
		}
		result[strings.ToLower(u.Login)] = info
	}

	// Live metadata is best-effort: a failure degrades to channel existence
	// only rather than failing the whole publish.
	liveByLogin := map[string]helixStream{}
	if streams, err := c.fetchStreams(ctx, unique); err == nil {
		for _, st := range streams {
			liveByLogin[strings.ToLower(st.UserLogin)] = st
		}
	}

	for login, info := range result {
		if st, ok := liveByLogin[login]; ok {
			info.IsLive = true
			info.Title = st.Title
			info.GameName = st.GameName
			info.ThumbnailURL = sizedThumbnail(st.ThumbnailURL)
			info.ViewerCount = st.ViewerCount
		} else {
			info.Title = ""
			info.GameName = ""
			info.ThumbnailURL = ""
			info.ViewerCount = 0
		}
		result[login] = info
	}

	return result, nil
}

// token returns a cached app access token, fetching a fresh one when expired.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.token != "" && time.Now().Before(c.expiresAt.Add(-tokenBufferSec*time.Second)) {
		return c.token, nil
	}

	form := url.Values{}
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request app token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if payload.AccessToken == "" {
		return "", fmt.Errorf("token response did not include an access token")
	}

	c.token = payload.AccessToken
	if payload.ExpiresIn > 0 {
		c.expiresAt = time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second)
	} else {
		c.expiresAt = time.Now().Add(time.Hour)
	}
	return c.token, nil
}

func (c *Client) apiGet(ctx context.Context, path string, params url.Values) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}

	attempt := func(token string) (*http.Response, error) {
		req.Header.Set("Client-ID", c.clientID)
		req.Header.Set("Authorization", "Bearer "+token)
		return c.httpClient.Do(req)
	}

	tok, err := c.accessToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	resp, err := attempt(tok)
	if err != nil {
		return nil, 0, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		// Token may have been revoked; clear cache and retry once.
		c.tokenMu.Lock()
		c.token = ""
		c.tokenMu.Unlock()
		tok, err = c.accessToken(ctx)
		if err != nil {
			return nil, 0, err
		}
		resp, err = attempt(tok)
		if err != nil {
			return nil, 0, err
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("helix %s returned %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, resp.StatusCode, nil
}

type helixUser struct {
	ID          string `json:"id"`
	Login       string `json:"login"`
	DisplayName string `json:"display_name"`
}

func (c *Client) fetchUsers(ctx context.Context, logins []string) ([]helixUser, error) {
	params := url.Values{}
	for _, l := range logins {
		params.Add("login", l)
	}
	body, _, err := c.apiGet(ctx, "/users", params)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data []helixUser `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode users response: %w", err)
	}
	return payload.Data, nil
}

type helixStream struct {
	UserID       string `json:"user_id"`
	UserLogin    string `json:"user_login"`
	UserName     string `json:"user_name"`
	GameName     string `json:"game_name"`
	Title        string `json:"title"`
	ViewerCount  int    `json:"viewer_count"`
	ThumbnailURL string `json:"thumbnail_url"`
}

func (c *Client) fetchStreams(ctx context.Context, logins []string) ([]helixStream, error) {
	params := url.Values{}
	for _, l := range logins {
		params.Add("user_login", l)
	}
	body, _, err := c.apiGet(ctx, "/streams", params)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data []helixStream `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode streams response: %w", err)
	}
	return payload.Data, nil
}

// sizedThumbnail swaps Helix's {width}/{height} placeholders for a 320x180
// crop and returns the concrete URL, or "" when the template is missing.
func sizedThumbnail(template string) string {
	if template == "" {
		return ""
	}
	return strings.NewReplacer("{width}", "320", "{height}", "180").Replace(template)
}

func dedupeLogins(logins []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(logins))
	for _, l := range logins {
		k := strings.ToLower(strings.TrimSpace(l))
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
