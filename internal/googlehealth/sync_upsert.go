package googlehealth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/healthdata"
)

// This file is pure DB-write: every function takes already-fetched
// healthdata structs and upserts them, with no HTTP calls of its own — the
// fetch/parse side lives in sync.go (fetchDailySummary) and sync_details.go
// (everything else), which build these structs and call in here. Kept
// separate so "how do we get this value" and "how do we store it" don't mix
// in the same function.

// upsertDailySummary writes sum into watch_daily_summary. Every column
// binds a nullable value: COALESCE(excluded.col, watch_daily_summary.col)
// means a field this fetch didn't find (nil) leaves whatever was already
// stored untouched, while a freshly fetched value always overwrites —
// sync is the sole writer of these columns (see ARCHITECTURE.md §3), so
// there's no manual edit to protect against clobbering.
func (s *DBSyncer) upsertDailySummary(ctx context.Context, sum healthdata.DailySummary) error {
	var rawPayload any
	if len(sum.RawPayload) > 0 {
		b, err := json.Marshal(sum.RawPayload)
		if err != nil {
			return fmt.Errorf("marshaling raw_payload: %w", err)
		}
		rawPayload = string(b)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO watch_daily_summary (
			day, steps_total, distance_m, floors_climbed, altitude_gain_m, sedentary_minutes, active_minutes,
			kcal_burned_google, active_energy_burned_kcal,
			resting_heart_rate_bpm, heart_rate_min_bpm, heart_rate_max_bpm, heart_rate_avg_bpm, hrv_avg_ms,
			vo2_max, vo2_max_sample, vo2_max_run_sample,
			spo2_avg_pct, spo2_min_pct, respiratory_rate_avg_bpm,
			blood_glucose_avg_mg_dl, blood_glucose_min_mg_dl, blood_glucose_max_mg_dl,
			core_body_temperature_avg_c, core_body_temperature_min_c, core_body_temperature_max_c,
			sleep_duration_minutes, sleep_temperature_c, sleep_temperature_baseline_c, sleep_temperature_deviation_30d_c,
			source_synced_at, raw_payload, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(day) DO UPDATE SET
			steps_total              = COALESCE(excluded.steps_total, watch_daily_summary.steps_total),
			distance_m               = COALESCE(excluded.distance_m, watch_daily_summary.distance_m),
			floors_climbed           = COALESCE(excluded.floors_climbed, watch_daily_summary.floors_climbed),
			altitude_gain_m          = COALESCE(excluded.altitude_gain_m, watch_daily_summary.altitude_gain_m),
			sedentary_minutes        = COALESCE(excluded.sedentary_minutes, watch_daily_summary.sedentary_minutes),
			active_minutes           = COALESCE(excluded.active_minutes, watch_daily_summary.active_minutes),
			kcal_burned_google       = COALESCE(excluded.kcal_burned_google, watch_daily_summary.kcal_burned_google),
			active_energy_burned_kcal = COALESCE(excluded.active_energy_burned_kcal, watch_daily_summary.active_energy_burned_kcal),
			resting_heart_rate_bpm   = COALESCE(excluded.resting_heart_rate_bpm, watch_daily_summary.resting_heart_rate_bpm),
			heart_rate_min_bpm       = COALESCE(excluded.heart_rate_min_bpm, watch_daily_summary.heart_rate_min_bpm),
			heart_rate_max_bpm       = COALESCE(excluded.heart_rate_max_bpm, watch_daily_summary.heart_rate_max_bpm),
			heart_rate_avg_bpm       = COALESCE(excluded.heart_rate_avg_bpm, watch_daily_summary.heart_rate_avg_bpm),
			hrv_avg_ms               = COALESCE(excluded.hrv_avg_ms, watch_daily_summary.hrv_avg_ms),
			vo2_max                  = COALESCE(excluded.vo2_max, watch_daily_summary.vo2_max),
			vo2_max_sample           = COALESCE(excluded.vo2_max_sample, watch_daily_summary.vo2_max_sample),
			vo2_max_run_sample       = COALESCE(excluded.vo2_max_run_sample, watch_daily_summary.vo2_max_run_sample),
			spo2_avg_pct             = COALESCE(excluded.spo2_avg_pct, watch_daily_summary.spo2_avg_pct),
			spo2_min_pct             = COALESCE(excluded.spo2_min_pct, watch_daily_summary.spo2_min_pct),
			respiratory_rate_avg_bpm = COALESCE(excluded.respiratory_rate_avg_bpm, watch_daily_summary.respiratory_rate_avg_bpm),
			blood_glucose_avg_mg_dl  = COALESCE(excluded.blood_glucose_avg_mg_dl, watch_daily_summary.blood_glucose_avg_mg_dl),
			blood_glucose_min_mg_dl  = COALESCE(excluded.blood_glucose_min_mg_dl, watch_daily_summary.blood_glucose_min_mg_dl),
			blood_glucose_max_mg_dl  = COALESCE(excluded.blood_glucose_max_mg_dl, watch_daily_summary.blood_glucose_max_mg_dl),
			core_body_temperature_avg_c = COALESCE(excluded.core_body_temperature_avg_c, watch_daily_summary.core_body_temperature_avg_c),
			core_body_temperature_min_c = COALESCE(excluded.core_body_temperature_min_c, watch_daily_summary.core_body_temperature_min_c),
			core_body_temperature_max_c = COALESCE(excluded.core_body_temperature_max_c, watch_daily_summary.core_body_temperature_max_c),
			sleep_duration_minutes   = COALESCE(excluded.sleep_duration_minutes, watch_daily_summary.sleep_duration_minutes),
			sleep_temperature_c               = COALESCE(excluded.sleep_temperature_c, watch_daily_summary.sleep_temperature_c),
			sleep_temperature_baseline_c       = COALESCE(excluded.sleep_temperature_baseline_c, watch_daily_summary.sleep_temperature_baseline_c),
			sleep_temperature_deviation_30d_c  = COALESCE(excluded.sleep_temperature_deviation_30d_c, watch_daily_summary.sleep_temperature_deviation_30d_c),
			source_synced_at         = excluded.source_synced_at,
			raw_payload              = COALESCE(excluded.raw_payload, watch_daily_summary.raw_payload),
			updated_at               = excluded.updated_at
	`,
		sum.Day, sum.StepsTotal, sum.DistanceM, sum.FloorsClimbed, sum.AltitudeGainM, sum.SedentaryMinutes, sum.ActiveMinutesTotal,
		sum.KcalBurnedGoogle, sum.ActiveEnergyBurnedKcal,
		sum.RestingHeartRateBpm, sum.HeartRateMinBpm, sum.HeartRateMaxBpm, sum.HeartRateAvgBpm, sum.HrvAvgMs,
		sum.Vo2Max, sum.Vo2MaxSample, sum.Vo2MaxRunSample,
		sum.Spo2AvgPct, sum.Spo2MinPct, sum.RespiratoryRateAvgBpm,
		sum.BloodGlucoseAvgMgDl, sum.BloodGlucoseMinMgDl, sum.BloodGlucoseMaxMgDl,
		sum.CoreBodyTemperatureAvgC, sum.CoreBodyTemperatureMinC, sum.CoreBodyTemperatureMaxC,
		sum.SleepDurationMinutes, sum.SleepTemperatureC, sum.SleepTemperatureBaselineC, sum.SleepTemperatureDeviation30dC,
		now, rawPayload, now,
	)
	if err != nil {
		return fmt.Errorf("upserting watch_daily_summary: %w", err)
	}
	return nil
}

// upsertActiveMinutesByLevel replaces day's watch_active_minutes_by_level
// rows (delete-then-insert — sync is the sole writer, no natural row key
// beyond (day, level) to upsert against more granularly).
func (s *DBSyncer) upsertActiveMinutesByLevel(ctx context.Context, day string, rows []healthdata.ActiveMinutesByLevel) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM watch_active_minutes_by_level WHERE day = ?`, day); err != nil {
		return fmt.Errorf("clearing watch_active_minutes_by_level for %s: %w", day, err)
	}
	for _, r := range rows {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_active_minutes_by_level (day, activity_level, minutes) VALUES (?, ?, ?)
		`, r.Day, r.Level, r.Minutes); err != nil {
			return fmt.Errorf("inserting watch_active_minutes_by_level: %w", err)
		}
	}
	return nil
}

// upsertActiveZoneMinutesByZone replaces day's
// watch_active_zone_minutes_by_zone rows (delete-then-insert, same
// reasoning as upsertActiveMinutesByLevel).
func (s *DBSyncer) upsertActiveZoneMinutesByZone(ctx context.Context, day string, rows []healthdata.ActiveZoneMinutesByZone) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM watch_active_zone_minutes_by_zone WHERE day = ?`, day); err != nil {
		return fmt.Errorf("clearing watch_active_zone_minutes_by_zone for %s: %w", day, err)
	}
	for _, r := range rows {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_active_zone_minutes_by_zone (day, zone_type, minutes) VALUES (?, ?, ?)
		`, r.Day, r.ZoneType, r.Minutes); err != nil {
			return fmt.Errorf("inserting watch_active_zone_minutes_by_zone: %w", err)
		}
	}
	return nil
}

// upsertHeartRateZoneMinutes upserts day's watch_heart_rate_zone_minutes
// rows (natural PK (day, zone_type), true upsert rather than delete+insert).
func (s *DBSyncer) upsertHeartRateZoneMinutes(ctx context.Context, rows []healthdata.HeartRateZoneMinutes) error {
	for _, r := range rows {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_heart_rate_zone_minutes (day, zone_type, minutes) VALUES (?, ?, ?)
			ON CONFLICT(day, zone_type) DO UPDATE SET minutes = excluded.minutes
		`, r.Day, r.ZoneType, r.Minutes); err != nil {
			return fmt.Errorf("upserting watch_heart_rate_zone_minutes: %w", err)
		}
	}
	return nil
}

