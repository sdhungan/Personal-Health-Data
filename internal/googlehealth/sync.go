package googlehealth

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/healthdata"
)

const dateLayout = "2006-01-02"

// DBSyncer fetches one day of Google Health data and upserts it into
// watch_* tables (see internal/db/schema.sql). It implements the same
// SyncDay(ctx, time.Time) (bool, error) shape internal/syncengine.DaySyncer
// expects, without importing that package — the two are wired together by
// whatever constructs them (the CLI/scheduler), not by a shared type.
//
// Known gaps, deliberately left for later rather than guessed at:
//   - sleep_score, stress_management_score were dropped from the schema
//     entirely (see internal/db/schema.sql) — Fitbit's proprietary
//     sleep/stress scores don't appear anywhere in this API, so the
//     columns could never be populated.
//   - swim-lengths-data (see datatypes.go) is listed for DumpToday to
//     capture but not synced — its field shape has never been confirmed
//     against a real response.
// DBSyncer syncs one user's Google Health data. UserID scopes every write
// this syncer makes — one DBSyncer is constructed per user per sync pass
// (see internal/cli's scheduler fan-out), never shared across users.
type DBSyncer struct {
	Client *Client
	DB     *sql.DB
	UserID int64
}

// SyncDay fetches and upserts every data type healthd currently maps for
// the calendar day day represents (its own Year/Month/Day, interpreted in
// its own Location — see dayBounds), and reports whether any data was
// found at all. Every sync step that contributes to watch_daily_summary
// folds its result into one healthdata.DailySummary, upserted exactly once
// at the end — see fetchDailySummary's doc comment for which steps those
// are and why (Timeline types compute their daily stats from the same
// fetch that populates their sample table, not a second API call).
func (s *DBSyncer) SyncDay(ctx context.Context, day time.Time) (bool, error) {
	dayStr := dayKey(day)
	hasData := false

	summary, summaryHasData, err := s.fetchDailySummary(ctx, day)
	if err != nil {
		return false, fmt.Errorf("fetching daily summary for %s: %w", dayStr, err)
	}
	hasData = hasData || summaryHasData

	activeMinutesFound, total, err := s.syncActiveMinutesByLevel(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing active minutes by level for %s: %w", dayStr, err)
	}
	hasData = hasData || activeMinutesFound
	summary.ActiveMinutesTotal = total

	azmFound, err := s.syncActiveZoneMinutesByZone(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing active zone minutes by zone for %s: %w", dayStr, err)
	}
	hasData = hasData || azmFound

	activityLevelFound, err := s.syncActivityLevelSegments(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing activity level segments for %s: %w", dayStr, err)
	}
	hasData = hasData || activityLevelFound

	hrFound, hrMin, hrMax, hrAvg, err := s.syncHeartRateIntraday(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing intraday heart rate for %s: %w", dayStr, err)
	}
	hasData = hasData || hrFound
	summary.HeartRateMinBpm, summary.HeartRateMaxBpm, summary.HeartRateAvgBpm = hrMin, hrMax, hrAvg

	bgFound, bgAvg, bgMin, bgMax, err := s.syncBloodGlucose(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing blood glucose for %s: %w", dayStr, err)
	}
	hasData = hasData || bgFound
	summary.BloodGlucoseAvgMgDl, summary.BloodGlucoseMinMgDl, summary.BloodGlucoseMaxMgDl = bgAvg, bgMin, bgMax

	cbtFound, cbtAvg, cbtMin, cbtMax, err := s.syncCoreBodyTemperature(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing core body temperature for %s: %w", dayStr, err)
	}
	hasData = hasData || cbtFound
	summary.CoreBodyTemperatureAvgC, summary.CoreBodyTemperatureMinC, summary.CoreBodyTemperatureMaxC = cbtAvg, cbtMin, cbtMax

	if err := s.upsertDailySummary(ctx, summary); err != nil {
		return false, fmt.Errorf("upserting daily summary for %s: %w", dayStr, err)
	}

	sleepFound, err := s.syncSleep(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing sleep for %s: %w", dayStr, err)
	}
	hasData = hasData || sleepFound

	exerciseFound, err := s.syncExercise(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing exercise for %s: %w", dayStr, err)
	}
	hasData = hasData || exerciseFound

	ecgFound, err := s.syncECG(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing ECG for %s: %w", dayStr, err)
	}
	hasData = hasData || ecgFound

	bodyFound, err := s.syncBodyMeasurement(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing body measurement for %s: %w", dayStr, err)
	}
	hasData = hasData || bodyFound

	stepsFound, err := s.syncStepsHourly(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing hourly steps for %s: %w", dayStr, err)
	}
	hasData = hasData || stepsFound

	zoneDefsFound, err := s.syncHeartRateZoneDefinitions(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing heart rate zone definitions for %s: %w", dayStr, err)
	}
	hasData = hasData || zoneDefsFound

	zoneMinutesFound, err := s.syncHeartRateZoneMinutes(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing heart rate zone minutes for %s: %w", dayStr, err)
	}
	hasData = hasData || zoneMinutesFound

	caloriesByZoneFound, err := s.syncCaloriesByZone(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing calories by zone for %s: %w", dayStr, err)
	}
	hasData = hasData || caloriesByZoneFound

	respRateSleepFound, err := s.syncRespiratoryRateSleepSummary(ctx, day)
	if err != nil {
		return false, fmt.Errorf("syncing sleep respiratory rate for %s: %w", dayStr, err)
	}
	hasData = hasData || respRateSleepFound

	return hasData, nil
}

