# Personal Health Data Pipeline — Architecture

## 1. Intent

The whole point of this system is to own the full chain from wrist to dashboard, with no third party (Notion, a cloud dashboard, anyone) sitting between your data and you. That constraint drives almost every decision below: one binary instead of a fleet of services (less to deploy, less to break), the database as the one source of truth (so the UI, the sync job, and Claude never disagree with each other), and encryption at rest (because a laptop with your body-composition history and food log on it is worth protecting even on your own network).

The single-binary-with-subcommands shape mirrors how tools like `restic`, `caddy`, and `k3s` are distributed: one artifact, multiple entry points, no separate installer needed.

## 2. The binary: one artifact, many roles

Built with Cobra, the binary exposes itself as one executable with subcommands instead of separate programs. This matters because the sync logic (talking to Google/Cronometer) and the serving logic (Echo + Datastar UI) share the same models, the same DB access layer, and the same encryption code — splitting them into two binaries would mean keeping two copies of that logic in sync by hand.

```
healthd [subcommand] [flags]

healthd [--google-client-secret <path>]  # foreground mode — runs EVERYTHING
                            # as one process: the web dashboard (Echo +
                            # Datastar) as the main loop, the sync scheduler
                            # as a lightweight ticker goroutine, and the MCP
                            # connector as a route on the same server (§11)
                            # — logging straight to stdout/stderr as well as
                            # <root>/logs/healthd.log (§4). --google-client-
                            # secret is optional (see §5) — if given and
                            # valid, it's the *only* way to (re)configure the
                            # app-wide Google OAuth client for the rest of
                            # this run; if empty or invalid, that's logged
                            # as a warning and the dashboard's upload form
                            # stays available as a fallback instead.

healthd --action=install   # registers this one process as an OS service
                            # (systemd unit on Linux, Windows Service via
                            # kardianos/service) so it survives reboots.
                            # --service-name overrides the registered name
                            # (default "healthDSafal"); --google-client-
                            # secret, if given here, is baked into the
                            # service's own re-invocation arguments, so it's
                            # reapplied on every future start without
                            # passing it again.
healthd --action=start     # start/stop/uninstall the service installed
healthd --action=stop      # above (internal/cli/service.go). Installing on
healthd --action=uninstall # Windows requires an elevated/Administrator
                            # shell; on Linux, root or an equivalent
                            # systemd/sysv-managing user.

healthd sync                 # runs one ingestion pass immediately, both
                              # sources (Google Health, then Cronometer) —
                              # manual trigger, useful for testing or
                              # "run it now" without waiting on the
                              # dashboard's click-driven sync or the
                              # scheduler's interval.

healthd auth google          # runs the OAuth2 local-redirect flow for
                              # Google Health API access.

healthd auth cronometer      # prompts for Cronometer email/password
                              # (hidden input), verifies them with a real
                              # login, then saves them encrypted at rest —
                              # see §5. The web dashboard's own login card
                              # does the same thing without a terminal.

healthd db decrypt <out.sql>  # dumps the encrypted DB to a plaintext .sql
                               # file — see §6, this is explicit and logged

healthd db init               # first-run: creates the DB, prompts for the
                               # encryption passphrase, writes folder structure
```

There's no `healthd serve` or `healthd mcp` subcommand anymore (2026-08-05) — both used to be separate processes/services (the dashboard as its own service, the MCP connector as a stdio subprocess an MCP host spawned per session), merged into the one process above: a user account can only ever be created through the dashboard, the MCP connector can't authenticate without that account existing, and sync has nothing to do until that account has connected a provider, so the three were always fully order-dependent — splitting their *processes* bought no real isolation, just more things to install/start/stop/wire up. See §11 for how the MCP connector works now that it's an HTTP route instead of a subcommand.

Why `--action` and not a distinct subcommand? `install/start/stop/uninstall` are lifecycle actions on a service *registration*, so they're naturally flags on a `service` concept rather than top-level verbs — while `sync`, `auth`, and `db decrypt` are one-shot operations you run and watch finish, so they're plain subcommands. Splitting them this way keeps `healthd --help` from being a wall of near-duplicate service-management verbs mixed in with one-shot tools.

