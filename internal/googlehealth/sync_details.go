package googlehealth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// syncSleep upserts every sleep session starting on day into
// watch_sleep_session/watch_sleep_stage. The longest session of the day
// (by time in the sleep period) is flagged is_main_sleep — Google's API
// doesn't mark this itself, unlike Fitbit's own app.
func (s *DBSyncer) syncSleep(ctx context.Context, day time.Time) (bool, error) {
	sessions, err := fetchUnfilteredForDay[Sleep](ctx, s.Client, "sleep", day, func(v Sleep) (time.Time, bool) {
		t, err := v.Interval.Start()
		return t, err == nil
	})
	if err != nil {
		return false, err
	}

	mainIdx := -1
	var longest int64
	for i, sess := range sessions {
		if m := int64(sess.Summary.MinutesInSleepPeriod); m > longest {
			longest, mainIdx = m, i
		}
	}

	for i, sess := range sessions {
		if err := s.upsertSleepSession(ctx, sess, i == mainIdx); err != nil {
			return false, err
		}
	}
	return len(sessions) > 0, nil
}

func (s *DBSyncer) upsertSleepSession(ctx context.Context, sess Sleep, isMain bool) error {
	start, err := sess.Interval.Start()
	if err != nil {
		return fmt.Errorf("parsing sleep start time: %w", err)
	}
	end, err := sess.Interval.End()
	if err != nil {
		return fmt.Errorf("parsing sleep end time: %w", err)
	}

	var minutesLight, minutesDeep, minutesRem int64
	for _, st := range sess.Summary.StagesSummary {
		switch st.Type {
		case "LIGHT":
			minutesLight = int64(st.Minutes)
		case "DEEP":
			minutesDeep = int64(st.Minutes)
		case "REM":
			minutesRem = int64(st.Minutes)
		}
	}

	var efficiencyPct *float64
	if sess.Summary.MinutesInSleepPeriod > 0 {
		e := float64(sess.Summary.MinutesAsleep) / float64(sess.Summary.MinutesInSleepPeriod) * 100
		efficiencyPct = &e
	}

	isMainInt := 0
	if isMain {
		isMainInt = 1
	}

	var id int64
	err = s.DB.QueryRowContext(ctx, `
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
		dayKey(start.In(time.Local)), start.Format(time.RFC3339), end.Format(time.RFC3339), isMainInt,
		int64(sess.Summary.MinutesInSleepPeriod), efficiencyPct, int64(sess.Summary.MinutesAwake),
		minutesLight, minutesDeep, minutesRem,
	).Scan(&id)
	if err != nil {
		return fmt.Errorf("upserting watch_sleep_session: %w", err)
	}

	if _, err := s.DB.ExecContext(ctx, `DELETE FROM watch_sleep_stage WHERE sleep_session_id = ?`, id); err != nil {
		return fmt.Errorf("clearing old sleep stages: %w", err)
	}
	for _, stage := range sess.Stages {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_sleep_stage (sleep_session_id, stage_type, start_time, end_time)
			VALUES (?, ?, ?, ?)
		`, id, stage.Type, stage.StartTime, stage.EndTime); err != nil {
			return fmt.Errorf("inserting sleep stage: %w", err)
		}
	}
	return nil
}

// syncExercise upserts every workout starting on day into
// watch_exercise_session.
func (s *DBSyncer) syncExercise(ctx context.Context, day time.Time) (bool, error) {
	sessions, err := fetchUnfilteredForDay[Exercise](ctx, s.Client, "exercise", day, func(v Exercise) (time.Time, bool) {
		t, err := v.Interval.Start()
		return t, err == nil
	})
	if err != nil {
		return false, err
	}
	for _, sess := range sessions {
		if err := s.upsertExerciseSession(ctx, sess); err != nil {
			return false, err
		}
	}
	return len(sessions) > 0, nil
}