// dayKey is the sync_state/table "day" key for t: its own calendar date,
// read in its own Location. Callers pass either a precise "now" (today,
// Local) or a date-only time.Parse result (historical days, UTC
// midnight) — either way, dayKey and dayBounds derive the same calendar
// day consistently from whichever Location t already carries.
func dayKey(t time.Time) string { return t.Format(dateLayout) }

// dayBounds returns the [start, end) window covering t's calendar day,
// anchored to time.Local — since "today" per internal/syncengine's
// bookkeeping is always the local calendar day, this must match, even
// though the value we're handed for a re-derived historical day arrives
// in UTC (from time.Parse("2006-01-02", ...)).
func dayBounds(t time.Time) (start, end time.Time) {
	y, m, d := t.Date()
	start = time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	end = start.AddDate(0, 0, 1)
	return start, end
}

func isOnDay(t time.Time, day time.Time) bool {
	ty, tm, td := t.In(time.Local).Date()
	dy, dm, dd := day.Date()
	return ty == dy && tm == dm && td == dd
}

// fetchDailySummary builds the DailyScalar portion of the day's
// healthdata.DailySummary: everything with no separate Timeline/category
// table counterpart. Heart-rate/blood-glucose/core-body-temperature stats
// and the active-minutes/active-zone-minutes totals are deliberately NOT
// here — those are computed by their own sync*/upsert* functions (see
// SyncDay) from the same fetch that populates their sample/category
// tables, rather than a second, redundant API call.
func (s *DBSyncer) fetchDailySummary(ctx context.Context, day time.Time) (healthdata.DailySummary, bool, error) {
	out := healthdata.DailySummary{Day: dayKey(day)}
	hasData := false

	// ---- Daily aggregates: list() unfiltered, matched to day client-side ----

	if v, found, err := fetchDailyAggregate[DailyRestingHeartRate](ctx, s.Client, "daily-resting-heart-rate", day,
		func(v DailyRestingHeartRate) Date { return v.Date }); err != nil {
		return out, false, err
	} else if found {
		bpm := float64(v.BeatsPerMinute)
		out.RestingHeartRateBpm = &bpm
		hasData = true
	}

	if v, found, err := fetchDailyAggregate[DailyHeartRateVariability](ctx, s.Client, "daily-heart-rate-variability", day,
		func(v DailyHeartRateVariability) Date { return v.Date }); err != nil {
		return out, false, err
	} else if found {
		ms := float64(v.AverageHeartRateVariabilityMilliseconds)
		out.HrvAvgMs = &ms
		hasData = true
	}

	if v, found, err := fetchDailyAggregate[DailyOxygenSaturation](ctx, s.Client, "daily-oxygen-saturation", day,
		func(v DailyOxygenSaturation) Date { return v.Date }); err != nil {
		return out, false, err
	} else if found {
		avg, lower := float64(v.AveragePercentage), float64(v.LowerBoundPercentage)
		out.Spo2AvgPct, out.Spo2MinPct = &avg, &lower
		hasData = true
	}

	if v, found, err := fetchDailyAggregate[DailyRespiratoryRate](ctx, s.Client, "daily-respiratory-rate", day,
		func(v DailyRespiratoryRate) Date { return v.Date }); err != nil {
		return out, false, err
	} else if found {
		bpm := float64(v.BreathsPerMinute)
		out.RespiratoryRateAvgBpm = &bpm
		hasData = true
	}

	if v, found, err := fetchDailyAggregate[DailyVo2Max](ctx, s.Client, "daily-vo2-max", day,
		func(v DailyVo2Max) Date { return v.Date }); err != nil {
		return out, false, err
	} else if found {
		vo2 := float64(v.Vo2Max)
		out.Vo2Max = &vo2
		hasData = true
	}

	// daily-heart-rate-zones (zone BPM thresholds, not a time series) is
	// synced separately into its own table — see syncHeartRateZoneDefinitions.
	if v, found, err := fetchDailyAggregate[DailySleepTemperatureDerivations](ctx, s.Client, "daily-sleep-temperature-derivations", day,
		func(v DailySleepTemperatureDerivations) Date { return v.Date }); err != nil {
		return out, false, err
	} else if found {
		night, base, dev := float64(v.NightlyTemperatureCelsius), float64(v.BaselineTemperatureCelsius), float64(v.RelativeNightlyStddev30dCelsius)
		out.SleepTemperatureC, out.SleepTemperatureBaselineC, out.SleepTemperatureDeviation30dC = &night, &base, &dev
		hasData = true
	}

	// ---- dailyRollUp-only types (list() rejected outright) ----

	if v, found, err := fetchDailyRollup[TotalCaloriesRollup](ctx, s.Client, "total-calories", day); err != nil {
		return out, false, err
	} else if found {
		kcal := float64(v.KcalSum)
		out.KcalBurnedGoogle = &kcal
		hasData = true
	}

	// floors' own rollup response has always come back empty for this
	// account, so FloorsRollup's shape (values.go) is a best-effort guess,
	// not confirmed — safe if wrong, ExtractRollupValues simply finds no
	// points rather than silently decoding the wrong field.
	if v, found, err := fetchDailyRollup[FloorsRollup](ctx, s.Client, "floors", day); err != nil {
		return out, false, err
	} else if found {
		climbed := int64(v.FloorsSum)
		out.FloorsClimbed = &climbed
		hasData = true
	}

	// ---- Interval types, filtered server-side to this day ----

	if steps, err := fetchIntervalPoints[Steps](ctx, s.Client, "steps", day); err != nil {
		return out, false, err
	} else if len(steps) > 0 {
		var total int64
		for _, p := range steps {
			total += int64(p.Count)
		}
		out.StepsTotal = &total
		hasData = true
	}

	if dist, err := fetchIntervalPoints[Distance](ctx, s.Client, "distance", day); err != nil {
		return out, false, err
	} else if len(dist) > 0 {
		var total int64
		for _, p := range dist {
			total += int64(p.Meters)
		}
		metersF := float64(total)
		out.DistanceM = &metersF
		hasData = true
	}

	if alt, err := fetchIntervalPoints[Altitude](ctx, s.Client, "altitude", day); err != nil {
		return out, false, err
	} else if len(alt) > 0 {
		var totalMm int64
		for _, p := range alt {
			totalMm += int64(p.GainMillimeters)
		}
		gainM := float64(totalMm) / 1000
		out.AltitudeGainM = &gainM
		hasData = true
	}

	if sed, err := fetchIntervalPoints[SedentaryPeriod](ctx, s.Client, "sedentary-period", day); err != nil {
		return out, false, err
	} else if len(sed) > 0 {
		var totalMinutes int64
		for _, p := range sed {
			start, errS := p.Interval.Start()
			end, errE := p.Interval.End()
			if errS == nil && errE == nil && end.After(start) {
				totalMinutes += int64(end.Sub(start).Minutes())
			}
		}
		out.SedentaryMinutes = &totalMinutes
		hasData = true
	}

	if aeb, err := fetchIntervalPoints[ActiveEnergyBurned](ctx, s.Client, "active-energy-burned", day); err != nil {
		return out, false, err
	} else if len(aeb) > 0 {
		var total float64
		for _, p := range aeb {
			total += float64(p.Kcal)
		}
		out.ActiveEnergyBurnedKcal = &total
		hasData = true
	}

	// ---- Sample types, last-of-day (same pattern as syncBodyMeasurement's
	// weight/height/body-fat) ----

	if pts, err := fetchSamplePoints[Vo2Max](ctx, s.Client, "vo2-max", day); err != nil {
		return out, false, err
	} else if len(pts) > 0 {
		v := float64(pts[len(pts)-1].Vo2MaxMillilitersPerKgPerMinute)
		out.Vo2MaxSample = &v
		hasData = true
	}

	if pts, err := fetchSamplePoints[RunVo2Max](ctx, s.Client, "run-vo2-max", day); err != nil {
		return out, false, err
	} else if len(pts) > 0 {
		v := float64(pts[len(pts)-1].MlPerKgPerMin)
		out.Vo2MaxRunSample = &v
		hasData = true
	}

	// ---- Sleep duration, from the same sleep sessions synced separately ----
	if sleeps, err := fetchUnfilteredForDay[Sleep](ctx, s.Client, "sleep", day, func(v Sleep) (time.Time, bool) {
		t, err := v.Interval.Start()
		return t, err == nil
	}); err != nil {
		return out, false, err
	} else if len(sleeps) > 0 {
		var total int64
		for _, sess := range sleeps {
			total += int64(sess.Summary.MinutesAsleep)
		}
		out.SleepDurationMinutes = &total
		hasData = true
	}

	return out, hasData, nil
}

