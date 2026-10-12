package repository

import (
	"fmt"
	"os"
	"testing"
	"time"

	"twitch_room/internal/service"

	"github.com/lib/pq"
)

// newTestPostgresStore connects to a real Postgres instance when a DSN is
// configured and skips the test otherwise. Point TEST_DATABASE_DSN (falling
// back to DATABASE_DSN) at a database you are willing to let tests write to:
// every test cleans up after itself.
func newTestPostgresStore(t *testing.T) *PostgresStore {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_DSN")
	}
	if dsn == "" {
		t.Skip("no TEST_DATABASE_DSN or DATABASE_DSN set; skipping Postgres tests")
	}

	ps, err := NewPostgresStore(dsn)
	if err != nil {
		t.Fatalf("NewPostgresStore failed: %v", err)
	}
	t.Cleanup(func() { ps.Close() })
	return ps
}

// uniqueID isolates a test run's rows so they never collide with existing
// data; the cleanup helpers remove them afterwards.
func uniqueID(prefix string) string {
	return fmt.Sprintf("pgtest-%s-%d", prefix, time.Now().UnixNano())
}

func cleanupRows(t *testing.T, ps *PostgresStore, userID string) {
	t.Helper()
	t.Cleanup(func() {
		ps.db.Exec(`DELETE FROM user_watch_state WHERE user_id = $1`, userID)
		ps.db.Exec(`DELETE FROM publisher_settings WHERE user_id = $1`, userID)
		ps.db.Exec(`DELETE FROM subscriptions WHERE subscriber_id = $1 OR publisher_id = $1`, userID)
	})
}

func TestPostgresWatchStateRoundTrip(t *testing.T) {
	ps := newTestPostgresStore(t)
	userID := uniqueID("watch")
	cleanupRows(t, ps, userID)

	streams := []service.Stream{
		{ID: "shroud", ChannelName: "shroud", Title: "live now", GameName: "VALORANT", ThumbnailURL: "https://cb", ViewerCount: 42, IsLive: true, UpdatedAt: time.Now().UTC()},
		{ID: "xqc", ChannelName: "xQc", UpdatedAt: time.Now().UTC()},
	}

	state, err := ps.Upsert(userID, streams, time.Now().UTC())
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if state.UserID != userID {
		t.Fatalf("expected user %s, got %s", userID, state.UserID)
	}
	if len(state.Streams) != 2 || len(state.StreamIDs) != 2 {
		t.Fatalf("expected 2 streams/ids, got %d/%d", len(state.Streams), len(state.StreamIDs))
	}

	got, ok := ps.Get(userID)
	if !ok {
		t.Fatal("expected state to exist")
	}
	if len(got.Streams) != 2 {
		t.Fatalf("expected 2 streams on read, got %d", len(got.Streams))
	}
	s := got.Streams[0]
	if !s.IsLive || s.Title != "live now" || s.GameName != "VALORANT" || s.ViewerCount != 42 {
		t.Fatalf("expected enriched stream round-trip, got %+v", s)
	}

	if listed := ps.List(10); len(listed) == 0 {
		t.Fatal("expected List to include the state")
	}

	ps.Delete(userID)
	if _, ok := ps.Get(userID); ok {
		t.Fatal("expected state to be deleted")
	}
}

