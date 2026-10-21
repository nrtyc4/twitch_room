package service

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	"twitch_room/internal/validate"
)

var (
	ErrUserNotFound          = errors.New("user not found")
	ErrInvalidUser           = errors.New("invalid user")
	ErrInvalidStream         = errors.New("invalid stream id")
	ErrStreamNotFound        = errors.New("stream not found")
	ErrInvalidSubscription   = errors.New("invalid subscription")
	ErrCannotSubscribeToSelf = errors.New("cannot subscribe to yourself")
	ErrNotSubscribed         = errors.New("not subscribed")
	ErrNoPendingRequest      = errors.New("no pending subscription request")
	ErrInvalidSettings       = errors.New("invalid settings")
	ErrNotPublic             = errors.New("watch state is not public")
)

// MaxStreamsPerWatch caps the number of streams a single watch state may
// hold, matching the strict 1MB request body budget with a carefully chosen upper bound.
const MaxStreamsPerWatch = 50

type SubscriptionMode string

const (
	SubscriptionModeOpen     SubscriptionMode = "open"
	SubscriptionModeApproved SubscriptionMode = "approved"
)

type Visibility string

const (
	VisibilityPublic      Visibility = "public"
	VisibilitySubscribers Visibility = "subscribers"
	VisibilityPrivate     Visibility = "private"
)

type SubscriptionStatus string

const (
	SubscriptionStatusActive  SubscriptionStatus = "active"
	SubscriptionStatusPending SubscriptionStatus = "pending"
)

type PublisherSettings struct {
	UserID           string           `json:"user_id"`
	SubscriptionMode SubscriptionMode `json:"subscription_mode"`
	Visibility       Visibility       `json:"visibility"`
}

type Subscription struct {
	SubscriberID string             `json:"subscriber_id"`
	PublisherID  string             `json:"publisher_id"`
	Status       SubscriptionStatus `json:"status"`
	CreatedAt    time.Time          `json:"created_at"`
}

const (
	DefaultSubscriptionMode = SubscriptionModeOpen
	DefaultVisibility       = VisibilitySubscribers
)

