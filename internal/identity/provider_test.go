package identity

import (
	"context"
	"net/http/httptest"
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
