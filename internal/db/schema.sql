-- healthd initial schema
--
-- Source-of-truth notes (see ARCHITECTURE.md §3):
--   * Tables prefixed `watch_` are populated exclusively by the Google Health
--     API sync job (the modern successor to the Fitbit Web API — Pixel Watch
--     3 syncs through Fitbit/the Google Health app, not the deprecated
--     Google Fit REST API). These tables have NO override columns: the user
--     explicitly does not edit watch-recorded metrics from the UI, ever.
--   * Tables prefixed `cronometer_` are populated exclusively by the
--     Cronometer sync job (unofficial export API via a reverse-engineered
--     client). Kept in entirely separate tables from watch data so a
--     Cronometer outage/breakage never touches watch data or vice versa.
--   * `body_measurement` is the one place watch data and manual entry mix:
--     weight/height can arrive from a Google Health-connected smart scale
--     (raw_*) or be typed into the UI (override_*), so it keeps the
--     raw/override split. Waist/neck/hip have no upstream source at all
--     (neither Google Health nor Cronometer expose circumference data), so
--     they are plain user-entered columns.
--   * `daily_journal` and `daily_tag` are pure UI-authored content with no
--     sync source, so they don't need the raw/override split either.
--   * Effective-value resolution (COALESCE(override, raw)) belongs in the DB
--     access layer per the architecture doc; the `v_*` views below exist
--     only as read-time convenience, not as the enforcement point.
--
-- Dates are stored as ISO-8601 'YYYY-MM-DD' TEXT (SQLite has no native DATE
-- type). Timestamps are ISO-8601 TEXT in UTC.

PRAGMA foreign_keys = ON;

-- =============================================================================
-- Reference / bookkeeping
-- =============================================================================

