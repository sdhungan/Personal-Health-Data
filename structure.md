# Repository structure

One Go module (`github.com/sdhungan/Personal-Health-Data`), one binary
(`healthd`), built with Cobra + Echo + templ + Datastar. See
`ARCHITECTURE.md` for the *why*; this doc is the *where*.

```
.
├── ARCHITECTURE.md         # design intent, read this first
├── prerequisite.md         # gotchas + findings from working sessions
├── structure.md            # this file
├── Makefile                # generate / build / install / run / clean
├── go.mod / go.sum
├── Creds/                  # gitignored — Google OAuth client secret JSON
├── bin/                    # gitignored — build output + local dev/test roots
│   ├── healthd.exe
│   ├── explore-root/       # a real (test-flavored) --root directory: db/, config/, keys/, logs/, service/
│   └── google-health-dump/ # one-time diagnostic dump of raw Google Health API responses per data type
├── cmd/
│   ├── healthd/main.go     # package main — the only entrypoint, calls cli.Execute()
│   └── tmpinspect/         # scratch DB-inspection tool (not part of the product; ad hoc dev use only)
└── internal/
    ├── paths/       # package paths   — resolves --root and every file/dir path under it
    ├── config/      # package config  — config.yaml (port, sync interval, Google/Cronometer creds)
    ├── crypto/      # package crypto  — AES-256-GCM + Argon2id, whole-file encryption at rest
    ├── db/          # package db      — schema.sql + embedded Schema string, Store (open/checkpoint/close encrypted SQLite)
    ├── googleauth/  # package googleauth — OAuth2 local-redirect flow, encrypted token storage, auto-refreshing http.Client
    ├── googlehealth/# package googlehealth — Google Health API client, data-type definitions, sync engine (DBSyncer), one-shot diagnostic dump
    ├── syncengine/  # package syncengine — source-agnostic day-completeness state machine (pending/partial/complete/missing)
    ├── cli/         # package cli     — Cobra command tree (root/sync/auth/db/serve), shared *paths.Paths
    └── web/         # package web     — Echo server + dashboard (see below)
        └── views/   # package views  — templ components + their Go view-model structs; no DB access here
```

## Package responsibilities and how they connect

- **`cmd/healthd`** — trivial `main()`, delegates entirely to `internal/cli`.
- **`internal/cli`** — Cobra command tree. `root.go` resolves `--root` once
  (`PersistentPreRunE`) into a shared `appPaths *paths.Paths` every
  subcommand file (`sync.go`, `auth.go`, `db.go`, `serve.go`,
  `googlehealthsync.go`, `service.go`) reads from. `service.go` handles the
  `--action=install/start/stop/uninstall` OS-service lifecycle (currently
  stubbed — see its TODOs).
- **`internal/paths`** — the *only* place that builds a path under `--root`
  (`DBFile()`, `ConfigFile()`, `KeysDir()`, etc.) — every other package asks
  this one rather than joining paths itself.
- **`internal/config`** — `Config` struct + `Load(path)`, overlaying
  `config.yaml` onto `Default()`. Read once at startup by `cli`.
- **`internal/crypto`** — `Key`, `Encrypt`/`Decrypt` (AES-256-GCM),
  `DeriveKey` (Argon2id, `kdf.go`), `LoadKey`/key-file handling
  (`keyfile.go`). Used by `internal/db.Store` (DB file) and
  `internal/googleauth` (OAuth token file) — the same primitive for both.