func (s *DBSyncer) upsertExerciseSession(ctx context.Context, sess Exercise) error {
	start, err := sess.Interval.Start()
	if err != nil {
		return fmt.Errorf("parsing exercise start time: %w", err)
	}
	end, err := sess.Interval.End()
	if err != nil {
		return fmt.Errorf("parsing exercise end time: %w", err)
	}

	durationMinutes := int64(sess.ActiveDuration.Duration().Minutes())
	var avgHR *float64
	if sess.MetricsSummary.AverageHeartRateBeatsPerMinute > 0 {
		v := float64(sess.MetricsSummary.AverageHeartRateBeatsPerMinute)
		avgHR = &v
	}

	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO watch_exercise_session (
			day, exercise_type, start_time, end_time, duration_minutes,
			calories_burned, avg_heart_rate_bpm
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(day, start_time) DO UPDATE SET
			end_time           = excluded.end_time,
			duration_minutes   = excluded.duration_minutes,
			calories_burned    = excluded.calories_burned,
			avg_heart_rate_bpm = excluded.avg_heart_rate_bpm
	`,
		dayKey(start.In(time.Local)), sess.ExerciseType, start.Format(time.RFC3339), end.Format(time.RFC3339),
		durationMinutes, float64(sess.MetricsSummary.CaloriesKcal), avgHR,
	)
	if err != nil {
		return fmt.Errorf("upserting watch_exercise_session: %w", err)
	}
	return nil
}

// syncECG upserts every ECG reading starting on day into
// watch_ecg_reading. The raw waveform is never stored — see values.go's
// file-level comment.
func (s *DBSyncer) syncECG(ctx context.Context, day time.Time) (bool, error) {
	readings, err := fetchUnfilteredForDay[Electrocardiogram](ctx, s.Client, "electrocardiogram", day, func(v Electrocardiogram) (time.Time, bool) {
		t, err := v.Interval.Start()
		return t, err == nil
	})
	if err != nil {
		return false, err
	}
	for _, r := range readings {
		if err := s.upsertECGReading(ctx, r); err != nil {
			return false, err
		}
	}
	return len(readings) > 0, nil
}

func (s *DBSyncer) upsertECGReading(ctx context.Context, r Electrocardiogram) error {
	start, err := r.Interval.Start()
	if err != nil {
		return fmt.Errorf("parsing ECG start time: %w", err)
	}

	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO watch_ecg_reading (type, recorded_at, classification, avg_heart_rate_bpm)
		VALUES ('ecg', ?, ?, ?)
		ON CONFLICT(type, recorded_at) DO UPDATE SET
			classification     = excluded.classification,
			avg_heart_rate_bpm = excluded.avg_heart_rate_bpm
	`, start.Format(time.RFC3339), r.ResultClassification, float64(r.BeatsPerMinuteAvg))
	if err != nil {
		return fmt.Errorf("upserting watch_ecg_reading: %w", err)
	}
	return nil
}

// syncBodyMeasurement upserts the day's most recent weight/height/body-fat
// sample (if any) into body_measurement's raw_* columns — the columns
// that can come from either a Google Health-connected scale or manual UI
// entry (see internal/db/schema.sql's body_measurement comment).
func (s *DBSyncer) syncBodyMeasurement(ctx context.Context, day time.Time) (bool, error) {
	var weightKg, heightCm, bodyFatPct *float64

	if pts, err := fetchSamplePoints[Weight](ctx, s.Client, "weight", day); err != nil {
		return false, err
	} else if len(pts) > 0 {
		v := float64(pts[len(pts)-1].Kilograms)
		weightKg = &v
	}
	if pts, err := fetchSamplePoints[Height](ctx, s.Client, "height", day); err != nil {
		return false, err
	} else if len(pts) > 0 {
		v := float64(pts[len(pts)-1].Meters) * 100 // meters -> cm, matching height_cm_raw
		heightCm = &v
	}
	if pts, err := fetchSamplePoints[BodyFat](ctx, s.Client, "body-fat", day); err != nil {
		return false, err
	} else if len(pts) > 0 {
		v := float64(pts[len(pts)-1].Percentage)
		bodyFatPct = &v
	}

	if weightKg == nil && heightCm == nil && bodyFatPct == nil {
		return false, nil
	}

	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO body_measurement (day, weight_kg_raw, height_cm_raw, body_fat_pct_raw)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(day) DO UPDATE SET
			weight_kg_raw    = COALESCE(excluded.weight_kg_raw, body_measurement.weight_kg_raw),
			height_cm_raw    = COALESCE(excluded.height_cm_raw, body_measurement.height_cm_raw),
			body_fat_pct_raw = COALESCE(excluded.body_fat_pct_raw, body_measurement.body_fat_pct_raw),
			updated_at       = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
	`, dayKey(day), weightKg, heightCm, bodyFatPct)
	if err != nil {
		return false, fmt.Errorf("upserting body_measurement: %w", err)
	}
	return true, nil
}

// syncHeartRateZoneDefinitions upserts the day's personalized heart-rate
// zone BPM thresholds (from daily-heart-rate-zones — confirmed via a real
// response to be threshold definitions, not time-in-zone data, despite the
// similar name to syncHeartRateZoneMinutes below).
func (s *DBSyncer) syncHeartRateZoneDefinitions(ctx context.Context, day time.Time) (bool, error) {
	v, found, err := fetchDailyAggregate[DailyHeartRateZones](ctx, s.Client, "daily-heart-rate-zones", day,
		func(v DailyHeartRateZones) Date { return v.Date })
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	dayStr := dayKey(day)
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM watch_heart_rate_zone_definition WHERE day = ?`, dayStr); err != nil {
		return false, fmt.Errorf("clearing old heart rate zone definitions for %s: %w", dayStr, err)
	}
	for _, z := range v.HeartRateZones {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_heart_rate_zone_definition (day, zone_type, min_bpm, max_bpm)
			VALUES (?, ?, ?, ?)
		`, dayStr, z.Type, int64(z.MinBeatsPerMinute), int64(z.MaxBeatsPerMinute)); err != nil {
			return false, fmt.Errorf("inserting heart rate zone definition: %w", err)
		}
	}
	return true, nil
}

// syncHeartRateZoneMinutes upserts the day's actual minutes spent in each
// heart-rate zone (from time-in-heart-rate-zone) — the real time-series
// counterpart to the thresholds above.
func (s *DBSyncer) syncHeartRateZoneMinutes(ctx context.Context, day time.Time) (bool, error) {
	points, err := fetchIntervalPoints[TimeInHeartRateZone](ctx, s.Client, "time-in-heart-rate-zone", day)
	if err != nil {
		return false, err
	}
	if len(points) == 0 {
		return false, nil
	}

	byZone := map[string]float64{}
	for _, p := range points {
		byZone[p.HeartRateZone] += float64(p.DurationMillis) / 60000
	}
	dayStr := dayKey(day)
	for zone, minutes := range byZone {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_heart_rate_zone_minutes (day, zone_type, minutes)
			VALUES (?, ?, ?)
			ON CONFLICT(day, zone_type) DO UPDATE SET minutes = excluded.minutes
		`, dayStr, zone, minutes); err != nil {
			return false, fmt.Errorf("upserting heart rate zone minutes: %w", err)
		}
	}
	return true, nil
}

