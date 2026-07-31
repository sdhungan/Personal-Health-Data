// Package syncengine implements the day-completeness state machine
// described in ARCHITECTURE.md §3, shared by every sync source (Google
// Health, Cronometer, ...). What differs per source is only how a single
// day gets fetched and upserted (DaySyncer); the rules for when a day is
// still "in progress" vs. finalized as complete or genuinely missing are
// the same regardless of source, so they live here once.
package syncengine

import (
	"context"
	"fmt"
	"time"
)

// DateLayout is the sync_state.day text format ("YYYY-MM-DD").
const DateLayout = "2006-01-02"

// Status values for sync_state.status (see internal/db/schema.sql).
const (
	StatusPending  = "pending"
	StatusPartial  = "partial"
	StatusComplete = "complete"
	StatusMissing  = "missing"
)

// DaySyncer performs the actual fetch-and-upsert for one source and day.
// hasData reports whether any data was found for that day — the signal
// the completeness state machine uses to decide complete vs. missing.
type DaySyncer interface {
	SyncDay(ctx context.Context, day time.Time) (hasData bool, err error)
}

// StateStore is the sync_state persistence RunDay needs. Kept minimal and
// interface-based so the state machine is testable without a real
// database (see engine_test.go's in-memory implementation).
type StateStore interface {
	// EnsurePending creates a StatusPending row for (source, day) if one
	// doesn't already exist; no-op otherwise.
	EnsurePending(ctx context.Context, source, day string) error
	// SetStatus updates (source, day)'s status (and last_synced_at).
	SetStatus(ctx context.Context, source, day, status string) error
	// LatestDay returns the most recent day (any status) tracked for
	// source, or ok=false if none exists yet.
	LatestDay(ctx context.Context, source string) (day string, ok bool, err error)
	// UnresolvedDays returns every day for source with status
	// StatusPending or StatusPartial that is strictly before beforeDay,
	// ascending.
	UnresolvedDays(ctx context.Context, source, beforeDay string) ([]string, error)
}

// RunDay executes one sync pass for source, as of now (the caller's
// current local time — callers should pass a consistent timezone, since
// day boundaries are computed from it). Each call:
//
//  1. Backfills a StatusPending row for every day between the last day
//     ever tracked and yesterday (so a multi-day gap — service down, watch
//     dead — still gets every individual day tracked and eventually
//     resolved, not silently skipped), plus today.
//  2. Always syncs today; its row stays StatusPartial — it's inherently
//     still changing and is never auto-promoted while it IS today.
//  3. Walks every unresolved (pending/partial) day strictly before today,
//     most-recent-first:
//     - data found -> StatusComplete.
//     - no data, but a later day already resolved to "had data at some
//     point" (either directly, or transitively through a chain of
//     StatusMissing days that trace back to one that did) -> StatusMissing
//     immediately. This is deliberate: proof the pipeline/watch was
//     capturing data by a later day is proof an earlier gap is a real
//     gap, not sync lag — retrying it forever would never resolve
//     anything a human can't already tell from the later day's presence.
//     - no data, and nothing after it has resolved with data either ->
//     left pending/partial for the next run (could still be sync lag).
//
// Once a day reaches StatusComplete or StatusMissing, RunDay never touches
// it again — a forced re-sync of a specific historical day is a separate,
// explicit operation (a UI/CLI action), not something this engine does on
// its own.
func RunDay(ctx context.Context, source string, store StateStore, syncer DaySyncer, now time.Time) error {
	todayStr := now.Format(DateLayout)
	yesterday := now.AddDate(0, 0, -1)

	if err := backfillPendingDays(ctx, store, source, yesterday); err != nil {
		return err
	}
	if err := store.EnsurePending(ctx, source, todayStr); err != nil {
		return fmt.Errorf("ensuring sync_state row for today: %w", err)
	}

	todayHasData, err := syncer.SyncDay(ctx, now)
	if err != nil {
		return fmt.Errorf("syncing today (%s): %w", todayStr, err)
	}
	if err := store.SetStatus(ctx, source, todayStr, StatusPartial); err != nil {
		return fmt.Errorf("updating sync_state for today: %w", err)
	}

	unresolved, err := store.UnresolvedDays(ctx, source, todayStr)
	if err != nil {
		return fmt.Errorf("listing unresolved days: %w", err)
	}

	laterDayHasData := todayHasData
	for i := len(unresolved) - 1; i >= 0; i-- {
		dayStr := unresolved[i]
		day, err := time.Parse(DateLayout, dayStr)
		if err != nil {
			return fmt.Errorf("parsing unresolved day %q: %w", dayStr, err)
		}

		hasData, err := syncer.SyncDay(ctx, day)
		if err != nil {
			return fmt.Errorf("syncing %s: %w", dayStr, err)
		}

		switch {
		case hasData:
			if err := store.SetStatus(ctx, source, dayStr, StatusComplete); err != nil {
				return fmt.Errorf("marking %s complete: %w", dayStr, err)
			}
			laterDayHasData = true
		case laterDayHasData:
			if err := store.SetStatus(ctx, source, dayStr, StatusMissing); err != nil {
				return fmt.Errorf("marking %s missing: %w", dayStr, err)
			}
		default:
			// Leave pending/partial: neither this day nor anything after
			// it (so far) has resolved with data, so this could still be
			// ordinary sync lag rather than a confirmed gap.
		}
	}

	return nil
}

// backfillPendingDays ensures a StatusPending row exists for every day from
// the one after the latest tracked day (or, on a source's very first run,
// just yesterday — we don't auto-backfill deep history) through
// throughDay inclusive.
func backfillPendingDays(ctx context.Context, store StateStore, source string, throughDay time.Time) error {
	latest, ok, err := store.LatestDay(ctx, source)
	start := throughDay
	if err != nil {
		return fmt.Errorf("finding latest tracked day: %w", err)
	}
	if ok {
		latestDate, err := time.Parse(DateLayout, latest)
		if err != nil {
			return fmt.Errorf("parsing latest tracked day %q: %w", latest, err)
		}
		start = latestDate.AddDate(0, 0, 1)
	}

	for d := start; !d.After(throughDay); d = d.AddDate(0, 0, 1) {
		if err := store.EnsurePending(ctx, source, d.Format(DateLayout)); err != nil {
			return fmt.Errorf("ensuring sync_state row for %s: %w", d.Format(DateLayout), err)
		}
	}
	return nil
}
