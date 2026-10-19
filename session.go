package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"twitch_room/internal/repository"
)

const (
	sessionCookieName = "twitch_room_session"
	sessionTTL        = 24 * time.Hour
	sessionTokenBytes = 32
)

// sessionBackend persists the token -> user mapping behind the cookie
// handling. The default is an in-memory map; when PostgreSQL is configured a
// postgresSessionBackend is used so sessions survive restarts and are shared
// across instances.
type sessionBackend interface {
	save(token, userID, accessToken string, expiresAt time.Time) error
	userID(token string) (string, bool)
	accessToken(token string) (string, bool)
	destroy(token string)
}

// sessionStore issues opaque session tokens after a successful Twitch OAuth
// login and keeps the mapping between a token and its user.
type sessionStore struct {
	backend       sessionBackend
	timeout       time.Duration
	secureCookies bool
}

// enableSecureCookies forces the Secure flag on issued session cookies even
// when the request did not arrive over a TLS connection. Use this when TLS is
// terminated by a reverse proxy in front of the service.
func (s *sessionStore) enableSecureCookies() {
	s.secureCookies = true
}

func newSessionStore(timeout time.Duration) *sessionStore {
	return &sessionStore{
		backend: newMemorySessionBackend(),
		timeout: timeout,
	}
}

func newSessionStoreWithBackend(timeout time.Duration, backend sessionBackend) *sessionStore {
	return &sessionStore{
		backend: backend,
		timeout: timeout,
	}
}

// memorySessionBackend is the in-memory token store used when no database is
// configured.
type memorySessionBackend struct {
	mu     sync.RWMutex
	tokens map[string]*sessionEntry
}

type sessionEntry struct {
	userID      string
	accessToken string
	expiresAt   time.Time
}

func newMemorySessionBackend() *memorySessionBackend {
	return &memorySessionBackend{tokens: make(map[string]*sessionEntry)}
}

func (b *memorySessionBackend) save(token, userID, accessToken string, expiresAt time.Time) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens[token] = &sessionEntry{userID: userID, accessToken: accessToken, expiresAt: expiresAt}
	return nil
}

func (b *memorySessionBackend) userID(token string) (string, bool) {
	entry, ok := b.entry(token)
	if !ok {
		return "", false
	}
	return entry.userID, true
}

func (b *memorySessionBackend) accessToken(token string) (string, bool) {
	entry, ok := b.entry(token)
	if !ok {
		return "", false
	}
	return entry.accessToken, entry.accessToken != ""
}

// entry returns the live (non-expired) session entry for a token, destroying
// expired sessions as side effect.
func (b *memorySessionBackend) entry(token string) (sessionEntry, bool) {
	if token == "" {
		return sessionEntry{}, false
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.tokens[token]
	if !ok {
		return sessionEntry{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(b.tokens, token)
		return sessionEntry{}, false
	}
	return *entry, true
}

func (b *memorySessionBackend) destroy(token string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.tokens, token)
}

// Cleanup removes expired sessions and returns how many were removed.
func (b *memorySessionBackend) Cleanup() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	removed := 0
	for token, entry := range b.tokens {
		if now.After(entry.expiresAt) {
			delete(b.tokens, token)
			removed++
		}
	}
	return removed
}

// postgresSessionBackend persists sessions in the shared database.
type postgresSessionBackend struct {
	store *repository.PostgresStore
}

func (b postgresSessionBackend) save(token, userID, accessToken string, expiresAt time.Time) error {
	return b.store.SaveSession(token, userID, accessToken, expiresAt)
}

func (b postgresSessionBackend) userID(token string) (string, bool) {
	return b.store.SessionUserID(token)
}

func (b postgresSessionBackend) accessToken(token string) (string, bool) {
	return b.store.SessionAccessToken(token)
}

func (b postgresSessionBackend) destroy(token string) {
	b.store.DeleteSession(token)
}

func (s *sessionStore) create(userID, accessToken string) (string, error) {
	b := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)

	if err := s.backend.save(token, userID, accessToken, time.Now().Add(s.timeout)); err != nil {
		return "", err
	}
	return token, nil
}

func (s *sessionStore) userID(token string) (string, bool) {
	return s.backend.userID(token)
}

// accessTokenFromRequest returns the stored Twitch access token for the
// request's session cookie, if one was attached at login time.
func (s *sessionStore) accessTokenFromRequest(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	return s.backend.accessToken(cookie.Value)
}

func (s *sessionStore) destroy(token string) {
	s.backend.destroy(token)
}

// Cleanup removes expired sessions from the backing store.
func (s *sessionStore) Cleanup() int {
	if c, ok := s.backend.(interface{ Cleanup() int }); ok {
		return c.Cleanup()
	}
	return 0
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
// response. accessToken is the optional Twitch user access token captured
// during a real OAuth login, used to power follow-based suggestions; it is
// empty for dev-auth sessions. The cookie is HttpOnly, SameSite=Lax (blocks
// cross-site sending) and Secure when served over TLS.
func (s *sessionStore) setSessionCookie(w http.ResponseWriter, r *http.Request, userID, accessToken string) error {
	token, err := s.create(userID, accessToken)
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil || s.secureCookies,
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
