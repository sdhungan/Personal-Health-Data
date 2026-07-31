# Prerequisite knowledge for working on this repo

Findings from a cold-start review of this project (2026-07-31), so a future
agent/session doesn't have to re-derive them. Read `ARCHITECTURE.md` first —
this doc is the practical addendum: environment gotchas, where the real dev
data lives, and specific issues already found (some fixed, some deliberately
left alone). If the task at hand is specifically about Cronometer, read
`cronometer-integration.md` instead/first — it's a dedicated handoff doc
with the reverse-engineered API research, current scaffolding state, and an
ordered task list already done, so that work doesn't need to be re-derived
either.

## Environment gotchas on this machine

- **Go is not on PATH by default.** The toolchain lives at
  `C:\Users\meror\sdk\go1.25.5\bin` (user-installed, not registered in
  `Path`). If a fresh shell can't find `go`, that's why — check
  `[Environment]::GetEnvironmentVariable('Path','User')` first before
  assuming Go isn't installed at all. `~/go/bin/templ.exe` (GOPATH bin) is a
  standalone binary and works even without `go` on PATH.
- **No `sqlite3` CLI, no `python3`, no `node`, no `.NET SDK`** are installed.
  `dotnet` is on PATH but is runtime-only (no SDK, can't compile C#).
  `PowerShell 5.1`'s `Add-Type` still works because it shells out to the
  legacy .NET Framework `csc.exe`, which ships with Windows regardless of an
  SDK — this is how a real SQLite file got queried in this session without
  Go: P/Invoke a handful of `sqlite3_*` functions out of a native
  `e_sqlite3.dll` (one happened to already be on disk under
  `C:\Program Files\Logi\LogiPluginService\e_sqlite3.dll` — it's the same
  ABI as upstream `sqlite3.dll`/the amalgamation, since it's a
  SQLitePCLRaw-bundled build). Useful fallback if Go is ever unavailable
  again and you need to read a `.db`/working file directly.
- **Never query `bin/explore-root/db/.health.db.work` (or its `-wal`/`-shm`)
  in place.** It's a real (if seeded/test-flavored) dev root the user points
  `--root` at locally; `internal/db.Store` assumes exclusive ownership of
  that file. Copy it to a scratch directory first, always. Check
  `Get-Process healthd` before touching it at all — if healthd is running,
  leave it alone.
- `bin/` (and therefore `bin/explore-root/`) is gitignored — nothing under it
  is tracked, so it's safe to treat as disposable/local, but it's also the
  only place real-shaped data exists to test against. `bin/google-health-dump/`
  is a one-time diagnostic dump (see `internal/googlehealth/dump.go`
  `DumpToday`) of what the real Google Health API returns per data type —
  most files are literally empty (`[]`, 3 bytes) because that data type had
  nothing to return on whatever day the dump was taken; the non-trivial ones
  (`exercise.list.json`, `sleep.list.json`, the `daily-*.list.json` files)
  are genuine API response shapes and match `internal/googlehealth/values.go`'s
  structs.

## Datastar gotcha: never combine `data-show` with `data-attr:style` on the same element

Confirmed by isolated reproduction (2026-07-31), not a guess: this project vendors
**Datastar v1.0.2** (`internal/web/static/datastar.js`'s own header comment —
the `datastar-go` Go module version, `v1.2.2` in `go.mod`, is a separate SDK
version number, not the JS runtime version). In this version, putting
**both** `data-show="$foo"` **and** `data-attr:style="..."` on the *same*
element causes an infinite reactive loop that hangs the page's JS thread
entirely (even a trivial `1+1` eval stops responding) — both directives end
up writing the `style` attribute, and something in how mutations are
observed re-triggers itself forever. Reproduced with a ~10-line standalone
HTML file outside this app to confirm it wasn't application-specific.

**Fix**: give each element exactly one thing driving its `style` attribute.
Fold the show/hide logic into the `data-attr:style` expression itself:
```html
<div data-attr:style="($foo ? '' : 'display:none;') + 'left:' + $x + '%'"></div>
```
rather than:
```html
<!-- DON'T: hangs the page -->
<div data-show="$foo" data-attr:style="'left:' + $x + '%'"></div>
```
See `internal/web/views/chart.go`'s `renderChartOverlay`/`hoverAttrs` for the
working pattern (used for the chart hover crosshair/tooltip). Plain
`data-show` alone (no `data-attr` on the same element) is fine and used
correctly elsewhere (e.g. the activity-detail overlay in `layout.templ`).

Also: **verify Datastar syntax against `data-star.dev/reference/attributes`
before writing it**, not from memory/training-data recall — it's a young,
fast-moving framework. E.g. `data-signals` multi-key init uses **unquoted**
object-literal keys (`data-signals="{foo: 1, bar: 2}"`), not
`JSON.Marshal`-style quoted keys — untested whether quoted keys also work
for a nested (non-root) `data-signals`, so don't assume they do.

## Architecture rules that matter when changing code (see `ARCHITECTURE.md`)

- **`raw_value`/`override_value` split**: the sync job is the *only* writer
  of `_raw` columns; UI edit endpoints are the *only* writer of `_override`
  columns. Never have UI code write a `_raw` column, even for convenience.
  `body_measurement.weight_kg_override`/`height_cm_override` follow this;
  `waist_cm`/`neck_cm`/`hip_cm` have no upstream source at all (Google Health
  and Cronometer don't expose circumference), so those are plain
  user-entered columns with no raw/override split — write them directly.
- **`internal/googlehealth` (the sync engine) was originally treated as
  out-of-scope-unless-asked** — but a later session (2026-07-31, same day)
  *did* deliberately reopen it after an explicit data-completeness audit and
  user sign-off, wiring up floors/altitude/sedentary-period/
  active-energy-burned/per-level-active-minutes/vo2max-samples/heart-rate
  zone definitions+minutes/calories-by-zone/sleep respiratory
  rate/sleep temperature/blood-glucose/core-body-temperature, plus a schema
  migration mechanism (`internal/db/migrations.go` — this project's first;
  `Store.migrate()` applies pending entries via `PRAGMA user_version` on
  every `Open`). Point: "out of scope" here meant "don't touch incidentally
  while fixing something else," not "never" — confirm with the user before
  reopening it for real, same as happened here, rather than assuming the
  earlier note still blocks it.
- **Nutrition/food data is deliberately never pulled from Google Health** —
  Cronometer is meant to own that domain — but **Cronometer sync doesn't
  exist in this codebase at all**: no `internal/cronometer` package, no
  `gocronometer` dependency in `go.mod`, just schema tables
  (`cronometer_*`) and a `// TODO: run the Cronometer sync pass too` stub in
  `internal/cli/sync.go`. `cronometer_daily_nutrition.kcal_burned_cronometer`
  exists in the schema (added 2026-07-31, to disambiguate from
  `watch_daily_summary.kcal_burned_google`) but will sit NULL forever until
  that sync client is actually built — a separate, much larger task than
  anything wired up so far (new external API integration, credential
  handling), not attempted.
- **Google Health has no location/GPS/route data type** — checked directly
  against the RPC reference
  (`developers.google.com/health/reference/rpc/google.devicesandservices.health.v4`),
  confirmed absent. There IS a separate, confirmed-real
  `dataPoints.exportExerciseTcx` REST method (TCX format carries GPS track
  points when a device recorded them) that could expose per-exercise routes
  — a genuinely different mechanism from the `dataTypes/{id}` list/rollup
  API everything else in `internal/googlehealth` uses, undocumented in
  detail on the REST reference index page, and NOT implemented — flagged as
  a possible future enhancement, not attempted (would need its own schema
  for route points and TCX/XML parsing).
- `watch_heart_rate_intraday` is a **rolling cache**, not permanent history —
  its own schema comment says so, and it's true in practice (confirmed only
  ~3 days of samples present in the live dev DB). Any UI that reads it must
  handle "no data because it aged out of the cache" as a distinct, clearly
  labeled state — don't conflate that with "the watch recorded nothing."
- Every DB read/write in `internal/web` goes through plain `database/sql`
  against `*sql.DB` (no ORM); the pattern for a new feature needing DB access
  is a small package-level file like `journal.go` (fetch + save functions)
  plus a struct in `views/models.go`, not a new abstraction layer.
- `.templ` files are compiled to `_templ.go` by `templ generate` (Makefile's
  `generate` target) — never hand-edit a `*_templ.go` file, it says so at
  the top and will be silently overwritten.

## Specific findings from this session (2026-07-31 review)

Verified live against `bin/explore-root`'s dev DB:

- The activities/workout list only ever rendered exercise type + start time +
  duration (`views/dashboard.templ`'s `activitiesTileBody`), never calories
  or heart rate — even though the query behind it
  (`buildActivitiesTile` in `internal/web/data.go`) already selects
  `calories_burned` and `avg_heart_rate_bpm` into `ActivitySummary`. Confirmed
  on the two real 2026-07-30 BIKING sessions (70 kcal/106 bpm and 113 kcal/104
  bpm, both correctly stored) — a template gap, not a data gap.
- Expanding *any* stat tile (`buildStatTile`/`buildChart` in `data.go`) always
  built a 7-day bar chart of one value per day — including Heart Rate, where
  a user tapping to expand reasonably expects the *selected day's* intraday
  trace, not a week of daily averages. The DB already had ~5,900
  `watch_heart_rate_intraday` samples for a single day; nothing in
  `internal/web` queried that table except the activity-detail overlay.
  Likewise `watch_steps_hourly` (hourly steps) and `watch_sleep_stage`
  (full sleep-stage timeline, confirmed present and populated from the
  Google Health `sleep.list.json` shape) were stored but never rendered
  anywhere.
- `body_measurement` (weight/waist/neck/hip) has zero rows in the live dev
  DB and there was no route, handler, or template anywhere that reads or
  writes it — despite the schema being fully built out for exactly this
  (see its comment in `schema.sql`). This was a pure gap: nothing to fix,
  a feature to add.
- The literal "growing water glass" step-goal visual is
  `views.goalGlassSVG` + `views.buildGoalTile` (`TileKindGoal`), driven by a
  separate "Daily Goal" tile in `DefaultTileKinds`
  (`internal/web/handlers.go`).
- **Still-open, deliberately-not-fixed gap** (as of 2026-07-31, after the
  data-completeness pass below): `watch_steps_hourly.distance_m` and
  `.calories` columns exist in `schema.sql` but `syncStepsHourly`
  (`internal/googlehealth/sync_upsert.go`) only ever populates `steps` —
  confirmed both columns are NULL for every row in the live DB. Any hourly
  distance/calories view is not currently buildable from real data; only
  hourly *steps* has actual data behind it. (Floors used to be listed here
  too as a "no confirmed fetch" gap — that's now fixed, see below; this
  specific hourly-distance/calories one is still open.)
- `floors_climbed` **is now synced** (as of 2026-07-31) via `floors`'
  `dailyRollUp` — its `list()` path is rejected outright by the API
  (`NoList: true` in `datatypes.go`), and this account's own rollup response
  has always come back empty, so `FloorsRollup`'s field shape
  (`internal/googlehealth/values.go`) is an unconfirmed best-effort guess —
  safe if wrong (`ExtractRollupValues` just finds no points, doesn't
  silently decode the wrong field), but verify against a real non-empty
  response if you ever get one.

## How to run/test locally

```bash
make generate   # templ generate
make build      # go build -o bin/healthd ./cmd/healthd
make run        # go run ./cmd/healthd (foreground, needs --root or ~/.healthd)
```

To test against realistic data without touching the user's live dev root:
copy `bin/explore-root/` to a scratch directory, then run against the copy
(`healthd --root <copy-path>` or `healthd serve` under that root) — never
run directly against `bin/explore-root/db/.health.db.work` while it might
also be in use elsewhere. Default port is `8080`
(`bin/explore-root/config/config.yaml`).
