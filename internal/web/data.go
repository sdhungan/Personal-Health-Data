package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/healthdata"
	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

const dateLayout = "2006-01-02"
const stepGoal = 10000.0

// dailySummaryColumns lists watch_daily_summary's columns in the exact
// order scanDailySummaryRow reads them, matching healthdata.DailySummary's
// field order — see that type's doc comment for why every field is a
// plain pointer: database/sql scans NULL into a nil *T and a real value
// into an allocated one when the destination is **T (confirmed against
// modernc.org/sqlite), so this reads straight into the same struct the
// sync job writes, no intermediate sql.Null* type needed.
const dailySummaryColumns = `steps_total, distance_m, floors_climbed, altitude_gain_m, sedentary_minutes, active_minutes,
	kcal_burned_google, active_energy_burned_kcal,
	resting_heart_rate_bpm, heart_rate_min_bpm, heart_rate_max_bpm, heart_rate_avg_bpm, hrv_avg_ms,
	vo2_max, vo2_max_sample, vo2_max_run_sample,
	spo2_avg_pct, spo2_min_pct, respiratory_rate_avg_bpm,
	blood_glucose_avg_mg_dl, blood_glucose_min_mg_dl, blood_glucose_max_mg_dl,
	core_body_temperature_avg_c, core_body_temperature_min_c, core_body_temperature_max_c,
	sleep_duration_minutes, sleep_temperature_c, sleep_temperature_baseline_c, sleep_temperature_deviation_30d_c`

// dashboardDailyRow is web's own composite view of one day, spanning both
// data sources: healthdata.DailySummary (Google Health) embedded directly,
// plus the Cronometer macros the dashboard also shows as stat tiles. This
// composite lives here, not in internal/healthdata, since "one day's
// dashboard row" is a web-layer concept that legitimately spans sources —
// healthdata.DailySummary itself stays Google-Health-only, matching
// internal/cronometer's own domain (NutritionAmounts etc.) staying
// Cronometer-only.
type dashboardDailyRow struct {
	healthdata.DailySummary
	NutritionEnergyKcal *float64
	NutritionKcalBurned *float64 // cronometer_daily_nutrition.kcal_burned_cronometer — Cronometer's own BMR+activity+TEF estimate, not watch_daily_summary.kcal_burned_google
	NutritionProteinG   *float64
	NutritionCarbsG     *float64
	NutritionFatG       *float64
}

func scanDailySummaryRow(row *healthdata.DailySummary) []any {
	return []any{
		&row.StepsTotal, &row.DistanceM, &row.FloorsClimbed, &row.AltitudeGainM, &row.SedentaryMinutes, &row.ActiveMinutesTotal,
		&row.KcalBurnedGoogle, &row.ActiveEnergyBurnedKcal,
		&row.RestingHeartRateBpm, &row.HeartRateMinBpm, &row.HeartRateMaxBpm, &row.HeartRateAvgBpm, &row.HrvAvgMs,
		&row.Vo2Max, &row.Vo2MaxSample, &row.Vo2MaxRunSample,
		&row.Spo2AvgPct, &row.Spo2MinPct, &row.RespiratoryRateAvgBpm,
		&row.BloodGlucoseAvgMgDl, &row.BloodGlucoseMinMgDl, &row.BloodGlucoseMaxMgDl,
		&row.CoreBodyTemperatureAvgC, &row.CoreBodyTemperatureMinC, &row.CoreBodyTemperatureMaxC,
		&row.SleepDurationMinutes, &row.SleepTemperatureC, &row.SleepTemperatureBaselineC, &row.SleepTemperatureDeviation30dC,
	}
}

// fetchDailySummaryRow loads day's watch_daily_summary row (zero-value if
// none yet — every field nil, tiles render their "no data" state for
// that) plus cronometer_daily_nutrition's macros, merged onto the same
// struct since a day can have nutrition data with no watch_daily_summary
// row at all (food logged, watch never synced).
func fetchDailySummaryRow(ctx context.Context, db *sql.DB, day string) (dashboardDailyRow, error) {
	r := dashboardDailyRow{DailySummary: healthdata.DailySummary{Day: day}}
	err := db.QueryRowContext(ctx, `SELECT `+dailySummaryColumns+` FROM watch_daily_summary WHERE day = ?`, day).
		Scan(scanDailySummaryRow(&r.DailySummary)...)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("querying watch_daily_summary for %s: %w", day, err)
	}

	err = db.QueryRowContext(ctx, `SELECT energy_kcal, kcal_burned_cronometer, protein_g, carbs_g, fat_g FROM cronometer_daily_nutrition WHERE day = ?`, day).
		Scan(&r.NutritionEnergyKcal, &r.NutritionKcalBurned, &r.NutritionProteinG, &r.NutritionCarbsG, &r.NutritionFatG)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("querying cronometer_daily_nutrition for %s: %w", day, err)
	}
	return r, nil
}

