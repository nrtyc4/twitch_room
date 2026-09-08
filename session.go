package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookieName = "twitch_room_session"
	sessionTTL        = 24 * time.Hour
	sessionTokenBytes = 32
)

// sessionStore is an in-memory store of opaque session tokens issued after a
// successful Twitch OAuth login. Sessions survive server restarts no longer
// than the in-memory oauth state store; with multiple instances a shared store
// would be required.
type sessionStore struct {
	mu      sync.RWMutex
	tokens  map[string]*sessionEntry
	timeout time.Duration
}

type sessionEntry struct {
	userID    string
	expiresAt time.Time
}

func newSessionStore(timeout time.Duration) *sessionStore {
	return &sessionStore{
		tokens:  make(map[string]*sessionEntry),
		timeout: timeout,
	}
}

func (s *sessionStore) create(userID string) (string, error) {
	b := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = &sessionEntry{
		userID:    userID,
		expiresAt: time.Now().Add(s.timeout),
	}
	return token, nil
}

func (s *sessionStore) userID(token string) (string, bool) {
	if token == "" {
		return "", false
	}

	s.mu.RLock()
	entry, ok := s.tokens[token]
	s.mu.RUnlock()
	if !ok {
		return "", false
	}
	if time.Now().After(entry.expiresAt) {
		s.destroy(token)
		return "", false
	}
	return entry.userID, true
}

func (s *sessionStore) destroy(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

// userIDFromRequest resolves the caller identity from the session cookie if a
// valid session is present.
func (s *sessionStore) userIDFromRequest(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	return s.userID(cookie.Value)
}

// setSessionCookie stores a new session for the user and attaches it to the
// response. The cookie is HttpOnly, SameSite=Lax (blocks cross-site sending)
// and Secure when served over TLS.
func (s *sessionStore) setSessionCookie(w http.ResponseWriter, r *http.Request, userID string) error {
	token, err := s.create(userID)
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(s.timeout),
	})
	return nil
}

// destroySessionCookie removes the session and expires the cookie.
func (s *sessionStore) destroySessionCookie(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.destroy(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}