Why foreground-by-default instead of always logging to a file? Because the moment something's actually wrong, you want to watch it happen, not `tail -f` a log path you have to remember. Every run — foreground or hosted — also writes structured logs to `<root>/logs/healthd.log` (`internal/applog`, §4) with simple line-count rotation (archived past 1000 lines), so a hosted service with no attached console still has a record to check after the fact.

## 3. Data flow and the sync scheduler

Rather than relying on the OS's own cron table (editing crontab / Task Scheduler entries is one more thing to keep in sync with the binary's install/uninstall lifecycle), the installed service runs its own internal scheduler once started — a plain `time.Ticker` at a fixed interval (`runForegroundCtx` in `internal/cli/service.go`), not a cron-expression library, since the schedule is always "every N minutes," never a real cron spec. `healthd --action=install && healthd --action=start` is the entire setup — uninstalling removes the service and its schedule in one step, with nothing left behind in the system's own cron config to clean up later.

```
   Google Health API ──┐
                        ├──► sync job (internal cron, e.g. every 30 min,
   Cronometer (mobile API) ──┘      or "healthd sync" / the dashboard's
                                    Sync button on demand)
                                        │
                                        ▼
                              writes watch_*/cronometer_* tables
                                        │
                                        ▼
                              ┌─────────────────┐
                              │   Encrypted DB   │◄──── UI edits write
                              │  (source of truth)│      _override columns
                              └─────────────────┘
                                   │         ▲
                          UI (Echo+Datastar) reads + writes via
                          REST — dashboard tiles, journal, body
                          measurements, food log
```

The `_raw` / `_override` column split applies to `watch_*` tables specifically (see `internal/db/schema.sql` — e.g. `body_measurement.weight_kg_raw`/`weight_kg_override`): the sync job is the only writer of a `_raw` column, the UI's edit endpoints (body measurements, journal) are the only writer of an `_override` column, and reads resolve `COALESCE(_override, _raw)`. Cronometer's own tables (`cronometer_*`) have no such split — Cronometer sync is the sole writer of those columns outright (`INSERT ... ON CONFLICT DO UPDATE`), since there's no UI path that edits nutrition data directly; the source of truth for what you ate lives in Cronometer itself, not in this app.

### Data model: five fixed representation shapes

Every `watch_*` table (the Google Health side of the schema) follows one of five fixed shapes, chosen per metric by how its values are actually *useful to look at* — not just by what the API can technically return. See `internal/db/schema.sql`'s own header comment on this section for the authoritative, up-to-date version of this list:

1. **DailyScalar** — one column in `watch_daily_summary`, one row per day (steps, calories, resting HR, ...). `raw_payload` on that table keeps the full JSON response for the day so a first-pass column set can't silently lose data — future columns can be backfilled from it without re-hitting the API.
2. **DailyByCategory** — day + enum-category breakdown (`watch_active_minutes_by_level`, `watch_heart_rate_zone_minutes`, ...) — e.g. minutes per heart-rate zone, not just a daily total.
3. **Hourly** — day + hour buckets (`watch_steps_hourly`) — coarser than raw samples, finer than a daily total.
4. **Timeline** — exact-timestamp samples (`watch_heart_rate_intraday`, `watch_blood_glucose_sample`, ...) — kept granular because *when* it happened matters (heart rate during exercise, glucose after a meal). The sync job always computes that day's avg/min/max from the same fetch straight into `watch_daily_summary`, rather than issuing a separate simplified query later.
5. **SegmentTimeline** — start/end spans through the day (`watch_sleep_stage`, `watch_activity_level_segment`) — for metrics whose *shape across the day* is the point, not a total.

Session types (sleep, exercise, ECG) don't fit any of the five — their internal structure is genuinely bespoke, kept as their own tables rather than forced into a shape that doesn't fit.

**`internal/healthdata`** is the plain Go struct package mirroring these tables (`DailySummary`, `HourlySteps`, `HeartRateSample`, `ActivityLevelSegment`, etc.) — deliberately dependency-free (verified via `go list -deps`, only itself among internal packages), so both `internal/googlehealth` (the writer) and `internal/web` (the reader) can import it directly without an import cycle between them.

