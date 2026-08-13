package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"strings"
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
				userID := strings.TrimSpace(r.Header.Get(HeaderUserID))
				if userID != "" {
					return userID, nil
				}
				return "", ErrUnauthorized
			}
		}
	}

	if m.AllowDevAuth {
		userID := strings.TrimSpace(r.Header.Get(HeaderUserID))
		if userID != "" {
			return userID, nil
		}
	}

	return "", ErrUnauthorized
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
