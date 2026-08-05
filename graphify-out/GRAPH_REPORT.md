# Graph Report - .  (2026-08-02)

## Corpus Check
- 106 files · ~92,145 words
- Verdict: corpus is large enough that graph structure adds value.

## Summary
- 986 nodes · 2292 edges · 42 communities (41 shown, 1 thin omitted)
- Extraction: 83% EXTRACTED · 17% INFERRED · 0% AMBIGUOUS · INFERRED: 384 edges (avg confidence: 0.8)
- Token cost: 0 input · 134,726 output

## Community Hubs (Navigation)
- Dashboard Tile Builders
- Templ UI Components
- Google Health Data Types
- Account Settings & Auth UI
- Architecture & Design Rationale
- Cronometer API Client
- Google Health Sync Jobs
- DB Store & Migrations
- Path Resolution Utilities
- Sync State Engine
- Chart Rendering & Empty Tiles
- Health Data Upsert Logic
- Database Schema Tables
- Google Health REST Client
- Encrypted Credential Storage
- OAuth Token Management
- Auth CLI Commands
- Web Server Bootstrap
- OAuth Consent Flow
- Google Health Data Catalog
- CLI Entry & Root Command
- Cronometer Diagnostic Tool
- Cronometer Sync Test Harness
- Google Health Sync CLI
- Body Measurement Tile Logic
- Google Health Envelope Parsing
- User Account CLI
- Google Client Settings Page
- Datastar JS Runtime
- Google Client CLI Commands
- Go Module Definition

## God Nodes (most connected - your core abstractions)
1. `Paths` - 34 edges
2. `Key` - 29 edges
3. `TileData` - 28 edges
4. `Open()` - 27 edges
5. `users` - 26 edges
6. `PBFloat64` - 25 edges
7. `buildDashboardData()` - 21 edges
8. `Server` - 20 edges
9. `New()` - 18 edges
10. `iconFor()` - 18 edges

## Surprising Connections (you probably didn't know these)
- `Cronometer two-file encrypted credential storage` --semantically_similar_to--> `Two encryption layers, one primitive (root key + per-user key)`  [INFERRED] [semantically similar]
  cronometer-integration.md → ARCHITECTURE.md
- `main()` --calls--> `Execute()`  [INFERRED]
  cmd/healthd/main.go → internal/cli/root.go
- `main()` --calls--> `Open()`  [INFERRED]
  cmd/tmpinspect/main.go → internal/db/store.go
- `newRootCmd()` --calls--> `newAuthCmd()`  [INFERRED]
  internal/cli/root.go → internal/cli/auth.go
- `newAuthCronometerCmd()` --calls--> `resolveUserID()`  [INFERRED]
  internal/cli/auth.go → internal/cli/users.go

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Five fixed watch_* representation shapes taxonomy** — architecture_five_representation_shapes, architecture_dailyscalar_shape, architecture_dailybycategory_shape, architecture_hourly_shape, architecture_timeline_shape, architecture_segmenttimeline_shape [EXTRACTED 1.00]
- **Both source syncers implement the DaySyncer shape** — internal_syncengine_daysyncer, internal_googlehealth_dbsyncer, internal_cronometer_dbsyncer [EXTRACTED 1.00]
- **Distinct secrets sharing the internal/crypto encryption primitive** — internal_crypto, architecture_oauth_client_vs_token, architecture_two_encryption_layers, cronometer_credential_storage [INFERRED 0.75]

## Communities (42 total, 1 thin omitted)

### Community 0 - "Dashboard Tile Builders"
Cohesion: 0.06
Nodes (66): DailySummary, Context, Server, buildActiveMinutesByLevelTile(), buildActiveZoneMinutesByZoneTile(), buildActivitiesTile(), buildActivityLevelSegmentTile(), buildCategoryTile() (+58 more)

### Community 1 - "Templ UI Components"
Cohesion: 0.07
Nodes (66): bodyTileBody(), Component, numberAttr(), CronometerAccountCard(), Component, clampPct(), Component, iconActivity() (+58 more)

### Community 2 - "Google Health Data Types"
Cohesion: 0.06
Nodes (56): Duration, ActiveEnergyBurned, ActiveMinutes, ActiveMinutesByLevel, ActiveZoneMinutes, ActivityLevel, Altitude, BloodGlucose (+48 more)

