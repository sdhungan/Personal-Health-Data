package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

const dateLayout = "2006-01-02"
const stepGoal = 10000.0

// dailySummaryRow mirrors watch_daily_summary's nullable columns — a
// day with no row yet (never synced) or with gaps (a given metric wasn't
// available) both come back as a zero-value row with every field invalid,
// rather than an error; tiles render their "no data" state for that.
type dailySummaryRow struct {
	StepsTotal             sql.NullInt64
	DistanceM              sql.NullFloat64
	FloorsClimbed          sql.NullInt64
	AltitudeGainM          sql.NullFloat64
	SedentaryMinutes       sql.NullInt64
	ActiveMinutes          sql.NullInt64
	KcalBurnedGoogle       sql.NullFloat64
	ActiveEnergyBurnedKcal sql.NullFloat64
	RestingHeartRateBpm    sql.NullFloat64
	HeartRateAvgBpm        sql.NullFloat64
	HrvAvgMs               sql.NullFloat64
	Vo2Max                 sql.NullFloat64
	Vo2MaxSample           sql.NullFloat64
	Vo2MaxRunSample        sql.NullFloat64
	Spo2AvgPct             sql.NullFloat64
	RespiratoryRateAvgBpm  sql.NullFloat64
	SleepDurationMinutes   sql.NullInt64
	// Populated only by fetchDailySummaryRow (today's tile value), not
	// fetch7DayRows — these come from separate sample tables this account
	// has never returned data for, so a 7-day sparkline for them would
	// always be empty anyway; see fetchLastSampleOfDay.
	BloodGlucoseMgDl sql.NullFloat64
	CoreBodyTempC    sql.NullFloat64
}

const dailySummaryColumns = `steps_total, distance_m, floors_climbed, altitude_gain_m, sedentary_minutes, active_minutes,
	       kcal_burned_google, active_energy_burned_kcal, resting_heart_rate_bpm, heart_rate_avg_bpm,
	       hrv_avg_ms, vo2_max, vo2_max_sample, vo2_max_run_sample, spo2_avg_pct, respiratory_rate_avg_bpm, sleep_duration_minutes`

func scanDailySummaryRow(row *dailySummaryRow) []any {
	return []any{&row.StepsTotal, &row.DistanceM, &row.FloorsClimbed, &row.AltitudeGainM, &row.SedentaryMinutes, &row.ActiveMinutes,
		&row.KcalBurnedGoogle, &row.ActiveEnergyBurnedKcal, &row.RestingHeartRateBpm, &row.HeartRateAvgBpm,
		&row.HrvAvgMs, &row.Vo2Max, &row.Vo2MaxSample, &row.Vo2MaxRunSample, &row.Spo2AvgPct, &row.RespiratoryRateAvgBpm, &row.SleepDurationMinutes}
}

func fetchDailySummaryRow(ctx context.Context, db *sql.DB, day string) (dailySummaryRow, error) {
	var r dailySummaryRow
	err := db.QueryRowContext(ctx, `SELECT `+dailySummaryColumns+` FROM watch_daily_summary WHERE day = ?`, day).
		Scan(scanDailySummaryRow(&r)...)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("querying watch_daily_summary for %s: %w", day, err)
	}

	if dayTime, perr := time.ParseInLocation(dateLayout, day, time.Local); perr == nil {
		v, err := fetchLastSampleOfDay(ctx, db, dayTime, "watch_blood_glucose_sample", "mg_dl")
		if err != nil {
			return r, err
		}
		r.BloodGlucoseMgDl = v
		v, err = fetchLastSampleOfDay(ctx, db, dayTime, "watch_core_body_temperature_sample", "celsius")
		if err != nil {
			return r, err
		}
		r.CoreBodyTempC = v
	}
	return r, nil
}

// fetchLastSampleOfDay returns the most recent value of column in table
// (a recorded_at-keyed sample table) falling within day's local calendar
// date. table/column are always one of a fixed set of internal string
// literals this file controls, never request input.
func fetchLastSampleOfDay(ctx context.Context, db *sql.DB, day time.Time, table, column string) (sql.NullFloat64, error) {
	var v sql.NullFloat64
	startUTC, endUTC := localDayRangeUTC(day)
	query := fmt.Sprintf(`SELECT %s FROM %s WHERE recorded_at >= ? AND recorded_at < ? ORDER BY recorded_at DESC LIMIT 1`, column, table)
	err := db.QueryRowContext(ctx, query, startUTC, endUTC).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return v, fmt.Errorf("querying latest %s.%s for %s: %w", table, column, day.Format(dateLayout), err)
	}
	return v, nil
}