- **`internal/db`** — `Schema` (embedded from `schema.sql`), `Store` (the
  encrypted-DB lifecycle: `Init`/`Open`/`Checkpoint`/`Close`/`Discard`,
  see `store.go`'s doc comments for the raw/working-file dance), `dump.go`
  (plaintext `.sql` export for `healthd db decrypt`), `migrations.go`
  (`Migrations` — an ordered list of incremental SQL changes applied to a
  pre-existing database via `Store.migrate()`, tracked by `PRAGMA
  user_version`; a fresh `Init()` applies `Schema` directly at the latest
  version instead of replaying history). Every other package that touches
  the DB gets a `*sql.DB` from `Store.DB()` — nothing else opens a `sql.DB`
  directly.
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
  (Go structs mirroring each data type's JSON shape), `envelope.go`/`wire.go`
  (response envelope decoding helpers), `sync.go`/`sync_details.go`/
  `sync_upsert.go` (`DBSyncer.SyncDay` — fetches one calendar day and upserts
  into every `watch_*` table), `sync_filters.go` (per-kind time-range filter
  builders), `dump.go` (`DumpToday` — the one-shot "dump every data type as
  JSON" diagnostic that produced `bin/google-health-dump/`). **Sync is
  considered stable/out-of-scope for incidental changes** — see
  `prerequisite.md`.
- **`internal/syncengine`** — `engine.go` (the day-completeness state
  machine: `pending → partial → complete/missing`, shared by every source),
  `sqlstore.go` (`sync_state` table read/write). `googlehealth.DBSyncer`
  implements the `DaySyncer` shape this package expects but doesn't import
  it directly — they're wired together by whatever constructs both (the CLI
  / the dashboard's force-sync handler).
- **`internal/web`** — the dashboard server itself:
  - `server.go` — `Server` struct, `New()` wiring (Echo + optional
    `googlehealth.DBSyncer` if Google auth is set up + `syncengine.SQLStore`),
    `Start()` (serve + periodic `Store.Checkpoint()` + graceful shutdown).
  - `routes.go` — every HTTP route, all under `views.APIPrefix` (`/api`)
    except the page (`/`) and `/static`.
  - `handlers.go` — Echo handlers: `handleIndex` (full page),
    `handleView` (day-nav / data↔journal tab switch, SSE), `handleTile`
    (expand/collapse one tile, SSE), `handleForceSync` (manual sync button,
    backgrounded goroutine + `syncingDays` de-dup), `handleActivity`
    (activity detail overlay), `handleJournalSave`/`handleJournalBeacon`.
    Also `buildDashboardData` (assembles every tile for a day) and
    `dayLabel`/`parseDay` helpers.
  - `data.go` — the stat-tile data layer: `dailySummaryRow` (mirrors
    `watch_daily_summary`), `fetchDailySummaryRow`/`fetch7DayRows`,
    `metricDef`/`metricDefs` (one entry per stat metric: title/icon/unit/
    extractor/formatter), `DefaultTileKinds`, `buildStatTile`/`buildChart`
    (7-day trend), `buildActivitiesTile`/`fetchActivityDetail`,
    `buildGoalTile` (step-goal tile — see `prerequisite.md`, slated for
    replacement).
  - `journal.go` — `fetchJournal`/`saveJournal` (the `daily_journal` table)
    + Markdown rendering (goldmark) for the preview pane.
  - `assets.go` — `//go:embed` of `static/datastar.js` + `static/style.css`.
  - `static/` — `datastar.js` (vendored, self-hosted) and `style.css` (the
    entire visual system — no build step, plain CSS with custom properties).
  - **`internal/web/views`** (package `views`) — templ components + their
    plain-Go view-model structs (`models.go`: `DashboardData`, `TileData`,
    `ChartData`, `ActivitySummary`/`ActivityDetail`, `JournalData`, etc.).
    Nothing here touches `*sql.DB` — `internal/web` builds structs, hands
    them to a component. Key files: `layout.templ` (page shell, header,
    day-nav, sync button), `dashboard.templ` (tile grid + per-kind tile
    bodies), `activities.templ` (activity detail overlay fragment),
    `journal.templ`, `icons.templ` (inline SVG icon set), `chart.go`
    (server-side SVG chart rendering — bar chart with optional goal line,
    line chart — no client-side charting library), `urls.go` (every
    backend URL the templates reference, all built through `APIURL`),
    `helpers.go` (formatting: `FormatMinutes`, `FormatNumber`,
    `humanizeExerciseType`). Each `X.templ` has a generated, do-not-edit
    `X_templ.go` sibling.

## Data flow in one line

`internal/googlehealth.DBSyncer.SyncDay` (driven by `internal/syncengine` +
`internal/cli`'s scheduler/manual trigger) writes `watch_*` tables → `internal/web`
(`data.go`/`journal.go`/`handlers.go`) reads them via `*sql.DB` → builds
`views` structs → templ components render HTML, patched into the page over
Datastar SSE. `internal/db.Store` is the only thing that ever has the
database in plaintext on disk, and only for the life of one run.