// syncCaloriesByZone upserts the day's calories attributed to each
// heart-rate zone (from calories-in-heart-rate-zone — INFERRED field shape,
// see values.go's confidence-level convention).
func (s *DBSyncer) syncCaloriesByZone(ctx context.Context, day time.Time) (bool, error) {
	points, err := fetchIntervalPoints[CaloriesInHeartRateZone](ctx, s.Client, "calories-in-heart-rate-zone", day)
	if err != nil {
		return false, err
	}
	if len(points) == 0 {
		return false, nil
	}

	byZone := map[string]float64{}
	for _, p := range points {
		byZone[p.HeartRateZone] += float64(p.Kcal)
	}
	dayStr := dayKey(day)
	for zone, kcal := range byZone {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_calories_by_zone (day, zone_type, kcal)
			VALUES (?, ?, ?)
			ON CONFLICT(day, zone_type) DO UPDATE SET kcal = excluded.kcal
		`, dayStr, zone, kcal); err != nil {
			return false, fmt.Errorf("upserting calories by zone: %w", err)
		}
	}
	return true, nil
}

// syncRespiratoryRateSleepSummary matches each respiratory-rate-sleep-summary
// sample to the sleep session covering its sample time (falling back to the
// day's main sleep session if none contains it exactly) and updates that
// session's per-stage respiratory rate columns.
func (s *DBSyncer) syncRespiratoryRateSleepSummary(ctx context.Context, day time.Time) (bool, error) {
	points, err := fetchSamplePoints[RespiratoryRateSleepSummary](ctx, s.Client, "respiratory-rate-sleep-summary", day)
	if err != nil {
		return false, err
	}
	if len(points) == 0 {
		return false, nil
	}

	dayStr := dayKey(day)
	found := false
	for _, p := range points {
		sampleTime, err := p.SampleTime.Time()
		if err != nil {
			continue
		}

		var sessionID int64
		err = s.DB.QueryRowContext(ctx, `
			SELECT id FROM watch_sleep_session
			WHERE day = ? AND start_time <= ? AND end_time > ?
			ORDER BY duration_minutes DESC LIMIT 1
		`, dayStr, sampleTime.Format(time.RFC3339), sampleTime.Format(time.RFC3339)).Scan(&sessionID)
		if errors.Is(err, sql.ErrNoRows) {
			err = s.DB.QueryRowContext(ctx, `
				SELECT id FROM watch_sleep_session WHERE day = ? AND is_main_sleep = 1 LIMIT 1
			`, dayStr).Scan(&sessionID)
		}
		if errors.Is(err, sql.ErrNoRows) {
			continue // no session to attach this sample to yet
		}
		if err != nil {
			return false, fmt.Errorf("finding sleep session for respiratory rate sample: %w", err)
		}

		if _, err := s.DB.ExecContext(ctx, `
			UPDATE watch_sleep_session SET
				deep_resp_rate_bpm  = ?,
				light_resp_rate_bpm = ?,
				rem_resp_rate_bpm   = ?,
				full_resp_rate_bpm  = ?
			WHERE id = ?
		`, float64(p.DeepSleepStats.BreathsPerMinute), float64(p.LightSleepStats.BreathsPerMinute),
			float64(p.RemSleepStats.BreathsPerMinute), float64(p.FullSleepStats.BreathsPerMinute), sessionID); err != nil {
			return false, fmt.Errorf("updating sleep session respiratory rate: %w", err)
		}
		found = true
	}
	return found, nil
}

// syncBloodGlucose caches every blood-glucose sample for the day in full
// (low volume, clinically precise readings — kept as-is rather than
// day-summarized, same reasoning as watch_heart_rate_intraday but without
// the pruning since this account has never returned any).
func (s *DBSyncer) syncBloodGlucose(ctx context.Context, day time.Time) (bool, error) {
	points, err := fetchSamplePoints[BloodGlucose](ctx, s.Client, "blood-glucose", day)
	if err != nil {
		return false, err
	}
	if len(points) == 0 {
		return false, nil
	}
	for _, p := range points {
		t, err := p.SampleTime.Time()
		if err != nil {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_blood_glucose_sample (recorded_at, mg_dl, measurement_source, measurement_timing, meal_type, specimen)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(recorded_at) DO UPDATE SET
				mg_dl = excluded.mg_dl, measurement_source = excluded.measurement_source,
				measurement_timing = excluded.measurement_timing, meal_type = excluded.meal_type, specimen = excluded.specimen
		`, t.Format(time.RFC3339), float64(p.BloodGlucoseMilligramsPerDeciliter), p.MeasurementSource, p.MeasurementTiming, p.MealType, p.Specimen); err != nil {
			return false, fmt.Errorf("upserting blood glucose sample: %w", err)
		}
	}
	return true, nil
}

