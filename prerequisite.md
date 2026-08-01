# Prerequisite knowledge for working on this repo

Findings accumulated across working sessions on this project (started
2026-07-31, most recently updated 2026-08-01), so a future agent/session
doesn't have to re-derive them. Read `ARCHITECTURE.md` first — this doc is
the practical addendum: environment gotchas, where the real dev data lives,
and specific issues already found (some fixed, some deliberately left
alone). If the task at hand is specifically about Cronometer, read
`cronometer-integration.md` instead/first — it now describes the finished
integration (client, auth, sync, UI, known gaps), not a handoff/TODO list.

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
- **Never seed, wipe, or `db init` `bin/explore-root` for testing — ever.**
  It's the user's real, actually-configured `--root`, not a throwaway
  fixture: real Google Health/Cronometer syncs land in it independently of
  whatever session is running (confirmed the hard way — a repeated
  wipe/seed/verify/wipe test cycle against it during one session
  discovered a genuinely real, independently-timestamped nutrition sync
  row mixed into the "clean" test data afterward, meaning real data had
  likely been getting swept into `old-schema-backup-*` folders and
  discarded across several earlier "clean" cycles without being caught
  each time). For any live-verification need, create and use an isolated
  scratch root instead — e.g. `healthd db init --root <scratchpad-path>`
  with a throwaway `HEALTHD_DB_PASSPHRASE` — and never touch
  `bin/explore-root` unless the task itself is explicitly about
  `bin/explore-root` (the user directly asking to reinitialize or inspect
  it is a deliberate action, not test churn). Separately, never query
  `bin/explore-root/db/.health.db.work` (or its `-wal`/`-shm`) directly
  even for a real, requested action — `internal/db.Store` assumes exclusive
  ownership of that file, so check `Get-Process healthd` first and stop the
  process (or copy the file aside) before touching it.
