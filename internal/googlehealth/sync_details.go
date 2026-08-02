package googlehealth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/healthdata"
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
		start, err := sess.Interval.Start()
		if err != nil {
			return false, fmt.Errorf("parsing sleep start time: %w", err)
		}
		end, err := sess.Interval.End()
		if err != nil {
			return false, fmt.Errorf("parsing sleep end time: %w", err)
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

		duration := int64(sess.Summary.MinutesInSleepPeriod)
		awake := int64(sess.Summary.MinutesAwake)
		domain := healthdata.SleepSession{
			Day: dayKey(start.In(time.Local)), StartTime: start, EndTime: end, IsMainSleep: i == mainIdx,
			DurationMinutes: &duration, EfficiencyPct: efficiencyPct, MinutesAwake: &awake,
			MinutesLight: &minutesLight, MinutesDeep: &minutesDeep, MinutesRem: &minutesRem,
		}

		stages := make([]healthdata.SleepStage, 0, len(sess.Stages))
		for _, st := range sess.Stages {
			stStart, e1 := time.Parse(time.RFC3339, st.StartTime)
			stEnd, e2 := time.Parse(time.RFC3339, st.EndTime)
			if e1 != nil || e2 != nil {
				continue
			}
			stages = append(stages, healthdata.SleepStage{StageType: st.Type, StartTime: stStart, EndTime: stEnd})
		}

		if _, err := s.upsertSleepSession(ctx, domain, stages); err != nil {
			return false, err
		}
	}
	return len(sessions) > 0, nil
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
		start, err := sess.Interval.Start()
		if err != nil {
			return false, fmt.Errorf("parsing exercise start time: %w", err)
		}
		end, err := sess.Interval.End()
		if err != nil {
			return false, fmt.Errorf("parsing exercise end time: %w", err)
		}

		duration := int64(sess.ActiveDuration.Duration().Minutes())
		var avgHR *float64
		if sess.MetricsSummary.AverageHeartRateBeatsPerMinute > 0 {
			v := float64(sess.MetricsSummary.AverageHeartRateBeatsPerMinute)
			avgHR = &v
		}
		calories := float64(sess.MetricsSummary.CaloriesKcal)

		domain := healthdata.ExerciseSession{
			Day: dayKey(start.In(time.Local)), ExerciseType: sess.ExerciseType, StartTime: start, EndTime: end,
			DurationMinutes: &duration, CaloriesBurned: &calories, AvgHeartRateBpm: avgHR,
		}
		if err := s.upsertExerciseSession(ctx, domain); err != nil {
			return false, err
		}
	}
	return len(sessions) > 0, nil
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
		start, err := r.Interval.Start()
		if err != nil {
			return false, fmt.Errorf("parsing ECG start time: %w", err)
		}
		avgHR := float64(r.BeatsPerMinuteAvg)
		domain := healthdata.ECGReading{Type: "ecg", RecordedAt: start, Classification: r.ResultClassification, AvgHeartRateBpm: &avgHR}
		if err := s.upsertECGReading(ctx, domain); err != nil {
			return false, err
		}
	}
	return len(readings) > 0, nil
}

