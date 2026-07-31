package syncengine

import (
	"context"
	"sort"
	"testing"
	"time"
)

// fakeStore is an in-memory StateStore for testing the state machine in
// isolation from any real database.
type fakeStore struct {
	rows map[string]map[string]string // source -> day -> status
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[string]map[string]string{}}
}

func (s *fakeStore) seed(source, day, status string) {
	m := s.rows[source]
	if m == nil {
		m = map[string]string{}
		s.rows[source] = m
	}
	m[day] = status
}

func (s *fakeStore) EnsurePending(ctx context.Context, source, day string) error {
	m := s.rows[source]
	if m == nil {
		m = map[string]string{}
		s.rows[source] = m
	}
	if _, ok := m[day]; !ok {
		m[day] = StatusPending
	}
	return nil
}

func (s *fakeStore) SetStatus(ctx context.Context, source, day, status string) error {
	m := s.rows[source]
	if m == nil {
		m = map[string]string{}
		s.rows[source] = m
	}
	m[day] = status
	return nil
}

func (s *fakeStore) LatestDay(ctx context.Context, source string) (string, bool, error) {
	m := s.rows[source]
	if len(m) == 0 {
		return "", false, nil
	}
	var latest string
	for day := range m {
		if latest == "" || day > latest {
			latest = day
		}
	}
	return latest, true, nil
}

func (s *fakeStore) UnresolvedDays(ctx context.Context, source, beforeDay string) ([]string, error) {
	m := s.rows[source]
	var days []string
	for day, status := range m {
		if day < beforeDay && (status == StatusPending || status == StatusPartial) {
			days = append(days, day)
		}
	}
	sort.Strings(days)
	return days, nil
}

// fakeSyncer returns canned hasData answers per day and records every day
// it was asked to sync, so tests can assert exactly which days were (and
// weren't) touched.
type fakeSyncer struct {
	dataDays map[string]bool
	calls    []string
}