// upsertCaloriesByZone upserts day's watch_calories_by_zone rows.
func (s *DBSyncer) upsertCaloriesByZone(ctx context.Context, rows []healthdata.CaloriesByZone) error {
	for _, r := range rows {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_calories_by_zone (day, zone_type, kcal) VALUES (?, ?, ?)
			ON CONFLICT(day, zone_type) DO UPDATE SET kcal = excluded.kcal
		`, r.Day, r.ZoneType, r.Kcal); err != nil {
			return fmt.Errorf("upserting watch_calories_by_zone: %w", err)
		}
	}
	return nil
}

// upsertHeartRateZoneDefinitions replaces day's
// watch_heart_rate_zone_definition rows (definitions drift, delete+insert
// keeps stale zone types from lingering).
func (s *DBSyncer) upsertHeartRateZoneDefinitions(ctx context.Context, day string, rows []healthdata.HeartRateZoneDefinition) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM watch_heart_rate_zone_definition WHERE day = ?`, day); err != nil {
		return fmt.Errorf("clearing watch_heart_rate_zone_definition for %s: %w", day, err)
	}
	for _, r := range rows {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_heart_rate_zone_definition (day, zone_type, min_bpm, max_bpm) VALUES (?, ?, ?, ?)
		`, r.Day, r.ZoneType, r.MinBpm, r.MaxBpm); err != nil {
			return fmt.Errorf("inserting watch_heart_rate_zone_definition: %w", err)
		}
	}
	return nil
}

// upsertHourlySteps upserts day's watch_steps_hourly buckets.
func (s *DBSyncer) upsertHourlySteps(ctx context.Context, rows []healthdata.HourlySteps) error {
	for _, r := range rows {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_steps_hourly (day, hour, steps, distance_m, calories) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(day, hour) DO UPDATE SET
				steps      = COALESCE(excluded.steps, watch_steps_hourly.steps),
				distance_m = COALESCE(excluded.distance_m, watch_steps_hourly.distance_m),
				calories   = COALESCE(excluded.calories, watch_steps_hourly.calories)
		`, r.Day, r.Hour, r.Steps, r.DistanceM, r.Calories); err != nil {
			return fmt.Errorf("upserting watch_steps_hourly: %w", err)
		}
	}
	return nil
}

