# Cronometer integration — how it works

Reference doc for the Cronometer side of this codebase — the client, auth,
sync engine, and UI, all built and in use as of 2026-08-01. See also
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
- **`internal/cronometer/client.go`** — `Client.Login`, `GetDiary`,
  `GetFoods`, `GetNutritionScores`, `GetMetrics` — thin wrappers over the
  mobile API's `POST /api/v2/*` endpoints.
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
  field on a diary entry via this API.
- `cronometer_note` stays entirely unpopulated — no `"Note"` diary entry
  type was observed in the real dump this was built against. Whether
  Cronometer's notes feature is reachable via this API at all is still
  unconfirmed, not just unpopulated by omission — don't assume the table
  is simply "not synced yet" without checking a real account that has a
  note logged on some day first.
