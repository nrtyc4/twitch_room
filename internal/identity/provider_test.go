package identity
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
	provider := &TwitchProvider{ClientID: "client-id"}
	req := httptest.NewRequest("GET", "/v1/watch", nil)

	if _, err := provider.ResolveUserID(context.Background(), req); err == nil {
		t.Fatal("expected missing bearer token to fail")
	}
}

func TestTwitchProviderAcceptsBearerTokenShape(t *testing.T) {
	provider := &TwitchProvider{ClientID: "client-id"}
	req := httptest.NewRequest("GET", "/v1/watch", nil)
	req.Header.Set("Authorization", "Bearer token-value")

	userID, err := provider.ResolveUserID(context.Background(), req)
	if err != nil {
		t.Fatalf("expected valid bearer token shape to pass, got error: %v", err)
	}
	if userID != "twitch-user" {
		t.Fatalf("expected twitch-user, got %s", userID)
	}
}