// fetch7DayRows returns a day->row map for the 7 days ending on endDay
// (inclusive), missing days simply absent from the map.
func fetch7DayRows(ctx context.Context, db *sql.DB, endDay time.Time) (map[string]dailySummaryRow, error) {
	start := endDay.AddDate(0, 0, -6).Format(dateLayout)
	end := endDay.Format(dateLayout)

	rows, err := db.QueryContext(ctx, `SELECT day, `+dailySummaryColumns+` FROM watch_daily_summary WHERE day BETWEEN ? AND ?`, start, end)
	if err != nil {
		return nil, fmt.Errorf("querying 7-day watch_daily_summary range: %w", err)
	}
	defer rows.Close()

	out := map[string]dailySummaryRow{}
	for rows.Next() {
		var day string
		var r dailySummaryRow
		dest := append([]any{&day}, scanDailySummaryRow(&r)...)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scanning watch_daily_summary row: %w", err)
		}
		out[day] = r
	}
	return out, rows.Err()
}

// metricDef describes one stat-tile's data: how to pull today's value and
// a 7-day series out of a dailySummaryRow, and how to format it.
type metricDef struct {
	Title    string
	Icon     string
	Category string // dashboard section: "activity", "heart", "respiratory", "sleep"
	Unit     string
	EmptyMsg string
	Extract  func(dailySummaryRow) (float64, bool)
	Format   func(float64) string
}

func fmtInt(v float64) string    { return views.FormatNumber(int64(v)) }
func fmtOneDec(v float64) string { return fmt.Sprintf("%.1f", v) }
func fmtNoDec(v float64) string  { return fmt.Sprintf("%.0f", v) }

