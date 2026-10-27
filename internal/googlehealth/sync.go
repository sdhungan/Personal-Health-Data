package googlehealth

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

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
//
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

	// Phase 1: fetchDailySummary (itself fanned out internally — see its
	// own doc comment) plus every other step that folds a result into the
	// same watch_daily_summary row, run concurrently rather than one after
	// another — nothing here depends on anything else in this phase, only
	// on all of them finishing before the one combined upsert below.
	var (
		summary                            healthdata.DailySummary
		summaryHasData, activeMinutesFound bool
		activeMinutesTotal                 *int64
		azmFound, activityLevelFound       bool
		hrFound                            bool
		hrMin, hrMax, hrAvg                *float64
		bgFound                            bool
		bgAvg, bgMin, bgMax                *float64
		cbtFound                           bool
		cbtAvg, cbtMin, cbtMax             *float64
	)
	g1, gctx1 := errgroup.WithContext(ctx)
	g1.Go(func() (err error) {
		summary, summaryHasData, err = s.fetchDailySummary(gctx1, day)
		return err
	})
	g1.Go(func() (err error) {
		activeMinutesFound, activeMinutesTotal, err = s.syncActiveMinutesByLevel(gctx1, day)
		return err
	})
	g1.Go(func() (err error) {
		azmFound, err = s.syncActiveZoneMinutesByZone(gctx1, day)
		return err
	})
	g1.Go(func() (err error) {
		activityLevelFound, err = s.syncActivityLevelSegments(gctx1, day)
		return err
	})
	g1.Go(func() (err error) {
		hrFound, hrMin, hrMax, hrAvg, err = s.syncHeartRateIntraday(gctx1, day)
		return err
	})
	g1.Go(func() (err error) {
		bgFound, bgAvg, bgMin, bgMax, err = s.syncBloodGlucose(gctx1, day)
		return err
	})
	g1.Go(func() (err error) {
		cbtFound, cbtAvg, cbtMin, cbtMax, err = s.syncCoreBodyTemperature(gctx1, day)
		return err
	})
	if err := g1.Wait(); err != nil {
		return false, fmt.Errorf("syncing %s: %w", dayStr, err)
	}

	hasData := summaryHasData || activeMinutesFound || azmFound || activityLevelFound || hrFound || bgFound || cbtFound
	summary.ActiveMinutesTotal = activeMinutesTotal
	summary.HeartRateMinBpm, summary.HeartRateMaxBpm, summary.HeartRateAvgBpm = hrMin, hrMax, hrAvg
	summary.BloodGlucoseAvgMgDl, summary.BloodGlucoseMinMgDl, summary.BloodGlucoseMaxMgDl = bgAvg, bgMin, bgMax
	summary.CoreBodyTemperatureAvgC, summary.CoreBodyTemperatureMinC, summary.CoreBodyTemperatureMaxC = cbtAvg, cbtMin, cbtMax

	if err := s.upsertDailySummary(ctx, summary); err != nil {
		return false, fmt.Errorf("upserting daily summary for %s: %w", dayStr, err)
	}

	// Phase 2: everything else, each writing to its own table independent
	// of phase 1 and of each other — except syncRespiratoryRateSleepSummary,
	// which looks up the sleep session syncSleep just wrote (by day/time
	// overlap) and UPDATEs it: a real data dependency, not just the
	// sequential list's incidental ordering it was hiding behind before
	// (confirmed the hard way — a flaky test failure during this refactor,
	// from it running before syncSleep's insert was visible, finding no
	// session to attach to, and silently dropping that day's respiratory
	// data). Chained within its own single goroutine below so the rest of
	// phase 2 still doesn't wait on either of them.
	var (
		sleepFound, exerciseFound, ecgFound, bodyFound, stepsFound bool
		zoneDefsFound, zoneMinutesFound, caloriesByZoneFound       bool
		respRateSleepFound                                         bool
	)
	g2, gctx2 := errgroup.WithContext(ctx)
	g2.Go(func() error {
		var err error
		sleepFound, err = s.syncSleep(gctx2, day)
		if err != nil {
			return err
		}
		respRateSleepFound, err = s.syncRespiratoryRateSleepSummary(gctx2, day)
		return err
	})
	g2.Go(func() (err error) { exerciseFound, err = s.syncExercise(gctx2, day); return err })
	g2.Go(func() (err error) { ecgFound, err = s.syncECG(gctx2, day); return err })
	g2.Go(func() (err error) { bodyFound, err = s.syncBodyMeasurement(gctx2, day); return err })
	g2.Go(func() (err error) { stepsFound, err = s.syncStepsHourly(gctx2, day); return err })
	g2.Go(func() (err error) { zoneDefsFound, err = s.syncHeartRateZoneDefinitions(gctx2, day); return err })
	g2.Go(func() (err error) { zoneMinutesFound, err = s.syncHeartRateZoneMinutes(gctx2, day); return err })
	g2.Go(func() (err error) { caloriesByZoneFound, err = s.syncCaloriesByZone(gctx2, day); return err })
	if err := g2.Wait(); err != nil {
		return false, fmt.Errorf("syncing %s: %w", dayStr, err)
	}
	hasData = hasData || sleepFound || exerciseFound || ecgFound || bodyFound || stepsFound ||
		zoneDefsFound || zoneMinutesFound || caloriesByZoneFound || respRateSleepFound

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

	// Every one of the 16 data types below is fetched independently of
	// every other — nothing here reads a value another fetch produced —
	// so they run concurrently instead of one after another. This used to
	// be 16 sequential Google Health API round trips, which is exactly
	// what made a never-before-synced day (or the scheduler's own pass)
	// take "15-20+ seconds" (see runGoogleRangeSync's doc comment in
	// internal/web/settings.go): now bounded by whichever single call is
	// slowest, not their sum. Each goroutine below only ever writes its
	// own result variable, never touching another's or `out` directly —
	// everything is folded into `out`/`hasData` sequentially, after
	// g.Wait(), in the exact same order and with the exact same
	// per-type logic this function always had, so a day's computed
	// totals are unchanged, only how long they take to get here.
	var (
		restingHR      DailyRestingHeartRate
		restingHRFound bool
		hrv            DailyHeartRateVariability
		hrvFound       bool
		spo2           DailyOxygenSaturation
		spo2Found      bool
		respRate       DailyRespiratoryRate
		respRateFound  bool
		vo2Daily       DailyVo2Max
		vo2DailyFound  bool
		sleepTemp      DailySleepTemperatureDerivations
		sleepTempFound bool
		totalCal       TotalCaloriesRollup
		totalCalFound  bool
		floors         FloorsRollup
		floorsFound    bool
		steps          []SourcedValue[Steps]
		distance       DistanceRollup
		distanceFound  bool
		altitude       []Altitude
		sedentary      []SedentaryPeriod
		activeEnergy   []ActiveEnergyBurned
		vo2Sample      []Vo2Max
		runVo2Sample   []RunVo2Max
		sleeps         []Sleep
	)

	g, gctx := errgroup.WithContext(ctx)

	// ---- Daily aggregates: list() unfiltered, matched to day client-side ----
	g.Go(func() (err error) {
		restingHR, restingHRFound, err = fetchDailyAggregate[DailyRestingHeartRate](gctx, s.Client, "daily-resting-heart-rate", day,
			func(v DailyRestingHeartRate) Date { return v.Date })
		return err
	})
	g.Go(func() (err error) {
		hrv, hrvFound, err = fetchDailyAggregate[DailyHeartRateVariability](gctx, s.Client, "daily-heart-rate-variability", day,
			func(v DailyHeartRateVariability) Date { return v.Date })
		return err
	})
	g.Go(func() (err error) {
		spo2, spo2Found, err = fetchDailyAggregate[DailyOxygenSaturation](gctx, s.Client, "daily-oxygen-saturation", day,
			func(v DailyOxygenSaturation) Date { return v.Date })
		return err
	})
	g.Go(func() (err error) {
		respRate, respRateFound, err = fetchDailyAggregate[DailyRespiratoryRate](gctx, s.Client, "daily-respiratory-rate", day,
			func(v DailyRespiratoryRate) Date { return v.Date })
		return err
	})
	g.Go(func() (err error) {
		vo2Daily, vo2DailyFound, err = fetchDailyAggregate[DailyVo2Max](gctx, s.Client, "daily-vo2-max", day,
			func(v DailyVo2Max) Date { return v.Date })
		return err
	})
	// daily-heart-rate-zones (zone BPM thresholds, not a time series) is
	// synced separately into its own table — see syncHeartRateZoneDefinitions.
	g.Go(func() (err error) {
		sleepTemp, sleepTempFound, err = fetchDailyAggregate[DailySleepTemperatureDerivations](gctx, s.Client, "daily-sleep-temperature-derivations", day,
			func(v DailySleepTemperatureDerivations) Date { return v.Date })
		return err
	})

	// ---- dailyRollUp-only types (list() rejected outright) ----
	g.Go(func() (err error) {
		totalCal, totalCalFound, err = fetchDailyRollup[TotalCaloriesRollup](gctx, s.Client, "total-calories", day)
		return err
	})
	g.Go(func() (err error) {
		floors, floorsFound, err = fetchDailyRollup[FloorsRollup](gctx, s.Client, "floors", day)
		return err
	})
	// distance: rollup, not list() — see DistanceRollup.
	g.Go(func() (err error) {
		distance, distanceFound, err = fetchDailyRollup[DistanceRollup](gctx, s.Client, "distance", day)
		return err
	})

	// ---- Interval types, filtered server-side to this day ----
	g.Go(func() (err error) {
		steps, err = fetchIntervalPointsWithSource[Steps](gctx, s.Client, "steps", day)
		return err
	})
	g.Go(func() (err error) {
		altitude, err = fetchIntervalPoints[Altitude](gctx, s.Client, "altitude", day)
		return err
	})
	g.Go(func() (err error) {
		sedentary, err = fetchIntervalPoints[SedentaryPeriod](gctx, s.Client, "sedentary-period", day)
		return err
	})
	g.Go(func() (err error) {
		activeEnergy, err = fetchIntervalPoints[ActiveEnergyBurned](gctx, s.Client, "active-energy-burned", day)
		return err
	})

	// ---- Sample types, last-of-day (same pattern as syncBodyMeasurement's
	// weight/height/body-fat) ----
	g.Go(func() (err error) {
		vo2Sample, err = fetchSamplePoints[Vo2Max](gctx, s.Client, "vo2-max", day)
		return err
	})
	g.Go(func() (err error) {
		runVo2Sample, err = fetchSamplePoints[RunVo2Max](gctx, s.Client, "run-vo2-max", day)
		return err
	})

	// ---- Sleep duration, from the same sleep sessions synced separately ----
	// Keyed by wake-up day, same as syncSleep.
	g.Go(func() (err error) {
		sleeps, err = fetchUnfilteredForDay[Sleep](gctx, s.Client, "sleep", day, func(v Sleep) (time.Time, bool) {
			t, err := v.Interval.End()
			return t, err == nil
		})
		return err
	})

	if err := g.Wait(); err != nil {
		return out, false, err
	}

	hasData := false
	if restingHRFound {
		bpm := float64(restingHR.BeatsPerMinute)
		out.RestingHeartRateBpm = &bpm
		hasData = true
	}
	if hrvFound {
		ms := float64(hrv.AverageHeartRateVariabilityMilliseconds)
		out.HrvAvgMs = &ms
		hasData = true
	}
	if spo2Found {
		avg, lower := float64(spo2.AveragePercentage), float64(spo2.LowerBoundPercentage)
		out.Spo2AvgPct, out.Spo2MinPct = &avg, &lower
		hasData = true
	}
	if respRateFound {
		bpm := float64(respRate.BreathsPerMinute)
		out.RespiratoryRateAvgBpm = &bpm
		hasData = true
	}
	if vo2DailyFound {
		vo2 := float64(vo2Daily.Vo2Max)
		out.Vo2Max = &vo2
		hasData = true
	}
	if sleepTempFound {
		night, base, dev := float64(sleepTemp.NightlyTemperatureCelsius), float64(sleepTemp.BaselineTemperatureCelsius), float64(sleepTemp.RelativeNightlyStddev30dCelsius)
		out.SleepTemperatureC, out.SleepTemperatureBaselineC, out.SleepTemperatureDeviation30dC = &night, &base, &dev
		hasData = true
	}
	if totalCalFound {
		kcal := float64(totalCal.KcalSum)
		out.KcalBurnedGoogle = &kcal
		hasData = true
	}
	if floorsFound {
		climbed := int64(floors.CountSum)
		out.FloorsClimbed = &climbed
		hasData = true
	}
	if len(steps) > 0 {
		var total int64
		for _, count := range dedupedHourlySteps(steps) {
			total += count
		}
		out.StepsTotal = &total
		hasData = true
	}
	if distanceFound {
		meters := float64(distance.MillimetersSum) / 1000
		out.DistanceM = &meters
		hasData = true
	}
	if len(altitude) > 0 {
		var totalMm int64
		for _, p := range altitude {
			totalMm += int64(p.GainMillimeters)
		}
		gainM := float64(totalMm) / 1000
		out.AltitudeGainM = &gainM
		hasData = true
	}
	if len(sedentary) > 0 {
		var totalMinutes int64
		for _, p := range sedentary {
			start, errS := p.Interval.Start()
			end, errE := p.Interval.End()
			if errS == nil && errE == nil && end.After(start) {
				totalMinutes += int64(end.Sub(start).Minutes())
			}
		}
		out.SedentaryMinutes = &totalMinutes
		hasData = true
	}
	if len(activeEnergy) > 0 {
		var total float64
		for _, p := range activeEnergy {
			total += float64(p.Kcal)
		}
		out.ActiveEnergyBurnedKcal = &total
		hasData = true
	}
	if len(vo2Sample) > 0 {
		v := float64(vo2Sample[len(vo2Sample)-1].Vo2MaxMillilitersPerKgPerMinute)
		out.Vo2MaxSample = &v
		hasData = true
	}
	if len(runVo2Sample) > 0 {
		v := float64(runVo2Sample[len(runVo2Sample)-1].MlPerKgPerMin)
		out.Vo2MaxRunSample = &v
		hasData = true
	}
	if len(sleeps) > 0 {
		// Main sleep plus naps: every session ending on this day.
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

// fetchIntervalPointsWithSource is fetchIntervalPoints, but keeps each
// point's data source (see ExtractValuesWithSource) — used by metrics where
// more than one device reporting the same period must be deduplicated
// rather than summed together (currently just steps; see
// dedupedHourlySteps).
func fetchIntervalPointsWithSource[T any](ctx context.Context, client *Client, dataTypeName string, day time.Time) ([]SourcedValue[T], error) {
	filter := intervalDayFilter(dataTypeName, day)
	raw, err := client.ListDataPoints(ctx, dataTypeName, filter, 10000)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dataTypeName, err)
	}
	points, err := ExtractValuesWithSource[T](raw, ValueKey(dataTypeName))
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