// fetch7DayRows returns a day->row map for the 7 days ending on endDay
// (inclusive), missing days simply absent from the map.
func fetch7DayRows(ctx context.Context, db *sql.DB, endDay time.Time) (map[string]dashboardDailyRow, error) {
	start := endDay.AddDate(0, 0, -6).Format(dateLayout)
	end := endDay.Format(dateLayout)

	rows, err := db.QueryContext(ctx, `SELECT day, `+dailySummaryColumns+` FROM watch_daily_summary WHERE day BETWEEN ? AND ?`, start, end)
	if err != nil {
		return nil, fmt.Errorf("querying 7-day watch_daily_summary range: %w", err)
	}
	defer rows.Close()

	out := map[string]dashboardDailyRow{}
	for rows.Next() {
		var day string
		var r healthdata.DailySummary
		dest := append([]any{&day}, scanDailySummaryRow(&r)...)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning watch_daily_summary row: %w", err)
		}
		r.Day = day
		out[day] = dashboardDailyRow{DailySummary: r}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Merged in by day key, not a JOIN — a day can have nutrition data with
	// no watch_daily_summary row at all (food logged, watch never synced),
	// and this must not silently drop that day.
	nutRows, err := db.QueryContext(ctx, `SELECT day, energy_kcal, kcal_burned_cronometer, protein_g, carbs_g, fat_g FROM cronometer_daily_nutrition WHERE day BETWEEN ? AND ?`, start, end)
	if err != nil {
		return nil, fmt.Errorf("querying 7-day cronometer_daily_nutrition range: %w", err)
	}
	defer nutRows.Close()
	for nutRows.Next() {
		var day string
		var energy, burned, protein, carbs, fat *float64
		if err := nutRows.Scan(&day, &energy, &burned, &protein, &carbs, &fat); err != nil {
			return nil, fmt.Errorf("scanning cronometer_daily_nutrition row: %w", err)
		}
		r := out[day]
		r.Day = day
		r.NutritionEnergyKcal, r.NutritionKcalBurned, r.NutritionProteinG, r.NutritionCarbsG, r.NutritionFatG = energy, burned, protein, carbs, fat
		out[day] = r
	}
	return out, nutRows.Err()
}

// fromInt64/fromFloat64 adapt healthdata.DailySummary's *int64/*float64
// fields to metricDef.Extract's (float64, bool) shape — nil means "not
// found for this day" (false), same convention the sync layer's pointer
// fields already use.
func fromInt64(p *int64) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return float64(*p), true
}

func fromFloat64(p *float64) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

// metricDef describes one stat-tile's data: how to pull today's value and
// a 7-day series out of a healthdata.DailySummary, and how to format it.
type metricDef struct {
	Title    string
	Icon     string
	Category string // dashboard section: "activity", "heart", "respiratory", "sleep"
	Unit     string
	EmptyMsg string
	Extract  func(dashboardDailyRow) (float64, bool)
	Format   func(float64) string
}

func fmtInt(v float64) string    { return views.FormatNumber(int64(v)) }
func fmtOneDec(v float64) string { return fmt.Sprintf("%.1f", v) }
func fmtNoDec(v float64) string  { return fmt.Sprintf("%.0f", v) }

