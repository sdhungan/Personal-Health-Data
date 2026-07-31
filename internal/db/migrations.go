package db

// Migrations is the ordered list of incremental schema changes applied to a
// database that predates them (see Store.Migrate). Each entry is one
// migration's SQL — possibly several statements — applied inside its own
// transaction; PRAGMA user_version tracks how many have been applied so
// Migrate only ever runs the ones a given database hasn't seen yet.
//
// A fresh database created by Init applies Schema directly (already at the
// shape every migration below produces) and sets user_version to
// len(Migrations), skipping this list entirely — these are only for
// upgrading a database that already has data in it.
var Migrations = []string{
	// 0: disambiguate Google's watch-based calorie estimate from
	// Cronometer's own (BMR + activity + food thermic-effect) expenditure
	// figure — see schema.sql's comment on both columns. v_daily_overview
	// is dropped and recreated against the new column name explicitly
	// rather than relying on RENAME COLUMN to patch view definitions.
	`ALTER TABLE watch_daily_summary RENAME COLUMN total_calories TO kcal_burned_google;
	ALTER TABLE cronometer_daily_nutrition ADD COLUMN kcal_burned_cronometer REAL;
	DROP VIEW IF EXISTS v_daily_overview;
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
	LEFT JOIN daily_journal j ON j.day = days.day;`,

	// 1: activity/fitness data types that were always fetchable under our
	// existing scopes but never persisted (floors_climbed already existed
	// as a column but sync never filled it).
	`ALTER TABLE watch_daily_summary ADD COLUMN altitude_gain_m REAL;
	ALTER TABLE watch_daily_summary ADD COLUMN sedentary_minutes INTEGER;
	ALTER TABLE watch_daily_summary ADD COLUMN active_energy_burned_kcal REAL;
	ALTER TABLE watch_daily_summary ADD COLUMN light_active_minutes INTEGER;
	ALTER TABLE watch_daily_summary ADD COLUMN moderate_active_minutes INTEGER;
	ALTER TABLE watch_daily_summary ADD COLUMN vigorous_active_minutes INTEGER;`,

	// 2: VO2 max sample sources, alongside the existing daily aggregate.
	`ALTER TABLE watch_daily_summary ADD COLUMN vo2_max_sample REAL;
	ALTER TABLE watch_daily_summary ADD COLUMN vo2_max_run_sample REAL;`,

	// 3: nightly sleep temperature, parsed instead of stashed as raw_payload.
	`ALTER TABLE watch_daily_summary ADD COLUMN sleep_temperature_c REAL;
	ALTER TABLE watch_daily_summary ADD COLUMN sleep_temperature_baseline_c REAL;
	ALTER TABLE watch_daily_summary ADD COLUMN sleep_temperature_deviation_30d_c REAL;`,

	// 4: sleep-stage respiratory rate, alongside the session it belongs to.
	`ALTER TABLE watch_sleep_session ADD COLUMN deep_resp_rate_bpm REAL;
	ALTER TABLE watch_sleep_session ADD COLUMN light_resp_rate_bpm REAL;
	ALTER TABLE watch_sleep_session ADD COLUMN rem_resp_rate_bpm REAL;
	ALTER TABLE watch_sleep_session ADD COLUMN full_resp_rate_bpm REAL;`,

	// 5: heart-rate zone thresholds and actual time-in-zone minutes — two
	// different data types that both happen to be about heart-rate zones
	// (see schema.sql's comment on why they're not the same thing).
	`CREATE TABLE watch_heart_rate_zone_definition (
		day       TEXT NOT NULL,
		zone_type TEXT NOT NULL,
		min_bpm   INTEGER,
		max_bpm   INTEGER,
		PRIMARY KEY (day, zone_type)
	);
	CREATE TABLE watch_heart_rate_zone_minutes (
		day       TEXT NOT NULL,
		zone_type TEXT NOT NULL,
		minutes   REAL,
		PRIMARY KEY (day, zone_type)
	);
	CREATE TABLE watch_calories_by_zone (
		day       TEXT NOT NULL,
		zone_type TEXT NOT NULL,
		kcal      REAL,
		PRIMARY KEY (day, zone_type)
	);`,

	// 6: sample-level types this account has never returned data for, but
	// get full plumbing anyway per "take over all non-nil measured data."
	`CREATE TABLE watch_blood_glucose_sample (
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
	);`,
}
