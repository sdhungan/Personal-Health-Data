# Cronometer integration — how it works

Reference doc for the Cronometer side of this codebase — the client, auth,
sync engine, and UI, originally built 2026-08-01, extended 2026-08-05 with
write-endpoint verification and the MCP food-logging connector (see
`ARCHITECTURE.md` §11 for the connector's own design). See also
`prerequisite.md` (general project gotchas) and `structure.md` (repo
layout). This used to be a handoff/TODO doc written before any of this
existed; that plan is done, this describes what actually got built —
including one real decision reversal from the original plan (see "Which API"
below).

## Which API: mobile REST, not the GWT-RPC library originally planned

`ARCHITECTURE.md` originally named `gocronometer` (wrapping Cronometer's
older GWT-RPC web API) as the intended dependency. That didn't happen.
`internal/cronometer/client.go` is a hand-written client against
`mobile.cronometer.com/api/v2/*` — Cronometer's own mobile app's REST API,
reverse-engineered — confirmed directly against real captured responses
from the user's own account (via `cmd/cronodump`, see below), not guessed
from third-party research docs. The switch happened because the mobile
API's JSON payloads are simply cleaner to work with directly than wrapping
a GWT-RPC-parsing library would have been, and everything else in this sync
job is already hand-written Go against a REST API (Google Health) anyway —
no reason to special-case Cronometer with a wrapped dependency.

Every struct in `internal/cronometer/values.go` follows the same
CONFIRMED/DOCUMENTED/INFERRED confidence-tier discipline
`internal/googlehealth/values.go` established: don't guess a field name,
because a wrong guess doesn't error, it silently decodes to a zero value.

## What exists

- **`cmd/cronodump`** — a standalone diagnostic dump tool (mirrors
  `internal/googlehealth/dump.go`'s role): logs in and dumps raw API
  responses to disk for a real account, the same discipline the Google
  Health work used to build `values.go` against real data instead of
  guesses.
- **`cmd/cronoverify`** — a second diagnostic tool (2026-08-05), unlike
  `cronodump` in that it calls `internal/cronometer.Client`'s actual typed
  methods directly rather than raw requests, since its job is confirming
  those methods work as coded, not discovering new shapes. Round-trips
  every write endpoint (`find_food` → `add_food` → `get_foods` →
  `add_serving` → `get_diary` → `delete_entries` → `get_diary`) against a
  real account, printing a PASS/FAIL line per step — see "Write endpoints"
  below for what it found.
- **`internal/cronometer/client.go`** — `Client.Login`, `GetDiary`,
  `GetFoods`, `GetNutritionScores`, `GetMetrics` — thin wrappers over the
  mobile API's `POST /api/v2/*` endpoints. Also `FindFood`, `CreateCustomFood`,
  `AddServing`, `DeleteEntries` — the write endpoints, see below.
- **`internal/cronometer/actions.go`** (2026-08-05) — `DBSyncer.SearchFood`/
  `CreateCustomFood`/`LogServing`/`Diary`/`DeleteServing`, built on the write
  endpoints above plus `DBSyncer`'s existing login/retry machinery. Backs
  `internal/mcpserver`'s tools (see `ARCHITECTURE.md` §11) — not called by
  the sync path (`SyncDay`) at all, these are a separate, additive action
  surface for pushing data *to* Cronometer rather than pulling it.
- **`internal/cronometer/session.go`** — `Credentials`/`Session` types,
  `SaveCredentials`/`LoadCredentials`/`SaveSession`/`LoadSession` (all via
  `internal/crypto`, same AES-256-GCM primitive the Google OAuth tokens and
  the DB itself use) — two separate encrypted files, not one:
  `config/cronometer_credentials.json.enc` (long-term email/password) and
  `config/cronometer_session.json.enc` (short-lived session token, cached
  so a sync run doesn't have to log in from scratch every time — Cronometer
  throttles repeated logins per account; safe to lose, just triggers a
  fresh login next sync).
- **`internal/cronometer/values.go`** — wire structs for every API response
  shape actually used (`DiaryResponse`/`DiarySummary`/`DiaryEntry`, `Food`/
  `FoodMeasure`/`FoodNutrient`, `NutritionScoresResponse`/`ScoreComponent`,
  `Metric`/`MetricUnit`).
- **`internal/cronometer/nutrients.go`** — Cronometer's numeric nutrient-ID
  → `cronometer_daily_nutrition`/`cronometer_serving` column mapping
  (`NutritionAmounts` + the ID table).
- **`internal/cronometer/sync.go`/`sync_upsert.go`** — `DBSyncer.SyncDay(ctx,
  day) (bool, error)` — the exact shape `internal/syncengine.DaySyncer`
  expects, matching `googlehealth.DBSyncer`'s pattern so both sources can be
  driven identically without either package importing `syncengine`
  directly. One upsert function per table
  (`INSERT ... ON CONFLICT DO UPDATE` — Cronometer sync is the sole writer
  of these columns, no raw/override split needed, unlike the `watch_*`
  tables).
- **Credential setup, two ways**: `healthd auth cronometer` (CLI, prompts
  for email + hidden password, verifies with a real login before saving)
  or the web dashboard's own Cronometer account card at the bottom of the
  Nutrition section (same verify-then-save flow, no terminal needed).
- **UI**: Energy (consumption), Expenditure (Cronometer's own
  BMR+activity+food-thermic-effect estimate, from
  `kcal_burned_cronometer`), and a computed Deficit (expenditure minus
  consumption) tile, then Protein/Carbs/Fat — laid out as two explicit rows
  (kcal metrics, then macros) — followed by the food log (plain water
  servings filtered out — see `foodLogTileBody`'s comment in
  `internal/web/foodlog.go` — a food log full of zero-calorie hydration
  entries is just noise) with a click-to-detail popup per item (calories,
  macros, and whichever of fiber/sugars/saturated-fat/sodium/cholesterol
  Cronometer's food lookup resolved).
- **Sync trigger**: `healthd sync` (one-shot), or the dashboard's Sync
  button, which runs Google Health and Cronometer sync in parallel
  goroutines for the selected day.

## Write endpoints (2026-08-05)

`find_food`, `add_food`, `add_serving`, `delete_entries` were originally
transcribed from a reference project (`rwestergren/cronometer-api-mcp`) as
"DOCUMENTED, not CONFIRMED" — never decoded from a real response the way
the read side above was. `cmd/cronoverify` verified them against a real
account:

- **`find_food`/`add_food`/`get_foods`-immediately-after-`add_food`**: all
  CONFIRMED clean on the first run. A freshly created custom food's measure
  ID is reliably resolvable via `get_foods` right away — no propagation
  delay observed.
- **`add_serving`'s day format**: CONFIRMED to accept the same zero-padded
  `"YYYY-MM-DD"` `GetDiary` already uses (`dateLayout` in `sync.go`). The
  original DOCUMENTED claim of non-padded `"YYYY-M-D"` was never actually
  exercised and turned out unnecessary.
- **`add_serving`'s response `"id"` field**: found and fixed a real bug the
  first live run caught — it's a JSON **number**, not the string the
  original DOCUMENTED guess assumed. `Client.AddServing` now returns
  `int64`, matching `DiaryEntry.ServingID`'s type on the read side exactly
  — there is no read/write type asymmetry here after all, just a wrong
  initial guess. `DiaryEntryRef.ServingID` (the `delete_entries` input) is
  `int64` too, for consistency, though that specific field's real wire
  shape hasn't been independently exercised yet (see below).
- **`delete_entries`**: still not actually confirmed end to end. The
  verification run that would have reached it failed one step earlier, at
  the `add_serving` decode bug above; the throwaway custom food + serving
  it created were cleaned up manually via the Cronometer app instead of
  through this code path. Treat `DeleteEntries`/`DiaryEntryRef` as
  DOCUMENTED, not CONFIRMED, until a clean `cmd/cronoverify` pass actually
  reaches and exercises it.

This is the same day the MCP food-logging connector (`internal/mcpserver`,
`healthd mcp`) started using these endpoints for real — see
`ARCHITECTURE.md` §11.

## Data model

Five tables, matching Cronometer's own export categories exactly:
`cronometer_daily_nutrition`, `cronometer_serving`, `cronometer_exercise`,
`cronometer_biometric`, `cronometer_note`. `daily_nutrition` and `serving`
share the same ~70-column nutrient dictionary so they're directly
comparable at daily vs. per-entry granularity.

## Known gaps (confirmed against real account data, not guessed)

- `cronometer_serving.category` stays NULL — Cronometer's `food.Category`
  is a numeric ID with no confirmed name catalog captured yet; storing the
  raw number would just be a meaningless value in the UI.
- `cronometer_serving.meal_group` stays NULL — never observed as a usable
  field on a diary entry via this API. This is also why the MCP connector's
  `cronometer_log_serving` tool treats "meal" (breakfast/lunch/dinner/snack)
  as a *time-of-day default* rather than a real field to send — Cronometer
  buckets diary entries into meal groups by clock time against boundaries
  configured in the account's own settings, not a per-entry value this API
  exposes (see `ARCHITECTURE.md` §11's "Meal categorization" paragraph).
- `cronometer_note` stays entirely unpopulated — no `"Note"` diary entry
  type was observed in the real dump this was built against. Whether
  Cronometer's notes feature is reachable via this API at all is still
  unconfirmed, not just unpopulated by omission — don't assume the table
  is simply "not synced yet" without checking a real account that has a
  note logged on some day first.
