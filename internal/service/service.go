package service

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrUserNotFound  = errors.New("user not found")
	ErrInvalidUser   = errors.New("invalid user")
	ErrInvalidStream = errors.New("invalid stream id")
)

type Stream struct {
	ID          string    `json:"id"`
	Title       string    `json:"title,omitempty"`
	ChannelName string    `json:"channel_name,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type UserWatchState struct {
	UserID    string    `json:"user_id"`
	StreamIDs []string  `json:"stream_ids"`
	UpdatedAt time.Time `json:"updated_at"`
}

type FeedEntry struct {
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Streams     []Stream  `json:"streams"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Repository interface {
	UpsertUserWatchState(userID string, streamIDs []string, updatedAt time.Time) (UserWatchState, error)
	GetUserWatchState(userID string) (UserWatchState, bool)
	ListActiveUsers(limit int) []UserWatchState
	DeleteUserWatchState(userID string)
}

type Service struct {
	mu        sync.RWMutex
	repo      Repository
	actionTTL time.Duration
}

func NewService(repo Repository, actionTTL time.Duration) *Service {
	if actionTTL <= 0 {
		actionTTL = 5 * time.Minute
	}

	return &Service{
		repo:      repo,
		actionTTL: actionTTL,
	}
}

func (s *Service) SetUserWatchState(userID string, streamIDs []string) (UserWatchState, error) {
	if userID == "" {
		return UserWatchState{}, ErrInvalidUser
	}

	cleaned := normalizeStreamIDs(streamIDs)
	if len(cleaned) == 0 {
		return UserWatchState{}, ErrInvalidStream
	}

	now := time.Now().UTC()
	state, err := s.repo.UpsertUserWatchState(userID, cleaned, now)
	if err != nil {
		return UserWatchState{}, err
	}

	return state, nil
}

func (s *Service) GetUserWatchState(userID string) (UserWatchState, error) {
	if userID == "" {
		return UserWatchState{}, ErrInvalidUser
	}

	state, ok := s.repo.GetUserWatchState(userID)
	if !ok {
		return UserWatchState{}, ErrUserNotFound
	}

	if s.isExpired(state.UpdatedAt) {
		s.repo.DeleteUserWatchState(userID)
		return UserWatchState{}, ErrUserNotFound
	}

	return state, nil
}

func (s *Service) ListFeed(limit int) []FeedEntry {
	if limit <= 0 {
		limit = 20
	}

	entries := make([]FeedEntry, 0)
	for _, state := range s.repo.ListActiveUsers(limit) {
		if s.isExpired(state.UpdatedAt) {
			s.repo.DeleteUserWatchState(state.UserID)
			continue
		}

		streams := make([]Stream, 0, len(state.StreamIDs))
		for _, streamID := range state.StreamIDs {
			streams = append(streams, Stream{
				ID:        streamID,
				UpdatedAt: state.UpdatedAt,
			})
		}

		entries = append(entries, FeedEntry{
			UserID:      state.UserID,
			DisplayName: state.UserID,
			Streams:     streams,
			UpdatedAt:   state.UpdatedAt,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})

	return entries
}

func (s *Service) ClearUserWatchState(userID string) error {
	if userID == "" {
		return ErrInvalidUser
	}

	if _, ok := s.repo.GetUserWatchState(userID); !ok {
		return ErrUserNotFound
	}

	s.repo.DeleteUserWatchState(userID)
	return nil
}

func (s *Service) isExpired(updatedAt time.Time) bool {
	return time.Since(updatedAt) > s.actionTTL
}

func normalizeStreamIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	cleaned := make([]string, 0, len(ids))
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		cleaned = append(cleaned, trimmed)
	}
	return cleaned
}
