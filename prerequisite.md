# Prerequisite knowledge for working on this repo

Findings accumulated across working sessions on this project (started
2026-07-31, most recently updated 2026-08-02), so a future agent/session
doesn't have to re-derive them. Read `ARCHITECTURE.md` first — this doc is
the practical addendum: environment gotchas, where the real dev data lives,
and specific issues already found (some fixed, some deliberately left
alone). If the task at hand is specifically about Cronometer, read
`cronometer-integration.md` instead/first — it now describes the finished
integration (client, auth, sync, UI, known gaps), not a handoff/TODO list.

**2026-08-02: work moved to a Mac.** Everything under "Environment gotchas
on this machine" below (the `C:\Users\meror\...` paths, PowerShell/`csc.exe`
SQLite workaround, etc.) describes the *original Windows* machine this
project started on — it's kept for whoever's back on that machine, but does
not apply here. The Makefile is OS-aware now (see its own `ifeq
($(OS),Windows_NT)` block) and was fixed this session for a genuine cross-
platform gotcha worth recording: **the macOS-bundled GNU Make (3.81, Apple's
last-GPLv2 build, frozen since ~2006) mis-parses a TAB-indented line inside
an `ifeq`/`else` conditional block when a `.PHONY:` line appears earlier in
the file** — it silently swallows the variable assignment instead of erroring,
so `$(BINARY)` resolved to empty and the build command ran against no output
path at all, no error printed. Fix: indent conditional bodies in the
Makefile with spaces, never tabs (tabs are only meaningful for recipe
lines). If a `make` target on macOS is silently doing the wrong thing with
no error, check for this before assuming the code itself is broken.

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
- **The `bin/explore-root` warning from the original Windows sessions does
  not apply here.** That was a real, actually-configured `--root` on the
  *other* machine with real synced data in it. This Mac's `bin/` is
  gitignored and this was the first working session on this machine — there
  is no real persisted data anywhere on it. As of 2026-08-02 the database is
  under active multi-user refactoring (see "Multi-user accounts" below):
  treat any scratch root under `bin/` on this machine as fully disposable —
  freely `rm -rf` and re-`db init` it between changes, no need to preserve
  anything. If real data ever does accumulate on this machine (a real
  account synced against a real Google/Cronometer login), revisit this note
  before treating scratch roots as disposable again.
- **`go build ./...` does NOT rebuild the `healthd` binary.** It only
  typechecks/compiles every package to verify correctness — it produces no
  output file. If you edit code, run `templ generate` (if `.templ` files
  changed) and then explicitly `go build -o bin/healthd ./cmd/healthd`
  (or `make build`, which now picks `bin/healthd.exe` vs `bin/healthd`
  automatically depending on host OS) before restarting the server to test
  the change, every time — running the old binary after "successfully"
  building looks identical (no error) but silently serves stale behavior.
  Bit this exact project more than once already.
- **`term.ReadPassword` fails over this harness's non-interactive shell tool**
  (`healthd auth cronometer`, `healthd user create`, `cmd/cronodump`,
  `cmd/cronoverify` all use it) — piped/non-TTY stdin errors with "the
  handle is invalid" on Windows. Any command that needs a real password
  prompt has to be run by the human in their own interactive terminal, not
  by an agent through this tool. For smoke-testing plumbing *around* such a
  command without a real password, seed a scratch DB directly instead (a
  small throwaway Go program calling `webauth.CreateUser` +
  `cronometer.SaveCredentials` with fake credentials — see the MCP connector
  work, 2026-08-05).
- **Piping a fixed batch of newline-delimited JSON-RPC requests into an MCP
  stdio server (`healthd mcp`) and closing stdin immediately (`cat file |
  program`) can race the server's write of the last response(s) before it
  sees EOF and exits** — the earlier requests' responses may never reach
  stdout. Hold stdin open a beat longer (`{ cat file; sleep 3; } | program`)
  when smoke-testing a stdio MCP server this way.