var metricDefs = map[string]metricDef{
	"steps": {
		Title: "Steps", Icon: "steps", Category: "activity", Unit: "steps", EmptyMsg: "No steps recorded",
		Extract: func(r dailySummaryRow) (float64, bool) { return float64(r.StepsTotal.Int64), r.StepsTotal.Valid },
		Format:  fmtInt,
	},
	"calories": {
		Title: "Calories (Google)", Icon: "flame", Category: "activity", Unit: "kcal", EmptyMsg: "No calories to eat",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.KcalBurnedGoogle.Float64, r.KcalBurnedGoogle.Valid },
		Format:  fmtNoDec,
	},
	"active_energy": {
		Title: "Active Energy", Icon: "flame", Category: "activity", Unit: "kcal", EmptyMsg: "No active energy data",
		Extract: func(r dailySummaryRow) (float64, bool) {
			return r.ActiveEnergyBurnedKcal.Float64, r.ActiveEnergyBurnedKcal.Valid
		},
		Format: fmtNoDec,
	},
	"distance": {
		Title: "Distance", Icon: "pin", Category: "activity", Unit: "km", EmptyMsg: "No distance recorded",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.DistanceM.Float64 / 1000, r.DistanceM.Valid },
		Format:  fmtOneDec,
	},
	"floors": {
		Title: "Floors", Icon: "stairs", Category: "activity", Unit: "floors", EmptyMsg: "No floor data",
		Extract: func(r dailySummaryRow) (float64, bool) { return float64(r.FloorsClimbed.Int64), r.FloorsClimbed.Valid },
		Format:  fmtInt,
	},
	"altitude": {
		Title: "Altitude Gain", Icon: "mountain", Category: "activity", Unit: "m", EmptyMsg: "No altitude data",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.AltitudeGainM.Float64, r.AltitudeGainM.Valid },
		Format:  fmtNoDec,
	},
	"active_minutes": {
		Title: "Active minutes", Icon: "bolt", Category: "activity", Unit: "min", EmptyMsg: "No active minutes recorded",
		Extract: func(r dailySummaryRow) (float64, bool) { return float64(r.ActiveMinutes.Int64), r.ActiveMinutes.Valid },
		Format:  fmtInt,
	},
	"sedentary": {
		Title: "Sedentary Time", Icon: "chair", Category: "activity", Unit: "", EmptyMsg: "No sedentary time data",
		Extract: func(r dailySummaryRow) (float64, bool) {
			return float64(r.SedentaryMinutes.Int64), r.SedentaryMinutes.Valid
		},
		Format: func(v float64) string { return views.FormatMinutes(int64(v)) },
	},
	"heart_rate": {
		Title: "Heart rate", Icon: "heart", Category: "heart", Unit: "bpm avg", EmptyMsg: "No heart rate data",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.HeartRateAvgBpm.Float64, r.HeartRateAvgBpm.Valid },
		Format:  fmtNoDec,
	},
	"resting_hr": {
		Title: "Resting heart rate", Icon: "heart", Category: "heart", Unit: "bpm", EmptyMsg: "No resting heart rate data",
		Extract: func(r dailySummaryRow) (float64, bool) {
			return r.RestingHeartRateBpm.Float64, r.RestingHeartRateBpm.Valid
		},
		Format: fmtNoDec,
	},
	"hrv": {
		Title: "Heart rate variability", Icon: "heart", Category: "heart", Unit: "ms", EmptyMsg: "No HRV data",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.HrvAvgMs.Float64, r.HrvAvgMs.Valid },
		Format:  fmtOneDec,
	},
	"spo2": {
		Title: "Blood oxygen", Icon: "drop", Category: "respiratory", Unit: "% SpO2", EmptyMsg: "No SpO2 data",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.Spo2AvgPct.Float64, r.Spo2AvgPct.Valid },
		Format:  fmtOneDec,
	},
	"respiratory_rate": {
		Title: "Respiratory rate", Icon: "lungs", Category: "respiratory", Unit: "breaths/min", EmptyMsg: "No respiratory rate data",
		Extract: func(r dailySummaryRow) (float64, bool) {
			return r.RespiratoryRateAvgBpm.Float64, r.RespiratoryRateAvgBpm.Valid
		},
		Format: fmtOneDec,
	},
	"vo2max": {
		// Prefers the daily aggregate (daily-vo2-max) but falls back to
		// whichever sample source (vo2-max, then run-vo2-max) has a value
		// for the day — three different upstream sources for the same
		// underlying concept, kept as one tile rather than three.
		Title: "VO2 max", Icon: "lungs", Category: "respiratory", Unit: "ml/kg/min", EmptyMsg: "No VO2 max data",
		Extract: func(r dailySummaryRow) (float64, bool) {
			if r.Vo2Max.Valid {
				return r.Vo2Max.Float64, true
			}
			if r.Vo2MaxSample.Valid {
				return r.Vo2MaxSample.Float64, true
			}
			if r.Vo2MaxRunSample.Valid {
				return r.Vo2MaxRunSample.Float64, true
			}
			return 0, false
		},
		Format: fmtOneDec,
	},
	"blood_glucose": {
		Title: "Blood Glucose", Icon: "drop", Category: "body", Unit: "mg/dL", EmptyMsg: "No blood glucose data",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.BloodGlucoseMgDl.Float64, r.BloodGlucoseMgDl.Valid },
		Format:  fmtNoDec,
	},
	"core_body_temp": {
		Title: "Core Body Temp", Icon: "thermometer", Category: "body", Unit: "°C", EmptyMsg: "No core temperature data",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.CoreBodyTempC.Float64, r.CoreBodyTempC.Valid },
		Format:  fmtOneDec,
	},
	"sleep": {
		Title: "Sleep", Icon: "moon", Category: "sleep", Unit: "", EmptyMsg: "No sleep recorded",
		Extract: func(r dailySummaryRow) (float64, bool) {
			return float64(r.SleepDurationMinutes.Int64), r.SleepDurationMinutes.Valid
		},
		Format: func(v float64) string { return views.FormatMinutes(int64(v)) },
	},
}

// DefaultTileKinds is every tile shown on a fresh dashboard. "steps" is
// deliberately first and pre-expanded — the reference dashboard's default
// "steps, last 7 days" view.
var DefaultTileKinds = []string{
	"steps", "calories", "active_energy", "distance", "active_minutes", "floors", "altitude", "sedentary", "activities",
	"heart_rate", "resting_hr", "hrv", "hr_zones",
	"body", "blood_glucose", "core_body_temp",
	"sleep",
	"spo2", "respiratory_rate", "vo2max",
}

