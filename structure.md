# Repository structure

One Go module (`github.com/sdhungan/Personal-Health-Data`), one binary
(`healthd`), built with Cobra + Echo + templ + Datastar. See
`ARCHITECTURE.md` for the *why*; this doc is the *where*.

```
.
├── ARCHITECTURE.md              # design intent, read this first
├── structure.md                 # this file
├── prerequisite.md              # gotchas + findings from working sessions
├── cronometer-integration.md    # how the Cronometer integration specifically works
├── Makefile                     # generate / build / install / run / test / vet / fmt / clean
├── go.mod / go.sum
├── Creds/                       # gitignored — a convenient place to keep the downloaded
│                                 # client_secret_*.json before uploading it; NOT read
│                                 # automatically, see "Google OAuth client" below
├── bin/                         # gitignored — build output + local dev/test roots
│   ├── healthd.exe
│   ├── explore-root/            # the user's real --root directory: db/, config/, keys/, logs/, service/ —
│   │                            # NEVER seed/wipe this for testing, see prerequisite.md
│   └── google-health-dump/      # one-time diagnostic dump of raw Google Health API responses per data type
├── cmd/
│   ├── healthd/main.go     # package main — the only entrypoint, calls cli.Execute()
│   ├── cronodump/          # diagnostic dump tool for Cronometer's API (mirrors googlehealth's dump.go), kept as a real tool
│   └── tmpinspect/         # scratch DB-inspection tool (not part of the product; ad hoc dev use only)
└── internal/
    ├── paths/       # package paths   — resolves --root and every file/dir path under it
    ├── config/      # package config  — config.yaml (port, sync interval, Google creds; Cronometer creds live encrypted, not here)
    ├── crypto/      # package crypto  — AES-256-GCM + Argon2id, whole-file encryption at rest
    ├── db/          # package db      — schema.sql + embedded Schema string, Store (open/checkpoint/close encrypted SQLite)
    ├── healthdata/  # package healthdata — dependency-free domain structs mirroring watch_* tables, shared by googlehealth (writer) and web (reader)
    ├── googleauth/  # package googleauth — OAuth2 local-redirect flow, encrypted token storage, auto-refreshing http.Client
    ├── googlehealth/# package googlehealth — Google Health API client, data-type definitions, sync engine (DBSyncer), one-shot diagnostic dump
    ├── cronometer/  # package cronometer — mobile-API client, session/credential handling, sync engine (DBSyncer)
    ├── syncengine/  # package syncengine — source-agnostic day-completeness state machine (pending/partial/complete/missing), scoped per user (SQLStore.UserID)
    ├── webauth/     # package webauth  — dashboard accounts/sessions: CreateUser/Authenticate (bcrypt + per-user Argon2id credential key), CreateSession/LookupSession (24h sliding cookie session), Echo middleware
    ├── cli/         # package cli     — Cobra command tree (root/sync/auth/db/serve/user), shared *paths.Paths
    └── web/         # package web     — Echo server + dashboard (see below)
        └── views/   # package views  — templ components + their Go view-model structs; no DB access here
```

## Package responsibilities and how they connect