var metricDefs = map[string]metricDef{
	"steps": {
		Title: "Steps", Icon: "steps", Category: "activity", Unit: "steps", EmptyMsg: "No steps recorded",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromInt64(r.StepsTotal) },
		Format:  fmtInt,
	},
	"calories": {
		Title: "Calories (Google)", Icon: "flame", Category: "activity", Unit: "kcal", EmptyMsg: "No calories to eat",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.KcalBurnedGoogle) },
		Format:  fmtNoDec,
	},
	"active_energy": {
		Title: "Active Energy", Icon: "flame", Category: "activity", Unit: "kcal", EmptyMsg: "No active energy data",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.ActiveEnergyBurnedKcal) },
		Format:  fmtNoDec,
	},
	"distance": {
		Title: "Distance", Icon: "pin", Category: "activity", Unit: "km", EmptyMsg: "No distance recorded",
		Extract: func(r dashboardDailyRow) (float64, bool) {
			v, ok := fromFloat64(r.DistanceM)
			return v / 1000, ok
		},
		Format: fmtOneDec,
	},
	"floors": {
		Title: "Floors", Icon: "stairs", Category: "activity", Unit: "floors", EmptyMsg: "No floor data",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromInt64(r.FloorsClimbed) },
		Format:  fmtInt,
	},
	"altitude": {
		Title: "Altitude Gain", Icon: "mountain", Category: "activity", Unit: "m", EmptyMsg: "No altitude data",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.AltitudeGainM) },
		Format:  fmtNoDec,
	},
	"active_minutes": {
		Title: "Active minutes", Icon: "bolt", Category: "activity", Unit: "min", EmptyMsg: "No active minutes recorded",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromInt64(r.ActiveMinutesTotal) },
		Format:  fmtInt,
	},
	"sedentary": {
		Title: "Sedentary Time", Icon: "chair", Category: "activity", Unit: "", EmptyMsg: "No sedentary time data",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromInt64(r.SedentaryMinutes) },
		Format:  func(v float64) string { return views.FormatMinutes(int64(v)) },
	},
	"heart_rate": {
		Title: "Heart rate", Icon: "heart", Category: "heart", Unit: "bpm avg", EmptyMsg: "No heart rate data",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.HeartRateAvgBpm) },
		Format:  fmtNoDec,
	},
	"resting_hr": {
		Title: "Resting heart rate", Icon: "heart", Category: "heart", Unit: "bpm", EmptyMsg: "No resting heart rate data",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.RestingHeartRateBpm) },
		Format:  fmtNoDec,
	},
	"hrv": {
		Title: "Heart rate variability", Icon: "heart", Category: "heart", Unit: "ms", EmptyMsg: "No HRV data",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.HrvAvgMs) },
		Format:  fmtOneDec,
	},
	"spo2": {
		Title: "Blood oxygen", Icon: "drop", Category: "respiratory", Unit: "% SpO2", EmptyMsg: "No SpO2 data",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.Spo2AvgPct) },
		Format:  fmtOneDec,
	},
	"respiratory_rate": {
		Title: "Respiratory rate", Icon: "lungs", Category: "respiratory", Unit: "breaths/min", EmptyMsg: "No respiratory rate data",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.RespiratoryRateAvgBpm) },
		Format:  fmtOneDec,
	},
	"vo2max": {
		// Prefers the daily aggregate (daily-vo2-max) but falls back to
		// whichever sample source (vo2-max, then run-vo2-max) has a value
		// for the day — three different upstream sources for the same
		// underlying concept, kept as one tile rather than three.
		Title: "VO2 max", Icon: "lungs", Category: "respiratory", Unit: "ml/kg/min", EmptyMsg: "No VO2 max data",
		Extract: func(r dashboardDailyRow) (float64, bool) {
			if v, ok := fromFloat64(r.Vo2Max); ok {
				return v, true
			}
			if v, ok := fromFloat64(r.Vo2MaxSample); ok {
				return v, true
			}
			return fromFloat64(r.Vo2MaxRunSample)
		},
		Format: fmtOneDec,
	},
	"blood_glucose": {
		Title: "Blood Glucose", Icon: "drop", Category: "body", Unit: "mg/dL avg", EmptyMsg: "No blood glucose data",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.BloodGlucoseAvgMgDl) },
		Format:  fmtNoDec,
	},
	"core_body_temp": {
		Title: "Core Body Temp", Icon: "thermometer", Category: "body", Unit: "°C avg", EmptyMsg: "No core temperature data",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.CoreBodyTemperatureAvgC) },
		Format:  fmtOneDec,
	},
	"sleep": {
		Title: "Sleep", Icon: "moon", Category: "sleep", Unit: "", EmptyMsg: "No sleep recorded",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromInt64(r.SleepDurationMinutes) },
		Format:  func(v float64) string { return views.FormatMinutes(int64(v)) },
	},
	"nutrition_energy": {
		Title: "Energy (Cronometer)", Icon: "apple", Category: "nutrition", Unit: "kcal", EmptyMsg: "No food logged",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.NutritionEnergyKcal) },
		Format:  fmtNoDec,
	},
	"nutrition_expenditure": {
		Title: "Expenditure (Cronometer)", Icon: "apple", Category: "nutrition", Unit: "kcal", EmptyMsg: "No expenditure data",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.NutritionKcalBurned) },
		Format:  fmtNoDec,
	},
	"nutrition_deficit": {
		// Cronometer's own convention: deficit = expenditure - consumption
		// (positive means you burned more than you ate). Needs both sides
		// present for the day, unlike the other nutrition tiles which only
		// need their one column.
		Title: "Deficit (Cronometer)", Icon: "apple", Category: "nutrition", Unit: "kcal", EmptyMsg: "No expenditure/consumption data",
		Extract: func(r dashboardDailyRow) (float64, bool) {
			burned, ok := fromFloat64(r.NutritionKcalBurned)
			if !ok {
				return 0, false
			}
			consumed, ok := fromFloat64(r.NutritionEnergyKcal)
			if !ok {
				return 0, false
			}
			return burned - consumed, true
		},
		Format: fmtNoDec,
	},
	"nutrition_protein": {
		Title: "Protein", Icon: "apple", Category: "nutrition", Unit: "g", EmptyMsg: "No food logged",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.NutritionProteinG) },
		Format:  fmtNoDec,
	},
	"nutrition_carbs": {
		Title: "Carbs", Icon: "apple", Category: "nutrition", Unit: "g", EmptyMsg: "No food logged",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.NutritionCarbsG) },
		Format:  fmtNoDec,
	},
	"nutrition_fat": {
		Title: "Fat", Icon: "apple", Category: "nutrition", Unit: "g", EmptyMsg: "No food logged",
		Extract: func(r dashboardDailyRow) (float64, bool) { return fromFloat64(r.NutritionFatG) },
		Format:  fmtNoDec,
	},
}