func (s *fakeSyncer) SyncDay(ctx context.Context, day time.Time) (bool, error) {
	key := day.Format(DateLayout)
	s.calls = append(s.calls, key)
	return s.dataDays[key], nil
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(DateLayout, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return tm
}

const source = "google_health"

func TestFirstRunResolvesYesterdayComplete(t *testing.T) {
	store := newFakeStore()
	syncer := &fakeSyncer{dataDays: map[string]bool{"2026-07-30": true}}
	now := mustParse(t, "2026-07-31")

	if err := RunDay(context.Background(), source, store, syncer, now); err != nil {
		t.Fatalf("RunDay: %v", err)
	}

	if got := store.rows[source]["2026-07-31"]; got != StatusPartial {
		t.Errorf("today status = %q, want %q", got, StatusPartial)
	}
	if got := store.rows[source]["2026-07-30"]; got != StatusComplete {
		t.Errorf("yesterday status = %q, want %q", got, StatusComplete)
	}
	if len(store.rows[source]) != 2 {
		t.Errorf("expected only 2 tracked days on a first run, got %d: %v", len(store.rows[source]), store.rows[source])
	}
}

func TestWatchDeadYesterdayButTodayHasDataMarksMissingImmediately(t *testing.T) {
	store := newFakeStore()
	syncer := &fakeSyncer{dataDays: map[string]bool{"2026-07-31": true}}
	now := mustParse(t, "2026-07-31")

	if err := RunDay(context.Background(), source, store, syncer, now); err != nil {
		t.Fatalf("RunDay: %v", err)
	}

	if got := store.rows[source]["2026-07-31"]; got != StatusPartial {
		t.Errorf("today status = %q, want %q", got, StatusPartial)
	}
	if got := store.rows[source]["2026-07-30"]; got != StatusMissing {
		t.Errorf("yesterday status = %q, want %q (immediate finalize: today already has data)", got, StatusMissing)
	}
}

func TestGapWithNoLaterDataStaysPendingForRetry(t *testing.T) {
	store := newFakeStore()
	syncer := &fakeSyncer{dataDays: map[string]bool{}} // nothing has data anywhere yet
	now := mustParse(t, "2026-07-31")

	if err := RunDay(context.Background(), source, store, syncer, now); err != nil {
		t.Fatalf("RunDay: %v", err)
	}

	if got := store.rows[source]["2026-07-30"]; got != StatusPending {
		t.Errorf("yesterday status = %q, want %q (no evidence either way yet)", got, StatusPending)
	}
	if got := store.rows[source]["2026-07-31"]; got != StatusPartial {
		t.Errorf("today status = %q, want %q", got, StatusPartial)
	}
}

func TestMultiDayGapAllResolveMissingWhenTodayHasData(t *testing.T) {
	store := newFakeStore()
	// Simulate an installation that was already tracking days up through
	// 2026-07-25 (already resolved, e.g. from normal prior operation),
	// then the service was down for the rest of the week.
	store.seed(source, "2026-07-25", StatusComplete)

	syncer := &fakeSyncer{dataDays: map[string]bool{"2026-07-31": true}}
	now := mustParse(t, "2026-07-31")

	if err := RunDay(context.Background(), source, store, syncer, now); err != nil {
		t.Fatalf("RunDay: %v", err)
	}

	for _, day := range []string{"2026-07-26", "2026-07-27", "2026-07-28", "2026-07-29", "2026-07-30"} {
		if got := store.rows[source][day]; got != StatusMissing {
			t.Errorf("%s status = %q, want %q (gap day, later day has data)", day, got, StatusMissing)
		}
	}
	if got := store.rows[source]["2026-07-31"]; got != StatusPartial {
		t.Errorf("today status = %q, want %q", got, StatusPartial)
	}
	// The already-resolved day from before the outage must never be
	// re-synced.
	if got := store.rows[source]["2026-07-25"]; got != StatusComplete {
		t.Errorf("2026-07-25 status = %q, want unchanged %q", got, StatusComplete)
	}
	for _, day := range syncer.calls {
		if day == "2026-07-25" {
			t.Error("already-complete day 2026-07-25 was re-synced; it must never be touched automatically")
		}
	}
}

func TestDataInMiddleOfGapResolvesCorrectly(t *testing.T) {
	store := newFakeStore()
	store.seed(source, "2026-07-27", StatusComplete)

	// Gap days 07-28, 07-29, 07-30 get backfilled as pending; today is
	// 07-31. Only 07-29 (middle of the gap) actually has data.
	syncer := &fakeSyncer{dataDays: map[string]bool{"2026-07-29": true}}
	now := mustParse(t, "2026-07-31")

	if err := RunDay(context.Background(), source, store, syncer, now); err != nil {
		t.Fatalf("RunDay: %v", err)
	}

	want := map[string]string{
		"2026-07-28": StatusMissing,  // no data, but 07-29 after it resolved with data
		"2026-07-29": StatusComplete, // has data
		"2026-07-30": StatusPending,  // no data, and nothing after it (today) has resolved with data either
		"2026-07-31": StatusPartial,  // today
	}
	for day, wantStatus := range want {
		if got := store.rows[source][day]; got != wantStatus {
			t.Errorf("%s status = %q, want %q", day, got, wantStatus)
		}
	}
}

func TestResolvedDaysAreNeverReSyncedOnSubsequentRuns(t *testing.T) {
	store := newFakeStore()
	syncer := &fakeSyncer{dataDays: map[string]bool{"2026-07-30": true, "2026-07-31": true, "2026-08-01": true}}

	if err := RunDay(context.Background(), source, store, syncer, mustParse(t, "2026-07-31")); err != nil {
		t.Fatalf("RunDay (day 1): %v", err)
	}
	if got := store.rows[source]["2026-07-30"]; got != StatusComplete {
		t.Fatalf("2026-07-30 status = %q, want %q after day 1", got, StatusComplete)
	}
	// 2026-07-31 was "today" on day 1 (partial); by day 2 it's "yesterday"
	// and should resolve to complete since it has data.
	syncer.calls = nil
	if err := RunDay(context.Background(), source, store, syncer, mustParse(t, "2026-08-01")); err != nil {
		t.Fatalf("RunDay (day 2): %v", err)
	}
	if got := store.rows[source]["2026-07-31"]; got != StatusComplete {
		t.Errorf("2026-07-31 status = %q, want %q after day 2", got, StatusComplete)
	}

	calledDays := map[string]int{}
	for _, d := range syncer.calls {
		calledDays[d]++
	}
	if calledDays["2026-07-30"] != 0 {
		t.Errorf("2026-07-30 (already complete before day 2) was synced %d times on day 2, want 0", calledDays["2026-07-30"])
	}
	if calledDays["2026-07-31"] != 1 {
		t.Errorf("2026-07-31 was synced %d times on day 2, want exactly 1", calledDays["2026-07-31"])
	}
	if calledDays["2026-08-01"] != 1 {
		t.Errorf("2026-08-01 (today on day 2) was synced %d times, want exactly 1", calledDays["2026-08-01"])
	}
}

func TestTodayIsAlwaysSyncedAndNeverPromotedWhileStillToday(t *testing.T) {
	store := newFakeStore()
	syncer := &fakeSyncer{dataDays: map[string]bool{"2026-07-31": true}}
	now := mustParse(t, "2026-07-31")

	for i := 0; i < 3; i++ {
		if err := RunDay(context.Background(), source, store, syncer, now); err != nil {
			t.Fatalf("RunDay iteration %d: %v", i, err)
		}
		if got := store.rows[source]["2026-07-31"]; got != StatusPartial {
			t.Errorf("iteration %d: today status = %q, want %q (must stay partial while it IS today)", i, got, StatusPartial)
		}
	}

	todayCalls := 0
	for _, d := range syncer.calls {
		if d == "2026-07-31" {
			todayCalls++
		}
	}
	if todayCalls != 3 {
		t.Errorf("today was synced %d times across 3 runs, want 3 (always re-synced while in progress)", todayCalls)
	}
}