### Community 3 - "Account Settings & Auth UI"
Cohesion: 0.06
Nodes (48): Context, Server, Context, Server, Server, AccountSettingsPage(), Component, Component (+40 more)

### Community 4 - "Architecture & Design Rationale"
Cohesion: 0.06
Nodes (60): Explicit per-table account deletion (not FK cascade), Admin mode edit-control gating, DailyByCategory shape, DailyScalar shape, healthd db decrypt escape hatch, Empty-tile consolidation (MissingByCategory/EmptySummaryTile), Whole-file AES-256-GCM encryption at rest, Five fixed watch_* representation shapes (+52 more)

### Community 5 - "Cronometer API Client"
Cohesion: 0.07
Nodes (48): APIError, Client, DBSyncer, DiaryBurned, DiaryEntry, DiaryInfo, DiaryResponse, DiarySummary (+40 more)

### Community 6 - "Google Health Sync Jobs"
Cohesion: 0.10
Nodes (31): ValueKey(), T, TestExtractRollupValuesTotalCalories(), TestExtractValuesElectrocardiogramDropsWaveform(), TestExtractValuesEmptyResponseIsEmptyNotError(), TestExtractValuesSkipsPointsMissingTheKey(), TestExtractValuesSleepStagesAndSummary(), TestExtractValuesStripsMetadataDailyRestingHeartRate() (+23 more)

### Community 7 - "DB Store & Migrations"
Cohesion: 0.09
Nodes (33): main(), printRows(), Store, Command, newDBCmd(), newDBDecryptCmd(), newDBInitCmd(), promptNewPassphrase() (+25 more)

### Community 8 - "Path Resolution Utilities"
Cohesion: 0.10
Nodes (15): cutTilde(), EnsureParentDir(), expandUser(), Resolve(), SafeJoin(), T, TestAccessorsNestUnderRoot(), TestEnsureDirsCreatesStructureWithOwnerOnlyPerms() (+7 more)

### Community 9 - "Sync State Engine"
Cohesion: 0.10
Nodes (29): backfillPendingDays(), Context, Time, RunDay(), Context, T, Time, mustParse() (+21 more)

### Community 10 - "Chart Rendering & Empty Tiles"
Cohesion: 0.13
Nodes (40): shouldHideEmptyTile(), chartInstanceID(), chartWrapSignals(), escapeAttr(), escapeSVGText(), formatAxisNumber(), hoverAttrs(), jsEscape() (+32 more)

### Community 11 - "Health Data Upsert Logic"
Cohesion: 0.11
Nodes (19): ActiveMinutesByLevel, ActiveZoneMinutesByZone, ActivityLevelSegment, BloodGlucoseSample, CaloriesByZone, CoreBodyTemperatureSample, ECGReading, ExerciseSession (+11 more)

### Community 12 - "Database Schema Tables"
Cohesion: 0.14
Nodes (29): body_measurement, cronometer_biometric, cronometer_daily_nutrition, cronometer_exercise, cronometer_note, cronometer_serving, daily_journal, daily_tag (+21 more)

### Community 13 - "Google Health REST Client"
Cohesion: 0.14
Nodes (21): APIError, CivilDateTime, CivilTime, Client, fakeTransport, Context, RawMessage, NewClient() (+13 more)

### Community 14 - "Encrypted Credential Storage"
Cohesion: 0.17
Nodes (22): Credentials, Key, LoadCredentials(), loadEncrypted(), LoadSession(), SaveCredentials(), saveEncrypted(), SaveSession() (+14 more)

### Community 15 - "OAuth Token Management"
Cohesion: 0.18
Nodes (16): fakeTokenSource, savingTokenSource, Client, Context, Mutex, Token, HTTPClient(), LoadToken() (+8 more)

### Community 16 - "Auth CLI Commands"
Cohesion: 0.20
Nodes (14): keyFile, Command, newAuthCmd(), newAuthCronometerCmd(), newAuthGoogleCmd(), Context, DB, runCronometerSyncOnce() (+6 more)

### Community 17 - "Web Server Bootstrap"
Cohesion: 0.19
Nodes (8): Echo, Context, DB, DBSyncer, Mutex, Server, New(), Map

