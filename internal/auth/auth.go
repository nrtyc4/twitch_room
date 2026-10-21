package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"strings"

	"twitch_room/internal/validate"
)

var ErrUnauthorized = errors.New("unauthorized")

const (
	HeaderAuthorization = "Authorization"
	HeaderUserID        = "X-User-ID"
)

type Middleware struct {
	ExpectedToken string
	AllowDevAuth  bool
}

func NewMiddleware() *Middleware {
	return &Middleware{
		ExpectedToken: os.Getenv("TWITCH_ROOM_AUTH_TOKEN"),
		AllowDevAuth:  os.Getenv("ALLOW_DEV_AUTH") == "true",
	}
}

func (m *Middleware) RequireUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, err := m.UserID(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		r = r.WithContext(ContextWithUserID(r.Context(), userID))
		next(w, r)
	}
}

func (m *Middleware) UserID(r *http.Request) (string, error) {
	if m.ExpectedToken != "" {
		authorization := r.Header.Get(HeaderAuthorization)
		if strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
			token := strings.TrimSpace(authorization[7:])
			if subtle.ConstantTimeCompare([]byte(token), []byte(m.ExpectedToken)) == 1 {
				return headerUserID(r)
			}
		}
		// If a token is expected but not provided or incorrect, do not fall through to dev auth
		return "", ErrUnauthorized
	}

	if m.AllowDevAuth {
		return headerUserID(r)
	}

	return "", ErrUnauthorized
}

// headerUserID extracts and validates the X-User-ID header so that
// identifiers persisted and echoed by the API always conform to the strict
// name rules.
func headerUserID(r *http.Request) (string, error) {
	userID := strings.TrimSpace(r.Header.Get(HeaderUserID))
	if userID == "" {
		return "", ErrUnauthorized
	}
	if err := validate.UserID(userID); err != nil {
		return "", err
	}
	return userID, nil
}

// Authorized reports whether the request carries the configured bearer token.
// When no token is configured the endpoint is public to unauthenticated
// callers (used to gate aggregate endpoints such as /metrics).
func (m *Middleware) Authorized(r *http.Request) bool {
	if m.ExpectedToken == "" {
		return true
	}
	authorization := r.Header.Get(HeaderAuthorization)
	if !strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
		return false
	}
	token := strings.TrimSpace(authorization[7:])
	return subtle.ConstantTimeCompare([]byte(token), []byte(m.ExpectedToken)) == 1
}

type contextKey string

const userIDContextKey contextKey = "user_id"

func ContextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDContextKey, userID)
}

func UserIDFromContext(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(userIDContextKey).(string)
	return userID, ok
}
