package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"twitch_room/internal/auth"
	"twitch_room/internal/config"
	"twitch_room/internal/repository"
	"twitch_room/internal/security"
	"twitch_room/internal/service"
)

type watchRequest struct {
	StreamIDs []string `json:"stream_ids"`
}

type server struct {
	watchService   *service.Service
	authMiddleware *auth.Middleware
	rateLimiter    *security.RateLimiter
}

func main() {
	cfg := config.Load()

	store := repository.NewMemoryStore()
	repo := repository.NewServiceAdapter(store)
	svc := service.NewService(repo, time.Duration(cfg.WatchStateTTLMin)*time.Minute)

	s := &server{
		watchService:   svc,
		authMiddleware: &auth.Middleware{ExpectedToken: cfg.AuthToken, AllowDevAuth: cfg.AllowDevAuth},
		rateLimiter:    security.NewRateLimiter(30, time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/watch", s.handleWatch)
	mux.HandleFunc("/v1/users/", s.handleUserWatch)
	mux.HandleFunc("/v1/feed", s.handleFeed)

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

	if r.Method == http.MethodGet {
		state, err := s.watchService.GetUserWatchState(userID)
		if err != nil {
			if errors.Is(err, service.ErrUserNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "watch state not found"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
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

	writeJSON(w, http.StatusCreated, state)
}

func (s *server) handleUserWatch(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.requireUser(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}

	pathUserID := strings.TrimPrefix(r.URL.Path, "/v1/users/")
	pathUserID = strings.TrimSpace(pathUserID)
	if pathUserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing user id"})
		return
	}

	if pathUserID != userID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "you can only view your own watch state"})
		return
	}

	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	state, err := s.watchService.GetUserWatchState(pathUserID)
	if err != nil {
		if errors.Is(err, service.ErrUserNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "watch state not found"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

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

	feed := s.watchService.ListFeed(20)
	writeJSON(w, http.StatusOK, feed)
}

func (s *server) requireUser(r *http.Request) (string, bool) {
	if userID, ok := auth.UserIDFromContext(r.Context()); ok && userID != "" {
		return userID, true
	}

	userID, err := s.authMiddleware.UserID(r)
	if err != nil {
		return "", false
	}
	return userID, true
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
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