// fetchDailyAggregate fetches a daily-aggregate data type (list, no
// server-side filter available) and returns the single point matching
// day, if any.
func fetchDailyAggregate[T any](ctx context.Context, client *Client, dataTypeName string, day time.Time, dateOf func(T) Date) (T, bool, error) {
	var zero T
	raw, err := client.ListDataPoints(ctx, dataTypeName, "", 100)
	if err != nil {
		return zero, false, fmt.Errorf("listing %s: %w", dataTypeName, err)
	}
	points, err := ExtractValues[T](raw, ValueKey(dataTypeName))
	if err != nil {
		return zero, false, fmt.Errorf("decoding %s: %w", dataTypeName, err)
	}
	dayStr := dayKey(day)
	for _, p := range points {
		if dateOf(p).Time().Format(dateLayout) == dayStr {
			return p, true, nil
		}
	}
	return zero, false, nil
}

// fetchDailyRollup fetches a single dailyRollUp value for exactly day's
// civil range (window_size_days defaults to 1) and decodes it — the
// generic form of the floors/total-calories/calories-in-heart-rate-zone
// "list() is rejected, dailyRollUp is the only path" pattern.
func fetchDailyRollup[T any](ctx context.Context, client *Client, dataTypeName string, day time.Time) (T, bool, error) {
	var zero T
	start, end := dayBounds(day)
	raw, err := client.DailyRollUp(ctx, dataTypeName,
		CivilDateTime{Date: Date{Year: start.Year(), Month: int(start.Month()), Day: start.Day()}},
		CivilDateTime{Date: Date{Year: end.Year(), Month: int(end.Month()), Day: end.Day()}})
	if err != nil {
		return zero, false, fmt.Errorf("%s dailyRollUp: %w", dataTypeName, err)
	}
	points, err := ExtractRollupValues[T](raw, ValueKey(dataTypeName))
	if err != nil {
		return zero, false, fmt.Errorf("decoding %s rollup: %w", dataTypeName, err)
	}
	if len(points) == 0 {
		return zero, false, nil
	}
	return points[0].Value, true, nil
}

