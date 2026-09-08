package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"twitch_room/internal/auth"
	"twitch_room/internal/config"
	"twitch_room/internal/identity"
	"twitch_room/internal/performance"
	"twitch_room/internal/repository"
	"twitch_room/internal/secrets"
	"twitch_room/internal/security"
	"twitch_room/internal/service"
)

type watchRequest struct {
	StreamIDs []string `json:"stream_ids"`
}

type server struct {
	watchService       *service.Service
	authMiddleware     *auth.Middleware
	identityProvider   identity.Provider
	rateLimiter        *security.RateLimiter
	watchStateCache    *performance.WatchStateCache
	oauthStates        *oauthStateStore
	sessions           *sessionStore
	redirectAfterLogin string
}

// oauthStateStore tracks pending OAuth state values for CSRF protection.
type oauthStateStore struct {
	mu      sync.Mutex
	states  map[string]time.Time
	timeout time.Duration
}

func newOAuthStateStore(timeout time.Duration) *oauthStateStore {
	return &oauthStateStore{
		states:  make(map[string]time.Time),
		timeout: timeout,
	}
}

func (o *oauthStateStore) Put(state string) {
	if state == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.states[state] = time.Now().Add(o.timeout)
}

func (o *oauthStateStore) Verify(state string) bool {
	if state == "" {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	expires, ok := o.states[state]
	if !ok {
		return false
	}
	delete(o.states, state)
	return time.Now().Before(expires)
}

func main() {
	fmt.Println("Initializing twitch-room service")

	// Load secrets (optional .env file for local development)
	if err := secrets.LoadDotEnv(".env"); err != nil {
		log.Printf("Warning: could not load .env file: %v", err)
	}

	// Load secrets from environment
	sec, err := secrets.Load()
	if err != nil {
		log.Fatalf("Failed to load secrets: %v", err)
	}

	log.Printf("Secrets loaded: %s", sec)

	cfg := config.Load()

	var store repository.WatchStateStore

	// Try PostgreSQL first if DSN is configured
	dbDSN := sec.DatabaseDSN
	if dbDSN != "" {
		pgStore, err := repository.NewPostgresStore(dbDSN)
		if err != nil {
			log.Printf("WARNING: failed to connect to PostgreSQL: %v, falling back to in-memory store", err)
			store = repository.NewMemoryStore()
		} else {
			log.Printf("Connected to PostgreSQL database")
			store = pgStore
			// Optionally defer cleanup (commented out for now as it would close on every request)
			// defer pgStore.Close()
		}
	} else {
		log.Printf("No database DSN configured, using in-memory store")
		store = repository.NewMemoryStore()
	}

	repo := repository.NewServiceAdapter(store)
	svc := service.NewService(repo, time.Duration(cfg.WatchStateTTLMin)*time.Minute)

	// Create identity provider with Twitch OAuth configuration.
	// NewTwitchProvider sets up the pooled HTTP client used for the token
	// exchange; override the env-derived knobs with our loaded config.
	identityProvider := identity.NewTwitchProvider()
	identityProvider.ClientID = sec.TwitchClientID
	identityProvider.ClientSecret = sec.TwitchClientSecret
	identityProvider.RedirectURI = sec.TwitchRedirectURI
	identityProvider.EnableDevAuth = cfg.AllowDevAuth

	// Create watch state cache with same TTL as service
	cacheTTL := time.Duration(cfg.WatchStateTTLMin) * time.Minute
	watchStateCache := performance.NewWatchStateCache(cacheTTL)

	s := &server{
		watchService:       svc,
		authMiddleware:     &auth.Middleware{ExpectedToken: sec.AuthToken, AllowDevAuth: cfg.AllowDevAuth},
		identityProvider:   identityProvider,
		rateLimiter:        security.NewRateLimiter(30, time.Minute),
		watchStateCache:    watchStateCache,
		oauthStates:        newOAuthStateStore(15 * time.Minute),
		sessions:           newSessionStore(sessionTTL),
		redirectAfterLogin: cfg.RedirectAfterLogin,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/watch", s.handleWatch)
	mux.HandleFunc("/v1/users/", s.handleUserWatch)
	mux.HandleFunc("/v1/feed", s.handleFeed)
	mux.HandleFunc("/v1/settings", s.handleSettings)
	mux.HandleFunc("/v1/subscriptions", s.handleSubscriptions)
	mux.HandleFunc("/v1/subscriptions/", s.handleSubscription)
	mux.HandleFunc("/v1/subscription_requests", s.handleSubscriptionRequests)
	mux.HandleFunc("/v1/subscription_requests/", s.handleSubscriptionRequestAction)
	mux.HandleFunc("/auth/twitch", s.handleTwitchLogin)
	mux.HandleFunc("/auth/twitch/callback", s.handleTwitchCallback)
	mux.HandleFunc("/auth/me", s.handleMe)
	mux.HandleFunc("/auth/logout", s.handleLogout)

	webHandler, err := newWebHandler()
	if err != nil {
		log.Fatalf("failed to init web handler: %v", err)
	}
	mux.Handle("/", webHandler)

	log.Printf("twitch-room service starting on :%s", cfg.Port)
	if err := http.ListenAndServe(":"+cfg.Port, withLogging(mux)); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) handleWatch(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	if !s.rateLimiter.Allow(userID) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}

	if r.Method == http.MethodDelete {
		if err := s.watchService.ClearUserWatchState(userID); err != nil {
			if errors.Is(err, service.ErrUserNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "watch state not found"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.watchStateCache.Invalidate(userID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method == http.MethodGet {
		// Try cache first
		if cached, ok := s.watchStateCache.Get(userID); ok {
			writeJSON(w, http.StatusOK, cached)
			return
		}

		// Cache miss - fetch from service
		state, err := s.watchService.GetUserWatchState(userID)
		if err != nil {
			if errors.Is(err, service.ErrUserNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "watch state not found"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		// Cache the result
		s.watchStateCache.Set(userID, state)
		writeJSON(w, http.StatusOK, state)
		return
	}

	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var payload watchRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	state, err := s.watchService.SetUserWatchState(userID, payload.StreamIDs)
	if err != nil {
		if errors.Is(err, service.ErrInvalidStream) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one valid stream ID is required"})
			return
		}
		if errors.Is(err, service.ErrInvalidUser) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Invalidate cache on write
	s.watchStateCache.Invalidate(userID)

	writeJSON(w, http.StatusCreated, state)
}

func (s *server) handleUserWatch(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}

	pathUserID := strings.TrimPrefix(r.URL.Path, "/v1/users/")
	pathUserID = strings.TrimSuffix(pathUserID, "/watch")
	pathUserID = strings.TrimSpace(pathUserID)
	if pathUserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing user id"})
		return
	}

	canView, err := s.watchService.CanView(userID, pathUserID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !canView {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "you are not allowed to view this watch state"})
		return
	}

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// Try cache first
	if cached, ok := s.watchStateCache.Get(pathUserID); ok {
		writeJSON(w, http.StatusOK, cached)
		return
	}

	// Cache miss - fetch from service
	state, err := s.watchService.GetUserWatchState(pathUserID)
	if err != nil {
		if errors.Is(err, service.ErrUserNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "watch state not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Cache the result
	s.watchStateCache.Set(pathUserID, state)
	writeJSON(w, http.StatusOK, state)
}

func (s *server) handleFeed(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	if !s.rateLimiter.Allow(userID) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	feed := s.watchService.ListFeed(userID, 20)
	writeJSON(w, http.StatusOK, feed)
}

func (s *server) handleSettings(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}

	if r.Method == http.MethodGet {
		settings, err := s.watchService.GetPublisherSettings(userID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, settings)
		return
	}

	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	var payload service.PublisherSettings
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	payload.UserID = userID

	settings, err := s.watchService.UpdatePublisherSettings(payload)
	if err != nil {
		if errors.Is(err, service.ErrInvalidSettings) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "subscription_mode must be 'open' or 'approved' and visibility must be 'public', 'subscribers' or 'private'"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, settings)
}

func (s *server) handleSubscriptions(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	subs, err := s.watchService.ListSubscriptions(userID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, subs)
}

func (s *server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}

	publisherID := strings.TrimPrefix(r.URL.Path, "/v1/subscriptions/")
	publisherID = strings.TrimSpace(publisherID)
	if publisherID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing publisher id"})
		return
	}

	switch r.Method {
	case http.MethodPost:
		sub, err := s.watchService.Subscribe(userID, publisherID)
		if err != nil {
			if errors.Is(err, service.ErrCannotSubscribeToSelf) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "you cannot subscribe to yourself"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		status := http.StatusCreated
		if sub.Status == service.SubscriptionStatusPending {
			status = http.StatusAccepted
		}
		writeJSON(w, status, sub)
	case http.MethodDelete:
		if err := s.watchService.Unsubscribe(userID, publisherID); err != nil {
			if errors.Is(err, service.ErrNotSubscribed) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not subscribed"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *server) handleSubscriptionRequests(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	requests, err := s.watchService.ListPendingRequests(userID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, requests)
}

func (s *server) handleSubscriptionRequestAction(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/v1/subscription_requests/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected /v1/subscription_requests/{user_id}/{approve|reject}"})
		return
	}
	subscriberID := strings.TrimSpace(parts[0])
	action := strings.TrimSpace(parts[1])

	var approve bool
	switch action {
	case "approve":
		approve = true
	case "reject":
		approve = false
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be 'approve' or 'reject'"})
		return
	}

	if err := s.watchService.RespondToSubscriber(userID, subscriberID, approve); err != nil {
		if errors.Is(err, service.ErrNotSubscribed) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "subscription not found"})
			return
		}
		if errors.Is(err, service.ErrNoPendingRequest) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "no pending subscription request"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleTwitchLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	state := r.URL.Query().Get("state")
	if state == "" {
		state = randomState()
	}

	if s.oauthStates != nil {
		s.oauthStates.Put(state)
	}

	authURL, err := s.identityProvider.BuildAuthorizeURL(state, nil)
	if err != nil {
		log.Printf("failed to build authorize URL: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "OAuth not configured"})
		return
	}

	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *server) handleTwitchCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	if s.oauthStates != nil && !s.oauthStates.Verify(r.URL.Query().Get("state")) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or missing state"})
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing code"})
		return
	}

	userID, err := s.identityProvider.ResolveUserID(r.Context(), r)
	if err != nil {
		log.Printf("failed to resolve user id from OAuth code: %v", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication failed"})
		return
	}

	// Issue a session cookie so the browser can authenticate API calls without
	// holding the raw Twitch token.
	if s.sessions != nil {
		if err := s.sessions.setSessionCookie(w, r, userID); err != nil {
			log.Printf("failed to create session: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "authentication failed"})
			return
		}
	}

	// Redirect the browser into the app. The user_id query parameter is kept
	// as a convenience for clients that do not use cookies.
	redirect := s.redirectAfterLogin
	if redirect == "" {
		redirect = "/"
	}
	sep := "?"
	if strings.Contains(redirect, "?") {
		sep = "&"
	}
	http.Redirect(w, r, redirect+sep+"user_id="+url.QueryEscape(userID), http.StatusFound)
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	userID, ok := s.requireUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"user_id": userID})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	if s.sessions != nil {
		s.sessions.destroySessionCookie(w, r)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) requireUser(r *http.Request) (string, bool) {
	if userID, ok := auth.UserIDFromContext(r.Context()); ok && userID != "" {
		return userID, true
	}

	// A session cookie issued after Twitch login authenticates the caller.
	if s.sessions != nil {
		if userID, ok := s.sessions.userIDFromRequest(r); ok {
			return userID, true
		}
	}

	userID, err := s.authMiddleware.UserID(r)
	if err == nil {
		return userID, true
	}

	// Fall back to the identity provider, which validates real Twitch OAuth
	// bearer tokens (and dev auth when enabled).
	if s.identityProvider != nil {
		userID, err = s.identityProvider.ResolveUserID(r.Context(), r)
		if err != nil {
			return "", false
		}
		return userID, true
	}

	return "", false
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func randomState() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("failed to encode JSON response: %v", err)
	}
}

func init() {
	fmt.Println("Initializing twitch-room service")
}