// DefaultTileKinds is every tile shown on a fresh dashboard. "steps" is
// deliberately first and pre-expanded — the reference dashboard's default
// "steps, last 7 days" view.
var DefaultTileKinds = []string{
	"steps", "calories", "active_energy", "distance", "active_minutes", "active_minutes_by_level",
	"floors", "altitude", "sedentary", "activity_level", "activities",
	"heart_rate", "resting_hr", "hrv", "hr_zones", "active_zone_minutes_by_zone",
	"body", "blood_glucose", "core_body_temp",
	"sleep",
	"spo2", "respiratory_rate", "vo2max",
	"nutrition_energy", "nutrition_expenditure", "nutrition_deficit", "nutrition_protein", "nutrition_carbs", "nutrition_fat", "food_log",
}

// buildStatTile builds one stat/sleep tile. Collapsed, every metric gets a
// small 7-day sparkline alongside its big value (steps also gets a
// goal-progress ring). Expanded, metrics with real finer-than-daily data
// behind them (steps, heart rate, sleep) show that detail instead of yet
// another daily-average chart — everything else falls back to the 7-day
// trend, since there's genuinely nothing finer stored for it (see
// prerequisite.md).
func buildStatTile(ctx context.Context, db *sql.DB, t views.TileData, kind string, day time.Time, expanded bool, today dashboardDailyRow, history map[string]dashboardDailyRow) (views.TileData, error) {
	def := metricDefs[kind]
	t.Kind = views.TileKindStat
	if kind == "sleep" {
		t.Kind = views.TileKindSleep
	}
	t.Title = def.Title
	t.Icon = def.Icon
	t.Category = def.Category
	t.Unit = def.Unit

	val, ok := def.Extract(today)
	if ok {
		t.BigValue = def.Format(val)
	} else {
		t.Empty = true
		t.EmptyMsg = def.EmptyMsg
	}

	if kind == "steps" {
		pct := 0.0
		if ok {
			pct = val / stepGoal
		}
		t.RingPct = &pct
	}

	if hist := buildChart(day, history, def.Extract, nil); hist != nil {
		t.SparklineValues = hist.Values
	}

	if !expanded {
		return t, nil
	}

	switch kind {
	case "steps":
		hourly, err := fetchStepsHourly(ctx, db, day)
		if err != nil {
			return t, err
		}
		if chart := buildHourlyStepsChart(hourly); chart != nil {
			t.Chart = chart
			t.ChartSubtext = fmt.Sprintf("Total today: %s steps", fmtInt(chart.Total))
			t.Empty = false
		} else if t.Empty {
			t.EmptyMsg = "No hourly step data for this day"
		}
	case "heart_rate":
		detail, err := fetchHeartRateDetail(ctx, db, day, today)
		if err != nil {
			return t, err
		}
		t.Detail = detail
		if len(detail.LineSamples) > 0 {
			t.Empty = false
		}
	case "sleep":
		detail, err := fetchSleepDetail(ctx, db, day)
		if err != nil {
			return t, err
		}
		t.Detail = detail
		if len(detail.Stages) > 0 {
			t.Empty = false
		}
	default:
		if chart := buildChart(day, history, def.Extract, nil); chart != nil {
			t.Chart = chart
			t.BigValue = def.Format(chart.Average)
			t.ChartSubtext = fmt.Sprintf("7-day average: %s", t.BigValue)
			t.Empty = false
		}
	}
	return t, nil
}

// localDayRangeUTC returns the [start, end) window covering day's local
// calendar date, formatted as UTC RFC3339 strings — the same representation
// watch_heart_rate_intraday.recorded_at is stored in, so it can be used
// directly in a BETWEEN-style range query.
func localDayRangeUTC(day time.Time) (startUTC, endUTC string) {
	y, m, d := day.Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 0, 1)
	return start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)
}