-- Singleton row of facts about the person, needed for calculations (e.g. the
-- US Navy body-fat formula needs sex, and hip circumference only applies to
-- the female variant of that formula).
CREATE TABLE user_profile (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    sex        TEXT CHECK (sex IN ('male', 'female')),
    birth_date TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Drives the sync job's day-completeness logic for each source
-- independently.
--   pending  = never synced, or synced but not yet resolved either way.
--   partial  = today's row while the day is still in progress; always
--              re-synced, never auto-promoted to complete/missing while
--              it's still today.
--   complete = finalized WITH real data found. Terminal — the automatic
--              sync job never touches it again.
--   missing  = finalized WITHOUT data (e.g. the watch was off/dead that
--              day), confirmed by a later day already having real data.
--              Also terminal.
-- Both terminal states are only ever reached automatically the day after
-- (or later); a manual "force sync" of a specific day (a UI/CLI action,
-- not something the scheduler does on its own) is the only way to revisit
-- one afterwards.
CREATE TABLE sync_state (
    source         TEXT NOT NULL CHECK (source IN ('google_health', 'cronometer')),
    day            TEXT NOT NULL,
    status         TEXT NOT NULL CHECK (status IN ('pending', 'partial', 'complete', 'missing')) DEFAULT 'pending',
    last_synced_at TEXT,
    updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (source, day)
);

-- =============================================================================
-- Watch / Google Health API data
-- =============================================================================

-- One row per calendar day, covering everything the Google Health API
-- exposes as a daily aggregate or that we roll up ourselves (activity
-- totals, resting HR, HRV, VO2 max, SpO2, respiratory rate, sleep totals).
-- `raw_payload` keeps the full JSON response for that day so a first-pass
-- column set can't silently lose data — future columns can be backfilled
-- from it without re-hitting the API.
-- kcal_burned_google/kcal_burned_cronometer (on cronometer_daily_nutrition)
-- are deliberately two separate columns, not one: Google's watch-based
-- estimate and Cronometer's own (BMR + activity + food thermic-effect)
-- expenditure figure are different calculations from different sources,
-- and Cronometer sync doesn't exist in this codebase yet (see
-- prerequisite.md) so its column sits NULL until that's built.
CREATE TABLE watch_daily_summary (
    day                      TEXT PRIMARY KEY,
    steps_total              INTEGER,
    distance_m                REAL,
    floors_climbed            INTEGER,
    altitude_gain_m           REAL,
    sedentary_minutes         INTEGER,
    active_minutes            INTEGER,
    light_active_minutes      INTEGER,
    moderate_active_minutes   INTEGER,
    vigorous_active_minutes   INTEGER,
    active_zone_minutes       INTEGER,
    kcal_burned_google        REAL,
    active_energy_burned_kcal REAL,
    resting_heart_rate_bpm    REAL,
    heart_rate_min_bpm        REAL,
    heart_rate_max_bpm        REAL,
    heart_rate_avg_bpm        REAL,
    hrv_avg_ms                REAL,
    vo2_max                   REAL,
    vo2_max_sample            REAL,
    vo2_max_run_sample        REAL,
    spo2_avg_pct              REAL,
    spo2_min_pct              REAL,
    respiratory_rate_avg_bpm  REAL,
    sleep_duration_minutes    INTEGER,
    sleep_score               INTEGER,
    stress_management_score   INTEGER,
    sleep_temperature_c                 REAL,
    sleep_temperature_baseline_c        REAL,
    sleep_temperature_deviation_30d_c   REAL,
    source_synced_at          TEXT,
    raw_payload               TEXT,
    created_at                TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at                TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Hourly step buckets. Low volume (24 rows/day) so, unlike heart rate, it's
-- kept in full permanently rather than as a prunable cache.
CREATE TABLE watch_steps_hourly (
    day       TEXT NOT NULL,
    hour      INTEGER NOT NULL CHECK (hour BETWEEN 0 AND 23),
    steps     INTEGER,
    distance_m REAL,
    calories  REAL,
    PRIMARY KEY (day, hour)
);

-- Rolling CACHE of fine-grained (e.g. 1-5min) heart rate samples. This is
-- intentionally NOT the source of truth for historical data: intraday HR at
-- full resolution is high volume, so old rows may be pruned by the sync job
-- or a maintenance task. If the UI needs detailed HR for a day whose cache
-- has been pruned, fetch it live from the Google Health API instead of
-- treating this table as complete history.
CREATE TABLE watch_heart_rate_intraday (
    recorded_at TEXT PRIMARY KEY,
    bpm         REAL NOT NULL,
    cached_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_watch_heart_rate_intraday_cached_at ON watch_heart_rate_intraday (cached_at);

-- One row per sleep session (usually one "main sleep" per day, but naps can
-- add more).
CREATE TABLE watch_sleep_session (
    id                INTEGER PRIMARY KEY,
    day               TEXT NOT NULL,
    start_time        TEXT NOT NULL,
    end_time          TEXT NOT NULL,
    is_main_sleep     INTEGER NOT NULL DEFAULT 0,
    duration_minutes  INTEGER,
    efficiency_pct    REAL,
    sleep_score       INTEGER,
    minutes_awake     INTEGER,
    minutes_light     INTEGER,
    minutes_deep      INTEGER,
    minutes_rem       INTEGER,
    -- Per-stage respiratory rate (from respiratory-rate-sleep-summary,
    -- matched to whichever session's interval contains its sample time).
    deep_resp_rate_bpm    REAL,
    light_resp_rate_bpm   REAL,
    rem_resp_rate_bpm     REAL,
    full_resp_rate_bpm    REAL,
    UNIQUE (day, start_time)
);

CREATE INDEX idx_watch_sleep_session_day ON watch_sleep_session (day);

-- Stage-level timeline within a sleep session, for detailed hypnogram-style
-- UI views.
CREATE TABLE watch_sleep_stage (
    sleep_session_id INTEGER NOT NULL REFERENCES watch_sleep_session (id) ON DELETE CASCADE,
    stage_type       TEXT NOT NULL CHECK (stage_type IN ('AWAKE', 'LIGHT', 'DEEP', 'REM')),
    start_time       TEXT NOT NULL,
    end_time         TEXT NOT NULL
);

CREATE INDEX idx_watch_sleep_stage_session ON watch_sleep_stage (sleep_session_id);

-- One row per workout/exercise session recorded by the watch.
CREATE TABLE watch_exercise_session (
    id                  INTEGER PRIMARY KEY,
    day                 TEXT NOT NULL,
    exercise_type       TEXT NOT NULL,
    start_time          TEXT NOT NULL,
    end_time            TEXT NOT NULL,
    duration_minutes    INTEGER,
    calories_burned     REAL,
    avg_heart_rate_bpm  REAL,
    max_heart_rate_bpm  REAL,
    distance_m          REAL,
    UNIQUE (day, start_time)
);

CREATE INDEX idx_watch_exercise_session_day ON watch_exercise_session (day);

-- ECG readings and irregular-rhythm notifications. Kept as one table (with a
-- `type` discriminator) rather than two, since neither is high-volume and
-- only summary/classification is stored — not raw waveform data.
CREATE TABLE watch_ecg_reading (
    id                 INTEGER PRIMARY KEY,
    type               TEXT NOT NULL CHECK (type IN ('ecg', 'irregular_rhythm')),
    recorded_at        TEXT NOT NULL,
    classification     TEXT,
    avg_heart_rate_bpm REAL,
    UNIQUE (type, recorded_at)
);

CREATE INDEX idx_watch_ecg_reading_recorded_at ON watch_ecg_reading (recorded_at);

-- Personalized heart-rate zone BPM thresholds for the day (from
-- daily-heart-rate-zones) -- despite the name's similarity to
-- watch_heart_rate_zone_minutes below, this is NOT a time-series metric:
-- it's just "what BPM range counts as each zone type today" (drifts slowly
-- as fitness/resting HR changes), confirmed against a real response.
CREATE TABLE watch_heart_rate_zone_definition (
    day       TEXT NOT NULL,
    zone_type TEXT NOT NULL,
    min_bpm   INTEGER,
    max_bpm   INTEGER,
    PRIMARY KEY (day, zone_type)
);

-- Actual minutes spent in each heart-rate zone per day (from
-- time-in-heart-rate-zone) -- the real time-series counterpart to the
-- thresholds above.
CREATE TABLE watch_heart_rate_zone_minutes (
    day       TEXT NOT NULL,
    zone_type TEXT NOT NULL,
    minutes   REAL,
    PRIMARY KEY (day, zone_type)
);

-- Calories attributed to time spent in each heart-rate zone per day (from
-- calories-in-heart-rate-zone). INFERRED shape (see
-- internal/googlehealth/values.go's confidence-level convention) --
-- Google's reference doesn't document this type's fields directly.
CREATE TABLE watch_calories_by_zone (
    day       TEXT NOT NULL,
    zone_type TEXT NOT NULL,
    kcal      REAL,
    PRIMARY KEY (day, zone_type)
);

-- Full-fidelity sample tables for types this account's current
-- watch/phone combination has never returned data for, but are fetchable
-- under our existing scopes and get plumbing anyway (see prerequisite.md).
CREATE TABLE watch_blood_glucose_sample (
    recorded_at        TEXT PRIMARY KEY,
    mg_dl              REAL NOT NULL,
    measurement_source TEXT,
    measurement_timing TEXT,
    meal_type          TEXT,
    specimen           TEXT
);

CREATE TABLE watch_core_body_temperature_sample (
    recorded_at          TEXT PRIMARY KEY,
    celsius              REAL NOT NULL,
    measurement_location TEXT
);

-- =============================================================================
-- Body measurements (weight/height/body-fat can come from a Google
-- Health-connected scale OR manual entry; waist/neck/hip are manual-only)
-- =============================================================================

CREATE TABLE body_measurement (
    day                       TEXT PRIMARY KEY,
    weight_kg_raw             REAL,
    weight_kg_override        REAL,
    height_cm_raw             REAL,
    height_cm_override        REAL,
    waist_cm                  REAL,
    neck_cm                   REAL,
    hip_cm                    REAL,
    body_fat_pct_raw          REAL,
    body_fat_pct_calculated   REAL,
    created_at                TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at                TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Effective-value convenience view. body_fat_pct prefers a direct
-- measurement (e.g. a smart scale) over our own circumference-based
-- estimate.
CREATE VIEW v_body_measurement AS
SELECT
    day,
    COALESCE(weight_kg_override, weight_kg_raw) AS weight_kg,
    COALESCE(height_cm_override, height_cm_raw) AS height_cm,
    waist_cm,
    neck_cm,
    hip_cm,
    COALESCE(body_fat_pct_raw, body_fat_pct_calculated) AS body_fat_pct
FROM body_measurement;

-- =============================================================================
-- Cronometer data (via reverse-engineered export API, e.g. gocronometer)
-- =============================================================================

-- Per-day nutrition rollup, as computed by Cronometer itself (more
-- authoritative than summing cronometer_serving ourselves). `completed`
-- mirrors Cronometer's own "diary day marked complete" flag, which is a
-- useful signal for the sync job's day-completeness heuristic in addition
-- to our own sync_state bookkeeping.
CREATE TABLE cronometer_daily_nutrition (
    day               TEXT PRIMARY KEY,
    completed         INTEGER NOT NULL DEFAULT 0,
    energy_kcal       REAL,
    -- Cronometer's own total-expenditure estimate (BMR + activity + food
    -- thermic effect) -- deliberately a separate figure from
    -- watch_daily_summary.kcal_burned_google, not a replacement for it.
    -- Sits NULL until a Cronometer sync client exists (see
    -- prerequisite.md -- internal/cli/sync.go's "TODO: run the Cronometer
    -- sync pass too" is still just a TODO as of this column's addition).
    kcal_burned_cronometer REAL,
    caffeine_mg       REAL,
    water_g           REAL,
    b1_mg             REAL,
    b2_mg             REAL,
    b3_mg             REAL,
    b5_mg             REAL,
    b6_mg             REAL,
    b12_mg            REAL,
    biotin_ug         REAL,
    choline_mg        REAL,
    folate_ug         REAL,
    vitamin_a_iu      REAL,
    vitamin_c_mg      REAL,
    vitamin_d_iu      REAL,
    vitamin_e_mg      REAL,
    vitamin_k_mg      REAL,
    calcium_mg        REAL,
    chromium_ug       REAL,
    copper_mg         REAL,
    fluoride_ug       REAL,
    iodine_ug         REAL,
    iron_mg           REAL,
    magnesium_mg      REAL,
    manganese_mg      REAL,
    phosphorus_mg     REAL,
    potassium_mg      REAL,
    selenium_ug       REAL,
    sodium_mg         REAL,
    zinc_mg           REAL,
    carbs_g           REAL,
    fiber_g           REAL,
    fructose_g        REAL,
    galactose_g       REAL,
    glucose_g         REAL,
    lactose_g         REAL,
    maltose_g         REAL,
    starch_g          REAL,
    sucrose_g         REAL,
    sugars_g          REAL,
    net_carbs_g       REAL,
    added_sugars_g    REAL,
    allulose_g        REAL,
    sugar_alcohol_g   REAL,
    fat_g             REAL,
    cholesterol_mg    REAL,
    monounsaturated_g REAL,
    polyunsaturated_g REAL,
    saturated_g       REAL,
    trans_fat_g       REAL,
    omega3_g          REAL,
    omega6_g          REAL,
    protein_g         REAL,
    cystine_g         REAL,
    histidine_g       REAL,
    isoleucine_g      REAL,
    leucine_g         REAL,
    lysine_g          REAL,
    methionine_g      REAL,
    phenylalanine_g   REAL,
    threonine_g       REAL,
    tryptophan_g      REAL,
    tyrosine_g        REAL,
    valine_g          REAL,
    synced_at         TEXT
);

-- Per-entry food diary, same nutrient dictionary as cronometer_daily_nutrition
-- so the two are directly comparable, but at meal-entry granularity for a
-- detailed food-log view.
CREATE TABLE cronometer_serving (
    id                INTEGER PRIMARY KEY,
    day               TEXT NOT NULL,
    recorded_time     TEXT NOT NULL,
    meal_group        TEXT,
    food_name         TEXT NOT NULL,
    category          TEXT,
    quantity_value    REAL,
    quantity_units    TEXT,
    energy_kcal       REAL,
    caffeine_mg       REAL,
    water_g           REAL,
    b1_mg             REAL,
    b2_mg             REAL,
    b3_mg             REAL,
    b5_mg             REAL,
    b6_mg             REAL,
    b12_mg            REAL,
    biotin_ug         REAL,
    choline_mg        REAL,
    folate_ug         REAL,
    vitamin_a_iu      REAL,
    vitamin_c_mg      REAL,
    vitamin_d_iu      REAL,
    vitamin_e_mg      REAL,
    vitamin_k_mg      REAL,
    calcium_mg        REAL,
    chromium_ug       REAL,
    copper_mg         REAL,
    fluoride_ug       REAL,
    iodine_ug         REAL,
    iron_mg           REAL,
    magnesium_mg      REAL,
    manganese_mg      REAL,
    phosphorus_mg     REAL,
    potassium_mg      REAL,
    selenium_ug       REAL,
    sodium_mg         REAL,
    zinc_mg           REAL,
    carbs_g           REAL,
    fiber_g           REAL,
    fructose_g        REAL,
    galactose_g       REAL,
    glucose_g         REAL,
    lactose_g         REAL,
    maltose_g         REAL,
    starch_g          REAL,
    sucrose_g         REAL,
    sugars_g          REAL,
    net_carbs_g       REAL,
    added_sugars_g    REAL,
    allulose_g        REAL,
    sugar_alcohol_g   REAL,
    fat_g             REAL,
    cholesterol_mg    REAL,
    monounsaturated_g REAL,
    polyunsaturated_g REAL,
    saturated_g       REAL,
    trans_fat_g       REAL,
    omega3_g          REAL,
    omega6_g          REAL,
    protein_g         REAL,
    cystine_g         REAL,
    histidine_g       REAL,
    isoleucine_g      REAL,
    leucine_g         REAL,
    lysine_g          REAL,
    methionine_g      REAL,
    phenylalanine_g   REAL,
    threonine_g       REAL,
    tryptophan_g      REAL,
    tyrosine_g        REAL,
    valine_g          REAL
);

CREATE INDEX idx_cronometer_serving_day ON cronometer_serving (day);

-- Exercise logged manually within Cronometer itself (distinct from
-- watch_exercise_session, which is watch-recorded).
CREATE TABLE cronometer_exercise (
    id              INTEGER PRIMARY KEY,
    day             TEXT NOT NULL,
    recorded_time   TEXT NOT NULL,
    exercise_name   TEXT NOT NULL,
    group_name      TEXT,
    minutes         REAL,
    calories_burned REAL
);

CREATE INDEX idx_cronometer_exercise_day ON cronometer_exercise (day);

-- Generic biometric log as exposed by Cronometer's own biometrics feature
-- (metric/unit/amount triples — weight, body fat %, blood pressure, etc,
-- whatever the user chooses to log there). Kept as its own raw import
-- rather than merged into body_measurement; reconciling the two is an
-- application-level decision for later.
CREATE TABLE cronometer_biometric (
    id            INTEGER PRIMARY KEY,
    day           TEXT NOT NULL,
    recorded_time TEXT NOT NULL,
    metric        TEXT NOT NULL,
    unit          TEXT,
    amount        REAL NOT NULL
);

CREATE INDEX idx_cronometer_biometric_day ON cronometer_biometric (day);

-- Cronometer's own free-text diary note per day. Separate from
-- daily_journal below, which is authored directly in our own UI.
CREATE TABLE cronometer_note (
    day       TEXT PRIMARY KEY,
    note      TEXT,
    synced_at TEXT
);

-- =============================================================================
-- Local-only journal content (no sync source, authored via our own UI)
-- =============================================================================

CREATE TABLE daily_journal (
    day                   TEXT PRIMARY KEY,
    summary               TEXT,
    notes                 TEXT,
    pitfalls              TEXT,
    positives             TEXT,
    mood_rating           INTEGER CHECK (mood_rating BETWEEN 1 AND 5),
    energy_rating         INTEGER CHECK (energy_rating BETWEEN 1 AND 5),
    stress_rating         INTEGER CHECK (stress_rating BETWEEN 1 AND 5),
    sleep_quality_rating  INTEGER CHECK (sleep_quality_rating BETWEEN 1 AND 5),
    created_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at            TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Free-form day tagging (illness, travel, alcohol, high-stress-day,
-- injury, took-medication, ...) instead of a dedicated table per category —
-- covers symptom/medication/event logging without over-modeling categories
-- nobody has asked for concretely yet.
CREATE TABLE daily_tag (
    day TEXT NOT NULL,
    tag TEXT NOT NULL,
    PRIMARY KEY (day, tag)
);

CREATE INDEX idx_daily_tag_tag ON daily_tag (tag);

-- =============================================================================
-- Cross-source dashboard convenience view
-- =============================================================================

CREATE VIEW v_daily_overview AS
SELECT
    w.day,
    w.steps_total,
    w.active_minutes,
    w.kcal_burned_google,
    w.resting_heart_rate_bpm,
    w.sleep_duration_minutes,
    c.energy_kcal          AS nutrition_energy_kcal,
    c.protein_g             AS nutrition_protein_g,
    c.carbs_g               AS nutrition_carbs_g,
    c.fat_g                 AS nutrition_fat_g,
    bm.weight_kg,
    bm.body_fat_pct,
    j.summary,
    j.mood_rating,
    j.energy_rating,
    j.stress_rating,
    j.sleep_quality_rating
FROM (SELECT DISTINCT day FROM watch_daily_summary
      UNION SELECT DISTINCT day FROM cronometer_daily_nutrition
      UNION SELECT DISTINCT day FROM body_measurement
      UNION SELECT DISTINCT day FROM daily_journal) AS days
LEFT JOIN watch_daily_summary w ON w.day = days.day
LEFT JOIN cronometer_daily_nutrition c ON c.day = days.day
LEFT JOIN v_body_measurement bm ON bm.day = days.day
LEFT JOIN daily_journal j ON j.day = days.day;