## 4. Root folder layout

A configurable root path (flag `--root`, default `~/.healthd/`) keeps everything the binary owns in one predictable place — important once you're running this as an unattended service and need to know exactly what to back up or inspect.

```
~/.healthd/
├── db/
│   └── health.db.enc        # encrypted SQLite file
├── config/
│   ├── config.yaml           # ports, sync interval, root overrides
│   └── google_oauth.json.enc # encrypted OAuth2 client credentials + tokens
├── logs/
│   └── healthd.log            # structured (zap/JSON), every startup/shutdown
│                               # milestone + sync/connector errors — archived
│                               # under a timestamp suffix past 1000 lines
│                               # (internal/applog), a fresh file started
├── keys/
│   └── db.key                # DB encryption key material, 0600 perms
└── service/
    └── (platform-specific service registration files)
```

The intent: nothing lives next to the binary itself, so upgrading `healthd` is just replacing the executable — every stateful thing is under `--root` and untouched by that.

## 5. OAuth2 for Google

Google's Health API uses standard OAuth2 (authorization code flow), and involves two distinct credentials that are easy to conflate but serve different purposes and have different lifetimes:

- **The OAuth *client*** (`client_id`/`client_secret`) identifies this healthd *deployment* to Google — registered once per Google Cloud project. It is an app-wide setting, not a per-account one: every account's own Google login authorizes through this same client. It's configured as the standard `client_secret_*.json` downloaded from Google Cloud Console, validated (`golang.org/x/oauth2/google.ConfigFromJSON` must actually parse it) before being encrypted with the root DB key and written to `config/google_oauth_client.json.enc` (see `internal/googleauth.SaveClientJSON`/`LoadClientJSON`). Two ways in, deliberately not equal (2026-08-05, replacing three previously-equal ways to set it, one of which — the dashboard upload form — could silently clobber what the other two had set): `--google-client-secret <path>` (optional — if given and valid, re-saved from that path on every startup, *and* the only place it can be changed from then on for that run: `web.GoogleClientLockedByFlag` hides and rejects `/settings/google-client`'s upload form once this succeeds, precisely so the client secret can't drift between "whatever's on the command line" and "whatever was last uploaded through a browser"), or the dashboard's `/settings/google-client` upload form (available only when the flag is empty, or was invalid — logged as a warning rather than a fatal startup error, so a typo'd path doesn't leave the account with zero way to configure it). The former headless `healthd google-client set <path>` CLI command was removed for the same reason: as a standalone one-off invocation, it had no way to know about (or respect) `GoogleClientLockedByFlag`, so it could silently overwrite a flag-configured client out from under a running service. The upload form also lets the *currently logged-in* account connect its own Google Health right there (not onboarding-only, see that page's own doc comment) — useful the moment the form is actually available, i.e. before a flag value has locked it.
- **Each account's own OAuth *token*** (access + refresh) is obtained per user, through Google's own real consent screen at accounts.google.com — this is "logging in through Google," already true today, not something layered on separately:
  1. Clicking "Connect Google Health" (dashboard onboarding/settings, or `healthd auth google --user <name>`) starts a short-lived local HTTP listener (e.g. `localhost:9876/callback`) and opens the consent URL in the browser.
  2. After the user approves *their own* Google account access, Google redirects to that local callback with an auth code; the binary exchanges it for an access + refresh token and immediately shuts the listener down.
  3. The token is written to that user's own `config/users/<id>/google_oauth.json.enc`, encrypted with that user's own per-user credential key (§10) — a genuinely different key from the one protecting the shared OAuth client above.
  4. The sync job refreshes the access token silently on each run; if the refresh token itself is ever revoked, the sync job logs a clear "re-run `healthd auth google --user <name>`" error rather than failing silently.

