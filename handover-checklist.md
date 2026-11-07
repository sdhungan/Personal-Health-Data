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
- [x] User confirmed; backup deleted

## 3. Hourly activity, watch-only values (deploying 2026-11-07 21:17)

- [x] Migration 14: `watch_steps_hourly` -> `watch_activity_hourly` with
      active energy, altitude gain, floors, sedentary and active minutes
      by level per local hour (rollUp, `windowSize: 3600s`); re-opens all
      Google days for the backfill
- [x] Rollups and daily rollups send `dataSourceFamily: google-wearables`:
      the watch is the source of truth, no watch + phone sums
- [x] A day's hourly rows are replaced, not merged; sedentary periods are
      split across the hours they cover (the rollUp files a whole period
      under its start hour)
- [x] Client paced at 4 req/s, retries 429/503 (quota is 300/min)
- [x] Dashboard: activity tiles chart hourly at every range; bars up to
      1W, dotted zero-based line from 2W; per-day average over days with
      data
- [x] Preview (copy of live root, real API): 94 Google days complete,
      hourly steps == daily on all of them, sedentary <= 60 min/hour,
      integrity ok
- [x] Deployed 6a1c6c3; backup `health.db.enc.bak-before-v14-20261107`
      (hash-verified), previous binary `healthd.exe.bak`
- [x] Verified live on a copy (after an unrelated PC freeze + reboot at
      21:35; the service recovered from its working file and synced):
      integrity ok, v14, 94 Google days complete, hourly steps == daily on
      all of them, max 60 min/hour, 107 sleep sessions, 1.38 M HR rows
- [x] Charts: minutes-per-hour bars fixed at 0-60 (98f7ea2); 2W+ lines
      are one dot per day, the day's total (4ba5844)
- [x] Shipped 98f7ea2 + 4ba5844 (UI only) 2026-11-07 21:48, via a UAC
      prompt; clean restart, first sync pass ok (10 s).
      `healthd.exe.bak` (pre-v14) kept as the rollback
- [x] User confirmed 2026-11-07; backup and pre-v14 binary deleted,
      `main` pushed

## Next (discuss with the user first)

- [ ] Exercise has the same newest-page ceiling (unfiltered list(), and
      exercise accepts no time filter): fine while there are fewer than
      ~100 sessions in range, then older days silently miss workouts. Needs
      nextPageToken pagination. Check ECG the same way
- [ ] The sleep stage chart has 4 lanes (AWAKE/REM/LIGHT/DEEP); a CLASSIC
      session's `ASLEEP` segments aren't drawn. Only short naps so far
- [x] Housekeeping in `G:\HealthService`: old binaries and DB backups
      deleted (user OK'd 2026-11-07)
- [x] Throwaway probes and the preview scratch root (copies of keys)
      deleted