// syncBodyMeasurement fetches the day's most recent weight/height/body-fat
// sample (if any) and upserts it into body_measurement's raw_* columns.
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
	if err := s.upsertBodyMeasurement(ctx, dayKey(day), weightKg, heightCm, bodyFatPct); err != nil {
		return false, err
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
	rows := make([]healthdata.HeartRateZoneDefinition, 0, len(v.HeartRateZones))
	for _, z := range v.HeartRateZones {
		rows = append(rows, healthdata.HeartRateZoneDefinition{
			Day: dayStr, ZoneType: z.Type, MinBpm: int64(z.MinBeatsPerMinute), MaxBpm: int64(z.MaxBeatsPerMinute),
		})
	}
	if err := s.upsertHeartRateZoneDefinitions(ctx, dayStr, rows); err != nil {
		return false, err
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
	rows := make([]healthdata.HeartRateZoneMinutes, 0, len(byZone))
	for zone, minutes := range byZone {
		rows = append(rows, healthdata.HeartRateZoneMinutes{Day: dayStr, ZoneType: zone, Minutes: minutes})
	}
	if err := s.upsertHeartRateZoneMinutes(ctx, rows); err != nil {
		return false, err
	}
	return true, nil
}

// syncCaloriesByZone upserts the day's calories attributed to each
// heart-rate zone (from calories-in-heart-rate-zone) via dailyRollUp —
// list() is rejected outright for this data type (confirmed: HTTP 400
// "List is not supported for data type calories-in-heart-rate-zone, but
// the following actions are supported: rollup, dailyRollup"), same
// NoList restriction floors already works around. Response shape is
// DOCUMENTED (values.go), not yet confirmed against a real response.
func (s *DBSyncer) syncCaloriesByZone(ctx context.Context, day time.Time) (bool, error) {
	v, found, err := fetchDailyRollup[CaloriesInHeartRateZoneRollup](ctx, s.Client, "calories-in-heart-rate-zone", day)
	if err != nil {
		return false, err
	}
	if !found || len(v.CaloriesInHeartRateZones) == 0 {
		return false, nil
	}

	dayStr := dayKey(day)
	rows := make([]healthdata.CaloriesByZone, 0, len(v.CaloriesInHeartRateZones))
	for _, z := range v.CaloriesInHeartRateZones {
		rows = append(rows, healthdata.CaloriesByZone{Day: dayStr, ZoneType: z.HeartRateZone, Kcal: float64(z.Kcal)})
	}
	if err := s.upsertCaloriesByZone(ctx, rows); err != nil {
		return false, err
	}
	return true, nil
}

// syncActiveMinutesByLevel upserts the day's active minutes broken down by
// activity level (LIGHT/MODERATE/VIGOROUS), and returns the grand total for
// watch_daily_summary.active_minutes (a denormalized convenience column,
// same relationship steps_total has to watch_steps_hourly).
func (s *DBSyncer) syncActiveMinutesByLevel(ctx context.Context, day time.Time) (bool, *int64, error) {
	points, err := fetchIntervalPoints[ActiveMinutes](ctx, s.Client, "active-minutes", day)
	if err != nil {
		return false, nil, err
	}
	if len(points) == 0 {
		return false, nil, nil
	}

	byLevel := map[string]int64{}
	var total int64
	for _, p := range points {
		for _, lvl := range p.ActiveMinutesByActivityLevel {
			m := int64(lvl.Minutes)
			byLevel[lvl.ActivityLevel] += m
			total += m
		}
	}

	dayStr := dayKey(day)
	rows := make([]healthdata.ActiveMinutesByLevel, 0, len(byLevel))
	for level, minutes := range byLevel {
		rows = append(rows, healthdata.ActiveMinutesByLevel{Day: dayStr, Level: level, Minutes: minutes})
	}
	if err := s.upsertActiveMinutesByLevel(ctx, dayStr, rows); err != nil {
		return false, nil, err
	}
	return true, &total, nil
}

// syncActiveZoneMinutesByZone upserts the day's active zone minutes broken
// down by heart-rate zone (FAT_BURN/CARDIO/PEAK) — no denormalized daily
// total column exists for this one (unlike active-minutes), the category
// breakdown is the only representation.
func (s *DBSyncer) syncActiveZoneMinutesByZone(ctx context.Context, day time.Time) (bool, error) {
	points, err := fetchIntervalPoints[ActiveZoneMinutes](ctx, s.Client, "active-zone-minutes", day)
	if err != nil {
		return false, err
	}
	if len(points) == 0 {
		return false, nil
	}

	byZone := map[string]int64{}
	for _, p := range points {
		byZone[p.HeartRateZone] += int64(p.ActiveZoneMinutes)
	}

	dayStr := dayKey(day)
	rows := make([]healthdata.ActiveZoneMinutesByZone, 0, len(byZone))
	for zone, minutes := range byZone {
		rows = append(rows, healthdata.ActiveZoneMinutesByZone{Day: dayStr, ZoneType: zone, Minutes: minutes})
	}
	if err := s.upsertActiveZoneMinutesByZone(ctx, dayStr, rows); err != nil {
		return false, err
	}
	return true, nil
}

// syncActivityLevelSegments upserts the day's sedentary/lightly-active/
// moderately-active/very-active spans (from activity-level) — kept as a
// SegmentTimeline (one row per raw interval Google returns) rather than
// merged into coarser blocks: the point of this metric is exactly when
// each level occurred, and Google's own interval boundaries already are
// that answer without us re-deriving it.
func (s *DBSyncer) syncActivityLevelSegments(ctx context.Context, day time.Time) (bool, error) {
	points, err := fetchIntervalPoints[ActivityLevel](ctx, s.Client, "activity-level", day)
	if err != nil {
		return false, err
	}
	if len(points) == 0 {
		return false, nil
	}

	dayStr := dayKey(day)
	rows := make([]healthdata.ActivityLevelSegment, 0, len(points))
	for _, p := range points {
		start, errS := p.Interval.Start()
		end, errE := p.Interval.End()
		if errS != nil || errE != nil {
			continue
		}
		rows = append(rows, healthdata.ActivityLevelSegment{Day: dayStr, Level: p.ActivityLevelType, StartTime: start, EndTime: end})
	}
	if len(rows) == 0 {
		return false, nil
	}
	if err := s.upsertActivityLevelSegments(ctx, dayStr, rows); err != nil {
		return false, err
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
			WHERE user_id = ? AND day = ? AND start_time <= ? AND end_time > ?
			ORDER BY duration_minutes DESC LIMIT 1
		`, s.UserID, dayStr, sampleTime.Format(time.RFC3339), sampleTime.Format(time.RFC3339)).Scan(&sessionID)
		if errors.Is(err, sql.ErrNoRows) {
			err = s.DB.QueryRowContext(ctx, `
				SELECT id FROM watch_sleep_session WHERE user_id = ? AND day = ? AND is_main_sleep = 1 LIMIT 1
			`, s.UserID, dayStr).Scan(&sessionID)
		}
		if errors.Is(err, sql.ErrNoRows) {
			continue // no session to attach this sample to yet
		}
		if err != nil {
			return false, fmt.Errorf("finding sleep session for respiratory rate sample: %w", err)
		}

		deep, light := float64(p.DeepSleepStats.BreathsPerMinute), float64(p.LightSleepStats.BreathsPerMinute)
		rem, full := float64(p.RemSleepStats.BreathsPerMinute), float64(p.FullSleepStats.BreathsPerMinute)
		if err := s.updateSleepSessionRespiratoryRate(ctx, sessionID, &deep, &light, &rem, &full); err != nil {
			return false, err
		}
		found = true
	}
	return found, nil
}

// syncBloodGlucose fetches the day's blood-glucose samples once, caches
// them in full into watch_blood_glucose_sample (Timeline shape — *when* a
// reading happened matters, e.g. relative to meals), and returns the day's
// avg/min/max for watch_daily_summary — computed from this same fetch
// rather than a separate query.
func (s *DBSyncer) syncBloodGlucose(ctx context.Context, day time.Time) (found bool, avg, min, max *float64, err error) {
	points, err := fetchSamplePoints[BloodGlucose](ctx, s.Client, "blood-glucose", day)
	if err != nil {
		return false, nil, nil, nil, err
	}
	if len(points) == 0 {
		return false, nil, nil, nil, nil
	}

	rows := make([]healthdata.BloodGlucoseSample, 0, len(points))
	var sum, lo, hi float64
	first := true
	for _, p := range points {
		t, terr := p.SampleTime.Time()
		if terr != nil {
			continue
		}
		v := float64(p.BloodGlucoseMilligramsPerDeciliter)
		rows = append(rows, healthdata.BloodGlucoseSample{
			RecordedAt: t, MgDl: v,
			MeasurementSource: p.MeasurementSource, MeasurementTiming: p.MeasurementTiming,
			MealType: p.MealType, Specimen: p.Specimen,
		})
		sum += v
		if first || v < lo {
			lo = v
		}
		if first || v > hi {
			hi = v
		}
		first = false
	}
	if len(rows) == 0 {
		return false, nil, nil, nil, nil
	}
	if err := s.upsertBloodGlucoseSamples(ctx, rows); err != nil {
		return false, nil, nil, nil, err
	}

	a := sum / float64(len(rows))
	return true, &a, &lo, &hi, nil
}

// syncCoreBodyTemperature is syncBloodGlucose's exact counterpart for
// core-body-temperature.
func (s *DBSyncer) syncCoreBodyTemperature(ctx context.Context, day time.Time) (found bool, avg, min, max *float64, err error) {
	points, err := fetchSamplePoints[CoreBodyTemperature](ctx, s.Client, "core-body-temperature", day)
	if err != nil {
		return false, nil, nil, nil, err
	}
	if len(points) == 0 {
		return false, nil, nil, nil, nil
	}

	rows := make([]healthdata.CoreBodyTemperatureSample, 0, len(points))
	var sum, lo, hi float64
	first := true
	for _, p := range points {
		t, terr := p.SampleTime.Time()
		if terr != nil {
			continue
		}
		v := float64(p.TemperatureCelsius)
		rows = append(rows, healthdata.CoreBodyTemperatureSample{RecordedAt: t, Celsius: v, MeasurementLocation: p.MeasurementLocation})
		sum += v
		if first || v < lo {
			lo = v
		}
		if first || v > hi {
			hi = v
		}
		first = false
	}
	if len(rows) == 0 {
		return false, nil, nil, nil, nil
	}
	if err := s.upsertCoreBodyTemperatureSamples(ctx, rows); err != nil {
		return false, nil, nil, nil, err
	}

	a := sum / float64(len(rows))
	return true, &a, &lo, &hi, nil
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
	rows := make([]healthdata.HourlySteps, 0, len(hourly))
	for hour, steps := range hourly {
		steps := steps
		rows = append(rows, healthdata.HourlySteps{Day: dayStr, Hour: hour, Steps: &steps})
	}
	if err := s.upsertHourlySteps(ctx, rows); err != nil {
		return false, err
	}
	return true, nil
}

// syncHeartRateIntraday fetches the day's raw heart-rate samples once,
// caches them into watch_heart_rate_intraday (a rolling cache, not the
// source of truth for history — see the table's comment in
// internal/db/schema.sql), and returns the day's min/avg/max for
// watch_daily_summary — computed from this same fetch rather than a
// second API call.
func (s *DBSyncer) syncHeartRateIntraday(ctx context.Context, day time.Time) (found bool, min, max, avg *float64, err error) {
	points, err := fetchSamplePoints[HeartRate](ctx, s.Client, "heart-rate", day)
	if err != nil {
		return false, nil, nil, nil, err
	}
	if len(points) == 0 {
		return false, nil, nil, nil, nil
	}

	rows := make([]healthdata.HeartRateSample, 0, len(points))
	var sum, lo, hi float64
	first := true
	for _, p := range points {
		t, terr := p.SampleTime.Time()
		if terr != nil {
			continue
		}
		v := float64(p.BeatsPerMinute)
		rows = append(rows, healthdata.HeartRateSample{RecordedAt: t, Bpm: v})
		sum += v
		if first || v < lo {
			lo = v
		}
		if first || v > hi {
			hi = v
		}
		first = false
	}
	if len(rows) == 0 {
		return false, nil, nil, nil, nil
	}
	if err := s.upsertHeartRateSamples(ctx, rows); err != nil {
		return false, nil, nil, nil, err
	}

	a := sum / float64(len(rows))
	return true, &lo, &hi, &a, nil
}