- **`cmd/healthd`** — trivial `main()`, delegates entirely to `internal/cli`.
- **`internal/webauth`** — dashboard login accounts, separate from provider
  auth (`internal/googleauth`/`internal/cronometer`): `users.go`
  (`CreateUser`/`Authenticate`, bcrypt for login verification plus an
  independently-derived per-user Argon2id key — see `internal/crypto` —
  that encrypts that user's own Google/Cronometer credential files;
  `DeleteUser` permanently removes an account — every row across
  `userDataTables` (one `DELETE ... WHERE user_id = ?` per table, explicit
  rather than relying on `schema.sql`'s `ON DELETE CASCADE`, since SQLite's
  foreign-key enforcement is a per-connection `PRAGMA` that Go's pooled
  `*sql.DB` can't guarantee is set — see `ARCHITECTURE.md` §10), then the
  `users` row, then that account's `keys/users/<id>.key` and
  `config/users/<id>/` from disk), `sessions.go`
  (`CreateSession`/`LookupSession`/`DeleteSession`/
  `CleanupExpired` against `web_session`, a 24h sliding-inactivity cookie
  session, token hashed before storage), `middleware.go` (Echo middleware
  gating every dashboard route, `CurrentUserID`/`CurrentUsername` for
  handlers to read). See `ARCHITECTURE.md` §10.
- **`internal/cli`** — Cobra command tree. `root.go` resolves `--root` once
  (`PersistentPreRunE`) into a shared `appPaths *paths.Paths` every
  subcommand file (`sync.go`, `auth.go`, `db.go`, `serve.go`,
  `googlehealthsync.go`, `cronometersync.go`, `service.go`) reads from.
  `googlehealthsync.go`/`cronometersync.go` each hold one
  `run<Source>SyncOnce(ctx)` helper, called from both `sync.go` (one-shot
  manual trigger) and `service.go`'s scheduler — the same function either
  way, not duplicated. `service.go` also handles the
  `--action=install/start/stop/uninstall` OS-service lifecycle for both the
  scheduler (root `--action`) and the web dashboard (`serve --action`,
  registered as a separate service) — both still stubbed, see their TODOs.
  **`serve` and the bare scheduler are two separate processes** — there's no
  single command that runs both together yet (see `ARCHITECTURE.md` §2).
- **`internal/paths`** — the *only* place that builds a path under `--root`
  (`DBFile()`, `ConfigFile()`, `KeysDir()`, etc.) — every other package asks
  this one rather than joining paths itself.
- **`internal/config`** — `Config` struct + `Load(path)`, overlaying
  `config.yaml` onto `Default()`. Read once at startup by `cli`.
- **`internal/crypto`** — `Key`, `Encrypt`/`Decrypt` (AES-256-GCM),
  `DeriveKey` (Argon2id, `kdf.go`), `LoadKey`/key-file handling
  (`keyfile.go`). Used by `internal/db.Store` (DB file) and
  `internal/googleauth` (OAuth token file) — the same primitive for both.
- **`internal/db`** — `Schema` (embedded from `schema.sql` — see
  `ARCHITECTURE.md`'s "five fixed representation shapes" section for how the
  `watch_*` tables are organized), `Store` (the encrypted-DB lifecycle:
  `Init`/`Open`/`Checkpoint`/`Close`/`Discard`, see `store.go`'s doc
  comments for the raw/working-file dance), `dump.go` (plaintext `.sql`
  export for `healthd db decrypt`), `migrations.go` (the mechanism —
  `Migrations` ordered list applied via `Store.migrate()`, tracked by
  `PRAGMA user_version` — but currently **empty**: this project's `watch_*`
  schema was rewritten wholesale rather than migrated, since there was no
  real deployed data to preserve at the time; see `migrations.go`'s own doc
  comment. Re-populate this list for the *next* real schema change, once a
  real user's data actually needs preserving across it). Every other
  package that touches the DB gets a `*sql.DB` from `Store.DB()` — nothing
  else opens a `sql.DB` directly.
- **`internal/googleauth`** — OAuth2 flow (`flow.go`, `oauth.go`,
  `browser.go` for opening the consent URL), `scopes.go` (the exact scopes
  requested — determines which `googlehealth.DataTypes` are reachable),
  `token.go` (encrypted refresh/access token persistence + auto-refreshing
  `http.Client`).
- **`internal/googlehealth`** — `client.go` (thin REST client for
  `health.googleapis.com/v4`), `datatypes.go` (`DataTypes`/`RollUpDataTypes`
  — the full catalog of data types healthd's scopes can reach, each tagged
  with a `kind`: interval/sample/session/dailyAggregate — this classification
  drives how each type's server-side time filter is built), `values.go`
  (Go structs mirroring each data type's raw JSON wire shape — CONFIRMED/
  DOCUMENTED/INFERRED confidence tiers, see its own doc comment), `envelope.go`/
  `wire.go` (response envelope decoding helpers), `sync.go`/`sync_details.go`/
  `sync_upsert.go` (`DBSyncer.SyncDay` — fetches one calendar day, builds
  `internal/healthdata` structs, and upserts into every `watch_*` table —
  one upsert function per table/shape in `sync_upsert.go`, one fetch
  function per metric in `sync_details.go`), `sync_filters.go` (per-kind
  time-range filter builders), `dump.go` (`DumpToday` — the one-shot "dump
  every data type as JSON" diagnostic that produced `bin/google-health-dump/`).
- **`internal/healthdata`** — plain Go structs (`DailySummary`,
  `HourlySteps`, `HeartRateSample`, `ActivityLevelSegment`,
  `SleepSession`/`SleepStage`, `ExerciseSession`, `ECGReading`, etc.)
  mirroring the `watch_*` tables' five representation shapes (see
  `ARCHITECTURE.md`). Deliberately has zero internal-module dependencies
  (verified via `go list -deps`) so both `internal/googlehealth` (writer)
  and `internal/web` (reader) can import it without an import cycle between
  them.
- **`internal/cronometer`** — the Cronometer integration, mirroring
  `internal/googlehealth`'s shape but for the mobile API:
  `client.go` (thin REST client for `mobile.cronometer.com/api/v2/*`),
  `session.go` (login + session-token caching/renewal), `values.go` (wire
  structs — same CONFIRMED/DOCUMENTED/INFERRED discipline), `nutrients.go`
  (Cronometer's numeric nutrient-ID → column mapping), `sync.go`/
  `sync_upsert.go` (`DBSyncer.SyncDay`, matching `googlehealth`'s shape so
  both sources implement the same `syncengine.DaySyncer` interface without
  either package importing `syncengine` directly). See
  `cronometer-integration.md` for the full picture — auth flow, credential
  storage, data model, known gaps.
- **`internal/syncengine`** — `engine.go` (the day-completeness state
  machine: `pending → partial → complete/missing`, shared by every source),
  `sqlstore.go` (`sync_state` table read/write). `googlehealth.DBSyncer` and
  `cronometer.DBSyncer` both implement the `DaySyncer` shape this package
  expects but don't import it directly — they're wired together by whatever
  constructs both (the CLI / the dashboard's force-sync handler, which runs
  both sources' sync in parallel goroutines).
- **`internal/web`** — the dashboard server itself:
  - `server.go` — `Server` struct (`RootKey`, the root DB key, decrypts the
    one app-wide Google OAuth client JSON via `googleClientJSON()` — a
    different secret from any per-user credential), `New()` wiring (Echo +
    `googleSyncers`/`cronoSyncers`: one `*googlehealth.DBSyncer`/
    `*cronometer.DBSyncer` cached per user id, built lazily on first use from
    that user's own per-user credential files via `buildGoogleSyncer`/
    `buildCronometerSyncer` — a cached `nil` deliberately means "checked,
    this user hasn't connected this provider," distinguished from "never
    checked" via the map's own `ok` return; `setGoogleSyncer`/
    `setCronometerSyncer` overwrite an entry so a running server picks up a
    fresh connect or a deletion immediately, no restart needed —
    `handleOnboardingConnectGoogle`/`handleAccountDelete` are the two
    callers that matter here, see their own doc comments for why one passes
    a freshly-built syncer and the other a bare `delete()` off the map, never
    a stored `nil` after a successful connect), `cronometerConnected()`),
    `Start()` (serve + periodic `Store.Checkpoint()` + graceful shutdown).
  - `routes.go` — every route. `/login`, `/signup`, and `/static` are the
    only ones NOT behind `webauth.Middleware`; every other route (the
    dashboard page, `/settings/*`, `/onboarding/*`, `/logout`, and
    everything under `views.APIPrefix`, `/api`) is.
  - `handlers.go` — the main dashboard's Echo handlers: `handleIndex` (full
    page), `handleView` (day-nav / data↔journal tab switch, SSE),
    `handleTile` (expand/collapse one tile, SSE), `handleForceSync` (manual
    sync button, runs Google Health and Cronometer sync in parallel,
    backgrounded goroutine + `syncingDays` de-dup), `handleActivity`/
    `handleFoodServing` (the shared click-to-detail overlay, one handler per
    list kind), `handleJournalSave`/`handleJournalBeacon`. Also
    `buildDashboardData` (assembles every tile for a day; a tile with no
    data for the day is excluded from `Tiles` — see `shouldHideEmptyTile` —
    but its title is kept, bucketed by category into
    `DashboardData.MissingByCategory` for `dashboard.templ`'s
    `EmptySummaryTile`, rather than dropped; also defaults a handful of
    tiles to already-expanded, see `defaultExpandedKind`) and
    `dayLabel`/`parseDay` helpers.
  - `auth.go` — login/signup/logout (`webauth.Authenticate`/`CreateUser`/
    `CreateSession`, plain full-page POST+redirect, no Datastar) and the
    post-signup onboarding flow (`/onboarding/connect`,
    `handleOnboardingConnectGoogle`/`handleOnboardingConnectCronometer`/
    `handleOnboardingSkip`) — Google's reuses
    `googleauth.RunConsentFlow` unchanged; Cronometer's shares
    `connectCronometer` (verify-then-save-then-activate) with
    `cronometer.go`'s `handleCronometerLogin`, the dashboard's own ongoing
    account-settings card.
  - `settings.go` — the app-wide Google OAuth client upload page
    (`/settings/google-client`, `handleGoogleClientSettingsPage`/
    `handleGoogleClientUpload`) — not per-user, see `account.go` for the
    per-account settings page.
  - `account.go` — the per-account settings page (`/settings/account`,
    `handleAccountSettingsPage`) and self-service account deletion
    (`handleAccountDelete`: re-verifies the account password, calls
    `webauth.DeleteUser`, evicts that user's id from `googleSyncers`/
    `cronoSyncers`, clears the session cookie, redirects to
    `/login?deleted=1`) — always scoped to `webauth.CurrentUserID(c)`, never
    a request parameter.
  - `data.go` — the Google-Health-side stat-tile data layer:
    `dashboardDailyRow` (embeds `healthdata.DailySummary` plus the Cronometer
    macro/energy columns the dashboard also shows — a web-layer composite,
    since `healthdata.DailySummary` itself stays Google-Health-only),
    `fetchDailySummaryRow`/`fetch7DayRows`, `metricDef`/`metricDefs` (one
    entry per stat metric: title/icon/unit/extractor/formatter),
    `DefaultTileKinds`, `buildStatTile`/`buildChart` (7-day trend, or an
    hourly/intraday/stage-timeline detail view for metrics that have finer
    data than one value per day), `buildActivitiesTile`/`fetchActivityDetail`,
    `buildActivityLevelSegmentTile`, `buildCategoryTile` (generic
    day+category breakdown builder, backs the heart-rate-zone and
    active-minutes/active-zone-minutes-by-level tiles).
  - `bodymeasurement.go` — `buildBodyTile`/`fetchBodyMeasurement` (the
    weight/waist/neck form) + its save/carry-forward handlers.
  - `foodlog.go` — `buildFoodLogTile` (excludes plain water servings — see
    its own comment) + `fetchFoodServingDetail` (the click-to-detail popup).
  - `cronometer.go` — the Cronometer account card (login form / connected
    status) + its login handler, `cronometerConnected()`.
  - `journal.go` — `fetchJournal`/`saveJournal` (the `daily_journal` table)
    + Markdown rendering (goldmark) for the preview pane.
  - `assets.go` — `//go:embed` of `static/datastar.js` + `static/style.css`.
  - `static/` — `datastar.js` (vendored, self-hosted) and `style.css` (the
    entire visual system — no build step, plain CSS with custom properties).
    Two independently-tuned categorical palettes live here: `:root` (dark,
    default) and `:root[data-theme="light"]`, both validated with the
    `dataviz` skill's `scripts/validate_palette.js` against their own
    surface color — see each block's own comment for the exact checks and
    why a couple of values differ from a naive Apple-Health-color copy.
  - **`internal/web/views`** (package `views`) — templ components + their
    plain-Go view-model structs (`models.go`: `DashboardData` — including
    `MissingByCategory`, `Today` — `TileData`, `ChartData`, `StageSegment`,
    `ActivitySummary`/`ActivityDetail`, `ServingSummary`/`FoodServingDetail`,
    `JournalData`, etc.). Nothing here touches `*sql.DB` — `internal/web`
    builds structs, hands them to a component. Key files: `layout.templ`
    (page shell; `Header` — brand-as-home-link, the light/dark
    `ThemeToggleButton`, and the user-menu dropdown, avatar → Account/Google
    settings/Log out, `$userMenuOpen`; `DayNav` — prev/next day, the native
    date picker, a `day-nav-today-btn` shown only when the viewed day isn't
    `Today`, and the sync button; `themeScript()` — the before-first-paint
    `data-theme` bootstrap every full-page template includes; the shared
    `#detail-overlay`/`$detailOpen` click-to-detail overlay),
    `dashboard.templ` (tile grid + per-kind tile bodies, the Nutrition
    section's two-row kcal/macro layout — each row only renders if non-empty
    — and `EmptySummaryTile`, the one-per-section compact stand-in for
    every metric with no data that day), `body.templ` (body measurement
    form), `activities.templ`/`foodlog.templ` (the two detail-overlay
    fragments that patch into the shared overlay), `cronometer.templ`
    (account login card), `journal.templ`, `login.templ`/`signup.templ`
    (plain POST+redirect auth forms), `settings.templ` (Google OAuth client
    upload page), `account.templ` (per-account settings + delete-account
    form), `connect_accounts.templ` (post-signup onboarding page),
    `icons.templ` (inline SVG icon set), `chart.go` (server-side SVG chart
    rendering — bar chart with optional goal line, line chart, and segment
    timeline, all sharing one hover-tooltip mechanism — no client-side
    charting library), `urls.go` (every backend URL the templates
    reference, all built through `APIURL`; `initialSignals` seeds the
    dashboard's root Datastar signals including `userMenuOpen`),
    `helpers.go` (formatting: `FormatMinutes`, `FormatNumber`,
    `humanizeExerciseType`). Each `X.templ` has a generated, do-not-edit
    `X_templ.go` sibling.

## Data flow in one line

`internal/googlehealth.DBSyncer.SyncDay` and `internal/cronometer.DBSyncer.SyncDay`
(each driven by `internal/syncengine` + `internal/cli`'s scheduler/manual
trigger/dashboard Sync button, run in parallel) write `watch_*`/`cronometer_*`
tables → `internal/web` (`data.go`/`bodymeasurement.go`/`foodlog.go`/
`journal.go`/`handlers.go`) reads them via `*sql.DB` → builds `views` structs
→ templ components render HTML, patched into the page over Datastar SSE.
`internal/db.Store` is the only thing that ever has the database in
plaintext on disk, and only for the life of one run.
