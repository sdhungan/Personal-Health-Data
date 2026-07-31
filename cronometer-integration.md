# Cronometer integration — continuation notes

Handoff doc for building the Cronometer sync client, written so a new
session can start immediately without re-deriving context or re-searching
for reverse-engineered API options. Read this before touching anything
Cronometer-related. See also `prerequisite.md` (general project gotchas)
and `structure.md` (repo layout).

## Current state (as of 2026-07-31) — what already exists

- **Schema is fully scaffolded, zero rows populated.** `internal/db/schema.sql`
  has `cronometer_daily_nutrition`, `cronometer_serving`,
  `cronometer_exercise`, `cronometer_biometric`, `cronometer_note` — all
  five tables Cronometer's own export feature covers. `daily_nutrition` and
  `serving` share the exact same ~70-column nutrient dictionary so they're
  directly comparable at daily vs. per-entry granularity.
- `cronometer_daily_nutrition.kcal_burned_cronometer` was added
  2026-07-31 specifically to hold Cronometer's own total-expenditure
  estimate (BMR + activity + food thermic effect), deliberately kept
  separate from `watch_daily_summary.kcal_burned_google` (Google Health's
  watch-based estimate) — see that column's comment in `schema.sql`. **Not
  yet confirmed that Cronometer's daily-nutrition export actually carries
  an expenditure figure at all** — verify this once real data is captured
  (see "Ordered task list" step 2 below); if it doesn't, that column just
  stays unpopulated, no schema change needed.
- `internal/syncengine`'s `sync_state.source` CHECK constraint already
  accepts `'cronometer'` alongside `'google_health'` — the day-completeness
  state machine (`pending`/`partial`/`complete`/`missing`) is
  source-agnostic and ready to track Cronometer sync progress the same way
  it tracks Google Health's, no changes needed there.
- `internal/config/config.go` has `CronometerConfig{Username, Password
  string}` already wired into `Config` and `config.yaml`'s template
  (`internal/config/template.go`) — but currently stored **in plain text**;
  see "Credential handling" (step 4 below) — this needs the same
  encrypted-at-rest treatment `internal/crypto` already gives the Google
  OAuth token.
- `internal/cli/sync.go`'s `sync` command runs the Google Health pass then
  prints `fmt.Println("TODO: run the Cronometer sync pass too")` and
  returns — this is the exact call site a real Cronometer sync pass plugs
  into.
- **Nothing else exists.** No `internal/cronometer` package. No Cronometer
  client dependency in `go.mod` — `gocronometer` (named as the intended
  choice in `ARCHITECTURE.md` §5) has never actually been added.
- `internal/db/migrations.go` (added 2026-07-31, this project's first
  migration mechanism) is available if any schema change is needed once
  real field shapes are confirmed — see its own doc comment for the
  pattern (ordered `Migrations` slice, applied via `PRAGMA user_version`).

## External research already done — two competing reverse-engineered options

Cronometer has no official API. Two genuinely different reverse-engineered
approaches exist; this is a real decision, not just "pick a library":

### Option A: GWT-RPC web API (`gocronometer`) — what ARCHITECTURE.md names

- Repo: https://github.com/jrmycanady/gocronometer (Go, MIT/GPLv2). A
  companion CLI wrapping the same library, useful as a second reference
  for expected output shapes: https://github.com/jrmycanady/cronometer-export
  (its README documents exactly five export categories: servings,
  daily-nutrition, exercises, notes, biometrics — a 1:1 match to our five
  `cronometer_*` tables, which is presumably why the schema was shaped this
  way originally).
- **Auth flow**: GET `https://cronometer.com/login/` and scrape an
  anti-CSRF token out of the HTML. POST credentials + that token to
  `https://cronometer.com/login`. Server responds with a `LoginResponse`
  JSON (`Redirect`, `Success`, `Error` fields) and a `sesnonce` session
  cookie. Then call `GWTAuthenticate()`, which extracts a numeric `UserID`
  from the GWT response via regex.
- **Export functions**, all shaped `Export<Type>(ctx, startDate,
  endDate time.Time) (string, error)` (returns raw CSV/text; some have
  `*Parsed` variants returning typed slices — `ExportServingsParsed() →
  ServingRecords`, `ExportExercisesParsedWithLocation() → ExerciseRecords`,
  `ExportBiometricRecordsParsedWithLocation() → BiometricRecords`):
  `ExportDailyNutrition`, `ExportServings`, `ExportExercises`,
  `ExportBiometrics`, `ExportNotes`. Date params are `YYYY-mm-dd`.
- **Known fragility** (from the library's own documentation): GWT response
  parsing depends on internal values "that change over time with
  application updates"; no retry/timeout handling visible; brittle
  regex-based response extraction; no rate limiting.

### Option B: Mobile REST API (no existing Go client)

- Reference implementations (Python/TS, reverse-engineered via static
  analysis of the Android app's `libapp.so` — a Dart AOT snapshot — plus
  Frida + mitmproxy traffic interception against the live app):
  https://github.com/rwestergren/cronometer-api-mcp and
  https://github.com/mhyounis19/cronometer-api-mcp (MCP servers, not
  libraries, but document the protocol).
- **Two API generations in use simultaneously**: `POST /api/v2/*` with
  JSON-body auth for almost everything (food search, diary read/write,
  nutrition totals, macro targets, fasting history/stats, biometrics), and
  `DELETE /api/v3/user/{id}/*` with header-based auth (`x-crono-session`)
  specifically for diary-entry deletion.