// fetchStepsHourly returns steps bucketed by local hour (0-23) for day, from
// watch_steps_hourly.
func fetchStepsHourly(ctx context.Context, db *sql.DB, day time.Time) ([24]int64, error) {
	var hours [24]int64
	rows, err := db.QueryContext(ctx, `SELECT hour, steps FROM watch_steps_hourly WHERE day = ?`, day.Format(dateLayout))
	if err != nil {
		return hours, fmt.Errorf("querying watch_steps_hourly for %s: %w", day.Format(dateLayout), err)
	}
	defer rows.Close()
	for rows.Next() {
		var hour int
		var steps sql.NullInt64
		if err := rows.Scan(&hour, &steps); err != nil {
			return hours, fmt.Errorf("scanning watch_steps_hourly row: %w", err)
		}
		if hour >= 0 && hour < 24 {
			hours[hour] = steps.Int64
		}
	}
	return hours, rows.Err()
}

func buildHourlyStepsChart(hourly [24]int64) *views.ChartData {
	c := &views.ChartData{}
	var total float64
	any := false
	for h := 0; h < 24; h++ {
		v := float64(hourly[h])
		if v > 0 {
			any = true
		}
		total += v
		label := ""
		if h%3 == 0 {
			label = hourLabel(h)
		}
		c.Labels = append(c.Labels, label)
		c.Values = append(c.Values, v)
	}
	if !any {
		return nil
	}
	c.Total = total
	c.Average = total / 24
	return c
}

// hourLabel formats an hour-of-day (0-23) in 24h form ("00".."23") — plain
// digits, no am/pm, matching every other time axis on the dashboard.
func hourLabel(h int) string {
	return fmt.Sprintf("%02d", h)
}

// fetchHeartRateDetail builds the heart-rate tile's expanded detail: an
// intraday line of every cached sample for day, plus min/avg/max. Because
// watch_heart_rate_intraday is a rolling ~3-day cache (see its schema
// comment), a day outside that window legitimately has zero rows even
// though the daily average is still known — that's reported as a distinct,
// clearly labeled message rather than silently showing nothing.
func fetchHeartRateDetail(ctx context.Context, db *sql.DB, day time.Time, today dashboardDailyRow) (*views.DetailData, error) {
	startUTC, endUTC := localDayRangeUTC(day)
	rows, err := db.QueryContext(ctx, `
		SELECT recorded_at, bpm FROM watch_heart_rate_intraday
		WHERE recorded_at >= ? AND recorded_at < ? ORDER BY recorded_at
	`, startUTC, endUTC)
	if err != nil {
		return nil, fmt.Errorf("querying intraday heart rate for %s: %w", day.Format(dateLayout), err)
	}
	defer rows.Close()

	d := &views.DetailData{Kind: "line"}
	var minV, maxV, sum float64
	for rows.Next() {
		var ts string
		var bpm float64
		if err := rows.Scan(&ts, &bpm); err != nil {
			return nil, fmt.Errorf("scanning intraday heart rate sample: %w", err)
		}
		label := ts
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			label = t.Local().Format("15:04")
		}
		if len(d.LineSamples) == 0 {
			minV, maxV = bpm, bpm
		}
		if bpm < minV {
			minV = bpm
		}
		if bpm > maxV {
			maxV = bpm
		}
		sum += bpm
		d.LineSamples = append(d.LineSamples, views.HRSample{TimeLabel: label, Bpm: bpm})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(d.LineSamples) == 0 {
		if today.HeartRateAvgBpm != nil {
			d.Message = "Detailed heart rate is only cached for the last few days — showing the daily average only for older days."
		} else {
			d.Message = "No heart rate data for this day."
		}
		return d, nil
	}
	avg := sum / float64(len(d.LineSamples))
	d.Stats = []views.StatItem{
		{Label: "Min", Value: fmt.Sprintf("%.0f bpm", minV)},
		{Label: "Avg", Value: fmt.Sprintf("%.0f bpm", avg)},
		{Label: "Max", Value: fmt.Sprintf("%.0f bpm", maxV)},
	}
	return d, nil
}

