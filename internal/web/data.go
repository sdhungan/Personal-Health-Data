package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	StepsTotal            sql.NullInt64
	DistanceM             sql.NullFloat64
	FloorsClimbed         sql.NullInt64
	ActiveMinutes         sql.NullInt64
	TotalCalories         sql.NullFloat64
	RestingHeartRateBpm   sql.NullFloat64
	HeartRateAvgBpm       sql.NullFloat64
	HrvAvgMs              sql.NullFloat64
	Vo2Max                sql.NullFloat64
	Spo2AvgPct            sql.NullFloat64
	RespiratoryRateAvgBpm sql.NullFloat64
	SleepDurationMinutes  sql.NullInt64
}

func fetchDailySummaryRow(ctx context.Context, db *sql.DB, day string) (dailySummaryRow, error) {
	var r dailySummaryRow
	err := db.QueryRowContext(ctx, `
		SELECT steps_total, distance_m, floors_climbed, active_minutes,
		       total_calories, resting_heart_rate_bpm, heart_rate_avg_bpm,
		       hrv_avg_ms, vo2_max, spo2_avg_pct, respiratory_rate_avg_bpm, sleep_duration_minutes
		FROM watch_daily_summary WHERE day = ?
	`, day).Scan(&r.StepsTotal, &r.DistanceM, &r.FloorsClimbed, &r.ActiveMinutes,
		&r.TotalCalories, &r.RestingHeartRateBpm, &r.HeartRateAvgBpm,
		&r.HrvAvgMs, &r.Vo2Max, &r.Spo2AvgPct, &r.RespiratoryRateAvgBpm, &r.SleepDurationMinutes)
	if errors.Is(err, sql.ErrNoRows) {
		return dailySummaryRow{}, nil
	}
	if err != nil {
		return r, fmt.Errorf("querying watch_daily_summary for %s: %w", day, err)
	}
	return r, nil
}