// upsertHeartRateSamples caches heart-rate samples into
// watch_heart_rate_intraday — a rolling cache, not the source of truth for
// history (see the table's comment in internal/db/schema.sql).
func (s *DBSyncer) upsertHeartRateSamples(ctx context.Context, rows []healthdata.HeartRateSample) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, r := range rows {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_heart_rate_intraday (recorded_at, bpm, cached_at) VALUES (?, ?, ?)
			ON CONFLICT(recorded_at) DO UPDATE SET bpm = excluded.bpm, cached_at = excluded.cached_at
		`, r.RecordedAt.Format(time.RFC3339), r.Bpm, now); err != nil {
			return fmt.Errorf("upserting watch_heart_rate_intraday: %w", err)
		}
	}
	return nil
}

// upsertBloodGlucoseSamples caches every blood-glucose sample for the day
// in full (low volume, clinically precise readings).
func (s *DBSyncer) upsertBloodGlucoseSamples(ctx context.Context, rows []healthdata.BloodGlucoseSample) error {
	for _, r := range rows {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_blood_glucose_sample (recorded_at, mg_dl, measurement_source, measurement_timing, meal_type, specimen)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(recorded_at) DO UPDATE SET
				mg_dl = excluded.mg_dl, measurement_source = excluded.measurement_source,
				measurement_timing = excluded.measurement_timing, meal_type = excluded.meal_type, specimen = excluded.specimen
		`, r.RecordedAt.Format(time.RFC3339), r.MgDl, r.MeasurementSource, r.MeasurementTiming, r.MealType, r.Specimen); err != nil {
			return fmt.Errorf("upserting watch_blood_glucose_sample: %w", err)
		}
	}
	return nil
}

