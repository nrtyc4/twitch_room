package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"twitch_room/internal/performance"
	"twitch_room/internal/service"

	"github.com/lib/pq"
)

type PostgresStore struct {
	db                 *sql.DB
	cleanupMu          sync.Mutex
	lastSessionCleanup time.Time
}

// NewPostgresStore creates a new PostgreSQL-backed repository.
// dsn should be a PostgreSQL connection string (e.g., "postgres://user:pass@localhost/dbname")
// It configures connection pooling for optimal performance under load.
func NewPostgresStore(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pooling
	cfg := performance.DefaultConnectionPoolConfig()
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	ps := &PostgresStore{db: db}
	if err := ps.initSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return ps, nil
}

// initSchema creates the necessary tables if they don't exist.
func (ps *PostgresStore) initSchema(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS user_watch_state (
		user_id TEXT PRIMARY KEY,
		stream_ids TEXT[] NOT NULL DEFAULT '{}',
		streams JSONB NOT NULL DEFAULT '[]'::jsonb,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_updated_at ON user_watch_state(updated_at DESC);

	CREATE TABLE IF NOT EXISTS publisher_settings (
		user_id TEXT PRIMARY KEY,
		subscription_mode TEXT NOT NULL DEFAULT 'open',
		visibility TEXT NOT NULL DEFAULT 'subscribers'
	);

	CREATE TABLE IF NOT EXISTS subscriptions (
		subscriber_id TEXT NOT NULL,
		publisher_id TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (subscriber_id, publisher_id)
	);
	CREATE INDEX IF NOT EXISTS idx_subscriptions_publisher ON subscriptions(publisher_id, status);
	CREATE INDEX IF NOT EXISTS idx_subscriptions_subscriber ON subscriptions(subscriber_id);

	CREATE TABLE IF NOT EXISTS sessions (
		token TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		expires_at TIMESTAMP WITH TIME ZONE NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);

	ALTER TABLE user_watch_state ADD COLUMN IF NOT EXISTS streams JSONB;
	ALTER TABLE sessions ADD COLUMN IF NOT EXISTS access_token TEXT NOT NULL DEFAULT '';
	`

	if _, err := ps.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	_, err := ps.db.ExecContext(ctx, `UPDATE user_watch_state SET streams = '[]'::jsonb WHERE streams IS NULL`)
	return err
}

// Upsert inserts or updates a user's watch state.
func (ps *PostgresStore) Upsert(userID string, streams []service.Stream, updatedAt time.Time) (service.UserWatchState, error) {
	payload, err := json.Marshal(streams)
	if err != nil {
		return service.UserWatchState{}, fmt.Errorf("marshal streams: %w", err)
	}

	query := `
	INSERT INTO user_watch_state (user_id, stream_ids, streams, updated_at)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (user_id) DO UPDATE SET
		stream_ids = EXCLUDED.stream_ids,
		streams = EXCLUDED.streams,
		updated_at = EXCLUDED.updated_at
	RETURNING user_id, stream_ids, streams, updated_at
	`

	var userIDResult string
	var streamIDsResult pq.StringArray
	var streamsResult []byte
	var updatedAtResult time.Time

	err = ps.db.QueryRow(query, userID, pq.Array(streamIDs(streams)), payload, updatedAt).
		Scan(&userIDResult, &streamIDsResult, &streamsResult, &updatedAtResult)

	if err != nil {
		return service.UserWatchState{}, fmt.Errorf("upsert failed: %w", err)
	}

	return buildWatchState(userIDResult, streamIDsResult, streamsResult, updatedAtResult), nil
}

// Get retrieves a user's watch state by ID.
func (ps *PostgresStore) Get(userID string) (service.UserWatchState, bool) {
	query := `SELECT user_id, stream_ids, streams, updated_at FROM user_watch_state WHERE user_id = $1`

	var userIDResult string
	var streamIDsResult pq.StringArray
	var streamsResult []byte
	var updatedAtResult time.Time

	err := ps.db.QueryRow(query, userID).
		Scan(&userIDResult, &streamIDsResult, &streamsResult, &updatedAtResult)

	if err != nil {
		if err == sql.ErrNoRows {
			return service.UserWatchState{}, false
		}
		// Log error but return false for consistency
		return service.UserWatchState{}, false
	}

	return buildWatchState(userIDResult, streamIDsResult, streamsResult, updatedAtResult), true
}

// List retrieves the most recent user watch states up to the limit.
func (ps *PostgresStore) List(limit int) []service.UserWatchState {
	if limit <= 0 {
		limit = 20
	}

	query := `
	SELECT user_id, stream_ids, streams, updated_at
	FROM user_watch_state
	ORDER BY updated_at DESC
	LIMIT $1
	`

	rows, err := ps.db.Query(query, limit)
	if err != nil || rows.Err() != nil {
		return []service.UserWatchState{}
	}
	defer rows.Close()

	var states []service.UserWatchState
	for rows.Next() {
		var userID string
		var streamIDsResult pq.StringArray
		var streamsResult []byte
		var updatedAt time.Time

		if err := rows.Scan(&userID, &streamIDsResult, &streamsResult, &updatedAt); err != nil {
			continue
		}

		states = append(states, buildWatchState(userID, streamIDsResult, streamsResult, updatedAt))
	}

	return states
}

// buildWatchState reconstructs a watch state from Postgres columns, falling
// back to the plain stream_ids when the enriched streams column is empty
// (rows written before the streams column existed).
func buildWatchState(userID string, streamIDsResult pq.StringArray, streamsResult []byte, updatedAt time.Time) service.UserWatchState {
	state := service.UserWatchState{
		UserID:    userID,
		StreamIDs: []string(streamIDsResult),
		UpdatedAt: updatedAt,
	}

	var streams []service.Stream
	if len(streamsResult) > 0 {
		if err := json.Unmarshal(streamsResult, &streams); err == nil {
			state.Streams = streams
		}
	}
	if len(state.Streams) == 0 {
		for _, id := range state.StreamIDs {
			state.Streams = append(state.Streams, service.Stream{ID: id, UpdatedAt: updatedAt})
		}
	}
	return state
}

// Delete removes a user's watch state.
func (ps *PostgresStore) Delete(userID string) {
	query := `DELETE FROM user_watch_state WHERE user_id = $1`
	ps.db.Exec(query, userID)
}

// SetPublisherSettings inserts or updates a publisher's settings.
func (ps *PostgresStore) SetPublisherSettings(settings service.PublisherSettings) error {
	query := `
	INSERT INTO publisher_settings (user_id, subscription_mode, visibility)
	VALUES ($1, $2, $3)
	ON CONFLICT (user_id) DO UPDATE SET
		subscription_mode = EXCLUDED.subscription_mode,
		visibility = EXCLUDED.visibility
	`
	_, err := ps.db.Exec(query, settings.UserID, settings.SubscriptionMode, settings.Visibility)
	if err != nil {
		return fmt.Errorf("set publisher settings failed: %w", err)
	}
	return nil
}

// GetPublisherSettings retrieves a publisher's settings.
func (ps *PostgresStore) GetPublisherSettings(userID string) (service.PublisherSettings, bool) {
	query := `SELECT user_id, subscription_mode, visibility FROM publisher_settings WHERE user_id = $1`

	var settings service.PublisherSettings
	err := ps.db.QueryRow(query, userID).
		Scan(&settings.UserID, &settings.SubscriptionMode, &settings.Visibility)

	if err != nil {
		return service.PublisherSettings{}, false
	}
	return settings, true
}

// UpsertSubscription inserts or updates a subscription between two users.
func (ps *PostgresStore) UpsertSubscription(sub service.Subscription) error {
	query := `
	INSERT INTO subscriptions (subscriber_id, publisher_id, status, created_at)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (subscriber_id, publisher_id) DO UPDATE SET
		status = EXCLUDED.status
	`
	_, err := ps.db.Exec(query, sub.SubscriberID, sub.PublisherID, sub.Status, sub.CreatedAt)
	if err != nil {
		return fmt.Errorf("upsert subscription failed: %w", err)
	}
	return nil
}

// GetSubscription retrieves a subscription between a subscriber and publisher.
func (ps *PostgresStore) GetSubscription(subscriberID, publisherID string) (service.Subscription, bool) {
	query := `SELECT subscriber_id, publisher_id, status, created_at FROM subscriptions WHERE subscriber_id = $1 AND publisher_id = $2`

	var sub service.Subscription
	err := ps.db.QueryRow(query, subscriberID, publisherID).
		Scan(&sub.SubscriberID, &sub.PublisherID, &sub.Status, &sub.CreatedAt)

	if err != nil {
		return service.Subscription{}, false
	}
	return sub, true
}

// ListSubscriptionsForSubscriber returns all subscriptions created by a subscriber.
func (ps *PostgresStore) ListSubscriptionsForSubscriber(subscriberID string) []service.Subscription {
	query := `
	SELECT subscriber_id, publisher_id, status, created_at
	FROM subscriptions
	WHERE subscriber_id = $1
	ORDER BY created_at DESC
	`

	return ps.querySubscriptions(query, subscriberID)
}

// ListSubscriptionsForPublisher returns subscriptions to a publisher matching a status.
func (ps *PostgresStore) ListSubscriptionsForPublisher(publisherID string, status service.SubscriptionStatus) []service.Subscription {
	query := `
	SELECT subscriber_id, publisher_id, status, created_at
	FROM subscriptions
	WHERE publisher_id = $1 AND status = $2
	ORDER BY created_at DESC
	`

	return ps.querySubscriptions(query, publisherID, status)
}

// CountActiveWatchStates returns the number of stored states last updated at
// or after the given time, mirroring how the service treats non-expired ones.
func (ps *PostgresStore) CountActiveWatchStates(updatedAfter time.Time) int {
	const query = `SELECT COUNT(*) FROM user_watch_state WHERE updated_at >= $1`
	var count int
	if err := ps.db.QueryRow(query, updatedAfter).Scan(&count); err != nil {
		return 0
	}
	return count
}

// CountSubscriptionsByStatus tallies every stored subscription by status.
func (ps *PostgresStore) CountSubscriptionsByStatus() map[service.SubscriptionStatus]int {
	const query = `SELECT status, COUNT(*) FROM subscriptions GROUP BY status`
	rows, err := ps.db.Query(query)
	if err != nil {
		return map[service.SubscriptionStatus]int{}
	}
	defer rows.Close()

	counts := make(map[service.SubscriptionStatus]int)
	for rows.Next() {
		var status service.SubscriptionStatus
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			continue
		}
		counts[status] = count
	}
	return counts
}

// SetSubscriptionStatus updates the status of a subscription.
func (ps *PostgresStore) SetSubscriptionStatus(subscriberID, publisherID string, status service.SubscriptionStatus) error {
	query := `UPDATE subscriptions SET status = $3 WHERE subscriber_id = $1 AND publisher_id = $2`
	result, err := ps.db.Exec(query, subscriberID, publisherID, status)
	if err != nil {
		return fmt.Errorf("set subscription status failed: %w", err)
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 0 {
		return service.ErrNotSubscribed
	}
	return nil
}

// DeleteSubscription removes a subscription.
func (ps *PostgresStore) DeleteSubscription(subscriberID, publisherID string) {
	query := `DELETE FROM subscriptions WHERE subscriber_id = $1 AND publisher_id = $2`
	ps.db.Exec(query, subscriberID, publisherID)
}

func (ps *PostgresStore) querySubscriptions(query string, args ...interface{}) []service.Subscription {
	rows, err := ps.db.Query(query, args...)
	if err != nil {
		return []service.Subscription{}
	}
	defer rows.Close()

	subs := make([]service.Subscription, 0)
	for rows.Next() {
		var sub service.Subscription
		if err := rows.Scan(&sub.SubscriberID, &sub.PublisherID, &sub.Status, &sub.CreatedAt); err != nil {
			continue
		}
		subs = append(subs, sub)
	}
	return subs
}

// Close closes the database connection.
func (ps *PostgresStore) Close() error {
	return ps.db.Close()
}