// fetch7DayRows returns a day->row map for the 7 days ending on endDay
// (inclusive), missing days simply absent from the map.
func fetch7DayRows(ctx context.Context, db *sql.DB, endDay time.Time) (map[string]dailySummaryRow, error) {
	start := endDay.AddDate(0, 0, -6).Format(dateLayout)
	end := endDay.Format(dateLayout)

	rows, err := db.QueryContext(ctx, `
		SELECT day, steps_total, distance_m, floors_climbed, active_minutes,
		       total_calories, resting_heart_rate_bpm, heart_rate_avg_bpm,
		       hrv_avg_ms, vo2_max, spo2_avg_pct, respiratory_rate_avg_bpm, sleep_duration_minutes
		FROM watch_daily_summary WHERE day BETWEEN ? AND ?
	`, start, end)
	if err != nil {
		return nil, fmt.Errorf("querying 7-day watch_daily_summary range: %w", err)
	}
	defer rows.Close()

	out := map[string]dailySummaryRow{}
	for rows.Next() {
		var day string
		var r dailySummaryRow
		if err := rows.Scan(&day, &r.StepsTotal, &r.DistanceM, &r.FloorsClimbed, &r.ActiveMinutes,
			&r.TotalCalories, &r.RestingHeartRateBpm, &r.HeartRateAvgBpm,
			&r.HrvAvgMs, &r.Vo2Max, &r.Spo2AvgPct, &r.RespiratoryRateAvgBpm, &r.SleepDurationMinutes); err != nil {
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
	Unit     string
	EmptyMsg string
	Goal     *float64
	Extract  func(dailySummaryRow) (float64, bool)
	Format   func(float64) string
}

func fmtInt(v float64) string    { return views.FormatNumber(int64(v)) }
func fmtOneDec(v float64) string { return fmt.Sprintf("%.1f", v) }
func fmtNoDec(v float64) string  { return fmt.Sprintf("%.0f", v) }

var stepGoalPtr = stepGoal

var metricDefs = map[string]metricDef{
	"steps": {
		Title: "Steps", Icon: "steps", Unit: "steps", EmptyMsg: "No steps recorded",
		Goal:    &stepGoalPtr,
		Extract: func(r dailySummaryRow) (float64, bool) { return float64(r.StepsTotal.Int64), r.StepsTotal.Valid },
		Format:  fmtInt,
	},
	"calories": {
		Title: "Calories", Icon: "flame", Unit: "kcal", EmptyMsg: "No calories to eat",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.TotalCalories.Float64, r.TotalCalories.Valid },
		Format:  fmtNoDec,
	},
	"distance": {
		Title: "Distance", Icon: "pin", Unit: "km", EmptyMsg: "No distance recorded",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.DistanceM.Float64 / 1000, r.DistanceM.Valid },
		Format:  fmtOneDec,
	},
	"floors": {
		Title: "Floors", Icon: "stairs", Unit: "floors", EmptyMsg: "No floor data",
		Extract: func(r dailySummaryRow) (float64, bool) { return float64(r.FloorsClimbed.Int64), r.FloorsClimbed.Valid },
		Format:  fmtInt,
	},
	"active_minutes": {
		Title: "Active minutes", Icon: "bolt", Unit: "min", EmptyMsg: "No active minutes recorded",
		Extract: func(r dailySummaryRow) (float64, bool) { return float64(r.ActiveMinutes.Int64), r.ActiveMinutes.Valid },
		Format:  fmtInt,
	},
	"heart_rate": {
		Title: "Heart rate", Icon: "heart", Unit: "bpm avg", EmptyMsg: "No heart rate data",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.HeartRateAvgBpm.Float64, r.HeartRateAvgBpm.Valid },
		Format:  fmtNoDec,
	},
	"resting_hr": {
		Title: "Resting heart rate", Icon: "heart", Unit: "bpm", EmptyMsg: "No resting heart rate data",
		Extract: func(r dailySummaryRow) (float64, bool) {
			return r.RestingHeartRateBpm.Float64, r.RestingHeartRateBpm.Valid
		},
		Format: fmtNoDec,
	},
	"hrv": {
		Title: "Heart rate variability", Icon: "heart", Unit: "ms", EmptyMsg: "No HRV data",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.HrvAvgMs.Float64, r.HrvAvgMs.Valid },
		Format:  fmtOneDec,
	},
	"spo2": {
		Title: "Blood oxygen", Icon: "drop", Unit: "% SpO2", EmptyMsg: "No SpO2 data",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.Spo2AvgPct.Float64, r.Spo2AvgPct.Valid },
		Format:  fmtOneDec,
	},
	"respiratory_rate": {
		Title: "Respiratory rate", Icon: "lungs", Unit: "breaths/min", EmptyMsg: "No respiratory rate data",
		Extract: func(r dailySummaryRow) (float64, bool) {
			return r.RespiratoryRateAvgBpm.Float64, r.RespiratoryRateAvgBpm.Valid
		},
		Format: fmtOneDec,
	},
	"vo2max": {
		Title: "VO2 max", Icon: "lungs", Unit: "ml/kg/min", EmptyMsg: "No VO2 max data",
		Extract: func(r dailySummaryRow) (float64, bool) { return r.Vo2Max.Float64, r.Vo2Max.Valid },
		Format:  fmtOneDec,
	},
	"sleep": {
		Title: "Sleep", Icon: "moon", Unit: "", EmptyMsg: "No sleep recorded",
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
	"steps", "calories", "distance", "sleep", "activities", "goal",
	"active_minutes", "floors", "heart_rate", "resting_hr", "spo2", "hrv", "respiratory_rate", "vo2max",
}

func buildStatTile(t views.TileData, kind string, day time.Time, expanded bool, today dailySummaryRow, history map[string]dailySummaryRow) views.TileData {
	def := metricDefs[kind]
	t.Kind = views.TileKindStat
	if kind == "sleep" {
		t.Kind = views.TileKindSleep
	}
	t.Title = def.Title
	t.Icon = def.Icon
	t.Unit = def.Unit

	val, ok := def.Extract(today)
	if !ok {
		t.Empty = true
		t.EmptyMsg = def.EmptyMsg
		if !expanded {
			return t
		}
	} else {
		t.BigValue = def.Format(val)
	}

	if expanded {
		t.Chart = buildChart(day, history, def.Extract, def.Goal)
		if t.Chart != nil {
			t.BigValue = def.Format(t.Chart.Average)
			t.Empty = false
		}
	}
	return t
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

func buildGoalTile(ctx context.Context, db *sql.DB, t views.TileData, day time.Time) (views.TileData, error) {
	t.Kind = views.TileKindGoal
	t.Title = "Daily Goal"
	t.Icon = "goal"

	row, err := fetchDailySummaryRow(ctx, db, day.Format(dateLayout))
	if err != nil {
		return t, err
	}
	steps := 0.0
	if row.StepsTotal.Valid {
		steps = float64(row.StepsTotal.Int64)
	}
	t.GoalPct = steps / stepGoal
	if steps >= stepGoal {
		t.GoalMessage = "Step goal reached today!"
	} else {
		t.GoalMessage = "Meet your step goal to grow"
	}
	return t, nil
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