// upsertCoreBodyTemperatureSamples caches every core-body-temperature
// sample for the day in full — same reasoning as upsertBloodGlucoseSamples.
func (s *DBSyncer) upsertCoreBodyTemperatureSamples(ctx context.Context, rows []healthdata.CoreBodyTemperatureSample) error {
	for _, r := range rows {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_core_body_temperature_sample (recorded_at, celsius, measurement_location) VALUES (?, ?, ?)
			ON CONFLICT(recorded_at) DO UPDATE SET celsius = excluded.celsius, measurement_location = excluded.measurement_location
		`, r.RecordedAt.Format(time.RFC3339), r.Celsius, r.MeasurementLocation); err != nil {
			return fmt.Errorf("upserting watch_core_body_temperature_sample: %w", err)
		}
	}
	return nil
}

// upsertActivityLevelSegments replaces day's watch_activity_level_segment
// rows (delete-then-insert, same reasoning as upsertActiveMinutesByLevel).
func (s *DBSyncer) upsertActivityLevelSegments(ctx context.Context, day string, rows []healthdata.ActivityLevelSegment) error {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM watch_activity_level_segment WHERE day = ?`, day); err != nil {
		return fmt.Errorf("clearing watch_activity_level_segment for %s: %w", day, err)
	}
	for _, r := range rows {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_activity_level_segment (day, activity_level, start_time, end_time) VALUES (?, ?, ?, ?)
		`, r.Day, r.Level, r.StartTime.Format(time.RFC3339), r.EndTime.Format(time.RFC3339)); err != nil {
			return fmt.Errorf("inserting watch_activity_level_segment: %w", err)
		}
	}
	return nil
}

// upsertSleepSession writes sess into watch_sleep_session and replaces its
// stage timeline, returning the session's row id (needed to attach
// per-stage respiratory rate later in the same sync pass).
func (s *DBSyncer) upsertSleepSession(ctx context.Context, sess healthdata.SleepSession, stages []healthdata.SleepStage) (int64, error) {
	isMainInt := 0
	if sess.IsMainSleep {
		isMainInt = 1
	}

	var id int64
	err := s.DB.QueryRowContext(ctx, `
		INSERT INTO watch_sleep_session (
			day, start_time, end_time, is_main_sleep, duration_minutes,
			efficiency_pct, minutes_awake, minutes_light, minutes_deep, minutes_rem
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(day, start_time) DO UPDATE SET
			end_time         = excluded.end_time,
			is_main_sleep    = excluded.is_main_sleep,
			duration_minutes = excluded.duration_minutes,
			efficiency_pct   = excluded.efficiency_pct,
			minutes_awake    = excluded.minutes_awake,
			minutes_light    = excluded.minutes_light,
			minutes_deep     = excluded.minutes_deep,
			minutes_rem      = excluded.minutes_rem
		RETURNING id
	`,
		sess.Day, sess.StartTime.Format(time.RFC3339), sess.EndTime.Format(time.RFC3339), isMainInt,
		sess.DurationMinutes, sess.EfficiencyPct, sess.MinutesAwake, sess.MinutesLight, sess.MinutesDeep, sess.MinutesRem,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upserting watch_sleep_session: %w", err)
	}

	if _, err := s.DB.ExecContext(ctx, `DELETE FROM watch_sleep_stage WHERE sleep_session_id = ?`, id); err != nil {
		return 0, fmt.Errorf("clearing old sleep stages: %w", err)
	}
	for _, stage := range stages {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_sleep_stage (sleep_session_id, stage_type, start_time, end_time) VALUES (?, ?, ?, ?)
		`, id, stage.StageType, stage.StartTime.Format(time.RFC3339), stage.EndTime.Format(time.RFC3339)); err != nil {
			return 0, fmt.Errorf("inserting sleep stage: %w", err)
		}
	}
	return id, nil
}