// buildCategoryTile is the shared builder for every DailyByCategory metric
// (see internal/db/schema.sql) — one row per (day, category) in table,
// summed for the collapsed BigValue and shown as a proportional segmented
// bar (same renderer sleep stages use) when expanded. Heart-rate zones,
// active-minutes-by-level, and active-zone-minutes-by-zone all share this
// one function instead of three near-identical copies.
func buildCategoryTile(ctx context.Context, db *sql.DB, t views.TileData, day time.Time, expanded bool, table, categoryColumn, valueColumn, title, icon, category, unit, emptyMsg string, humanize func(string) string) (views.TileData, error) {
	t.Kind = views.TileKindStat
	t.Title = title
	t.Icon = icon
	t.Category = category
	t.Unit = unit

	dayStr := day.Format(dateLayout)
	query := fmt.Sprintf(`SELECT %s, %s FROM %s WHERE day = ? ORDER BY %s DESC`, categoryColumn, valueColumn, table, valueColumn)
	rows, err := db.QueryContext(ctx, query, dayStr)
	if err != nil {
		return t, fmt.Errorf("querying %s for %s: %w", table, dayStr, err)
	}
	defer rows.Close()

	type categoryValue struct {
		Category string
		Value    float64
	}
	var cats []categoryValue
	var total float64
	for rows.Next() {
		var c categoryValue
		if err := rows.Scan(&c.Category, &c.Value); err != nil {
			return t, fmt.Errorf("scanning %s row: %w", table, err)
		}
		cats = append(cats, c)
		total += c.Value
	}
	if err := rows.Err(); err != nil {
		return t, err
	}

	if total <= 0 {
		t.Empty = true
		t.EmptyMsg = emptyMsg
		return t, nil
	}
	t.BigValue = fmtInt(total)

	if !expanded {
		return t, nil
	}

	d := &views.DetailData{Kind: "stages"}
	cum := 0.0
	for _, c := range cats {
		start := cum / total
		cum += c.Value
		d.Stages = append(d.Stages, views.StageSegment{Type: c.Category, StartPct: start, EndPct: cum / total})
		d.Stats = append(d.Stats, views.StatItem{Label: humanize(c.Category), Value: views.FormatMinutes(int64(c.Value))})
	}
	t.Detail = d
	return t, nil
}

// buildHeartRateZonesTile builds the "Time in Zones" tile from
// watch_heart_rate_zone_minutes.
func buildHeartRateZonesTile(ctx context.Context, db *sql.DB, t views.TileData, day time.Time, expanded bool) (views.TileData, error) {
	return buildCategoryTile(ctx, db, t, day, expanded,
		"watch_heart_rate_zone_minutes", "zone_type", "minutes",
		"Time in Zones", "heart", "heart", "min", "No time-in-zone data", humanizeZoneType)
}

// buildActiveMinutesByLevelTile builds the active-minutes-by-activity-level
// breakdown tile from watch_active_minutes_by_level — the light/moderate/
// vigorous split behind the flat "Active minutes" total tile.
func buildActiveMinutesByLevelTile(ctx context.Context, db *sql.DB, t views.TileData, day time.Time, expanded bool) (views.TileData, error) {
	return buildCategoryTile(ctx, db, t, day, expanded,
		"watch_active_minutes_by_level", "activity_level", "minutes",
		"Active Minutes by Level", "bolt", "activity", "min", "No active minutes recorded", humanizeZoneType)
}

// buildActiveZoneMinutesByZoneTile builds the active-zone-minutes breakdown
// tile from watch_active_zone_minutes_by_zone — this metric has no flat
// total column (unlike active-minutes), the category breakdown is its only
// representation (see internal/db/schema.sql).
func buildActiveZoneMinutesByZoneTile(ctx context.Context, db *sql.DB, t views.TileData, day time.Time, expanded bool) (views.TileData, error) {
	return buildCategoryTile(ctx, db, t, day, expanded,
		"watch_active_zone_minutes_by_zone", "zone_type", "minutes",
		"Active Zone Minutes", "heart", "heart", "min", "No active zone minutes data", humanizeZoneType)
}