// buildStatTile builds one stat/sleep tile. Collapsed, every metric gets a
// small 7-day sparkline alongside its big value (steps also gets a
// goal-progress ring). Expanded, metrics with real finer-than-daily data
// behind them (steps, heart rate, sleep) show that detail instead of yet
// another daily-average chart — everything else falls back to the 7-day
// trend, since there's genuinely nothing finer stored for it (see
// prerequisite.md).
func buildStatTile(ctx context.Context, db *sql.DB, t views.TileData, kind string, day time.Time, expanded bool, today dailySummaryRow, history map[string]dailySummaryRow) (views.TileData, error) {
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

func hourLabel(h int) string {
	switch {
	case h == 0:
		return "12a"
	case h < 12:
		return fmt.Sprintf("%da", h)
	case h == 12:
		return "12p"
	default:
		return fmt.Sprintf("%dp", h-12)
	}
}

// fetchHeartRateDetail builds the heart-rate tile's expanded detail: an
// intraday line of every cached sample for day, plus min/avg/max. Because
// watch_heart_rate_intraday is a rolling ~3-day cache (see its schema
// comment), a day outside that window legitimately has zero rows even
// though the daily average is still known — that's reported as a distinct,
// clearly labeled message rather than silently showing nothing.
func fetchHeartRateDetail(ctx context.Context, db *sql.DB, day time.Time, today dailySummaryRow) (*views.DetailData, error) {
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
		if today.HeartRateAvgBpm.Valid {
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

// buildHeartRateZonesTile builds the "Time in Zones" tile from
// watch_heart_rate_zone_minutes — collapsed shows the day's total minutes
// across all zones, expanded reuses the same proportional segmented-bar
// renderer built for sleep stages (views.DetailData{Kind:"stages"}), just
// with zone-type segments sized by relative minutes instead of
// chronological time.
func buildHeartRateZonesTile(ctx context.Context, db *sql.DB, t views.TileData, day time.Time, expanded bool) (views.TileData, error) {
	t.Kind = views.TileKindStat
	t.Title = "Time in Zones"
	t.Icon = "heart"
	t.Category = "heart"
	t.Unit = "min"

	dayStr := day.Format(dateLayout)
	rows, err := db.QueryContext(ctx, `SELECT zone_type, minutes FROM watch_heart_rate_zone_minutes WHERE day = ? ORDER BY minutes DESC`, dayStr)
	if err != nil {
		return t, fmt.Errorf("querying watch_heart_rate_zone_minutes for %s: %w", dayStr, err)
	}
	defer rows.Close()

	type zoneMinutes struct {
		Zone    string
		Minutes float64
	}
	var zones []zoneMinutes
	var total float64
	for rows.Next() {
		var z zoneMinutes
		if err := rows.Scan(&z.Zone, &z.Minutes); err != nil {
			return t, fmt.Errorf("scanning heart rate zone minutes: %w", err)
		}
		zones = append(zones, z)
		total += z.Minutes
	}
	if err := rows.Err(); err != nil {
		return t, err
	}

	if total <= 0 {
		t.Empty = true
		t.EmptyMsg = "No time-in-zone data"
		return t, nil
	}
	t.BigValue = fmtInt(total)

	if !expanded {
		return t, nil
	}

	d := &views.DetailData{Kind: "stages"}
	cum := 0.0
	for _, z := range zones {
		start := cum / total
		cum += z.Minutes
		d.Stages = append(d.Stages, views.StageSegment{Type: z.Zone, StartPct: start, EndPct: cum / total})
		d.Stats = append(d.Stats, views.StatItem{Label: humanizeZoneType(z.Zone), Value: views.FormatMinutes(int64(z.Minutes))})
	}
	t.Detail = d
	return t, nil
}

func humanizeZoneType(zone string) string {
	if zone == "" {
		return zone
	}
	return strings.ToUpper(zone[:1]) + strings.ToLower(zone[1:])
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
			Type:     stageType,
			StartPct: segStart.Sub(start).Seconds() / totalSpan,
			EndPct:   segEnd.Sub(start).Seconds() / totalSpan,
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

func buildChart(day time.Time, history map[string]dailySummaryRow, extract func(dailySummaryRow) (float64, bool), goal *float64) *views.ChartData {
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