// updateSleepSessionRespiratoryRate attaches per-stage respiratory rate to
// an already-upserted sleep session (respiratory-rate-sleep-summary is
// fetched and matched to a session separately — see
// sync_details.go's syncRespiratoryRateSleepSummary).
func (s *DBSyncer) updateSleepSessionRespiratoryRate(ctx context.Context, sessionID int64, deep, light, rem, full *float64) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE watch_sleep_session SET
			deep_resp_rate_bpm  = ?,
			light_resp_rate_bpm = ?,
			rem_resp_rate_bpm   = ?,
			full_resp_rate_bpm  = ?
		WHERE id = ?
	`, deep, light, rem, full, sessionID)
	if err != nil {
		return fmt.Errorf("updating sleep session respiratory rate: %w", err)
	}
	return nil
}

// upsertExerciseSession writes sess into watch_exercise_session.
func (s *DBSyncer) upsertExerciseSession(ctx context.Context, sess healthdata.ExerciseSession) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO watch_exercise_session (
			day, exercise_type, start_time, end_time, duration_minutes,
			calories_burned, avg_heart_rate_bpm, max_heart_rate_bpm, distance_m
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(day, start_time) DO UPDATE SET
			end_time           = excluded.end_time,
			duration_minutes   = excluded.duration_minutes,
			calories_burned    = excluded.calories_burned,
			avg_heart_rate_bpm = excluded.avg_heart_rate_bpm,
			max_heart_rate_bpm = excluded.max_heart_rate_bpm,
			distance_m         = excluded.distance_m
	`,
		sess.Day, sess.ExerciseType, sess.StartTime.Format(time.RFC3339), sess.EndTime.Format(time.RFC3339),
		sess.DurationMinutes, sess.CaloriesBurned, sess.AvgHeartRateBpm, sess.MaxHeartRateBpm, sess.DistanceM,
	)
	if err != nil {
		return fmt.Errorf("upserting watch_exercise_session: %w", err)
	}
	return nil
}

// upsertECGReading writes r into watch_ecg_reading (type discriminates
// "ecg" vs "irregular_rhythm").
func (s *DBSyncer) upsertECGReading(ctx context.Context, r healthdata.ECGReading) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO watch_ecg_reading (type, recorded_at, classification, avg_heart_rate_bpm)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(type, recorded_at) DO UPDATE SET
			classification     = excluded.classification,
			avg_heart_rate_bpm = excluded.avg_heart_rate_bpm
	`, r.Type, r.RecordedAt.Format(time.RFC3339), r.Classification, r.AvgHeartRateBpm)
	if err != nil {
		return fmt.Errorf("upserting watch_ecg_reading: %w", err)
	}
	return nil
}

// upsertBodyMeasurement upserts the day's most recent weight/height/body-fat
// sample (if any) into body_measurement's raw_* columns — the columns that
// can come from either a Google Health-connected scale or manual UI entry
// (see internal/db/schema.sql's body_measurement comment).
func (s *DBSyncer) upsertBodyMeasurement(ctx context.Context, day string, weightKg, heightCm, bodyFatPct *float64) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO body_measurement (day, weight_kg_raw, height_cm_raw, body_fat_pct_raw)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(day) DO UPDATE SET
			weight_kg_raw    = COALESCE(excluded.weight_kg_raw, body_measurement.weight_kg_raw),
			height_cm_raw    = COALESCE(excluded.height_cm_raw, body_measurement.height_cm_raw),
			body_fat_pct_raw = COALESCE(excluded.body_fat_pct_raw, body_measurement.body_fat_pct_raw),
			updated_at       = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
	`, day, weightKg, heightCm, bodyFatPct)
	if err != nil {
		return fmt.Errorf("upserting body_measurement: %w", err)
	}
	return nil
}