Cronometer has no OAuth — it's username/password against `mobile.cronometer.com/api/v2/*`, Cronometer's own mobile app's REST API (reverse-engineered; there's also an older GWT-RPC web API a third-party Go library wraps, but the mobile API was chosen instead — cleaner JSON payloads, and it's what `internal/cronometer/client.go` is written against directly rather than through a wrapped dependency). `internal/cronometer/session.go` logs in, caches the resulting session token, and re-logs-in only when that session expires (Cronometer throttles repeated logins per account). Both the credentials and the cached session get the same encrypted-at-rest treatment as the Google OAuth tokens, but as two separate files rather than one: `config/cronometer_credentials.json.enc` (long-term) and `config/cronometer_session.json.enc` (short-lived, safe to lose — just triggers a fresh login next sync).

## 6. Encryption at rest, and the decrypt subcommand

The actual implementation is whole-**file** encryption, not an encrypting storage engine: `internal/db.Store` decrypts `db/health.db.enc` into a plaintext SQLite working file (`db/.health.db.work`) on `Open`, hands out a normal `*sql.DB` (via `modernc.org/sqlite`, a pure-Go driver — no cgo/SQLCipher build needed) for the life of the process, and re-encrypts the working file back over `health.db.enc` on `Checkpoint`/`Close` (AES-256-GCM, via `internal/crypto`). The plaintext working file only exists on disk for the duration of one run — never checked into anything, never left behind on a clean shutdown.

* On first run (`healthd db init`), you're prompted for a passphrase (or set `HEALTHD_DB_PASSPHRASE` for unattended/scripted init); a key is derived from it via Argon2id (`internal/crypto`'s `kdf.go`) and the *derived key* — not the passphrase — is what's stored in `keys/db.key` (0600 permissions).
* Every other subcommand that touches the DB reads that key file directly to open the encrypted database transparently — you don't re-enter a passphrase on every sync run, since this is unattended.
* **Unclean-shutdown recovery**: if `.health.db.work` is still present when `Open` runs (process killed, machine lost power, etc.), `Store` recovers from that working file instead of the last encrypted checkpoint, so a forced kill mid-session doesn't lose whatever was written since the last checkpoint — it just means that write never got re-encrypted onto disk as a checkpoint, not that it's gone.
* `healthd db decrypt <path>.sql` is the deliberate escape hatch: it opens the encrypted DB with the stored key (via a throwaway working file, so it can never collide with a running server/sync process) and dumps a plaintext `.sql` file to the path you specify — for backups you want to inspect, migrations, or moving to a different tool later. Because this is the one command that deliberately produces unencrypted data on disk, it logs a loud warning to stderr and refuses to run without an explicit `--confirm` flag.

## 7. UI: Echo + templ + Datastar

