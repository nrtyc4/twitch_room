package identity

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestTwitchProviderAcceptsDevHeader(t *testing.T) {
	provider := &TwitchProvider{EnableDevAuth: true}
	req := httptest.NewRequest("GET", "/v1/watch", nil)
	req.Header.Set("X-User-ID", "dev-user")

	userID, err := provider.ResolveUserID(context.Background(), req)
	if err != nil {
		t.Fatalf("expected dev user id to resolve, got error: %v", err)
	}
	if userID != "dev-user" {
		t.Fatalf("expected dev-user, got %s", userID)
	}
}

func TestTwitchProviderRejectsMissingBearerToken(t *testing.T) {
	provider := &TwitchProvider{EnableDevAuth: false}
	req := httptest.NewRequest("GET", "/v1/watch", nil)

	if _, err := provider.ResolveUserID(context.Background(), req); err == nil {
		t.Fatal("expected missing bearer token to fail")
	}
}

func TestTwitchProviderRejectsInvalidBearerTokenFormat(t *testing.T) {
	provider := &TwitchProvider{EnableDevAuth: false}
	req := httptest.NewRequest("GET", "/v1/watch", nil)
	// Missing "Bearer " prefix
	req.Header.Set("Authorization", "token-value")

	if _, err := provider.ResolveUserID(context.Background(), req); err == nil {
		t.Fatal("expected invalid token format to fail")
	}
}

func TestTwitchProviderFallsBackToDevAuthWhenNoValidBearerToken(t *testing.T) {
	provider := &TwitchProvider{
		EnableDevAuth: true,
		ClientID:      "client-id",
		ClientSecret:  "client-secret",
		// Note: no proper Twitch credentials for real API calls
	}
	req := httptest.NewRequest("GET", "/v1/watch", nil)
	req.Header.Set("X-User-ID", "fallback-user")

	userID, err := provider.ResolveUserID(context.Background(), req)
	if err != nil {
		t.Fatalf("expected fallback to dev auth, got error: %v", err)
	}
	if userID != "fallback-user" {
		t.Fatalf("expected fallback-user, got %s", userID)
	}
}

func TestTwitchProviderBuildAuthorizeURL(t *testing.T) {
	provider := &TwitchProvider{
		ClientID:    "client-id-123",
		RedirectURI: "https://app.example.com/auth/twitch/callback",
	}

	authURL, err := provider.BuildAuthorizeURL("state-token", []string{"openid", "user:read:follows"})
	if err != nil {
		t.Fatalf("expected authorize URL to build, got error: %v", err)
	}

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("failed to parse authorize URL: %v", err)
	}

	if parsed.Scheme != "https" || parsed.Host != "id.twitch.tv" || parsed.Path != "/oauth2/authorize" {
		t.Fatalf("unexpected authorize URL: %s", authURL)
	}

	q := parsed.Query()
	if q.Get("client_id") != "client-id-123" {
		t.Fatalf("expected client_id, got %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "https://app.example.com/auth/twitch/callback" {
		t.Fatalf("expected redirect_uri, got %q", q.Get("redirect_uri"))
	}
	if q.Get("response_type") != "code" {
		t.Fatalf("expected response_type=code, got %q", q.Get("response_type"))
	}
	if q.Get("state") != "state-token" {
		t.Fatalf("expected state, got %q", q.Get("state"))
	}
	if q.Get("scope") != "openid user:read:follows" {
		t.Fatalf("expected scopes, got %q", q.Get("scope"))
	}
}

func TestTwitchProviderBuildAuthorizeURLMissingCredentials(t *testing.T) {
	provider := &TwitchProvider{}

	if _, err := provider.BuildAuthorizeURL("state", nil); err == nil {
		t.Fatal("expected missing client credentials to fail")
	}
}
