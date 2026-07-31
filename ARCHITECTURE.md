# Personal Health Data Pipeline — Architecture

## 1. Intent

The whole point of this system is to own the full chain from wrist to dashboard, with no third party (Notion, a cloud dashboard, anyone) sitting between your data and you. That constraint drives almost every decision below: one binary instead of a fleet of services (less to deploy, less to break), the database as the one source of truth (so the UI, the sync job, and Claude never disagree with each other), and encryption at rest (because a laptop with your body-composition history and food log on it is worth protecting even on your own network).

The single-binary-with-subcommands shape mirrors how tools like `restic`, `caddy`, and `k3s` are distributed: one artifact, multiple entry points, no separate installer needed.

## 2. The binary: one artifact, many roles

Built with Cobra, the binary exposes itself as one executable with subcommands instead of separate programs. This matters because the sync logic (talking to Google/Cronometer) and the serving logic (Echo + Datastar UI) share the same models, the same DB access layer, and the same encryption code — splitting them into two binaries would mean keeping two copies of that logic in sync by hand.

```
healthd [subcommand] [flags]

healthd                    # foreground mode — runs the server, logs straight
                            # to stdout/stderr. This is your "just let me see
                            # what's happening" mode during development or
                            # debugging, no log file to tail.

healthd --action=install   # registers the binary as an OS service
                            # (systemd unit on Linux, Windows Service via
                            # kardianos/service) so it survives reboots
healthd --action=start
healthd --action=stop
healthd --action=uninstall

healthd sync                # runs one ingestion pass immediately (manual
                             # trigger, useful for testing or "run it now")

healthd auth google         # runs the OAuth2 device/local-redirect flow
                             # for Google Health API access

healthd db decrypt <out.sql>  # dumps the encrypted DB to a plaintext .sql
                               # file — see §6, this is explicit and logged

healthd db init              # first-run: creates the DB, prompts for the
                              # encryption passphrase, writes folder structure
```

Why `--action` and not five distinct subcommands only? Both are actually present here on purpose: `install/start/stop/uninstall` are lifecycle actions on the service registration, so they're naturally flags on a `service` concept rather than top-level verbs — while `sync`, `auth`, and `db decrypt` are one-shot operations you run and watch finish, so they're plain subcommands. Splitting them this way keeps `healthd --help` from being a wall of near-duplicate service-management verbs mixed in with one-shot tools.

Why foreground-by-default instead of always logging to a file? Because the moment something's actually wrong, you want to watch it happen, not `tail -f` a log path you have to remember. The installed-service mode still writes to the structured log directory (§4) — foreground mode is purely for the "I'm sitting here debugging" case.

## 3. Data flow and the sync scheduler

Rather than relying on the OS's own cron table (editing crontab / Task Scheduler entries is one more thing to keep in sync with the binary's install/uninstall lifecycle), the installed service runs its own internal scheduler (`robfig/cron` under the hood) once started. `healthd --action=install && healthd --action=start` is the entire setup — uninstalling removes the service and its schedule in one step, with nothing left behind in the system's own cron config to clean up later.

```
   Google Health API ──┐
                        ├──► sync job (internal cron, e.g. every 30 min)
   Cronometer (unofficial) ──┘         │
                                        ▼
                              writes raw_value rows
                                        │
                                        ▼
                              ┌─────────────────┐
                              │   Encrypted DB   │◄──── UI edits write
                              │  (source of truth)│      override_value
                              └─────────────────┘
                                   │         ▲
                     Claude job reads   UI (Echo+Datastar)
                     writes daily_notes  reads + writes via
                     (separate table)    REST, admin-gated
```

The `raw_value` / `override_value` split from earlier still stands — the sync job is the only writer of `raw_value`, the UI's admin-mode edit endpoints are the only writer of `override_value`, and reads everywhere else resolve `COALESCE(override_value, raw_value)`. That rule is enforced in the DB access layer (one Go package both the sync job and the API handlers import), not re-implemented in each caller.

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

Google's Health API uses standard OAuth2 (authorization code flow). Since this is a local, single-user tool, the flow that fits is:

1. `healthd auth google` starts a short-lived local HTTP listener (e.g. `localhost:9876/callback`) and opens the consent URL in your browser.
2. After you approve, Google redirects to that local callback with an auth code; the binary exchanges it for an access + refresh token and immediately shuts the listener down.
3. Tokens are written to `config/google_oauth.json.enc`, encrypted with the same key as the DB (§6) — the refresh token is the sensitive long-lived credential here, so it gets the same protection as the health data itself.
4. The sync job refreshes the access token silently on each run; if the refresh token itself is ever revoked, the sync job logs a clear "re-run `healthd auth google`" error rather than failing silently.

Cronometer has no OAuth — it's username/password against the unofficial mobile API. Of the reverse-engineered options, a Go-native client library (`gocronometer`, MIT/GPLv2-licensed, wraps the same export endpoints the SPA uses) is the natural fit here since everything else in the sync job is already Go — no need to shell out to a Python tool. The credentials for it get the same encrypted-at-rest treatment as the Google tokens; the session cookie it caches internally goes in `config/` alongside them, not written unencrypted anywhere.

## 6. Encryption at rest, and the decrypt subcommand

The DB engine here — SQLite, encrypted at rest — needs the encryption to live at the storage layer, which is what SQLCipher-compatible drivers give you: the `.db` file on disk is genuinely opaque without the key, and the binary is the only thing that knows how to open it.

* On first run (`healthd db init`), you're prompted for a passphrase; a key is derived from it (e.g. via Argon2) and the derived key material is what's stored in `keys/db.key` (0600 permissions) — the passphrase itself is never written to disk.
* Every other subcommand that touches the DB reads that key file to open the encrypted database transparently — you don't re-enter a passphrase on every sync run, since this is unattended.
* `healthd db decrypt <path>.sql` is the deliberate escape hatch: it opens the encrypted DB with the stored key and dumps a plaintext `.sql` file to the path you specify — for backups you want to inspect, migrations, or moving to a different tool later. Because this is the one command that deliberately produces unencrypted data on disk, it should log a loud warning to stderr and require a `--confirm` flag, rather than running silently.

## 7. UI: Echo + templ + Datastar

* templ components are compiled Go — typed HTML templates that catch mistakes (a mistyped field name, a missing close tag) at `go build` time instead of at runtime in the browser. This matters more than usual here because it's a single-maintainer project; there's no team code review catching template bugs, so pushing that to the compiler is worth it.
* Echo handles routing and the HTTP layer — request parsing, middleware (auth-gating the admin-mode edit routes), and serving the templ-rendered HTML.
* Datastar, using the Go SDK's `ServerSentEventGenerator`, is what makes the UI feel dynamic without a JS framework: an edit in the UI triggers a request to an Echo handler, which re-renders the affected templ fragment and pushes it back over SSE with `morph` merge mode (Datastar's default, backed by Idiomorph) — the DOM patches in place, no full page reload, no client-side state to keep in sync by hand. This is the same "backend renders HTML, browser just displays it" model as the rest of the system: the Go binary stays the single source of truth for both data and UI state.
* Admin mode: the UI can show/hide edit controls based on a client-side toggle for convenience, but the actual enforcement of which fields are editable happens in the Echo handler (checking a per-field metadata table against the request), same as discussed earlier — the client-side toggle is UX, not security.

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
