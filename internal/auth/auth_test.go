package auth

import (
	"net/http/httptest"
	"os"
	"testing"
)

func TestUserIDFromBearerToken(t *testing.T) {
	old := os.Getenv("TWITCH_ROOM_AUTH_TOKEN")
	if err := os.Setenv("TWITCH_ROOM_AUTH_TOKEN", "secret-token"); err != nil {
		t.Fatalf("Setenv failed: %v", err)
	}
	defer func() {
		_ = os.Setenv("TWITCH_ROOM_AUTH_TOKEN", old)
	}()

	req := httptest.NewRequest("GET", "/v1/watch", nil)
	req.Header.Set(HeaderAuthorization, "Bearer secret-token")
	req.Header.Set(HeaderUserID, "user-1")

	mw := NewMiddleware()
	userID, err := mw.UserID(req)
	if err != nil {
		t.Fatalf("expected valid user id, got error: %v", err)
	}
	if userID != "user-1" {
		t.Fatalf("expected user-1, got %s", userID)
	}
}

func TestUserIDFromDevAuth(t *testing.T) {
	old := os.Getenv("ALLOW_DEV_AUTH")
	if err := os.Setenv("ALLOW_DEV_AUTH", "true"); err != nil {
		t.Fatalf("Setenv failed: %v", err)
	}
	defer func() {
		_ = os.Setenv("ALLOW_DEV_AUTH", old)
	}()

	req := httptest.NewRequest("GET", "/v1/watch", nil)
	req.Header.Set(HeaderUserID, "user-dev")

	mw := NewMiddleware()
	userID, err := mw.UserID(req)
	if err != nil {
		t.Fatalf("expected valid dev user id, got error: %v", err)
	}
	if userID != "user-dev" {
		t.Fatalf("expected user-dev, got %s", userID)
	}
}

func TestUserIDRejectsMissingHeaders(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/watch", nil)
	mw := NewMiddleware()
	if _, err := mw.UserID(req); err == nil {
		t.Fatal("expected unauthorized error for missing auth")
	}
}
