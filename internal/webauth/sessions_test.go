package webauth

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	dbpkg "github.com/sdhungan/Personal-Health-Data/internal/db"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening sqlite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Exec(dbpkg.Schema); err != nil {
		t.Fatalf("applying schema: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x')`); err != nil {
		t.Fatalf("seeding test user: %v", err)
	}
	return conn
}

func TestCreateSessionThenLookupSucceeds(t *testing.T) {
	conn := newTestDB(t)
	ctx := context.Background()

	token, err := CreateSession(ctx, conn, 1)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if token == "" {
		t.Fatal("CreateSession returned an empty token")
	}

	userID, username, ok, err := LookupSession(ctx, conn, token)
	if err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	if !ok {
		t.Fatal("LookupSession: ok = false, want true for a freshly created session")
	}
	if userID != 1 {
		t.Errorf("userID = %d, want 1", userID)
	}
	if username != "test" {
		t.Errorf("username = %q, want %q", username, "test")
	}
}

func TestLookupSessionRejectsUnknownToken(t *testing.T) {
	conn := newTestDB(t)
	_, _, ok, err := LookupSession(context.Background(), conn, "not-a-real-token")
	if err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	if ok {
		t.Error("LookupSession: ok = true for a token that was never issued")
	}
}

func TestLookupSessionRejectsExpiredInactivity(t *testing.T) {
	conn := newTestDB(t)
	ctx := context.Background()

	token, err := CreateSession(ctx, conn, 1)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Simulate the session having gone untouched for longer than
	// SessionDuration — directly backdating last_seen_at rather than
	// waiting 24h real time.
	stale := time.Now().UTC().Add(-SessionDuration - time.Minute).Format(timeLayout)
	if _, err := conn.Exec(`UPDATE web_session SET last_seen_at = ?`, stale); err != nil {
		t.Fatalf("backdating session: %v", err)
	}

	_, _, ok, err := LookupSession(ctx, conn, token)
	if err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	if ok {
		t.Error("LookupSession: ok = true for a session inactive longer than SessionDuration, want false")
	}

	var count int
	if err := conn.QueryRow(`SELECT count(*) FROM web_session`).Scan(&count); err != nil {
		t.Fatalf("counting sessions: %v", err)
	}
	if count != 0 {
		t.Errorf("web_session has %d rows after an expired lookup, want 0 (expired session should be deleted)", count)
	}
}

func TestLookupSessionTouchesLastSeenAt(t *testing.T) {
	conn := newTestDB(t)
	ctx := context.Background()

	token, err := CreateSession(ctx, conn, 1)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Backdate, but not past expiry — still valid, and LookupSession
	// should slide the window forward again (the "sliding" part of the
	// 24h sliding inactivity window).
	almostStale := time.Now().UTC().Add(-SessionDuration + time.Hour).Format(timeLayout)
	if _, err := conn.Exec(`UPDATE web_session SET last_seen_at = ?`, almostStale); err != nil {
		t.Fatalf("backdating session: %v", err)
	}

	if _, _, ok, err := LookupSession(ctx, conn, token); err != nil || !ok {
		t.Fatalf("LookupSession: ok=%v, err=%v", ok, err)
	}

	var lastSeenStr string
	if err := conn.QueryRow(`SELECT last_seen_at FROM web_session`).Scan(&lastSeenStr); err != nil {
		t.Fatalf("reading last_seen_at: %v", err)
	}
	lastSeen, err := time.Parse(timeLayout, lastSeenStr)
	if err != nil {
		t.Fatalf("parsing last_seen_at: %v", err)
	}
	if time.Since(lastSeen) > time.Minute {
		t.Errorf("last_seen_at = %v, want touched to ~now by the lookup above", lastSeen)
	}
}

func TestDeleteSessionThenLookupFails(t *testing.T) {
	conn := newTestDB(t)
	ctx := context.Background()

	token, err := CreateSession(ctx, conn, 1)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := DeleteSession(ctx, conn, token); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	_, _, ok, err := LookupSession(ctx, conn, token)
	if err != nil {
		t.Fatalf("LookupSession: %v", err)
	}
	if ok {
		t.Error("LookupSession: ok = true after DeleteSession, want false")
	}
}

func TestCleanupExpiredRemovesOnlyStaleSessions(t *testing.T) {
	conn := newTestDB(t)
	ctx := context.Background()

	freshToken, err := CreateSession(ctx, conn, 1)
	if err != nil {
		t.Fatalf("CreateSession (fresh): %v", err)
	}
	staleToken, err := CreateSession(ctx, conn, 1)
	if err != nil {
		t.Fatalf("CreateSession (stale): %v", err)
	}
	stale := time.Now().UTC().Add(-SessionDuration - time.Hour).Format(timeLayout)
	if _, err := conn.Exec(`UPDATE web_session SET last_seen_at = ? WHERE token_hash = ?`, stale, hashToken(staleToken)); err != nil {
		t.Fatalf("backdating stale session: %v", err)
	}

	if err := CleanupExpired(ctx, conn); err != nil {
		t.Fatalf("CleanupExpired: %v", err)
	}

	if _, _, ok, _ := LookupSession(ctx, conn, staleToken); ok {
		t.Error("stale session survived CleanupExpired")
	}
	if _, _, ok, _ := LookupSession(ctx, conn, freshToken); !ok {
		t.Error("fresh session was removed by CleanupExpired")
	}
}