type Stream struct {
	ID           string    `json:"id"`
	ChannelName  string    `json:"channel_name,omitempty"`
	Title        string    `json:"title,omitempty"`
	GameName     string    `json:"game_name,omitempty"`
	ThumbnailURL string    `json:"thumbnail_url,omitempty"`
	ViewerCount  int       `json:"viewer_count,omitempty"`
	IsLive       bool      `json:"is_live,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type UserWatchState struct {
	UserID    string    `json:"user_id"`
	StreamIDs []string  `json:"stream_ids"`
	Streams   []Stream  `json:"streams,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type FeedEntry struct {
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url,omitempty"`
	Streams     []Stream  `json:"streams"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// MetricsSnapshot is a point-in-time aggregate over the whole repository,
// consumed by the /metrics endpoint.
type MetricsSnapshot struct {
	ActiveWatchStates     int
	SubscriptionsByStatus map[SubscriptionStatus]int
}

// StreamInfo describes what the Twitch API knows about a channel.
type StreamInfo struct {
	Exists       bool
	IsLive       bool
	ChannelName  string
	Title        string
	GameName     string
	ThumbnailURL string
	ViewerCount  int
}

// StreamEnricher resolves channel logins against an external source (Twitch)
// so published watch state can carry titles, games, and thumbnails.
type StreamEnricher interface {
	LookupStreams(ctx context.Context, logins []string) (map[string]StreamInfo, error)
}

// UserResolver resolves user IDs to human-readable display names and avatars. It is
// optional: entries whose info cannot be resolved keep their raw user ID and no avatar.
// Results are cached by the implementing client.
type UserResolver interface {
	ResolveUsers(ctx context.Context, userIDs []string) (map[string]UserInfo, error)
}

type UserInfo struct {
	DisplayName string
	AvatarURL   string
}

type Repository interface {
	UpsertUserWatchState(userID string, streams []Stream, updatedAt time.Time) (UserWatchState, error)
	GetUserWatchState(userID string) (UserWatchState, bool)
	ListActiveUsers(limit, offset int) []UserWatchState
	DeleteUserWatchState(userID string) error
	DeleteUserData(userID string) error
	DeleteExpiredWatchStates(updatedBefore time.Time) int
	CountActiveWatchStates(updatedAfter time.Time) int

	SetPublisherSettings(settings PublisherSettings) error
	GetPublisherSettings(userID string) (PublisherSettings, bool)

	UpsertSubscription(sub Subscription) error
	GetSubscription(subscriberID, publisherID string) (Subscription, bool)
	ListSubscriptionsForSubscriber(subscriberID string) []Subscription
	ListSubscriptionsForPublisher(publisherID string, status SubscriptionStatus) []Subscription
	SetSubscriptionStatus(subscriberID, publisherID string, status SubscriptionStatus) error
	DeleteSubscription(subscriberID, publisherID string) error
	CountSubscriptionsByStatus() map[SubscriptionStatus]int
}

// Clock provides a way to inject time for testing purposes.

type Clock interface {
	Now() time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

type Service struct {
	repo Repository

	actionTTL time.Duration

	enricher StreamEnricher

	clock Clock
}

func NewService(repo Repository, actionTTL time.Duration, clock Clock) *Service {
	if actionTTL <= 0 {
		actionTTL = 5 * time.Minute
	}
	if clock == nil {
		clock = RealClock{}
	}

	return &Service{
		repo:      repo,
		actionTTL: actionTTL,
		clock:     clock,
	}
}

func NewServiceWithEnricher(repo Repository, actionTTL time.Duration, enricher StreamEnricher, clock Clock) *Service {
	svc := NewService(repo, actionTTL, clock)
	svc.enricher = enricher
	return svc
}

func (s *Service) SetUserWatchState(userID string, streamIDs []string) (UserWatchState, error) {
	return s.SetUserWatchStateContext(context.Background(), userID, streamIDs)
}

// SetUserWatchStateContext is like SetUserWatchState but accepts a context for
// cancellation/timeout propagation to the enrichment client.
func (s *Service) SetUserWatchStateContext(ctx context.Context, userID string, streamIDs []string) (UserWatchState, error) {
	if err := checkUserID(userID); err != nil {
		return UserWatchState{}, err
	}

	cleaned := normalizeStreamIDs(streamIDs)
	if len(cleaned) == 0 {
		return UserWatchState{}, ErrInvalidStream
	}
	if len(cleaned) > MaxStreamsPerWatch {
		return UserWatchState{}, ErrInvalidStream
	}
	for _, id := range cleaned {
		if err := validate.StreamID(id); err != nil {
			return UserWatchState{}, ErrInvalidStream
		}
	}

	now := s.clock.Now()
	streams, err := s.resolveStreams(ctx, cleaned, now)
	if err != nil {
		return UserWatchState{}, err
	}

	state, err := s.repo.UpsertUserWatchState(userID, streams, now)
	if err != nil {
		return UserWatchState{}, err
	}

	return state, nil
}

// resolveStreams attaches Twitch metadata to the normalized logins. When no
// enricher is configured the logins are stored as-is. When the enricher is
// available, logins that do not resolve to an existing Twitch channel are
// rejected and lookup failures degrade gracefully to plain logins.
func (s *Service) resolveStreams(ctx context.Context, logins []string, now time.Time) ([]Stream, error) {
	if s.enricher == nil {
		streams := make([]Stream, 0, len(logins))
		for _, id := range logins {
			streams = append(streams, Stream{ID: id, UpdatedAt: now})
		}
		return streams, nil
	}

	info, err := s.enricher.LookupStreams(ctx, logins)
	if err != nil {
		// Twitch is unreachable: publish plain logins rather than failing the user.
		streams := make([]Stream, 0, len(logins))
		for _, id := range logins {
			streams = append(streams, Stream{ID: id, UpdatedAt: now})
		}
		return streams, nil
	}

	streams := make([]Stream, 0, len(logins))
	var missing []string
	for _, id := range logins {
		si, ok := info[id]
		if !ok || !si.Exists {
			missing = append(missing, id)
			continue
		}
		streams = append(streams, Stream{
			ID:           id,
			ChannelName:  si.ChannelName,
			Title:        si.Title,
			GameName:     si.GameName,
			ThumbnailURL: si.ThumbnailURL,
			ViewerCount:  si.ViewerCount,
			IsLive:       si.IsLive,
			UpdatedAt:    now,
		})
	}
	if len(missing) > 0 {
		return nil, ErrStreamNotFound
	}
	return streams, nil
}

// RefreshWatchStates re-resolves Twitch metadata for every non-expired watch
// state and returns the user IDs whose states were refreshed. Expired states
// are removed. The metadata refresh does not extend a state's lifetime: the
// stored updated_at is preserved. Without an enricher only expired states are
// removed and no metadata changes are made.
func (s *Service) RefreshWatchStates(ctx context.Context) ([]string, error) {
	// Drop expired states up front so paging below is stable.
	s.repo.DeleteExpiredWatchStates(s.clock.Now().Add(-s.actionTTL))

	if s.enricher == nil {
		return nil, nil
	}

	const batchSize = 200
	var allRefreshed []string
	offset := 0
	for {
		states := s.repo.ListActiveUsers(batchSize, offset)
		if len(states) == 0 {
			break
		}

		logins := make([]string, 0, len(states))
		for _, state := range states {
			for _, id := range watchLogins(state) {
				logins = append(logins, id)
			}
		}
		logins = normalizeStreamIDs(logins)

		info, err := s.enricher.LookupStreams(ctx, logins)
		if err != nil {
			return allRefreshed, err
		}

		for _, state := range states {
			streams := refreshStreams(state, info)
			if equalStreams(state.Streams, streams) {
				continue
			}
			if _, err := s.repo.UpsertUserWatchState(state.UserID, streams, state.UpdatedAt); err != nil {
				slog.Error("failed to refresh user watch state", "user_id", state.UserID, "error", err)
				continue
			}
			allRefreshed = append(allRefreshed, state.UserID)
		}

		if len(states) < batchSize {
			break
		}
		offset += batchSize
	}

	return allRefreshed, nil
}

// equalStreams reports whether two stream slices carry identical metadata.
func equalStreams(a, b []Stream) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func watchLogins(state UserWatchState) []string {
	streams := state.Streams
	if len(streams) == 0 {
		streams = make([]Stream, 0, len(state.StreamIDs))
		for _, id := range state.StreamIDs {
			streams = append(streams, Stream{ID: id})
		}
	}
	return streamIDsFromStreams(streams)
}

// refreshStreams rebuilds a watch state's streams from freshly looked-up Twitch
// metadata. Logins that no longer resolve (e.g. a renamed or deleted channel)
// keep their previous metadata rather than being dropped.
func refreshStreams(state UserWatchState, info map[string]StreamInfo) []Stream {
	src := state.Streams
	if len(src) == 0 {
		for _, id := range state.StreamIDs {
			src = append(src, Stream{ID: id})
		}
	}

	out := make([]Stream, 0, len(src))
	for _, st := range src {
		next := st
		if si, ok := info[st.ID]; ok && si.Exists {
			next.ChannelName = si.ChannelName
			next.Title = si.Title
			next.GameName = si.GameName
			next.ThumbnailURL = si.ThumbnailURL
			next.ViewerCount = si.ViewerCount
			next.IsLive = si.IsLive
		}
		next.UpdatedAt = state.UpdatedAt
		out = append(out, next)
	}
	return out
}

func (s *Service) GetUserWatchState(userID string) (UserWatchState, error) {
	if err := checkUserID(userID); err != nil {
		return UserWatchState{}, err
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

// GetPublicWatchState returns a publisher's watch state for anonymous share
// pages. Only publishers with public visibility are exposed; private or
// subscriber-only states (and missing or expired states) all surface the same
// ErrUserNotFound so the public page does not confirm whether a state exists.
func (s *Service) GetPublicWatchState(publisherID string) (UserWatchState, error) {
	if err := checkUserID(publisherID); err != nil {
		return UserWatchState{}, ErrUserNotFound
	}

	state, ok := s.repo.GetUserWatchState(publisherID)
	if !ok {
		return UserWatchState{}, ErrUserNotFound
	}

	if s.isExpired(state.UpdatedAt) {
		s.repo.DeleteUserWatchState(publisherID)
		return UserWatchState{}, ErrUserNotFound
	}

	settings, ok := s.repo.GetPublisherSettings(publisherID)
	if !ok {
		return UserWatchState{}, ErrNotPublic
	}
	if settings.Visibility != VisibilityPublic {
		return UserWatchState{}, ErrNotPublic
	}

	return state, nil
}

// ResolveDisplayName resolves a single user ID to its human-readable name via
// the enricher when available, degrading to the raw ID otherwise.
func (s *Service) ResolveDisplayName(userID string) string {
	return s.ResolveDisplayNameContext(context.Background(), userID)
}

// ResolveDisplayNameContext is like ResolveDisplayName but accepts a context.
func (s *Service) ResolveDisplayNameContext(ctx context.Context, userID string) string {
	if resolver, ok := s.enricher.(UserResolver); ok && userID != "" {
		if names, err := resolver.ResolveUsers(ctx, []string{userID}); err == nil {
			if info, ok := names[strings.ToLower(userID)]; ok && info.DisplayName != "" {
				return info.DisplayName
			}
		}
	}
	return userID
}

// MetricsSnapshot aggregates repository state for the /metrics endpoint: how
// many watch states are currently live (not expired) and how subscriptions
// are distributed across statuses.
func (s *Service) MetricsSnapshot() MetricsSnapshot {
	cutoff := s.clock.Now().Add(-s.actionTTL)
	return MetricsSnapshot{
		ActiveWatchStates:     s.repo.CountActiveWatchStates(cutoff),
		SubscriptionsByStatus: s.repo.CountSubscriptionsByStatus(),
	}
}

func (s *Service) ListFeed(viewerID string, limit int) []FeedEntry {
	return s.ListFeedContext(context.Background(), viewerID, limit)
}

// ListFeedContext is like ListFeed but accepts a context for cancellation.
func (s *Service) ListFeedContext(ctx context.Context, viewerID string, limit int) []FeedEntry {
	if limit <= 0 {
		limit = 20
	}

	subscriptions := s.repo.ListSubscriptionsForSubscriber(viewerID)
	entries := make([]FeedEntry, 0)

	for _, sub := range subscriptions {
		if sub.Status != SubscriptionStatusActive {
			continue
		}

		state, ok := s.repo.GetUserWatchState(sub.PublisherID)
		if !ok {
			continue
		}
		if s.isExpired(state.UpdatedAt) {
			s.repo.DeleteUserWatchState(state.UserID)
			continue
		}

		settings, ok := s.repo.GetPublisherSettings(sub.PublisherID)
		if !ok {
			settings = PublisherSettings{
				UserID:           sub.PublisherID,
				SubscriptionMode: DefaultSubscriptionMode,
				Visibility:       DefaultVisibility,
			}
		}
		if settings.Visibility == VisibilityPrivate {
			continue
		}

		streams := state.Streams
		if len(streams) == 0 {
			for _, streamID := range state.StreamIDs {
				streams = append(streams, Stream{
					ID:        streamID,
					UpdatedAt: state.UpdatedAt,
				})
			}
		}

		entries = append(entries, FeedEntry{
			UserID:      state.UserID,
			DisplayName: state.UserID,
			Streams:     streams,
			UpdatedAt:   state.UpdatedAt,
		})

		if len(entries) >= limit {
			break
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].UpdatedAt.After(entries[j].UpdatedAt)
	})

	// Populate human-readable publisher names when the enricher can resolve
	// them; failures degrade to the raw user ID. A partially resolved map is
	// still applied.
	if resolver, ok := s.enricher.(UserResolver); ok && len(entries) > 0 {
		ids := make([]string, 0, len(entries))
		for _, e := range entries {
			ids = append(ids, e.UserID)
		}
		names, _ := resolver.ResolveUsers(ctx, ids)
		for i := range entries {
			if info, ok := names[strings.ToLower(entries[i].UserID)]; ok && info.DisplayName != "" {
				entries[i].DisplayName = info.DisplayName
				entries[i].AvatarURL = info.AvatarURL
			}
		}
	}

	return entries
}

func (s *Service) ClearUserWatchState(userID string) error {
	if err := checkUserID(userID); err != nil {
		return err
	}

	if _, ok := s.repo.GetUserWatchState(userID); !ok {
		return ErrUserNotFound
	}

	if err := s.repo.DeleteUserWatchState(userID); err != nil {
		return err
	}
	return nil
}

// DeleteUser removes all stored data for a user (watch state, settings,
// subscriptions). Used for account deletion / logout purge.
func (s *Service) DeleteUser(userID string) error {
	if err := checkUserID(userID); err != nil {
		return err
	}
	if err := s.repo.DeleteUserData(userID); err != nil {
		return err
	}
	return nil
}

func (s *Service) UpdatePublisherSettings(settings PublisherSettings) (PublisherSettings, error) {
	if err := checkUserID(settings.UserID); err != nil {
		return PublisherSettings{}, err
	}
	if settings.SubscriptionMode != SubscriptionModeOpen && settings.SubscriptionMode != SubscriptionModeApproved {
		return PublisherSettings{}, ErrInvalidSettings
	}
	if settings.Visibility != VisibilityPublic &&
		settings.Visibility != VisibilitySubscribers &&
		settings.Visibility != VisibilityPrivate {
		return PublisherSettings{}, ErrInvalidSettings
	}

	if err := s.repo.SetPublisherSettings(settings); err != nil {
		return PublisherSettings{}, err
	}
	return settings, nil
}

func (s *Service) GetPublisherSettings(userID string) (PublisherSettings, error) {
	if err := checkUserID(userID); err != nil {
		return PublisherSettings{}, err
	}

	settings, ok := s.repo.GetPublisherSettings(userID)
	if !ok {
		return PublisherSettings{
			UserID:           userID,
			SubscriptionMode: DefaultSubscriptionMode,
			Visibility:       DefaultVisibility,
		}, nil
	}
	return settings, nil
}

func (s *Service) Subscribe(subscriberID, publisherID string) (Subscription, error) {
	if err := checkIDPair(subscriberID, publisherID); err != nil {
		return Subscription{}, err
	}
	if subscriberID == publisherID {
		return Subscription{}, ErrCannotSubscribeToSelf
	}

	settings, ok := s.repo.GetPublisherSettings(publisherID)
	if !ok {
		settings = PublisherSettings{
			UserID:           publisherID,
			SubscriptionMode: DefaultSubscriptionMode,
			Visibility:       DefaultVisibility,
		}
	}

	status := SubscriptionStatusActive
	if settings.SubscriptionMode == SubscriptionModeApproved {
		status = SubscriptionStatusPending
	}

	sub := Subscription{
		SubscriberID: subscriberID,
		PublisherID:  publisherID,
		Status:       status,
		CreatedAt:    s.clock.Now(),
	}

	if err := s.repo.UpsertSubscription(sub); err != nil {
		return Subscription{}, err
	}
	return sub, nil
}

func (s *Service) Unsubscribe(subscriberID, publisherID string) error {
	if err := checkIDPair(subscriberID, publisherID); err != nil {
		return err
	}
	if _, ok := s.repo.GetSubscription(subscriberID, publisherID); !ok {
		return ErrNotSubscribed
	}

	if err := s.repo.DeleteSubscription(subscriberID, publisherID); err != nil {
		return err
	}
	return nil
}

func (s *Service) ListSubscriptions(subscriberID string) ([]Subscription, error) {
	if err := checkUserID(subscriberID); err != nil {
		return nil, err
	}
	return s.repo.ListSubscriptionsForSubscriber(subscriberID), nil
}

func (s *Service) ListPendingRequests(publisherID string) ([]Subscription, error) {
	if err := checkUserID(publisherID); err != nil {
		return nil, err
	}
	return s.repo.ListSubscriptionsForPublisher(publisherID, SubscriptionStatusPending), nil
}

func (s *Service) RespondToSubscriber(publisherID, subscriberID string, approve bool) error {
	if err := checkIDPair(publisherID, subscriberID); err != nil {
		return err
	}
	if publisherID == subscriberID {
		return ErrCannotSubscribeToSelf
	}

	sub, ok := s.repo.GetSubscription(subscriberID, publisherID)
	if !ok {
		return ErrNotSubscribed
	}
	if sub.Status != SubscriptionStatusPending {
		return ErrNoPendingRequest
	}

	if approve {
		return s.repo.SetSubscriptionStatus(subscriberID, publisherID, SubscriptionStatusActive)
	}

	if err := s.repo.DeleteSubscription(subscriberID, publisherID); err != nil {
		return err
	}
	return nil
}

func (s *Service) CanView(viewerID, publisherID string) (bool, error) {
	if err := checkUserID(viewerID); err != nil {
		return false, err
	}
	if err := checkUserID(publisherID); err != nil {
		return false, err
	}
	if viewerID == publisherID {
		return true, nil
	}

	settings, ok := s.repo.GetPublisherSettings(publisherID)
	if !ok {
		settings = PublisherSettings{
			UserID:           publisherID,
			SubscriptionMode: DefaultSubscriptionMode,
			Visibility:       DefaultVisibility,
		}
	}

	switch settings.Visibility {
	case VisibilityPublic:
		return true, nil
	case VisibilityPrivate:
		return false, nil
	default:
		sub, ok := s.repo.GetSubscription(viewerID, publisherID)
		return ok && sub.Status == SubscriptionStatusActive, nil
	}
}

func (s *Service) isExpired(updatedAt time.Time) bool {
	return s.clock.Now().Sub(updatedAt) > s.actionTTL
}

func streamIDsFromStreams(streams []Stream) []string {
	if len(streams) == 0 {
		return nil
	}
	ids := make([]string, 0, len(streams))
	for _, st := range streams {
		ids = append(ids, st.ID)
	}
	return ids
}

// checkUserID enforces the strict identifier rules for user, publisher and
// subscriber IDs.
func checkUserID(id string) error {
	if err := validate.UserID(id); err != nil {
		return ErrInvalidUser
	}
	return nil
}

// checkIDPair validates both identifiers in subscription relationships.
func checkIDPair(a, b string) error {
	if err := checkUserID(a); err != nil {
		return ErrInvalidSubscription
	}
	if err := checkUserID(b); err != nil {
		return ErrInvalidSubscription
	}
	return nil
}

func normalizeStreamIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	cleaned := make([]string, 0, len(ids))
	for _, id := range ids {
		trimmed := strings.ToLower(strings.TrimSpace(id))
		if u, err := url.Parse(trimmed); err == nil && u.Host != "" {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) > 0 {
				trimmed = strings.ToLower(strings.TrimSpace(parts[0]))
			}
		}
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
