package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"twitch_room/internal/auth"
	"twitch_room/internal/config"
	"twitch_room/internal/identity"
	"twitch_room/internal/metrics"
	"twitch_room/internal/performance"
	"twitch_room/internal/repository"
	"twitch_room/internal/secrets"
	"twitch_room/internal/security"
	"twitch_room/internal/service"
	"twitch_room/internal/twitchapi"
	"twitch_room/internal/validate"
)

type watchRequest struct {
	StreamIDs []string `json:"stream_ids"`
}

type server struct {
	watchService       *service.Service
	authMiddleware     *auth.Middleware
	identityProvider   identity.Provider
	rateLimiter        *security.RateLimiter
	authLimiter        *security.RateLimiter
	watchStateCache    *performance.WatchStateCache
	oauthStates        *oauthStateStore
	sessions           *sessionStore
	redirectAfterLogin string
	trustedProxyCIDRs  []netip.Prefix
	shareLimiter       *security.RateLimiter
	metrics            *metrics.Registry
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

	if cfg.AllowDevAuth && cfg.AppEnv == "production" {
		log.Fatal("refusing to start: ALLOW_DEV_AUTH is enabled while APP_ENV=production; unauthenticated callers could impersonate any user")
	}

	// Stop serving gracefully when the process receives an interrupt or a
	// termination signal: cancel the background refresh loop and drain
	// in-flight requests before exiting.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	// Sessions default to memory; when PostgreSQL is reachable they are stored
	// there so a restart or a second instance keeps sessions valid.
	sessions := newSessionStore(sessionTTL)
	if pgStore, ok := store.(*repository.PostgresStore); ok {
		sessions = newSessionStoreWithBackend(sessionTTL, postgresSessionBackend{store: pgStore})
	}
	svc := service.NewService(repo, time.Duration(cfg.WatchStateTTLMin)*time.Minute)

	// Enrich published streams with Twitch metadata when app credentials exist
	// (the local .env carries them; the docker-compose build intentionally does
	// not, so the container publishes plain logins).
	if sec.TwitchClientID != "" && sec.TwitchClientSecret != "" {
		enricher := twitchapi.NewClient(sec.TwitchClientID, sec.TwitchClientSecret)
		svc = service.NewServiceWithEnricher(repo, time.Duration(cfg.WatchStateTTLMin)*time.Minute, enricher)
		log.Printf("Twitch enrichment enabled")
	}

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

	// Keep published stream metadata fresh when enrichment is on: re-resolve
	// Twitch metadata every WATCH_REFRESH_SECONDS and drop stale cache entries.
	if sec.TwitchClientID != "" && sec.TwitchClientSecret != "" {
		refreshInterval := time.Duration(cfg.WatchRefreshSeconds) * time.Second
		go func() {
			ticker := time.NewTicker(refreshInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					refCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					refreshed, err := svc.RefreshWatchStates(refCtx)
					cancel()
					if err != nil {
						log.Printf("watch state refresh failed: %v", err)
						continue
					}
					if len(refreshed) > 0 {
						watchStateCache.InvalidateAll()
						log.Printf("refreshed metadata for %d watch state(s)", len(refreshed))
					}
				}
			}
		}()
	}

	s := &server{
		watchService:       svc,
		authMiddleware:     &auth.Middleware{ExpectedToken: sec.AuthToken, AllowDevAuth: cfg.AllowDevAuth},
		identityProvider:   identityProvider,
		rateLimiter:        security.NewRateLimiter(30, time.Minute),
		authLimiter:        security.NewRateLimiter(20, time.Minute),
		shareLimiter:       security.NewRateLimiter(60, time.Minute),
		watchStateCache:    watchStateCache,
		oauthStates:        newOAuthStateStore(15 * time.Minute),
		sessions:           sessions,
		redirectAfterLogin: cfg.RedirectAfterLogin,
		trustedProxyCIDRs:  cfg.TrustedProxyCIDRs,
		metrics:            metrics.New(),
	}

	if cfg.SecureCookies {
		s.sessions.enableSecureCookies()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
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
	mux.HandleFunc("/u/", s.handleSharePage)

	webHandler, err := newWebHandler()
	if err != nil {
		log.Fatalf("failed to init web handler: %v", err)
	}
	mux.Handle("/", webHandler)

	log.Printf("twitch-room service starting on :%s", cfg.Port)

	// Middleware chain: HTTPS enforcement (when TLS is on), security headers,
	// response logging, request metrics, and a global cap on request bodies.
	var handler http.Handler = withLogging(mux)
	handler = security.MaxBodyBytes(1 << 20)(handler)
	handler = security.SecurityHeaders(cfg.TLSEnabled)(handler)
	if cfg.TLSEnabled {
		trusted := func(r *http.Request) bool { return security.TrustedRequest(r, cfg.TrustedProxyCIDRs) }
		isHTTPS := func(r *http.Request) bool { return security.HTTPSRequest(r, trusted(r)) }
		handler = security.ForceHTTPS(cfg.AllowedHosts, isHTTPS)(handler)
	}
	handler = s.metricsMiddleware(handler)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	if cfg.TLSEnabled && (cfg.TLSCertFile == "" || cfg.TLSKeyFile == "") {
		log.Fatal("TLS_ENABLED is true but TLS_CERT_FILE / TLS_KEY_FILE are not set")
	}

	// Optionally keep a plain-HTTP redirector running alongside the HTTPS
	// listener (e.g. :80 -> :443) for users connecting without a reverse proxy.
	// The redirect target host is validated against the allowlist, and proxy
	// headers are only honoured when the connection comes from a trusted CIDR.
	var redirector *http.Server
	if cfg.TLSEnabled && cfg.TLSPort != "" {
		trusted := func(r *http.Request) bool { return security.TrustedRequest(r, cfg.TrustedProxyCIDRs) }
		isHTTPS := func(r *http.Request) bool { return security.HTTPSRequest(r, trusted(r)) }
		redirector = &http.Server{
			Addr: ":" + cfg.TLSPort,
			Handler: security.ForceHTTPS(cfg.AllowedHosts, isHTTPS)(
				http.NotFoundHandler(),
			),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go func() {
			log.Printf("http->https redirector starting on :%s", cfg.TLSPort)
			if err := redirector.ListenAndServe(); err != nil {
				log.Printf("redirector failed: %v", err)
			}
		}()
	}

	// Serve until the listener fails or a shutdown signal arrives.
	errCh := make(chan error, 1)
	go func() {
		if cfg.TLSEnabled {
			errCh <- srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			errCh <- srv.ListenAndServe()
		}
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	case <-ctx.Done():
		log.Printf("shutdown signal received, draining connections")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if redirector != nil {
			_ = redirector.Shutdown(shutdownCtx)
		}
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
			_ = srv.Close()
		}
		log.Printf("server stopped")
	}
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// decodeJSON decodes a JSON request body into dst. Bodies that exceed the
// global size limit are answered with 413; malformed JSON gets a 400.
func (s *server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return false
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return false
	}
	return true
}