// fetchIntervalPoints fetches an interval-kind data type filtered
// server-side to day's window.
func fetchIntervalPoints[T any](ctx context.Context, client *Client, dataTypeName string, day time.Time) ([]T, error) {
	filter := intervalDayFilter(dataTypeName, day)
	raw, err := client.ListDataPoints(ctx, dataTypeName, filter, 10000)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dataTypeName, err)
	}
	points, err := ExtractValues[T](raw, ValueKey(dataTypeName))
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", dataTypeName, err)
	}
	return points, nil
}

// fetchSamplePoints fetches a sample-kind data type filtered server-side
// to day's window.
func fetchSamplePoints[T any](ctx context.Context, client *Client, dataTypeName string, day time.Time) ([]T, error) {
	filter := sampleDayFilter(dataTypeName, day)
	raw, err := client.ListDataPoints(ctx, dataTypeName, filter, 10000)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dataTypeName, err)
	}
	points, err := ExtractValues[T](raw, ValueKey(dataTypeName))
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", dataTypeName, err)
	}
	return points, nil
}

// fetchUnfilteredForDay fetches a session-kind data type unfiltered (see
// dump.go's filterFor doc comment on why session filtering isn't used)
// and returns only the points whose start time (per startTimeOf) falls on
// day.
func fetchUnfilteredForDay[T any](ctx context.Context, client *Client, dataTypeName string, day time.Time, startTimeOf func(T) (time.Time, bool)) ([]T, error) {
	raw, err := client.ListDataPoints(ctx, dataTypeName, "", 100)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dataTypeName, err)
	}
	points, err := ExtractValues[T](raw, ValueKey(dataTypeName))
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", dataTypeName, err)
	}
	var out []T
	for _, p := range points {
		t, ok := startTimeOf(p)
		if ok && isOnDay(t, day) {
			out = append(out, p)
		}
	}
	return out, nil
}
