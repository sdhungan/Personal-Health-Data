package syncengine

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	dbpkg "github.com/sdhungan/Personal-Health-Data/internal/db"
)

func newTestSQLStore(t *testing.T) (*SQLStore, *sql.DB) {
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
	return &SQLStore{DB: conn, UserID: 1}, conn
}

func TestSQLStoreEnsurePendingIsIdempotent(t *testing.T) {
	store, conn := newTestSQLStore(t)
	ctx := context.Background()

	if err := store.EnsurePending(ctx, "google_health", "2026-07-30"); err != nil {
		t.Fatalf("EnsurePending (1st): %v", err)
	}
	if err := store.SetStatus(ctx, "google_health", "2026-07-30", StatusComplete); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	// A second EnsurePending must not reset an already-resolved row back
	// to pending.
	if err := store.EnsurePending(ctx, "google_health", "2026-07-30"); err != nil {
		t.Fatalf("EnsurePending (2nd): %v", err)
	}

	var status string
	if err := conn.QueryRow(`SELECT status FROM sync_state WHERE source='google_health' AND day='2026-07-30'`).Scan(&status); err != nil {
		t.Fatalf("querying status: %v", err)
	}
	if status != StatusComplete {
		t.Errorf("status = %q, want %q (EnsurePending must not clobber an existing row)", status, StatusComplete)
	}
}

func TestSQLStoreLatestDayAndUnresolvedDays(t *testing.T) {
	store, _ := newTestSQLStore(t)
	ctx := context.Background()

	if _, ok, err := store.LatestDay(ctx, "google_health"); err != nil {
		t.Fatalf("LatestDay on empty store: %v", err)
	} else if ok {
		t.Error("LatestDay on empty store: ok = true, want false")
	}

	for _, day := range []string{"2026-07-28", "2026-07-29", "2026-07-30"} {
		if err := store.EnsurePending(ctx, "google_health", day); err != nil {
			t.Fatalf("EnsurePending(%s): %v", day, err)
		}
	}
	if err := store.SetStatus(ctx, "google_health", "2026-07-29", StatusComplete); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	latest, ok, err := store.LatestDay(ctx, "google_health")
	if err != nil || !ok {
		t.Fatalf("LatestDay: %v, ok=%v", err, ok)
	}
	if latest != "2026-07-30" {
		t.Errorf("LatestDay = %q, want 2026-07-30", latest)
	}

	unresolved, err := store.UnresolvedDays(ctx, "google_health", "2026-07-31")
	if err != nil {
		t.Fatalf("UnresolvedDays: %v", err)
	}
	want := []string{"2026-07-28", "2026-07-30"} // 2026-07-29 is complete, excluded
	if len(unresolved) != len(want) {
		t.Fatalf("UnresolvedDays = %v, want %v", unresolved, want)
	}
	for i, d := range want {
		if unresolved[i] != d {
			t.Errorf("UnresolvedDays[%d] = %q, want %q", i, unresolved[i], d)
		}
	}
}

// TestRunDayAgainstRealSQLStore exercises the full state machine against a
// real (in-memory) SQLite-backed StateStore rather than the fake used in
// engine_test.go, to prove the SQL implementation and the state machine
// agree with each other.
func TestRunDayAgainstRealSQLStore(t *testing.T) {
	store, conn := newTestSQLStore(t)
	syncer := &fakeSyncer{dataDays: map[string]bool{"2026-07-31": true}} // today has data; yesterday doesn't
	now := mustParse(t, "2026-07-31")

	if err := RunDay(context.Background(), "google_health", store, syncer, now); err != nil {
		t.Fatalf("RunDay: %v", err)
	}

	rows, err := conn.Query(`SELECT day, status FROM sync_state WHERE source='google_health' ORDER BY day`)
	if err != nil {
		t.Fatalf("querying sync_state: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var day, status string
		if err := rows.Scan(&day, &status); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		got[day] = status
	}

	if got["2026-07-30"] != StatusMissing {
		t.Errorf("2026-07-30 status = %q, want %q", got["2026-07-30"], StatusMissing)
	}
	if got["2026-07-31"] != StatusPartial {
		t.Errorf("2026-07-31 status = %q, want %q", got["2026-07-31"], StatusPartial)
	}
}
