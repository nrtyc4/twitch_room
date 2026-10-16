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
	usersChunkSize = 100
	userCacheTTL   = 15 * time.Minute
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

	usersMu   sync.Mutex
	userCache map[string]userCacheEntry
}

type userCacheEntry struct {
	displayName string
	avatarURL   string
	fetchedAt   time.Time
}

// NewClient creates a Helix client for app-level (client credentials) auth.
func NewClient(clientID, clientSecret string) *Client {
	return &Client{
		clientID:      clientID,
		clientSecret:  clientSecret,
		httpClient:    &http.Client{Timeout: 10 * time.Second},
		apiBase:       apiBase,
		tokenEndpoint: tokenEndpoint,
		userCache:     make(map[string]userCacheEntry),
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
	ProfileImage string `json:"profile_image_url"`
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

// FollowedStream is a live stream belonging to one of the token holder's
// followed channels.
type FollowedStream struct {
	UserLogin    string `json:"user_login"`
	UserName     string `json:"user_name"`
	GameName     string `json:"game_name"`
	Title        string `json:"title"`
	ViewerCount  int    `json:"viewer_count"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// FollowedLiveStreams fetches the channels the authenticated user follows
// that are live right now, sorted by viewer count. It is the data source for
// one-click watch publishing. The caller must supply the user's own OAuth
// access token (scopes: user:read:follows); the numeric Twitch ID required
// by /streams/followed is resolved from that same token. An empty token
// yields no streams.
func (c *Client) FollowedLiveStreams(ctx context.Context, userAccessToken string) ([]FollowedStream, error) {
	if userAccessToken == "" {
		return nil, nil
	}

	var userPayload struct {
		Data []helixUser `json:"data"`
	}
	body, err := c.userTokenGet(ctx, userAccessToken, "/users", url.Values{})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &userPayload); err != nil {
		return nil, fmt.Errorf("decode users response: %w", err)
	}
	if len(userPayload.Data) == 0 {
		return nil, nil
	}

	params := url.Values{}
	params.Set("user_id", userPayload.Data[0].ID)
	params.Set("first", "100")

	body, err = c.userTokenGet(ctx, userAccessToken, "/streams/followed", params)
	if err != nil {
		return nil, err
	}
	var streamPayload struct {
		Data []FollowedStream `json:"data"`
	}
	if err := json.Unmarshal(body, &streamPayload); err != nil {
		return nil, fmt.Errorf("decode followed streams response: %w", err)
	}
	for i := range streamPayload.Data {
		streamPayload.Data[i].ThumbnailURL = sizedThumbnail(streamPayload.Data[i].ThumbnailURL)
	}
	sort.Slice(streamPayload.Data, func(i, j int) bool {
		return streamPayload.Data[i].ViewerCount > streamPayload.Data[j].ViewerCount
	})
	return streamPayload.Data, nil
}

// userTokenGet performs a Helix GET authenticated as a Twitch user (rather
// than as the app), using the supplied user access token.
func (c *Client) userTokenGet(ctx context.Context, userAccessToken, path string, params url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+path+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Client-ID", c.clientID)
	req.Header.Set("Authorization", "Bearer "+userAccessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("helix %s returned %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// sizedThumbnail swaps Helix's {width}/{height} placeholders for a 320x180
// crop and returns the concrete URL, or "" when the template is missing.
func sizedThumbnail(template string) string {
	if template == "" {
		return ""
	}
	return strings.NewReplacer("{width}", "320", "{height}", "180").Replace(template)
}

// ResolveUsers returns the Twitch display name for each supplied user ID
// (assumed to be a channel login), keyed by lowercase login. Unknown or
// unresolvable IDs are absent from the map. Results are cached for the TTL so
// repeated feed reads do not hammer the users endpoint, and lookups are
// chunked to Helix's per-request filter limit.
func (c *Client) ResolveUsers(ctx context.Context, userIDs []string) (map[string]service.UserInfo, error) {
	unique := dedupeLogins(userIDs)
	if len(unique) == 0 {
		return map[string]service.UserInfo{}, nil
	}
	now := time.Now()

	out := make(map[string]service.UserInfo, len(unique))

	c.usersMu.Lock()
	var missing []string
	for _, id := range unique {
		if entry, ok := c.userCache[id]; ok && now.Sub(entry.fetchedAt) < userCacheTTL {
			if entry.displayName != "" {
				out[id] = service.UserInfo{
					DisplayName: entry.displayName,
					AvatarURL:   entry.avatarURL,
				}
			}
			continue
		}
		missing = append(missing, id)
	}
	c.usersMu.Unlock()

	if len(missing) == 0 {
		return out, nil
	}

	for i := 0; i < len(missing); i += usersChunkSize {
		end := i + usersChunkSize
		if end > len(missing) {
			end = len(missing)
		}
		users, err := c.fetchUsers(ctx, missing[i:end])
		if err != nil {
			return out, err
		}
		c.usersMu.Lock()
		for _, id := range missing[i:end] {
			c.userCache[id] = userCacheEntry{fetchedAt: now}
		}
		for _, u := range users {
			key := strings.ToLower(u.Login)
			c.userCache[key] = userCacheEntry{
				displayName: u.DisplayName,
				avatarURL:   u.ProfileImage,
				fetchedAt:   now,
			}
			out[key] = service.UserInfo{
				DisplayName: u.DisplayName,
				AvatarURL:   u.ProfileImage,
			}
		}
		c.usersMu.Unlock()
	}

	return out, nil
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