// syncCoreBodyTemperature caches every core-body-temperature sample for the
// day in full — same reasoning as syncBloodGlucose.
func (s *DBSyncer) syncCoreBodyTemperature(ctx context.Context, day time.Time) (bool, error) {
	points, err := fetchSamplePoints[CoreBodyTemperature](ctx, s.Client, "core-body-temperature", day)
	if err != nil {
		return false, err
	}
	if len(points) == 0 {
		return false, nil
	}
	for _, p := range points {
		t, err := p.SampleTime.Time()
		if err != nil {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_core_body_temperature_sample (recorded_at, celsius, measurement_location)
			VALUES (?, ?, ?)
			ON CONFLICT(recorded_at) DO UPDATE SET celsius = excluded.celsius, measurement_location = excluded.measurement_location
		`, t.Format(time.RFC3339), float64(p.TemperatureCelsius), p.MeasurementLocation); err != nil {
			return false, fmt.Errorf("upserting core body temperature sample: %w", err)
		}
	}
	return true, nil
}

// syncStepsHourly buckets the day's step intervals by local hour into
// watch_steps_hourly.
func (s *DBSyncer) syncStepsHourly(ctx context.Context, day time.Time) (bool, error) {
	points, err := fetchIntervalPoints[Steps](ctx, s.Client, "steps", day)
	if err != nil {
		return false, err
	}
	if len(points) == 0 {
		return false, nil
	}

	hourly := make(map[int]int64)
	for _, p := range points {
		start, err := p.Interval.Start()
		if err != nil {
			continue
		}
		hourly[start.In(time.Local).Hour()] += int64(p.Count)
	}

	dayStr := dayKey(day)
	for hour, steps := range hourly {
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_steps_hourly (day, hour, steps)
			VALUES (?, ?, ?)
			ON CONFLICT(day, hour) DO UPDATE SET steps = excluded.steps
		`, dayStr, hour, steps); err != nil {
			return false, fmt.Errorf("upserting watch_steps_hourly: %w", err)
		}
	}
	return true, nil
}

// syncHeartRateIntraday caches the day's raw heart-rate samples into
// watch_heart_rate_intraday — a rolling cache, not the source of truth
// for history (see the table's comment in internal/db/schema.sql).
func (s *DBSyncer) syncHeartRateIntraday(ctx context.Context, day time.Time) (bool, error) {
	points, err := fetchSamplePoints[HeartRate](ctx, s.Client, "heart-rate", day)
	if err != nil {
		return false, err
	}
	if len(points) == 0 {
		return false, nil
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, p := range points {
		t, err := p.SampleTime.Time()
		if err != nil {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `
			INSERT INTO watch_heart_rate_intraday (recorded_at, bpm, cached_at)
			VALUES (?, ?, ?)
			ON CONFLICT(recorded_at) DO UPDATE SET bpm = excluded.bpm, cached_at = excluded.cached_at
		`, t.Format(time.RFC3339), float64(p.BeatsPerMinute), now); err != nil {
			return false, fmt.Errorf("upserting watch_heart_rate_intraday: %w", err)
		}
	}
	return true, nil
}
