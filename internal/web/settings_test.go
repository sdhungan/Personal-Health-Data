package web

import (
	"testing"
	"time"
)

func mustParseDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation(dateLayout, s, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestValidateBulkSyncRangeAcceptsExactly30Days(t *testing.T) {
	now := mustParseDay(t, "2026-09-30")
	start := mustParseDay(t, "2026-09-01")
	end := mustParseDay(t, "2026-09-30") // 30 days inclusive

	gotStart, gotEnd, err := validateBulkSyncRange(start, end, now)
	if err != nil {
		t.Fatalf("validateBulkSyncRange: unexpected error: %v", err)
	}
	if !gotStart.Equal(start) || !gotEnd.Equal(end) {
		t.Errorf("validateBulkSyncRange = (%v, %v), want unchanged (%v, %v)", gotStart, gotEnd, start, end)
	}
}

func TestValidateBulkSyncRangeRejects31Days(t *testing.T) {
	now := mustParseDay(t, "2026-10-01")
	start := mustParseDay(t, "2026-09-01")
	end := mustParseDay(t, "2026-10-01") // 31 days inclusive

	if _, _, err := validateBulkSyncRange(start, end, now); err == nil {
		t.Error("validateBulkSyncRange(31 days): expected an error, got nil")
	}
}

func TestValidateBulkSyncRangeRejectsEndBeforeStart(t *testing.T) {
	now := mustParseDay(t, "2026-09-30")
	start := mustParseDay(t, "2026-09-10")
	end := mustParseDay(t, "2026-09-01")

	if _, _, err := validateBulkSyncRange(start, end, now); err == nil {
		t.Error("validateBulkSyncRange(end before start): expected an error, got nil")
	}
}

func TestValidateBulkSyncRangeClampsEndToNow(t *testing.T) {
	now := mustParseDay(t, "2026-09-15")
	start := mustParseDay(t, "2026-09-01")
	end := mustParseDay(t, "2026-09-30") // in the future relative to now

	_, gotEnd, err := validateBulkSyncRange(start, end, now)
	if err != nil {
		t.Fatalf("validateBulkSyncRange: unexpected error: %v", err)
	}
	if !gotEnd.Equal(now) {
		t.Errorf("validateBulkSyncRange: end = %v, want clamped to now (%v)", gotEnd, now)
	}
}