### Community 18 - "OAuth Consent Flow"
Cohesion: 0.22
Nodes (15): callbackResult, Context, Token, randomState(), RunConsentFlow(), freePort(), T, Token (+7 more)

### Community 19 - "Google Health Data Catalog"
Cohesion: 0.17
Nodes (14): dataType, kind, DumpToday(), filterFor(), Client, Context, RawMessage, Time (+6 more)

### Community 20 - "CLI Entry & Root Command"
Cohesion: 0.18
Nodes (11): main(), Execute(), Command, newRootCmd(), runRoot(), Command, newServeCmd(), runServeForeground() (+3 more)

### Community 21 - "Cronometer Diagnostic Tool"
Cohesion: 0.36
Nodes (13): authedPost(), extractDiaryIDs(), Client, RawMessage, isSessionFailure(), loadSession(), login(), main() (+5 more)

### Community 22 - "Cronometer Sync Test Harness"
Cohesion: 0.26
Nodes (9): countingTransport, pathTransport, DB, Request, Response, T, newTestDB(), TestSyncDayHappyPath() (+1 more)

### Community 23 - "Google Health Sync CLI"
Cohesion: 0.38
Nodes (8): Config, GoogleConfig, Context, DB, runGoogleHealthSyncOnce(), syncGoogleHealthForUser(), Default(), Load()

### Community 24 - "Body Measurement Tile Logic"
Cohesion: 0.53
Nodes (9): buildBodyTile(), carryForwardBodyMeasurement(), fetchBodyMeasurement(), Context, DB, Time, latestPriorValue(), queryBodyMeasurementRow() (+1 more)

### Community 25 - "Google Health Envelope Parsing"
Cohesion: 0.46
Nodes (7): listEnvelope, rollupEnvelope, RollupPoint, ExtractRollupValues(), ExtractValues(), RawMessage, T

### Community 26 - "User Account CLI"
Cohesion: 0.43
Nodes (7): allUserIDs(), Command, Context, DB, newUserCmd(), newUserCreateCmd(), resolveUserID()

### Community 27 - "Google Client Settings Page"
Cohesion: 0.38
Nodes (4): Context, Server, Component, GoogleClientSettingsPage()

### Community 28 - "Datastar JS Runtime"
Cohesion: 0.38
Nodes (3): apply(), get(), has()

### Community 29 - "Google Client CLI Commands"
Cohesion: 0.83
Nodes (3): Command, newGoogleClientCmd(), newGoogleClientSetCmd()

## Knowledge Gaps
- **25 isolated node(s):** `github.com/sdhungan/Personal-Health-Data`, `keyFile`, `callbackResult`, `SleepStage`, `ActiveMinutesByLevel` (+20 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **1 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `CurrentUserID()` connect `Dashboard Tile Builders` to `Account Settings & Auth UI`?**
  _High betweenness centrality (0.277) - this node is a cross-community bridge._
- **Why does `Key` connect `Encrypted Credential Storage` to `Cronometer API Client`, `DB Store & Migrations`, `OAuth Token Management`, `Auth CLI Commands`, `Web Server Bootstrap`?**
  _High betweenness centrality (0.190) - this node is a cross-community bridge._
- **Why does `Open()` connect `DB Store & Migrations` to `Account Settings & Auth UI`, `Sync State Engine`, `Google Health REST Client`, `Encrypted Credential Storage`, `Auth CLI Commands`, `CLI Entry & Root Command`, `Cronometer Sync Test Harness`, `Google Health Sync CLI`, `User Account CLI`?**
  _High betweenness centrality (0.174) - this node is a cross-community bridge._
- **Are the 22 inferred relationships involving `Open()` (e.g. with `main()` and `newAuthCronometerCmd()`) actually correct?**
  _`Open()` has 22 INFERRED edges - model-reasoned connections that need verification._
- **What connects `github.com/sdhungan/Personal-Health-Data`, `keyFile`, `callbackResult` to the rest of the system?**
  _25 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Dashboard Tile Builders` be split into smaller, more focused modules?**
  _Cohesion score 0.058363858363858365 - nodes in this community are weakly interconnected._
- **Should `Templ UI Components` be split into smaller, more focused modules?**
  _Cohesion score 0.06768388106416276 - nodes in this community are weakly interconnected._