func (s *server) handleWatch(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	if !s.rateLimiter.Allow(userID) {
		s.incThrottled("watch_per_user")
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
	if !s.decodeJSON(w, r, &payload) {
		return
	}

	state, err := s.watchService.SetUserWatchState(userID, payload.StreamIDs)
	if err != nil {
		if errors.Is(err, service.ErrInvalidStream) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid stream IDs: expected 1-100 characters of letters, digits, '_' or '-' (up to 50)"})
			return
		}
		if errors.Is(err, service.ErrStreamNotFound) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "one or more stream IDs do not correspond to an existing Twitch channel"})
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
	if err := validate.UserID(pathUserID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user id"})
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
		s.incThrottled("feed_per_user")
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
	if !s.decodeJSON(w, r, &payload) {
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
	if err := validate.UserID(publisherID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid publisher id"})
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

	if err := validate.UserID(subscriberID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid subscriber id"})
		return
	}

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

	if !s.rateLimitByIP(w, r) {
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

	if !s.rateLimitByIP(w, r) {
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

	if !s.rateLimitByIP(w, r) {
		return
	}

	if s.sessions != nil {
		s.sessions.destroySessionCookie(w, r)
	}
	w.WriteHeader(http.StatusNoContent)
}

// trustedRequest reports whether the request came from a proxy network we
// trust to supply X-Forwarded-* headers.
func (s *server) trustedRequest(r *http.Request) bool {
	return security.TrustedRequest(r, s.trustedProxyCIDRs)
}

// rateLimitByIP applies the auth endpoint rate limit keyed by client IP.
// Both the login and callback endpoints trigger outbound calls to Twitch, so
// they are throttled independently of per-user API limits.
func (s *server) rateLimitByIP(w http.ResponseWriter, r *http.Request) bool {
	if s.authLimiter != nil && !s.authLimiter.Allow(security.ClientIP(r, s.trustedRequest(r))) {
		s.incThrottled("auth_ip")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return false
	}
	return true
}

func (s *server) requireUser(r *http.Request) (string, bool) {
	if userID, ok := auth.UserIDFromContext(r.Context()); ok && userID != "" {
		if err := validate.UserID(userID); err != nil {
			return "", false
		}
		return userID, true
	}

	// A session cookie issued after Twitch login authenticates the caller.
	if s.sessions != nil {
		if userID, ok := s.sessions.userIDFromRequest(r); ok {
			if err := validate.UserID(userID); err != nil {
				return "", false
			}
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
		if err := validate.UserID(userID); err != nil {
			return "", false
		}
		return userID, true
	}

	return "", false
}

// handleMetrics renders Prometheus text metrics for scrapers. Aggregate
// gauges are refreshed from the repository on each scrape. When a static
// bearer token is configured, scrapers must present it.
func (s *server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authMiddleware.Authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.metrics == nil || s.watchService == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "metrics unavailable"})
		return
	}

	snap := s.watchService.MetricsSnapshot()
	s.metrics.GaugeSet("twitch_room_active_watch_states", "Publishers with a non-expired watch state.", float64(snap.ActiveWatchStates), nil)
	for status, count := range snap.SubscriptionsByStatus {
		s.metrics.GaugeSet("twitch_room_subscriptions", "Subscriptions by status.", float64(count), metrics.LabelSet{"status": string(status)})
	}
	s.metrics.GaugeSet("twitch_room_app_info", "Build information.", 1, metrics.LabelSet{"version": "dev"})

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(s.metrics.Prometheus())
}

// metricsMiddleware records request counts, status codes and durations.
// Route labels are bucketed to the first path segment to keep the label
// space small even under heavy user-id traffic.
func (s *server) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.metrics == nil {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		labels := metrics.LabelSet{
			"method": r.Method,
			"code":   strconv.Itoa(rec.code),
			"route":  routeKey(r.URL.Path),
		}
		s.metrics.CounterInc("twitch_room_http_requests_total", "Number of HTTP requests.", labels)
		s.metrics.HistogramObserve("twitch_room_http_request_duration_seconds", "HTTP request duration in seconds.", time.Since(start).Seconds(), metrics.LabelSet{"route": labels["route"]}, nil)
	})
}

// incThrottled bumps the rate-limit rejection counter for a given reason.
func (s *server) incThrottled(reason string) {
	if s.metrics != nil {
		s.metrics.CounterInc("twitch_room_throttled_requests_total", "Requests rejected by a rate limiter.", metrics.LabelSet{"reason": reason})
	}
}

// statusRecorder captures the response status code written by the handler.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// routeKey collapses a request path into a coarse label so per-user paths do
// not explode the metrics label space.
func routeKey(path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return "root"
	}
	parts := strings.Split(trimmed, "/")
	if parts[0] == "v1" {
		if len(parts) > 1 {
			return "v1/" + parts[1]
		}
		return "v1"
	}
	return parts[0]
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