- **Real incident, 2026-08-05: running `healthd serve` and a `healthd mcp`
  connector concurrently against the same `--root` destroyed a real
  account's data**, via a bug now fixed in `internal/db/store.go`'s
  `removeWorkingFile` (see `ARCHITECTURE.md`'s MCP connector §11 for the
  full account and the regression test that catches it). The practical
  takeaway if this ever resurfaces on some other Windows path: a `healthd
  mcp` subprocess exiting while `healthd serve` is still running against
  the same root used to zero the *shared* working DB file out from under
  the still-running server (Windows allows a concurrent write even when it
  blocks the delete that follows) — and the running server's own next
  periodic checkpoint then persisted those zeros into the encrypted backup
  too, with no recovery path. If `healthd serve` (or anything) suddenly
  errors with `file is not a database (26)` after a `healthd mcp` process
  exited nearby, that's this class of bug — check `removeWorkingFile`
  hasn't regressed before assuming it's something else.
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
  real deployed data to preserve at the time. The 2026-08-02 multi-user
  rewrite (see "Multi-user accounts" below) used the same
  no-real-data-yet/no-migration precedent a second time. Point for future
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

## Web dashboard auth/UI session (2026-08-02, same day as the multi-user rewrite below)

A follow-up session picked up the freshly-built multi-user login/onboarding
work and got it actually working end to end, then did a UI pass. Findings
worth not re-discovering:

- **A cached-`nil` map entry is not "uncached."** `web.Server`'s
  `googleSyncers map[int64]*googlehealth.DBSyncer` treats *any* present key —
  including one explicitly set to `nil` — as "already checked this user,"
  distinguished from "never checked" via the map's own `ok` return (so a
  user who never connects Google isn't re-probed the filesystem on every
  request). `handleOnboardingConnectGoogle` used to call
  `s.setGoogleSyncer(userID, nil)` right after saving a freshly-obtained
  token, with a comment claiming this "forces a rebuild on next use" — it
  does the opposite: it writes the literal "not connected" sentinel into
  the cache, permanently (until a process restart) reporting "Google Health
  isn't authorized yet" even though the token was just saved successfully.
  Fixed by building the real syncer immediately (`s.buildGoogleSyncer(userID)`)
  and caching *that* — see `ARCHITECTURE.md` §10 and `account.go`'s
  `handleAccountDelete` for the *other* correct pattern (a bare `delete()`
  off the map, when you actually do want "not connected" to be the honest
  answer, e.g. after the account itself is gone).
- **Two credential forms on one origin can get their browser-saved
  passwords swapped if their fields look the same to the browser.** The
  onboarding page's Cronometer connect form used `name="username"`/
  `name="password"` + `autocomplete="username"`/`"current-password"` —
  identical field identity to the actual dashboard login form on the same
  origin. Chrome/Brave/etc. save/link credentials by exactly those signals,
  so connecting Cronometer got the browser to autofill *those* credentials
  into the next `/login` visit, silently replacing what the user typed.
  Fixed by renaming the fields (`crono_username`/`crono_password`, updated
  in the handler too) and setting `autocomplete="off"` on both this form and
  the dashboard's ongoing Cronometer account card. General rule: never reuse
  `username`/`password`-shaped `name`+`autocomplete` pairs for a credential
  that isn't this site's own login, even on a page that has no visible
  connection to the login form.
- **`kill %1` doesn't work across separate Bash tool calls.** Each Bash
  invocation in this harness is a fresh shell with no job-control table, so
  a `&`-backgrounded test server started in one call can't be reaped by
  `%1` in a later call — it silently fails (redirect stderr and you'll miss
  it). A subsequent `rm -rf` of that server's `--root` while it's still
  running doesn't stop it either; the process keeps the deleted directory's
  inode open and any request that touches a since-deleted file (e.g. the
  uploaded Google OAuth client) fails confusingly. Always capture the exact
  PID (`pgrep -f "healthd --root <path>"`) and `kill` that, and verify with
  `ps`/`lsof` both before starting a scratch server (in case something's
  already bound to the port) and after killing it.
- **`schema.sql`'s `ON DELETE CASCADE` cannot be relied on from application
  code** — see `ARCHITECTURE.md` §10's "Account deletion" paragraph.
  SQLite's foreign-key enforcement is a per-connection `PRAGMA`; Go's pooled
  `*sql.DB` doesn't guarantee any given statement runs on a connection that
  ever had it turned on. `webauth.DeleteUser` deletes every table
  explicitly instead.