func humanizeZoneType(zone string) string {
	if zone == "" {
		return zone
	}
	words := strings.Split(strings.ToLower(zone), "_")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// buildActivityLevelSegmentTile builds the activity-level tile from
// watch_activity_level_segment — a SegmentTimeline (see
// internal/db/schema.sql) spanning the whole day rather than a bounded
// session like sleep stages: segments are positioned as fractions of the
// full local calendar day (00:00-24:00), reusing the same proportional bar
// renderer. Collapsed BigValue is non-sedentary time — the single number
// most people actually want from this tile ("how much of today wasn't
// spent sitting").
func buildActivityLevelSegmentTile(ctx context.Context, db *sql.DB, t views.TileData, day time.Time, expanded bool) (views.TileData, error) {
	t.Kind = views.TileKindStat
	t.Title = "Activity Level"
	t.Icon = "activity"
	t.Category = "activity"
	t.Unit = ""

	dayStr := day.Format(dateLayout)
	rows, err := db.QueryContext(ctx, `
		SELECT activity_level, start_time, end_time FROM watch_activity_level_segment
		WHERE day = ? ORDER BY start_time
	`, dayStr)
	if err != nil {
		return t, fmt.Errorf("querying watch_activity_level_segment for %s: %w", dayStr, err)
	}
	defer rows.Close()

	type segment struct {
		Level          string
		Start, End     time.Time
		DurationMinute float64
	}
	var segments []segment
	minutesByLevel := map[string]float64{}
	var nonSedentaryMinutes float64
	for rows.Next() {
		var level, startStr, endStr string
		if err := rows.Scan(&level, &startStr, &endStr); err != nil {
			return t, fmt.Errorf("scanning watch_activity_level_segment row: %w", err)
		}
		start, e1 := time.Parse(time.RFC3339, startStr)
		end, e2 := time.Parse(time.RFC3339, endStr)
		if e1 != nil || e2 != nil || !end.After(start) {
			continue
		}
		minutes := end.Sub(start).Minutes()
		segments = append(segments, segment{Level: level, Start: start, End: end, DurationMinute: minutes})
		minutesByLevel[level] += minutes
		if level != "SEDENTARY" {
			nonSedentaryMinutes += minutes
		}
	}
	if err := rows.Err(); err != nil {
		return t, err
	}

	if len(segments) == 0 {
		t.Empty = true
		t.EmptyMsg = "No activity level data for this day"
		return t, nil
	}
	t.BigValue = views.FormatMinutes(int64(nonSedentaryMinutes))
	t.Subtext = "non-sedentary today"

	if !expanded {
		return t, nil
	}

	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	daySpan := 24 * time.Hour
	d := &views.DetailData{Kind: "stages", RangeLabel: "00:00 – 23:59"}
	for _, s := range segments {
		d.Stages = append(d.Stages, views.StageSegment{
			Type:      s.Level,
			StartPct:  s.Start.Sub(dayStart).Seconds() / daySpan.Seconds(),
			EndPct:    s.End.Sub(dayStart).Seconds() / daySpan.Seconds(),
			TimeLabel: s.Start.Local().Format("15:04") + " – " + s.End.Local().Format("15:04"),
		})
	}
	for _, level := range []string{"SEDENTARY", "LIGHTLY_ACTIVE", "MODERATELY_ACTIVE", "VERY_ACTIVE"} {
		if m, ok := minutesByLevel[level]; ok {
			d.Stats = append(d.Stats, views.StatItem{Label: humanizeZoneType(level), Value: views.FormatMinutes(int64(m))})
		}
	}
	t.Detail = d
	return t, nil
}

// fetchSleepDetail builds the sleep tile's expanded detail: the day's main
// sleep session's stage-by-stage timeline (watch_sleep_stage) plus its
// awake/light/deep/rem minute totals (already computed at sync time onto
// watch_sleep_session, not recomputed here).
func fetchSleepDetail(ctx context.Context, db *sql.DB, day time.Time) (*views.DetailData, error) {
	dayStr := day.Format(dateLayout)
	var sessionID int64
	var startTime, endTime string
	var minutesAwake, minutesLight, minutesDeep, minutesRem sql.NullInt64

	err := db.QueryRowContext(ctx, `
		SELECT id, start_time, end_time, minutes_awake, minutes_light, minutes_deep, minutes_rem
		FROM watch_sleep_session WHERE day = ? AND is_main_sleep = 1
		ORDER BY duration_minutes DESC LIMIT 1
	`, dayStr).Scan(&sessionID, &startTime, &endTime, &minutesAwake, &minutesLight, &minutesDeep, &minutesRem)
	if errors.Is(err, sql.ErrNoRows) {
		return &views.DetailData{Kind: "stages", Message: "No sleep session recorded for this day."}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying watch_sleep_session for %s: %w", dayStr, err)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT stage_type, start_time, end_time FROM watch_sleep_stage
		WHERE sleep_session_id = ? ORDER BY start_time
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("querying watch_sleep_stage for session %d: %w", sessionID, err)
	}
	defer rows.Close()

	d := &views.DetailData{Kind: "stages"}
	start, errStart := time.Parse(time.RFC3339, startTime)
	end, errEnd := time.Parse(time.RFC3339, endTime)
	totalSpan := 1.0
	if errStart == nil && errEnd == nil && end.After(start) {
		totalSpan = end.Sub(start).Seconds()
		d.RangeLabel = start.Local().Format("3:04 PM") + " – " + end.Local().Format("3:04 PM")
	}

	for rows.Next() {
		var stageType, segStartStr, segEndStr string
		if err := rows.Scan(&stageType, &segStartStr, &segEndStr); err != nil {
			return nil, fmt.Errorf("scanning watch_sleep_stage row: %w", err)
		}
		if errStart != nil {
			continue
		}
		segStart, e1 := time.Parse(time.RFC3339, segStartStr)
		segEnd, e2 := time.Parse(time.RFC3339, segEndStr)
		if e1 != nil || e2 != nil {
			continue
		}
		d.Stages = append(d.Stages, views.StageSegment{
			Type:      stageType,
			StartPct:  segStart.Sub(start).Seconds() / totalSpan,
			EndPct:    segEnd.Sub(start).Seconds() / totalSpan,
			TimeLabel: segStart.Local().Format("3:04 PM") + " – " + segEnd.Local().Format("3:04 PM"),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	d.Stats = []views.StatItem{
		{Label: "Awake", Value: views.FormatMinutes(minutesAwake.Int64)},
		{Label: "Light", Value: views.FormatMinutes(minutesLight.Int64)},
		{Label: "Deep", Value: views.FormatMinutes(minutesDeep.Int64)},
		{Label: "REM", Value: views.FormatMinutes(minutesRem.Int64)},
	}
	return d, nil
}

func buildChart(day time.Time, history map[string]dashboardDailyRow, extract func(dashboardDailyRow) (float64, bool), goal *float64) *views.ChartData {
	c := &views.ChartData{Goal: goal}
	var total float64
	any := false
	for i := 6; i >= 0; i-- {
		d := day.AddDate(0, 0, -i)
		v, ok := extract(history[d.Format(dateLayout)])
		if ok {
			total += v
			any = true
		}
		c.Labels = append(c.Labels, d.Format("Mon"))
		c.Values = append(c.Values, v)
	}
	if !any {
		return nil
	}
	c.Total = total
	c.Average = total / 7
	return c
}

func buildActivitiesTile(ctx context.Context, db *sql.DB, t views.TileData, day time.Time, expanded bool) (views.TileData, error) {
	t.Kind = views.TileKindActivities
	t.Title = "Activities"
	t.Icon = "activity"
	t.Category = "activity"

	rangeStart := day
	if expanded {
		rangeStart = day.AddDate(0, 0, -6)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, exercise_type, start_time, duration_minutes, calories_burned, avg_heart_rate_bpm
		FROM watch_exercise_session
		WHERE day BETWEEN ? AND ?
		ORDER BY start_time DESC
	`, rangeStart.Format(dateLayout), day.Format(dateLayout))
	if err != nil {
		return t, fmt.Errorf("querying watch_exercise_session: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var a views.ActivitySummary
		var startTime string
		var avgHR sql.NullFloat64
		if err := rows.Scan(&a.ID, &a.Type, &startTime, &a.DurationMinutes, &a.CaloriesBurned, &avgHR); err != nil {
			return t, fmt.Errorf("scanning watch_exercise_session row: %w", err)
		}
		if avgHR.Valid {
			v := avgHR.Float64
			a.AvgHeartRateBpm = &v
		}
		if ts, err := time.Parse(time.RFC3339, startTime); err == nil {
			a.StartLabel = ts.Local().Format("Mon 3:04 PM")
		}
		t.Activities = append(t.Activities, a)
	}
	return t, rows.Err()
}

// fetchActivityDetail loads one exercise session plus every heart-rate
// sample recorded during its interval, for the detail overlay.
func fetchActivityDetail(ctx context.Context, db *sql.DB, id int64) (*views.ActivityDetail, error) {
	var d views.ActivityDetail
	var startTime, endTime string
	var avgHR sql.NullFloat64

	err := db.QueryRowContext(ctx, `
		SELECT id, day, exercise_type, start_time, end_time, duration_minutes, calories_burned, avg_heart_rate_bpm
		FROM watch_exercise_session WHERE id = ?
	`, id).Scan(&d.ID, &d.Day, &d.Type, &startTime, &endTime, &d.DurationMinutes, &d.CaloriesBurned, &avgHR)
	if err != nil {
		return nil, fmt.Errorf("querying activity %d: %w", id, err)
	}
	if avgHR.Valid {
		v := avgHR.Float64
		d.AvgHeartRateBpm = &v
	}

	if st, err := time.Parse(time.RFC3339, startTime); err == nil {
		d.StartTimeLabel = st.Local().Format("Jan 2, 3:04 PM")
	}
	if et, err := time.Parse(time.RFC3339, endTime); err == nil {
		d.EndTimeLabel = et.Local().Format("3:04 PM")
	}

	rows, err := db.QueryContext(ctx, `
		SELECT recorded_at, bpm FROM watch_heart_rate_intraday
		WHERE recorded_at BETWEEN ? AND ? ORDER BY recorded_at
	`, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("querying heart rate samples for activity %d: %w", id, err)
	}
	defer rows.Close()

	for rows.Next() {
		var ts string
		var bpm float64
		if err := rows.Scan(&ts, &bpm); err != nil {
			return nil, fmt.Errorf("scanning heart rate sample: %w", err)
		}
		label := ts
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			label = t.Local().Format("15:04")
		}
		d.HeartRateSamples = append(d.HeartRateSamples, views.HRSample{TimeLabel: label, Bpm: bpm})
	}
	return &d, rows.Err()
}