- **`go build ./...` does NOT rebuild `bin/healthd.exe`.** It only
  typechecks/compiles every package to verify correctness — it produces no
  output file. If you edit code, run `templ generate` (if `.templ` files
  changed) and then explicitly `go build -o bin/healthd.exe ./cmd/healthd`
  (or `make build`) before restarting the server to test the change, every
  time — running the old binary after "successfully" building looks
  identical (no error) but silently serves stale behavior. Bit this exact
  project more than once already.
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
correctly elsewhere (e.g. the shared `#detail-overlay` in `layout.templ`,
behind both the activities list and the food log).

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
- **`internal/googlehealth`'s `watch_*` schema/structs/sync were rewritten
  wholesale** (not incrementally patched) around the "five fixed
  representation shapes" taxonomy — see `ARCHITECTURE.md`. This happened
  after a real bug (calories-by-heart-rate-zone sync failing) triggered a
  full audit of which Google Health data types support `list()` vs
  `rollup()`/`dailyRollUp()`, and a deliberate design pass on which
  representation (raw timeline, hourly, daily scalar, category breakdown,
  segment) is actually *useful* per metric rather than just technically
  fetchable. `internal/db/migrations.go`'s mechanism exists but is
  currently **empty** (see that file's own comment) — this rewrite went
  straight into `schema.sql` rather than a migration, since there was no
  real deployed data to preserve at the time. Point for future
  schema-touching work: confirm with the user whether the *next* change
  needs a real migration (once there's real data on disk worth preserving)
  rather than assuming another wholesale rewrite is fine.
- **Cronometer sync fully exists now** (`internal/cronometer`, see
  `cronometer-integration.md`) — nutrition/food data is still deliberately
  never pulled from Google Health, and Cronometer is the sole writer of
  every `cronometer_*` table. Built against Cronometer's mobile REST API
  (`mobile.cronometer.com/api/v2/*`), **not** the `gocronometer`/GWT-RPC
  library `ARCHITECTURE.md` originally named as the plan — that plan
  changed once real captured data confirmed the mobile API's shapes work
  and are cleaner to work with directly. `cronometer_daily_nutrition.kcal_burned_cronometer`
  is populated (Cronometer's own BMR+activity+food-thermic-effect
  expenditure estimate) and surfaced on the dashboard as an "Expenditure"
  tile alongside "Energy" (consumption) and a computed "Deficit" tile.
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
  is a small package-level file like `journal.go`/`bodymeasurement.go`/
  `foodlog.go` (fetch + save functions) plus a struct in `views/models.go`,
  not a new abstraction layer.
- `.templ` files are compiled to `_templ.go` by `templ generate` (Makefile's
  `generate` target) — never hand-edit a `*_templ.go` file, it says so at
  the top and will be silently overwritten. And remember `templ generate`
  alone doesn't rebuild the binary either — see the `go build ./...` gotcha
  above.

## What's built as of 2026-08-01

The activities list (calories/heart rate), the heart-rate/sleep/steps
expanded detail views (intraday line, stage timeline, hourly bar), body
measurements (form + carry-forward), and the old "growing water glass"
step-goal tile (replaced by a progress ring next to the steps tile's big
value) are all done — see `structure.md`/`ARCHITECTURE.md` for what exists
now rather than re-deriving it from an old gap list. Cronometer is fully
integrated (`cronometer-integration.md`). The dashboard itself has had a UI
pass since: a native date picker on the day label, charts capped to a
sane on-screen size and given a consistent 24h/thin-line treatment, empty
tiles (genuinely no data for the day) omitted from the page instead of
showing a placeholder (`shouldHideEmptyTile` in `handlers.go` — Body
Measurements is exempt, it's an input form not a stat), a handful of tiles
(Steps/Heart rate/Body/Sleep) defaulting to expanded on load
(`defaultExpandedKind`), and the Nutrition section laid out as two explicit
rows (kcal metrics, then macros) rather than left to the general grid's
auto-fill.

## Still-open, deliberately-not-fixed gaps

- `watch_steps_hourly.distance_m`/`.calories` columns exist in `schema.sql`
  and the upsert SQL (`upsertHourlySteps` in
  `internal/googlehealth/sync_upsert.go`) writes them, but the fetch side
  (`internal/googlehealth/sync_details.go`, the hourly-steps sync function)
  only ever populates `healthdata.HourlySteps.Steps` — `DistanceM`/`Calories`
  are never set, so those two columns stay NULL for every row. Any hourly
  distance/calories view is not currently buildable from real data; only
  hourly *steps* has actual data behind it.
- `FloorsRollup`'s field shape (`internal/googlehealth/values.go`) is still
  an unconfirmed best-effort guess by symmetry with `TotalCaloriesRollup` —
  this account's own floors rollup response has always come back empty, so
  there's never been a real non-empty response to verify the shape against.
  Safe if wrong (`ExtractRollupValues` just finds no points rather than
  silently decoding the wrong field), but verify against a real response if
  you ever get one.
- Three Cronometer fields are structurally present but never populated,
  confirmed against real account data, not guessed: `cronometer_serving.category`
  (Cronometer's `food.Category` is a numeric ID with no confirmed name
  catalog captured yet — left NULL rather than storing a meaningless
  number), `cronometer_serving.meal_group` (never observed as a usable
  field on a diary entry via this API), and the entire `cronometer_note`
  table (no `"Note"` diary entry type was observed in the real dump this
  was built against — whether Cronometer's notes feature is reachable via
  this API at all is unconfirmed, not just unpopulated by omission).
- **`healthd serve` and the bare `healthd` scheduler are still two separate
  processes** — see `ARCHITECTURE.md` §2. There's no single "just run
  everything" command yet; running only `serve` means auto-sync never
  happens, only the dashboard's manual Sync button does.

## How to run/test locally

```bash
make generate        # templ generate
make build           # go build -o bin/healthd ./cmd/healthd
make run             # go run ./cmd/healthd (foreground scheduler, needs --root or ~/.healthd)
make test / make vet # go test ./... / go vet ./...
```

`go build ./...` (no `-o`) is NOT a substitute for `make build` — see the
environment gotcha above, it typechecks but produces no binary. To actually
exercise a change: `templ generate` → `go build -o bin/healthd.exe
./cmd/healthd` → restart whatever's running that binary.

For live UI verification, create an isolated scratch root — never
`bin/explore-root`, see the environment gotcha above:

```bash
HEALTHD_DB_PASSPHRASE=throwaway ./bin/healthd.exe --root <scratch-path> db init
./bin/healthd.exe --root <scratch-path> serve
```

`healthd serve` (see `ARCHITECTURE.md` §2) only runs the web dashboard, not
the sync scheduler — the dashboard's Sync button, `healthd sync`, or a
small one-off Go program using `internal/db.Open` directly (see any of this
project's `cmd/`-style throwaway scripts) are the ways to get real-shaped
data into a scratch root without needing real Google/Cronometer
credentials. Default port is `8080` (`config.yaml`'s `port` key, under
whatever root you point `--root` at).