func TestPostgresWatchStateStreamFallback(t *testing.T) {
	ps := newTestPostgresStore(t)
	userID := uniqueID("legacy")
	cleanupRows(t, ps, userID)

	updatedAt := time.Now().UTC()
	// Simulate a row written before the streams column was populated.
	if _, err := ps.db.Exec(
		`INSERT INTO user_watch_state (user_id, stream_ids, streams, updated_at) VALUES ($1, $2, '[]'::jsonb, $3)`,
		userID, pq.Array([]string{"stream-a", "stream-b"}), updatedAt,
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	got, ok := ps.Get(userID)
	if !ok {
		t.Fatal("expected legacy state to be readable")
	}
	if len(got.Streams) != 2 || got.Streams[0].ID != "stream-a" || got.Streams[1].ID != "stream-b" {
		t.Fatalf("expected streams built from stream_ids fallback, got %+v", got.Streams)
	}
}

func TestPostgresSettingsRoundTrip(t *testing.T) {
	ps := newTestPostgresStore(t)
	userID := uniqueID("settings")
	cleanupRows(t, ps, userID)

	settings := service.PublisherSettings{
		UserID:           userID,
		SubscriptionMode: service.SubscriptionModeApproved,
		Visibility:       service.VisibilityPublic,
	}
	if err := ps.SetPublisherSettings(settings); err != nil {
		t.Fatalf("SetPublisherSettings: %v", err)
	}

	got, ok := ps.GetPublisherSettings(userID)
	if !ok {
		t.Fatal("expected settings to exist")
	}
	if got.SubscriptionMode != service.SubscriptionModeApproved || got.Visibility != service.VisibilityPublic {
		t.Fatalf("unexpected settings: %+v", got)
	}
}

func TestPostgresSubscriptionRoundTrip(t *testing.T) {
	ps := newTestPostgresStore(t)
	subscriber := uniqueID("sub")
	publisher := uniqueID("pub")
	cleanupRows(t, ps, subscriber)
	cleanupRows(t, ps, publisher)

	sub := service.Subscription{
		SubscriberID: subscriber,
		PublisherID:  publisher,
		Status:       service.SubscriptionStatusPending,
		CreatedAt:    time.Now().UTC(),
	}
	if err := ps.UpsertSubscription(sub); err != nil {
		t.Fatalf("UpsertSubscription: %v", err)
	}

	if err := ps.SetSubscriptionStatus(subscriber, publisher, service.SubscriptionStatusActive); err != nil {
		t.Fatalf("SetSubscriptionStatus: %v", err)
	}
	got, ok := ps.GetSubscription(subscriber, publisher)
	if !ok {
		t.Fatal("expected subscription to exist")
	}
	if got.Status != service.SubscriptionStatusActive {
		t.Fatalf("expected active status, got %q", got.Status)
	}

	if subs := ps.ListSubscriptionsForSubscriber(subscriber); len(subs) != 1 {
		t.Fatalf("expected 1 sub for subscriber, got %d", len(subs))
	}
	if subs := ps.ListSubscriptionsForPublisher(publisher, service.SubscriptionStatusActive); len(subs) != 1 {
		t.Fatalf("expected 1 active sub for publisher, got %d", len(subs))
	}

	ps.DeleteSubscription(subscriber, publisher)
	if _, ok := ps.GetSubscription(subscriber, publisher); ok {
		t.Fatal("expected subscription to be deleted")
	}
}

func TestPostgresSessionRoundTrip(t *testing.T) {
	ps := newTestPostgresStore(t)
	token := uniqueID("tok")
	userID := uniqueID("user")

	if err := ps.SaveSession(token, userID, "", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	t.Cleanup(func() { ps.DeleteSession(token) })

	got, ok := ps.SessionUserID(token)
	if !ok || got != userID {
		t.Fatalf("expected session user %s, got %q (ok=%v)", userID, got, ok)
	}
	if _, ok := ps.SessionAccessToken(token); ok {
		t.Fatal("expected no access token for token-less session")
	}

	// A refreshed save attaches an access token.
	if err := ps.SaveSession(token, userID, "twitch-user-token", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SaveSession with token: %v", err)
	}
	tok, ok := ps.SessionAccessToken(token)
	if !ok || tok != "twitch-user-token" {
		t.Fatalf("expected stored access token, got %q (ok=%v)", tok, ok)
	}

	ps.DeleteSession(token)
	if _, ok := ps.SessionUserID(token); ok {
		t.Fatal("expected deleted session to be unresolved")
	}
}

func TestPostgresSessionExpiresAndUpserts(t *testing.T) {
	ps := newTestPostgresStore(t)
	user := uniqueID("suser")

	expired := uniqueID("expired")
	if err := ps.SaveSession(expired, user, "", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("SaveSession expired: %v", err)
	}

	valid := uniqueID("valid")
	if err := ps.SaveSession(valid, user, "tok", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("SaveSession valid: %v", err)
	}
	t.Cleanup(func() {
		ps.DeleteSession(expired)
		ps.DeleteSession(valid)
	})

	// Looking up the valid token reaps expired rows.
	if _, ok := ps.SessionUserID(valid); !ok {
		t.Fatal("expected valid session to resolve")
	}
	var count int
	if err := ps.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token = $1`, expired).Scan(&count); err != nil {
		t.Fatalf("count expired: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected expired session to be reaped, %d remain", count)
	}

	// SaveSession with an existing token replaces the session (renewal).
	if err := ps.SaveSession(valid, user, "tok2", time.Now().Add(2*time.Hour)); err != nil {
		t.Fatalf("SaveSession renewal: %v", err)
	}
	var rows int
	if err := ps.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token = $1`, valid).Scan(&rows); err != nil {
		t.Fatalf("count valid: %v", err)
	}
	if rows != 1 {
		t.Fatalf("expected exactly 1 row for token, got %d", rows)
	}
}
