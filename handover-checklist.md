# Handover checklist

Running list of storage/data-format work: what's done, what's waiting for
the user's review, and what's next. Keep it current; any agent picking this
up should read it (and `prerequisite.md`) first.

Live service: `healthDSafal`, root `G:\HealthService`, binary
`G:\HealthService\bin\healthd.exe`. Its DB is the user's real data: never
experiment on it, only read copies, and back it up before anything that
changes it.

## Baseline (measured 2026-11-06 on a copy of the live DB)

94 days synced, 210 MB (24 MB after sections 1-2). `watch_heart_rate_intraday` is 212 MB of that
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
- [x] Compacted the live DB (service stopped, VACUUM via `db.Open`): 309 ->
      136 MB; sorted full dump identical before/after; every pre-migration
      HR row present with the same bpm. User OK'd; backups deleted
- [x] VACUUM now runs automatically once after any migration
      (`Store.migrate`) — migrations are healthd's only bulk deletes

## 2. Heart-rate table shape, sleep history (deployed 2026-11-06 23:28)

- [x] Migration 11: `watch_heart_rate_intraday` WITHOUT ROWID, `cached_at`
      + index dropped (f1e7b03). DB 136 MB -> 24 MB
- [x] "Rolling ~3-day cache" statements fixed (f6e0a88);
      `grafana-integration.md` updated (untracked, left uncommitted)
- [x] Sleep fetched with a `sleep.interval.end_time` filter instead of
      unfiltered list() (newest page only); migration 12 re-opens resolved
      Google days without sleep (9ebd23d)
- [x] Migration 13: drop the stage_type CHECK — CLASSIC sleep sessions
      (older days, naps) use `ASLEEP`, which aborted the whole sync pass
      (702d3ba). Found by the dry run, not by tests
- [x] Dry run (copy of live root, real API): 23 -> 106 sleep sessions
      (Aug-Nov), all 94 Google days resolved, all old HR/sleep rows
      unchanged, integrity ok; ~10 min sync pass
- [x] Deployed; backup `health.db.enc.bak-before-v13-20261106`
      (hash-verified), previous binary `healthd.exe.bak`
- [x] Verified live: first sync pass ok (595 s, no errors); copy of the
      working file vs the backup matches the dry run (106 sleep sessions,
      94 Google days resolved, HR/sleep rows unchanged, integrity ok,
      24 MB); 2026-08-20 shows main sleep + nap on the dashboard
- [ ] **REVIEW (user):** confirm, then delete the backup

## Next (discuss with the user first)

- [ ] Exercise has the same newest-page ceiling (unfiltered list(), and
      exercise accepts no time filter): fine while there are fewer than
      ~100 sessions in range, then older days silently miss workouts. Needs
      nextPageToken pagination. Check ECG the same way
- [ ] Hourly breakdown for calories, active energy, distance, altitude,
      sedentary, active minutes, floors via `dataPoints:rollUp` with
      `windowSize: 3600s` (~3 MB/year). Probe real responses first
      (confirm-before-trusting field names)
- [ ] The sleep stage chart has 4 lanes (AWAKE/REM/LIGHT/DEEP); a CLASSIC
      session's `ASLEEP` segments aren't drawn. Only short naps so far
- [ ] Housekeeping in `G:\HealthService`: 15 old `healthd.exe.bak-*`
      binaries (~24 MB each) and 2 old DB backups (~180 MB each) — ask
      before deleting
