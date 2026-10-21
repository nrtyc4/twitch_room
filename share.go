package main

import (
	"embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"twitch_room/internal/security"
	"twitch_room/internal/service"
	"twitch_room/internal/validate"
)

//go:embed share_page.html extension/share.css extension/share.js
var sharePageFS embed.FS

var sharePageTmpl = template.Must(template.ParseFS(sharePageFS, "share_page.html"))

type shareStream struct {
	ID           string
	ChannelName  string
	Title        string
	GameName     string
	ThumbnailURL string
	ViewerCount  int
	IsLive       bool
	TwitchURL    string
}

// handleSharePage renders a read-only public page for a publisher's watch
// state at /u/{user_id}. No authentication is required; only publishers with
// public visibility are exposed, and private/unpublished states return the
// same 404 so the page does not reveal whether a state exists.
func (s *server) handleSharePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.shareLimiter != nil && !s.shareLimiter.Allow(security.ClientIP(r, s.trustedRequest(r))) {
		s.incThrottled("share_ip")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}

	userID := strings.TrimPrefix(r.URL.Path, "/u/")
	userID = strings.TrimSuffix(userID, "/")
	userID = strings.TrimSpace(userID)
	if err := validate.UserID(userID); err != nil {
		http.NotFound(w, r)
		return
	}

	state, err := s.watchService.GetPublicWatchState(userID)
	if err != nil {
		if !errors.Is(err, service.ErrUserNotFound) {
			slog.Error("share page issue", "user_id", userID, "error", err)
		}
		http.NotFound(w, r)
		return
	}

	streams := state.Streams
	if len(streams) == 0 {
		for _, id := range state.StreamIDs {
			streams = append(streams, service.Stream{ID: id, UpdatedAt: state.UpdatedAt})
		}
	}

	entries := make([]shareStream, 0, len(streams))
	for _, st := range streams {
		entries = append(entries, shareStream{
			ID:           st.ID,
			ChannelName:  st.ChannelName,
			Title:        st.Title,
			GameName:     st.GameName,
			ThumbnailURL: st.ThumbnailURL,
			ViewerCount:  st.ViewerCount,
			IsLive:       st.IsLive,
			TwitchURL:    "https://www.twitch.tv/" + st.ID,
		})
	}

	data := struct {
		UserID      string
		DisplayName string
		UpdatedAt   string
		Streams     []shareStream
		StreamCount int
	}{
		UserID:      userID,
		DisplayName: s.watchService.ResolveDisplayNameContext(r.Context(), userID),
		UpdatedAt:   state.UpdatedAt.In(time.Local).Format(time.RFC1123),
		Streams:     entries,
		StreamCount: len(entries),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The share page is a self-contained document with inline styles and a
	// small script; allow them for this route only.
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' https://static-cdn.jtvnw.net https://*.twitch.tv data:; connect-src 'self'")
	if err := sharePageTmpl.Execute(w, data); err != nil {
		slog.Error("failed to render share page", "error", err)
	}
}
