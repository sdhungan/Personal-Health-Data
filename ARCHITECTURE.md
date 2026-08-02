# Personal Health Data Pipeline — Architecture

## 1. Intent

The whole point of this system is to own the full chain from wrist to dashboard, with no third party (Notion, a cloud dashboard, anyone) sitting between your data and you. That constraint drives almost every decision below: one binary instead of a fleet of services (less to deploy, less to break), the database as the one source of truth (so the UI, the sync job, and Claude never disagree with each other), and encryption at rest (because a laptop with your body-composition history and food log on it is worth protecting even on your own network).

The single-binary-with-subcommands shape mirrors how tools like `restic`, `caddy`, and `k3s` are distributed: one artifact, multiple entry points, no separate installer needed.

## 2. The binary: one artifact, many roles

Built with Cobra, the binary exposes itself as one executable with subcommands instead of separate programs. This matters because the sync logic (talking to Google/Cronometer) and the serving logic (Echo + Datastar UI) share the same models, the same DB access layer, and the same encryption code — splitting them into two binaries would mean keeping two copies of that logic in sync by hand.

```
healthd [subcommand] [flags]

healthd                    # foreground mode — runs the sync scheduler only
                            # (every sync_interval_minutes, plus once
                            # immediately on start), logging straight to
                            # stdout/stderr. Does NOT serve the dashboard —
                            # see the gap noted below.

healthd --action=install   # registers the scheduler as an OS service
                            # (systemd unit on Linux, Windows Service via
                            # kardianos/service) so it survives reboots.
healthd --action=start     # STUB as of this writing — every action just
healthd --action=stop      # prints a "TODO" line (internal/cli/service.go);
healthd --action=uninstall # the shape is wired up, the OS integration isn't.

healthd serve                     # runs the web dashboard (Echo + Datastar).
                                   # Does NOT run the sync scheduler — the
                                   # dashboard's own Sync button is a manual
                                   # per-day trigger instead.
healthd serve --action=install    # same install/start/stop/uninstall stub
healthd serve --action=start      # pattern as above, but registers the web
healthd serve --action=stop       # dashboard as its OWN separate OS service
healthd serve --action=uninstall  # (so it can restart independently of the
                                   # scheduler service).

healthd sync                 # runs one ingestion pass immediately, both
                              # sources (Google Health, then Cronometer) —
                              # manual trigger, useful for testing or
                              # "run it now" without waiting on `serve`'s
                              # click-driven sync or the scheduler's interval.

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

Why `--action` and not five distinct subcommands only? `install/start/stop/uninstall` are lifecycle actions on a service *registration*, so they're naturally flags on a `service` concept rather than top-level verbs — while `sync`, `auth`, and `db decrypt` are one-shot operations you run and watch finish, so they're plain subcommands. Splitting them this way keeps `healthd --help` from being a wall of near-duplicate service-management verbs mixed in with one-shot tools.

Why foreground-by-default instead of always logging to a file? Because the moment something's actually wrong, you want to watch it happen, not `tail -f` a log path you have to remember. The installed-service mode still writes to the structured log directory (§4) — foreground mode is purely for the "I'm sitting here debugging" case.

**Known gap**: the bare `healthd` scheduler and `healthd serve`'s dashboard are still two separate processes — there's no single command that runs both together yet (`runForeground` in `internal/cli/service.go` has a literal `TODO: start the Echo+Datastar server alongside this scheduler`). Today, "always-on with auto-sync" means running both `healthd` and `healthd serve` at once; `serve` alone only syncs when the dashboard's Sync button is clicked.

## 3. Data flow and the sync scheduler

Rather than relying on the OS's own cron table (editing crontab / Task Scheduler entries is one more thing to keep in sync with the binary's install/uninstall lifecycle), the installed service runs its own internal scheduler (`robfig/cron` under the hood) once started. `healthd --action=install && healthd --action=start` is the entire setup — uninstalling removes the service and its schedule in one step, with nothing left behind in the system's own cron config to clean up later.

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
│   ├── sync.log               # rotated, one line per sync run + errors
│   └── server.log
├── keys/
│   └── db.key                # DB encryption key material, 0600 perms
└── service/
    └── (platform-specific service registration files)
```

The intent: nothing lives next to the binary itself, so upgrading `healthd` is just replacing the executable — every stateful thing is under `--root` and untouched by that.

## 5. OAuth2 for Google

Google's Health API uses standard OAuth2 (authorization code flow), and involves two distinct credentials that are easy to conflate but serve different purposes and have different lifetimes:

- **The OAuth *client*** (`client_id`/`client_secret`) identifies this healthd *deployment* to Google — registered once per Google Cloud project. It is an app-wide setting, not a per-account one: every account's own Google login authorizes through this same client. It's uploaded via the dashboard's `/settings/google-client` page (or `healthd google-client set <path>`) as the standard `client_secret_*.json` downloaded from Google Cloud Console, validated (`golang.org/x/oauth2/google.ConfigFromJSON` must actually parse it) before being encrypted with the root DB key and written to `config/google_oauth_client.json.enc` (see `internal/googleauth.SaveClientJSON`/`LoadClientJSON`) — there is deliberately no config.yaml file-path field for this anymore, so setting it up doesn't require filesystem access to wherever healthd runs.
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
* One shared click-to-detail overlay (`#detail-overlay`/`$detailOpen` in `layout.templ`) backs both the activities list and the food log — clicking an item fetches that item's detail fragment over SSE into the same `#detail-panel`, so adding a third "clickable list → popup detail" tile later reuses the same overlay rather than building a new one. See `prerequisite.md`'s Datastar gotcha section before changing anything here — this version of Datastar has a real footgun around combining `data-show` and `data-attr:style` on the same element.

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

**Onboarding.** A new account is prompted once (`/onboarding/connect`, freely revisitable later) to connect Google Health and Cronometer, each skippable with an explicit warning about which tiles won't have data until connected. Google's flow reuses `internal/googleauth.RunConsentFlow` unchanged (same local-browser OAuth dance the CLI already used, just triggered from a web handler); Cronometer's reuses the existing verify-then-save logic from the dashboard's account-settings card.