- Positioned by its own authors as **more stable** than the GWT approach —
  "clean payloads and stable, versioned endpoints" — but exact endpoint
  paths/payloads beyond the tool-level descriptions above weren't captured
  in this doc; re-fetch those two repos' source (not just READMEs) if this
  path is chosen, to get concrete request/response shapes before writing
  any Go code against it.
- **Cost of choosing this path**: no Go client exists at all — everything
  would be written from scratch against the reverse-engineered protocol
  notes above, a materially bigger lift than wrapping `gocronometer`.

### Recommendation (not yet acted on)

Start with **Option A (`gocronometer`)** — it's what `ARCHITECTURE.md`
already commits to, it's Go-native (matches "everything else in the sync
job is already Go" reasoning in ARCHITECTURE.md §5), and its five exports
already map 1:1 to the five existing `cronometer_*` tables. Only reach for
Option B if A proves too brittle in real use. This has NOT been decided
with the user beyond a lean — confirm before writing code if picking up
this thread fresh, in case their preference has changed.

## What actually blocks starting: real credentials

Every other data-source integration in this codebase (Google Health) was
built by first capturing real API responses and only then writing structs
against them — see `internal/googlehealth/values.go`'s CONFIRMED/
DOCUMENTED/INFERRED convention and `dump.go`'s `DumpToday` diagnostic tool.
The exact same discipline applies here: **do not guess Cronometer field
names**. A wrong guess doesn't error, it silently decodes to a zero value
(this bit the Google Health work once already — see `values.go`'s comment
on `HeartRate`/`HeartRateVariability`).

This means the very first step needs the user's actual Cronometer
username/password to run against their real account. If picking this up
in a fresh session: **ask the user for Cronometer credentials (or ask them
to run a diagnostic dump themselves and share the output) before writing
any struct definitions** — same as how the Google Health work required
`healthd auth google` before `dump.go` could capture anything real.

## Ordered task list

1. **Add the dependency**: `go get github.com/jrmycanady/gocronometer` (or
   pin a version — check its module path/latest tag on pkg.go.dev first).
2. **Diagnostic dump tool** (mirrors `internal/googlehealth/dump.go` +
   `internal/cli/googlehealthsync.go`'s `--dump-today` flag pattern): a
   throwaway or `--dump` mode that logs in and calls all five
   `Export<Type>` methods for a real recent date range, writing raw output
   to disk. Run it against the user's real account. **This is the step
   that needs credentials.**
3. **Write typed structs** in a new `internal/cronometer/values.go`,
   modeled on the real dump output — follow `internal/googlehealth/
   values.go`'s CONFIRMED/DOCUMENTED/INFERRED confidence-tier commenting
   convention exactly, since some fields may still need inference by
   symmetry even with real samples in hand (e.g. if a field is present but
   always empty/zero in the sample data).
4. **Auth + client** in `internal/cronometer/client.go`, wrapping
   `gocronometer.Client` — encrypt-at-rest the username/password (or the
   session cookie it caches) the same way `internal/googleauth` handles
   the OAuth token file, via `internal/crypto`. Do NOT store Cronometer
   credentials in plaintext `config.yaml` long-term even though that's
   where they currently sit scaffolded — same posture as the Google OAuth
   client secret.
5. **Sync engine wiring** in `internal/cronometer/sync.go`: a
   `DBSyncer`-equivalent implementing `SyncDay(ctx context.Context, day
   time.Time) (bool, error)` — the exact shape
   `internal/syncengine.DaySyncer` expects (see
   `internal/googlehealth/sync.go`'s `DBSyncer.SyncDay` for the reference
   shape, including the `hasData` aggregation pattern and
   `sync_state`-friendly day-boundary handling via `dayKey`/`dayBounds`-
   equivalent helpers). One upsert function per table, mirroring
   `internal/googlehealth/sync_upsert.go`'s
   `INSERT ... ON CONFLICT DO UPDATE` + `COALESCE(excluded.x, table.x)`
   pattern (sync is the sole writer of these columns, so overwrite-on-fetch
   is correct — no raw/override split needed here, these tables have none).
6. **Wire into `internal/cli/sync.go`**: replace the `TODO` println with a
   real call, matching how `runGoogleHealthSyncOnce` is invoked just above
   it in the same function.
7. **Scheduler wiring**: check wherever the internal cron scheduler
   (`robfig/cron`, per ARCHITECTURE.md §3) triggers the Google Health pass
   and add the Cronometer pass alongside it, sharing the same
   `sync_interval_minutes` config value unless a separate interval is
   wanted.
8. **UI surfacing** (only after 1-7 land and real data is flowing): the
   dashboard currently has zero Cronometer-sourced tiles. Once
   `cronometer_daily_nutrition` has real rows, decide what's worth
   surfacing (energy/macros already partially represented via
   `v_daily_overview`'s `nutrition_*` columns, unused in the UI so far) —
   follow the existing `metricDefs`/`buildStatTile` pattern in
   `internal/web/data.go`, same as every other stat tile.

## Things NOT to do

- Don't guess `gocronometer`'s struct field names from memory/training
  data — its module has almost certainly evolved; check the actual
  installed version's source under `~/go/pkg/mod/github.com/jrmycanady/
  gocronometer@<version>/` once `go get` has run, the same way this
  session verified Datastar's real vendored version instead of assuming.
- Don't skip the diagnostic-dump step even if `gocronometer`'s own example
  code looks self-explanatory — the whole point of the CONFIRMED/
  DOCUMENTED/INFERRED discipline elsewhere in this codebase is that
  "looks right" and "verified against a real response" are different
  claims.
- Don't store the Cronometer password in plaintext anywhere it persists
  (logs, `config.yaml` long-term, etc.) — encrypt it the same way the
  Google OAuth token is handled.