* templ components are compiled Go — typed HTML templates that catch mistakes (a mistyped field name, a missing close tag) at `go build` time instead of at runtime in the browser. This matters more than usual here because it's a single-maintainer project; there's no team code review catching template bugs, so pushing that to the compiler is worth it.
* Echo handles routing and the HTTP layer — request parsing, middleware (auth-gating the admin-mode edit routes), and serving the templ-rendered HTML.
* Datastar, using the Go SDK's `ServerSentEventGenerator`, is what makes the UI feel dynamic without a JS framework: an edit in the UI triggers a request to an Echo handler, which re-renders the affected templ fragment and pushes it back over SSE with `morph` merge mode (Datastar's default, backed by Idiomorph) — the DOM patches in place, no full page reload, no client-side state to keep in sync by hand. This is the same "backend renders HTML, browser just displays it" model as the rest of the system: the Go binary stays the single source of truth for both data and UI state.
* Admin mode: the UI can show/hide edit controls based on a client-side toggle for convenience, but the actual enforcement of which fields are editable happens in the Echo handler (checking a per-field metadata table against the request), same as discussed earlier — the client-side toggle is UX, not security.
* Charts (`internal/web/views/chart.go`) are server-rendered inline SVG, no client-side charting library — bar charts (7-day trends, hourly steps), line charts (heart-rate intraday), and segment timelines (sleep stages, activity level) all share one hover-tooltip mechanism (`hoverAttrs`/`renderChartOverlay`) driven entirely by Datastar signals, no JS beyond what Datastar itself provides.
* One shared click-to-detail overlay (`#detail-overlay`/`$detailOpen` in `layout.templ`) backs both the activities list and the food log — clicking an item fetches that item's detail fragment over SSE into the same `#detail-panel`, so adding a third "clickable list → popup detail" tile later reuses the same overlay rather than building a new one. The header's user-menu dropdown (avatar → Account/Google settings/Log out) uses the same show/hide-plus-backdrop shape, its own `$userMenuOpen` signal. See `prerequisite.md`'s Datastar gotcha section before changing anything here — this version of Datastar has a real footgun around combining `data-show` and `data-attr:style` on the same element.
* **Light/dark theme** is a client-only preference (`localStorage`, no DB column, no cookie) — a dashboard's visual theme has no bearing on any account's data, so there's nothing to sync or protect server-side, and a purely cosmetic per-browser toggle doesn't need a round trip. `layout.templ`'s `themeScript()` stamps `data-theme` onto `<html>` before first paint on every full page (dashboard, login, signup, settings, account, onboarding); `ThemeToggleButton()` (dashboard header) flips it and persists the choice. `style.css`'s `:root` (dark) and `:root[data-theme="light"]` blocks are two independently-tuned categorical palettes — validated separately against each mode's own surface color (the `dataviz` skill's own rule: never assume a palette that passes for dark also passes for light, or vice versa; a light surface has a wider, differently-shifted acceptable lightness band and different contrast-vs-surface arithmetic).
* **Empty tiles are consolidated, not hidden silently.** A metric with no data for the selected day never renders a full-size "No data" tile, but its title also isn't just dropped on the floor — it's collected per dashboard section (`DashboardData.MissingByCategory`) and shown as one compact `EmptySummaryTile` per section (e.g. "No data today: Blood Glucose · Core Body Temp"). This replaced an earlier version where a section with zero remaining tiles vanished entirely, which — combined with the Nutrition section's two always-rendered-even-when-empty row `<div>`s before this — left inconsistent, hard-to-explain gaps in the page depending on which providers happened to be connected that day.

## 8. Build system

A `Makefile` ties the multi-step build (templ codegen → Go build → asset embedding) into one command, since none of those steps alone produces a working binary:

```makefile
.PHONY: generate build install clean run

generate:       ## compile .templ files into Go
	templ generate

build: generate ## produce the healthd binary
	go mod tidy
	go build -o bin/healthd ./cmd/healthd

install: build   ## build, then register + start as a service
	./bin/healthd --action=install
	./bin/healthd --action=start

run: generate    ## foreground dev run, no build artifact
	go run ./cmd/healthd

clean:
	rm -rf bin/
```

The intent behind `generate` as an explicit, separate target (rather than baking `templ generate` into `go build` via `go:generate` alone) is that it makes the two-step nature of the build visible — anyone (including future you) reading the Makefile immediately sees that templ output is a build input, not something `go build` handles on its own.

## 9. Summary of the intent, end to end

Every piece of this is answering the same question: what's the smallest set of moving parts that keeps this fully local, fully yours, and hard to accidentally break?

* One binary → one thing to build, ship, and upgrade.
* DB as source of truth with `raw_value`/`override_value` → the sync job and your manual corrections can never silently fight each other.
* Internal scheduler over OS cron → the service's lifecycle (install/start/stop/uninstall) fully owns its own schedule, nothing external to keep in sync.
* Encryption at the storage layer, with an explicit, confirmed decrypt path → the data's protected by default, and the one way to get plaintext out is a deliberate, logged action, not an implicit side effect of some other command.
* templ + Datastar → the same "server renders, browser displays" philosophy as the rest of the system, instead of introducing a second, JS-side source of truth for UI state.

## 10. Multi-user accounts

As of 2026-08-02, `healthd` supports multiple independent accounts sharing one encrypted database file, gated behind a dashboard login. The design keeps three previously-entangled concerns deliberately separate:

**Viewing vs. syncing.** Logging into the dashboard only gates *seeing* your data — it has no bearing on whether the background scheduler keeps that data synced. The scheduler fans out one short-lived goroutine per user per source on every scheduled tick (`internal/cli`'s `runGoogleHealthSyncOnce`/`runCronometerSyncOnce`), driven entirely by which per-user credential files exist on disk, never by `web_session`. This preserves the single-user era's core promise — "set it up once, it keeps syncing unattended" — for every account, not just whoever's currently logged in.

**Two encryption layers, one primitive.** The root `keys/db.key` (Argon2id from the `db init` passphrase) still encrypts the whole SQLite file exactly as before — nothing changed there. Each user *additionally* gets `keys/users/<id>.key`, derived the same way but from their own account password at signup, encrypting only that user's own Google OAuth token / Cronometer credentials under `config/users/<id>/`. Both keys are cached to disk rather than held only in memory, for the same reason: sync must run unattended, without a human re-entering a password on every pass. This is the same trust boundary the original DB key already accepted (protects against casual disk browsing, not a compromised running machine) — extended to per-user secrets rather than inventing a stronger, harder-to-automate model.

**Sessions vs. provider credentials.** `web_session` (login cookie, 24h sliding inactivity window — touched on every authenticated request, not a fixed expiry from login time) is entirely separate machinery from the per-user credential keyfiles above. A session hashes its bearer token (SHA-256) before storage specifically so `healthd db decrypt` — a deliberate, supported plaintext-dump operation — can never leak a live, usable login credential.

**Data model.** Every table gained a `user_id` column folded into its primary key (see `schema.sql`'s own header comment) — the same wholesale-rewrite-not-migration approach the original `watch_*` schema redesign used, since there was still no real deployed data to preserve (see `internal/db/migrations.go`'s comment and `prerequisite.md`). `internal/web`'s query layer threads an explicit `userID` parameter through every function, always sourced from the authenticated session (`webauth.CurrentUserID`), never a request parameter — the actual enforcement point for per-account isolation, since SQL views/joins alone can't be trusted to prevent one account's data leaking into another's query.

**Onboarding.** A new account is prompted once (`/onboarding/connect`, freely revisitable later) to connect Google Health and Cronometer, each skippable with an explicit warning about which tiles won't have data until connected. Google's flow reuses `internal/googleauth.RunConsentFlow` unchanged (same local-browser OAuth dance the CLI already used, just triggered from a web handler); Cronometer's reuses the existing verify-then-save logic from the dashboard's account-settings card. Connecting a provider from the web UI rebuilds and caches the real syncer immediately (`buildGoogleSyncer`/`buildCronometerSyncer`, called right after saving the new credential and passed to `setGoogleSyncer`/`setCronometerSyncer`) rather than just invalidating the cache — a syncer cache keyed by user id treats *any* present key, including an explicitly-cached `nil`, as "already checked" (see `web.Server`'s `googleSyncers`/`cronoSyncers` doc comment), so writing `nil` after a successful connect would have permanently locked in "not connected" until the next process restart instead of picking up the token that was just saved.

**Account deletion.** Any account can permanently delete itself (`/settings/account`, password re-confirmation required) — `webauth.DeleteUser` explicitly `DELETE`s every `user_id`-scoped table by name in one transaction, then the `users` row, then removes that account's `keys/users/<id>.key` and `config/users/<id>/` from disk. This is deliberate, not an oversight: `schema.sql` already declares `ON DELETE CASCADE` on every one of these foreign keys, but SQLite's foreign-key enforcement is a per-connection `PRAGMA` (set once, during `db init`'s schema application, on a connection that then closes) — Go's `*sql.DB` pools multiple physical connections underneath one handle, and nothing guarantees a later `DELETE FROM users` lands on a connection that ever had it turned on. Relying on the schema's cascade here would be relying on undefined behavior; explicit per-table deletes (`userDataTables` in `internal/webauth/users.go`) are correct regardless of which connection a statement happens to run on.

## 11. MCP connector for food logging

`healthd mcp --user <username>` (`internal/cli/mcp.go`) runs a local **stdio** MCP server exposing Cronometer food search/logging tools (`internal/mcpserver`, backed by `internal/cronometer`'s `DBSyncer` action methods in `actions.go`). It's how food gets logged by describing a meal — or a photo of one — to Claude in an ordinary chat, using whatever Claude subscription is already paid for, instead of building a chat UI into this dashboard that would need its own separate, metered API key.

**Why stdio, not a network server** (reaffirmed 2026-08-05, after actually trying HTTP and reverting it the same day). An MCP host (Claude Code, Claude Desktop) spawns `healthd mcp` as a subprocess and talks to it over stdin/stdout — there is no listening port, nothing reachable from outside this machine. This is the same posture as §1's whole premise: no third party sits between the data and the person, and a local-only connector is the version of "connect Claude to my data" that doesn't compromise that. An HTTP route on the merged process (§2) was tried, since "one binary" briefly seemed to imply the connector should be a goroutine of it too — but that wasn't actually true: the connector was never one of the two OS services the merge was solving for (it has no `--action` lifecycle of its own regardless of transport, see below), and moving it to HTTP bought nothing but a new problem — Claude Desktop's own config file only understands a spawned `command`/`args` entry, not an arbitrary URL, so reaching an HTTP route from Claude Desktop needs a separate bridge process (`mcp-remote`, itself a new Node.js dependency) for zero actual benefit. Reverted the same day back to stdio, keeping only what that detour actually improved: the dish-vs-single-item tool policy below, which lives entirely in `internal/mcpserver` and is identical either way.

**No LLM call happens in Go, anywhere in this feature.** The calling Claude session does 100% of the food identification and nutrient estimation itself, in its own turn — that's the entire point of piggybacking on a chat subscription instead of paying per token from server code. `internal/mcpserver`'s tools are deliberately "dumb": search Cronometer's real food database (`cronometer_search_food`, wrapping the `find_food`/`get_foods` endpoints), log a matched serving (`cronometer_log_serving`), define a new food (`cronometer_create_custom_food`), read back what's already logged for a day straight from Cronometer's live API rather than the locally-synced tables (`cronometer_get_diary` — the local `cronometer_serving` table is only as fresh as the last scheduled sync, which wouldn't yet reflect something logged moments earlier in the same chat), and undo a mistaken log (`cronometer_delete_serving`).

**Dish vs. single item is a deliberate, explicit policy, not left to guesswork** (2026-08-05, replacing an earlier "always search first" instruction that caused a described multi-component dish to get decomposed into its ingredients and logged as several separate entries — not what was wanted). `mcp.NewServer`'s `Instructions` (`consumerInstructions` in `internal/mcpserver/server.go`) and each tool's own description now draw the line explicitly: `cronometer_search_food` is for a single, specific, likely-branded or well-known item only ("a banana", "Maltesers") — prefer a real database match there. A described dish or meal with more than one component (or a photo of a plated meal) must NOT be decomposed and searched ingredient-by-ingredient; instead the calling LLM estimates the whole dish's nutrition itself, confirms the ingredients/description it understood with the user first (never skip this for a photo, where what's actually in the dish is a guess until confirmed), then calls `cronometer_create_custom_food` exactly once for the whole dish (named for the dish, e.g. "Pasta with homemade arrabiata sauce and chicken/pork mince sausage") before logging it. This is instruction-level guidance, not something the Go server can structurally enforce — an MCP tool call carries no notion of "the user already confirmed this in chat" — so it depends on the calling LLM actually following the tool descriptions/instructions, the same as every other behavioral contract in this feature (e.g. `cronometer_delete_serving`'s "use for correcting a mistake, not arbitrary cleanup"). See `references/log-food` skill (or whatever it's named once created) for a stronger, explicitly-invoked reinforcement of this same policy, for when relying on tool descriptions alone isn't reliable enough.

**Auth reuses the multi-user credential model exactly, adds nothing new.** `--user <username>` resolves to an account id the same way `healthd auth google/cronometer --user` already does (`resolveUserID` in `internal/cli/users.go`), then loads that account's per-user credential key via `webauth.CredentialKey` — the identical key the background sync scheduler already uses to decrypt that user's saved Cronometer credentials unattended (see §10's "two encryption layers" paragraph). There's no new secret, no new prompt: `healthd mcp` only works for an account that already ran `healthd auth cronometer --user <name>` (or connected Cronometer from the dashboard) beforehand, and reuses `cronometer.DBSyncer`'s existing login/session-refresh machinery (`withRetry`) rather than reimplementing it.

**One process, one account, no `--action` lifecycle.** Unlike the merged scheduler+dashboard process, `healthd mcp` has no install/start/stop/uninstall story — its lifecycle is owned entirely by whatever MCP host spawned it, same as before and after the HTTP detour above. `/settings/mcp-connector` in the dashboard (`internal/web/mcp_connector.go`) is a purely static, per-account page showing the exact `claude mcp add`/`.mcp.json` snippet to wire it up — not an entry form or a chat UI; all natural-language/photo input happens in the MCP host's own chat.

**A `mcp_token` table exists in schema.sql/migrations.go but is currently unused** — leftover schema from the HTTP detour above. Left in place rather than retroactively deleting a migration that had already shipped against a real database with real user data by the time it was reverted (see migrations.go's own doc comment on why migrations are append-only once that's true) — harmless unused schema, not a functional issue. The Go code that read/wrote it (`internal/webauth.CreateMCPToken`/`LookupMCPToken`/`HasMCPToken`) was removed since dead code doesn't get the same append-only justification a shipped migration does.

**Meal categorization is a time default, not an API field.** `cronometer_log_serving` accepts an optional `meal` (breakfast/lunch/dinner/snack) purely as a fallback for picking a default clock time when the caller doesn't give one explicitly (`mealDefaultTimes` in `internal/mcpserver/server.go`) — Cronometer's own app buckets diary entries into meal groups by time of day against boundaries configured in the account's own settings, not an explicit per-entry field (confirmed absent from the real `add_serving`/`get_diary` shapes this client is built against; see `cronometer-integration.md`'s "Known gaps"). This is a best-effort approximation of Cronometer's stock default boundaries, not a guarantee for an account with customized meal times.

**Every write refreshes the dashboard's local mirror immediately, Cronometer-only.** `cronometer_log_serving`/`cronometer_delete_serving` both call `DBSyncer.SyncDay` for the affected day right after a successful write (`registrar.syncDashboard`) — otherwise the healthd dashboard's `cronometer_serving`/`cronometer_daily_nutrition` tables would only reflect a connector-logged meal after the next scheduled sync (potentially tens of minutes away, and only while the separate `healthd` scheduler process happens to be running at all). This never touches Google Health — it's the same single-source sync `SyncDay` already runs for the CLI/scheduler, just triggered on demand instead of on a timer. A sync failure here is logged to stderr, not surfaced as a tool error: the write to Cronometer itself already succeeded, which is what actually matters, and a briefly-stale local mirror is self-healing on the next real sync.

**Multiple processes against one root is now a supported case — it wasn't at first, and the gap cost a real database.** With `healthd serve` and one or more `healthd mcp` instances open concurrently (the normal shape once a connector is wired up), SQLite's WAL mode (already chosen for exactly this — see §7) lets every process's own `*sql.DB` handle operate safely against the single shared `db/.health.db.work` file `internal/db.Store.Open` decrypts, for ordinary reads/writes. But `Store.Close`'s cleanup (`removeWorkingFile`, §6) didn't account for a second process still needing that file: it zeroed the working file's bytes in place *before* attempting to delete it, and on Windows a concurrent zero-write silently succeeds even when the delete that follows fails because another process still has the file open. One `healthd mcp` process exiting while `healthd serve` was still running destroyed that shared file out from under the running server — confirmed 2026-08-05, and the running server's own next periodic `Checkpoint` faithfully encrypted those zeros over the on-disk backup too, before the corruption was caught, losing the account's data with no path to recover it. Fixed the same day: `removeWorkingFile` now renames the file to a private temp name *first* — a rename fails on Windows under exactly the same sharing condition a delete would, so "another process still has this open" is now caught before any content is touched, not after. `internal/db/store_test.go`'s `TestCloseLeavesSharedWorkingFileIntactWhenStillOpen` is a regression test for this (confirmed to fail against the pre-fix code with the identical "file is not a database" error hit live). The other rough edge, cosmetic only: `Open`'s "did I shut down uncleanly last time" check (§6) still can't distinguish a genuinely unclean prior shutdown from a sibling process's working file being legitimately in use, so a second process opening the same root logs that warning even when nothing is wrong.
