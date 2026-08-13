package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"twitch_room/internal/performance"
	"twitch_room/internal/service"

	"github.com/lib/pq"
)

type PostgresStore struct {
	db *sql.DB
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
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_updated_at ON user_watch_state(updated_at DESC);
	`

	_, err := ps.db.ExecContext(ctx, schema)
	return err
}

// Upsert inserts or updates a user's watch state.
func (ps *PostgresStore) Upsert(userID string, streamIDs []string, updatedAt time.Time) (service.UserWatchState, error) {
	query := `
	INSERT INTO user_watch_state (user_id, stream_ids, updated_at)
	VALUES ($1, $2, $3)
	ON CONFLICT (user_id) DO UPDATE SET
		stream_ids = EXCLUDED.stream_ids,
		updated_at = EXCLUDED.updated_at
	RETURNING user_id, stream_ids, updated_at
	`

	var userIDResult string
	var streamIDsResult pq.StringArray
	var updatedAtResult time.Time

	err := ps.db.QueryRow(query, userID, pq.Array(streamIDs), updatedAt).
		Scan(&userIDResult, &streamIDsResult, &updatedAtResult)

	if err != nil {
		return service.UserWatchState{}, fmt.Errorf("upsert failed: %w", err)
	}

	return service.UserWatchState{
		UserID:    userIDResult,
		StreamIDs: []string(streamIDsResult),
		UpdatedAt: updatedAtResult,
	}, nil
}

// Get retrieves a user's watch state by ID.
func (ps *PostgresStore) Get(userID string) (service.UserWatchState, bool) {
	query := `SELECT user_id, stream_ids, updated_at FROM user_watch_state WHERE user_id = $1`

	var userIDResult string
	var streamIDsResult pq.StringArray
	var updatedAtResult time.Time

	err := ps.db.QueryRow(query, userID).
		Scan(&userIDResult, &streamIDsResult, &updatedAtResult)

	if err != nil {
		if err == sql.ErrNoRows {
			return service.UserWatchState{}, false
		}
		// Log error but return false for consistency
		return service.UserWatchState{}, false
	}

	return service.UserWatchState{
		UserID:    userIDResult,
		StreamIDs: []string(streamIDsResult),
		UpdatedAt: updatedAtResult,
	}, true
}

// List retrieves the most recent user watch states up to the limit.
func (ps *PostgresStore) List(limit int) []service.UserWatchState {
	if limit <= 0 {
		limit = 20
	}

	query := `
	SELECT user_id, stream_ids, updated_at
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
		var updatedAt time.Time

		if err := rows.Scan(&userID, &streamIDsResult, &updatedAt); err != nil {
			continue
		}

		states = append(states, service.UserWatchState{
			UserID:    userID,
			StreamIDs: []string(streamIDsResult),
			UpdatedAt: updatedAt,
		})
	}

	return states
}

// Delete removes a user's watch state.
func (ps *PostgresStore) Delete(userID string) {
	query := `DELETE FROM user_watch_state WHERE user_id = $1`
	ps.db.Exec(query, userID)
}

// Close closes the database connection.
func (ps *PostgresStore) Close() error {
	return ps.db.Close()
}
