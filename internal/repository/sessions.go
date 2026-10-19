package repository

import (
	"log/slog"
	"time"
)

// SaveSession persists an opaque session token bound to a user (and an
// optional Twitch access token captured at login) until it expires, replacing
// any previous session that used the same token.
func (ps *PostgresStore) SaveSession(token, userID, accessToken string, expiresAt time.Time) error {
	if token == "" || userID == "" {
		return nil
	}
	_, err := ps.db.Exec(`
		INSERT INTO sessions (token, user_id, access_token, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (token) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			access_token = EXCLUDED.access_token,
			expires_at = EXCLUDED.expires_at`,
		token, userID, accessToken, expiresAt)
	return err
}

// SessionUserID resolves a session token to a user. Expired sessions are
// reported as missing. A database error degrades to "no session" so the caller
// is simply treated as unauthenticated.
func (ps *PostgresStore) SessionUserID(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	ps.reapExpiredSessions()

	var userID string
	err := ps.db.QueryRow(
		`SELECT user_id FROM sessions WHERE token = $1 AND expires_at > now()`, token,
	).Scan(&userID)
	if err != nil {
		return "", false
	}
	return userID, true
}

// SessionAccessToken resolves a live session token to its stored Twitch
// access token. Sessions without an attached token and expired sessions are
// reported as missing.
func (ps *PostgresStore) SessionAccessToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	ps.reapExpiredSessions()

	var accessToken string
	err := ps.db.QueryRow(
		`SELECT access_token FROM sessions WHERE token = $1 AND expires_at > now()`, token,
	).Scan(&accessToken)
	if err != nil || accessToken == "" {
		return "", false
	}
	return accessToken, true
}

// DeleteSession removes a session token, invalidating it immediately.
func (ps *PostgresStore) DeleteSession(token string) {
	if token == "" {
		return
	}
	if _, err := ps.db.Exec(`DELETE FROM sessions WHERE token = $1`, token); err != nil {
		slog.Warn("failed to delete session", "error", err)
	}
}

// reapExpiredSessions removes expired session rows at most once a minute.
func (ps *PostgresStore) reapExpiredSessions() {
	ps.cleanupMu.Lock()
	defer ps.cleanupMu.Unlock()

	now := time.Now()
	if !ps.lastSessionCleanup.IsZero() && now.Sub(ps.lastSessionCleanup) < time.Minute {
		return
	}
	ps.lastSessionCleanup = now
	if _, err := ps.db.Exec(`DELETE FROM sessions WHERE expires_at <= now()`); err != nil {
		slog.Warn("failed to reap expired sessions", "error", err)
	}
}