- The `Creds/`/Google-OAuth-client-upload confusion from earlier this same
  day (see `CLAUDE.md`'s "Google OAuth client setup") was a real dead end
  someone could hit again: nothing in the app reads a `Creds/`/`Certs/`
  folder automatically, ever — the client JSON must be uploaded once per
  `--root` via `/settings/google-client` or `healthd google-client set`.
- The dashboard's category colors (`--cat-*` in `style.css`) and the
  light-mode variant (`:root[data-theme="light"]`) are both validated with
  the `dataviz` skill's `scripts/validate_palette.js`, run separately per
  mode against that mode's own surface color — don't eyeball a new
  category color or assume a value that passes for dark also passes for
  light; re-run the validator (see `style.css`'s own comments for the exact
  checks each palette needed to pass).

## Multi-user accounts (2026-08-02)

`healthd` went from single-tenant (one implicit owner, no login) to a real
multi-account system in this session — see `ARCHITECTURE.md` for the design
writeup. The load-bearing points a future change needs to keep straight:

- **Sync is deliberately decoupled from login.** The background scheduler
  (`internal/cli/service.go`'s `runForeground`, and `healthd sync`) fans out
  one short-lived goroutine per user per source per run, driven purely by
  whether that user has a saved provider credential file on disk — not by
  whether anyone is logged into the dashboard. Never make the scheduler
  check `web_session` or gate on an active session; that would break the
  "runs unattended" model the whole app is built around.
- **Two independent encryption layers, same primitive.** The root
  `keys/db.key` (Argon2id-derived from the `db init` passphrase) still
  encrypts the whole SQLite file, unchanged. Each user *additionally* gets
  their own `keys/users/<id>.key`, derived the same way
  (`internal/crypto.GenerateAndSaveKey`/`LoadKey`, unchanged) but from that
  user's own account password — used only to encrypt that user's Google
  OAuth token / Cronometer credentials under `config/users/<id>/`. Both
  keys are cached to disk (not memory-only) for the same reason the
  original DB key is: unattended sync needs to read them without a human
  re-entering a password. This is the same trust boundary the DB key
  already had, not a stronger one — see `internal/webauth/users.go`'s
  `CreateUser` doc comment.
- **Every data table is scoped by `user_id`**, folded into its primary key
  (see `schema.sql`'s own header comment and its multi-user note). Any new
  query against `watch_*`/`cronometer_*`/`body_measurement`/
  `daily_journal`/`daily_tag`/`sync_state` needs a `user_id = ?` filter
  bound from `webauth.CurrentUserID(c)` (web) or the sync job's own
  per-user loop variable (scheduler) — never from a request/query
  parameter, and never omitted. `internal/web`'s query functions all take
  `userID int64` as an explicit parameter for exactly this reason.
- **Login sessions are separate from provider credentials.** `web_session`
  (dashboard login, 24h sliding inactivity window, cookie holds the raw
  token, only its SHA-256 is stored) has nothing to do with
  `keys/users/<id>.key` (provider credential encryption). Losing/expiring a
  session never affects sync; disconnecting a provider never affects
  login.
- CLI provider-auth commands (`healthd auth google`/`cronometer`) now
  require `--user <username>` — an account must already exist (via the
  dashboard's `/signup` or `healthd user create <username>`) before either
  command has anywhere to attach credentials to.
- `bin/seed/main.go` (throwaway dev seeder, not part of the product) now
  creates/reuses a named user (`-user`/`-password` flags, default
  `demo`/`demopassword`) and seeds all rows under that user's id.
- **The Google OAuth *client* JSON is a third, separate secret from
  everything above** (added 2026-08-02) — not per-user, not per-session:
  one Google Cloud OAuth client serves every account's own Google login.
  It's uploaded via `/settings/google-client` (or `healthd google-client
  set <path>`), validated, and encrypted with the *root* DB key
  (`config/google_oauth_client.json.enc` — see
  `internal/googleauth.SaveClientJSON`), never a per-user key. config.yaml
  no longer has a `google.credentials_file` field at all. Don't confuse
  this with a user's own OAuth *token* (`config/users/<id>/
  google_oauth.json.enc`, per-user key) — see `ARCHITECTURE.md` §5.

## What's built as of 2026-08-01

The activities list (calories/heart rate), the heart-rate/sleep/steps
expanded detail views (intraday line, stage timeline, hourly bar), body
measurements (form + carry-forward), and the old "growing water glass"
step-goal tile (replaced by a progress ring next to the steps tile's big
value) are all done — see `structure.md`/`ARCHITECTURE.md` for what exists
now rather than re-deriving it from an old gap list. Cronometer is fully
integrated (`cronometer-integration.md`). The dashboard itself has had a UI
pass since: a native date picker on the day label plus a "Today" button
(only rendered when the viewed day isn't today), charts capped to a
sane on-screen size and given a consistent 24h/thin-line treatment, a tile
with genuinely no data for the day excluded from the grid but its title
still surfaced via one compact per-section `EmptySummaryTile` rather than
either a full placeholder or silently vanishing (`shouldHideEmptyTile` +
`MissingByCategory` in `handlers.go` — Body Measurements is exempt, it's an
input form not a stat), a handful of tiles (Steps/Heart rate/Body/Sleep)
defaulting to expanded on load (`defaultExpandedKind`), and the Nutrition
section laid out as two explicit rows (kcal metrics, then macros, each only
rendered when non-empty) rather than left to the general grid's auto-fill.
As of 2026-08-02: full multi-user login/signup/onboarding/settings/account
pages (see "Multi-user accounts" below and `ARCHITECTURE.md` §10), a
light/dark theme toggle, and a consolidated header user-menu dropdown
replacing three separate inline controls — see the "Web dashboard auth/UI
session" findings above for the specific bugs this surfaced and fixed.

## OS-service lifecycle (`--action`), implemented 2026-08-05

`--action=install/start/stop/uninstall` (root command and `serve`, see
`internal/cli/service.go`/`serve.go`) is real now, via
`github.com/kardianos/service` — no longer the printed-TODO stub earlier
docs described. A few things worth knowing before touching this code again:

- **Installing a service needs elevated privileges.** On Windows, a
  non-Administrator shell gets a clean `Access is denied` error from
  `service.Control` (confirmed live on this machine) — not a bug, just the
  OS enforcing service-registry access. On Linux, the equivalent is
  root/an account that can write systemd or sysv units. This can't be
  worked around and shouldn't be "fixed" — it's the correct behavior to
  surface as-is with context, not swallow.
- **The re-invocation arguments matter.** `newHostedService`'s `Arguments`
  always bakes in an explicit `--root` (and `--service-name`, and for
  `serve`, `--google-client-secret` if one was given at install time) —
  the account a service runs under (a Windows service account, or
  root/systemd) won't share the interactive user's home directory, so
  relying on `--root`'s `~/.healthd` default would silently point the
  installed service at the wrong root.
- **`service.Interactive()` is the load-bearing check**, not something to
  remove as dead code: it's false only when the process was actually
  launched by the OS service manager (SCM on Windows, systemd/sysv-parent
  detection on Linux) rather than a terminal. That branch is what routes
  into `runHostedScheduler`/`runHostedServe` (kardianos's own `Run()`,
  which on Windows performs the service-control-dispatcher handshake a
  hosted service must complete within seconds of starting, or the SCM
  kills it as unresponsive) instead of the plain signal-driven
  `runForeground`/`runServeForeground` used for everyday interactive runs.
  Getting this branch wrong (e.g. always going one way) either breaks
  normal terminal use or breaks the installed service on Windows
  specifically — it won't show up as a failure on Linux, since a plain
  blocking process happens to satisfy systemd's expectations too.

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
exercise a change: `templ generate` → `go build -o bin/healthd ./cmd/healthd`
(or `bin/healthd.exe` on Windows) → restart whatever's running that binary.

For live UI verification, create an isolated scratch root (any root under
`bin/` on this machine is freely disposable, see above):

```bash
rm -rf bin/dev-root
HEALTHD_DB_PASSPHRASE=throwaway ./bin/healthd --root bin/dev-root db init
go run ./bin/seed --root bin/dev-root        # dummy 7-day data for a "demo" user
./bin/healthd --root bin/dev-root serve
```

`healthd serve` (see `ARCHITECTURE.md` §2) only runs the web dashboard, not
the sync scheduler — the dashboard's Sync button, `healthd sync`, or
`bin/seed` (see "Multi-user accounts" above) are the ways to get
real-shaped data into a scratch root without needing real Google/Cronometer
credentials. Visiting `/` with no session redirects to `/login`; use
`/signup` to create the first account (lands on `/onboarding/connect`
afterward, or "Continue to Dashboard" to skip both providers for now).
Default port is `8080` (`config.yaml`'s `port` key, under whatever root you
point `--root` at).
