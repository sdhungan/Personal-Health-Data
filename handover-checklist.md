# Handover checklist

Running list of storage/data-format work: what's done, what's waiting for
the user's review, and what's next. Keep it current; any agent picking this
up should read it (and `prerequisite.md`) first.

Live service: `healthDSafal`, root `G:\HealthService`, binary
`G:\HealthService\bin\healthd.exe`. Its DB is the user's real data: never
experiment on it, only read copies, and back it up before anything that
changes it.

## Baseline (measured 2026-11-06 on a copy of the live DB)

94 days synced, 210 MB. `watch_heart_rate_intraday` is 212 MB of that
(1.35M rows, ~14,300/day, ~155 bytes/row: 61 table + 53 primary-key index +
44 `cached_at` index). Everything else together is ~8 MB, mostly
`watch_activity_level_segment`. At this rate: ~0.8 GB/year.

## 1. Heart-rate timestamps: RFC3339 text -> INTEGER Unix seconds

- [x] Back up the live encrypted DB (`G:\HealthService\db\health.db.enc` ->
      `health.db.enc.bak-before-hr-int-timestamps-20261106`, hash-verified;
      the deploy command refreshes it right after stopping the service so
      it is exactly the pre-migration state)
- [x] Migration (version 10): rebuild `watch_heart_rate_intraday` with
      `recorded_at INTEGER` (Unix seconds, UTC). Same precision as before:
      the sync always wrote whole seconds (`time.RFC3339`) — commit 0815e5a
- [x] Sync writes `RecordedAt.Unix()`; dashboard/activity readers query by
      Unix seconds
- [x] Tests: migration test converts an old-format row exactly;
      build/vet/all tests pass
- [x] Dry run on a copy of the live DB: 1,346,528 rows before/after, all
      INTEGER, 0 rows differing from the original string; ~7 s;
      220 MB -> 136 MB after VACUUM
- [x] Deployed 2026-11-06 23:00 (0815e5a binary). Backup refreshed after
      stop, hash-matched the pre-migration DB; previous binary kept as
      `G:\HealthService\bin\healthd.exe.bak`. Verified on a copy of the
      live `.health.db.work` (copy deleted after): quick_check ok,
      `user_version` 10, 1,347,634 rows (all `integer`, 2026-08-05 to
      today), schema `recorded_at INTEGER`
- [x] Verified live: service running, migration + first sync pass logged
      ok, no errors; 1D heart-rate tile and the Running activity's
      heart-rate chart render
- [ ] Note: right after the rebuild the live working file was 309 MB
      (+140 MB WAL), up from 220 MB — old table's pages are now free
      pages. Makes the VACUUM question below more pressing
- [ ] **REVIEW (user):** confirm all is fine, then delete the backup
- [ ] The file won't shrink by itself: nothing in healthd runs `VACUUM`,
      so the ~85 MB freed stays as free pages (reused by new data). One-off
      VACUUM of the live DB (service stopped), or add it to the app?

## Next (not started; discuss with the user first)

- [ ] `cached_at` column + its index (44 bytes/row, ~59 MB now): nothing
      ever prunes the table, so `cached_at` is unused. Drop both?
- [ ] `WITHOUT ROWID` for `watch_heart_rate_intraday` (no hidden rowid
      b-tree on top of the primary-key index)
- [ ] Docs/UI claim the table is a "rolling ~3-day cache" (schema.sql
      comment, `prerequisite.md`, `healthdata.go`, the 1D heart-rate tile
      message in `internal/web/data.go`); it is actually full history. Fix
      the statements
- [ ] Hourly breakdown for calories, active energy, distance, altitude,
      sedentary, active minutes, floors via `dataPoints:rollUp` with
      `windowSize: 3600s` (~3 MB/year). Probe real responses first
      (confirm-before-trusting field names)
- [ ] Sleep-history bug: see the "refactoring handoff" memory (sleep list()
      only returns the newest page; use the `sleep.interval.end_time`
      filter + re-open google_health days)
- [ ] `grafana-integration.md` (the user's own untracked doc) says
      heart-rate timestamps are RFC3339 strings; they're Unix seconds now
- [ ] Housekeeping in `G:\HealthService`: 15 old `healthd.exe.bak-*`
      binaries (~24 MB each) and 2 old DB backups (~180 MB each) — ask
      before deleting
