package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

// buildBodyTile loads day's Body Measurements tile — the always-visible
// weight/height/waist/neck entry form (see views.BodyMeasurementData).
func buildBodyTile(ctx context.Context, db *sql.DB, userID int64, t views.TileData, day time.Time) (views.TileData, error) {
	t.Kind = views.TileKindBody
	t.Title = "Body Measurements"
	t.Icon = "body"
	t.Category = "body"

	b, err := fetchBodyMeasurement(ctx, db, userID, day.Format(dateLayout))
	if err != nil {
		return t, err
	}
	t.Body = b
	return t, nil
}

// queryBodyMeasurementRow reads day's raw values straight from
// body_measurement: weight prefers a manual override over a connected
// scale's raw reading (body_measurement's existing raw/override split);
// waist/neck have no upstream source at all, so they're read directly.
// Height isn't part of the form — it's static, see latestHeightCm.
func queryBodyMeasurementRow(ctx context.Context, db *sql.DB, userID int64, day string) (weightKg, waistCm, neckCm *float64, err error) {
	var weightRaw, weightOverride, waist, neck sql.NullFloat64
	dbErr := db.QueryRowContext(ctx, `
		SELECT weight_kg_raw, weight_kg_override, waist_cm, neck_cm
		FROM body_measurement WHERE user_id = ? AND day = ?
	`, userID, day).Scan(&weightRaw, &weightOverride, &waist, &neck)
	if dbErr != nil && !errors.Is(dbErr, sql.ErrNoRows) {
		return nil, nil, nil, fmt.Errorf("querying body_measurement for %s: %w", day, dbErr)
	}
	if weightOverride.Valid {
		v := weightOverride.Float64
		weightKg = &v
	} else if weightRaw.Valid {
		v := weightRaw.Float64
		weightKg = &v
	}
	if waist.Valid {
		v := waist.Float64
		waistCm = &v
	}
	if neck.Valid {
		v := neck.Float64
		neckCm = &v
	}
	return weightKg, waistCm, neckCm, nil
}

// latestHeightCm is the account's most recently recorded height on any day.
// Height is treated as static (no form field), so the Navy body-fat
// estimate always uses this one value rather than requiring it per day.
func latestHeightCm(ctx context.Context, db *sql.DB, userID int64) (*float64, error) {
	return latestPriorValue(ctx, db, userID, "9999-12-31", "COALESCE(height_cm_override, height_cm_raw)")
}

// fetchBodyMeasurement loads day's effective weight/waist/neck.
func fetchBodyMeasurement(ctx context.Context, db *sql.DB, userID int64, day string) (*views.BodyMeasurementData, error) {
	weightKg, waistCm, neckCm, err := queryBodyMeasurementRow(ctx, db, userID, day)
	if err != nil {
		return nil, err
	}
	b := &views.BodyMeasurementData{Day: day, WeightKg: weightKg, WaistCm: waistCm, NeckCm: neckCm}
	return b, nil
}

// navyBodyFatPercentMale estimates body fat % via the direct-%BF-regression
// form of the Navy circumference method (male variant: waist/neck/height
// only) — the equation DoD Instruction 1308.3 specifies as the actual
// current Army/Navy/Marine Corps standard: a 1999 Hodgdon & Friedl
// re-regression against %BF directly (R=0.903, SEE=3.52), not the older
// 1984 Hodgdon-Beckett formula that predicts body density and converts via
// Siri's equation (%BF = 495/D - 450) — the two are different regressions
// fit at different times, not the same formula in different notation; they
// diverge by several points away from an "average" build. See
// https://pmc.ncbi.nlm.nih.gov/articles/PMC4831679/, which confirms this is
// the equation actually implemented per DoDI 1308.3 and the one DEXA
// validation studies test.
//
// Returns false when the inputs can't produce a valid estimate (waist <=
// neck makes log10 of a non-positive number).
//
// ponytail: male-only. schema.sql already anticipates a female variant
// (user_profile.sex + body_measurement.hip_cm, formula uses waist+hip-neck
// instead) but there's no UI path to ever set sex='female' yet, so that
// branch would be unreachable dead code today. Add it — plus a hip_cm form
// field and a sex control somewhere in settings — together, the day a female
// account actually needs this.
func navyBodyFatPercentMale(waistCm, neckCm, heightCm float64) (float64, bool) {
	// Like the density form this replaced, the equation's constants (86.010,
	// 70.041, 36.76) were fit against measurements in inches.
	const cmPerInch = 2.54
	waistIn, neckIn, heightIn := waistCm/cmPerInch, neckCm/cmPerInch, heightCm/cmPerInch
	diff := waistIn - neckIn
	if diff <= 0 || heightIn <= 0 {
		return 0, false
	}
	pct := 86.010*math.Log10(diff) - 70.041*math.Log10(heightIn) + 36.76
	return pct, true
}

// saveBodyMeasurement upserts day's manually-entered weight/waist/neck
// and recomputes body_fat_pct_calculated from them plus latestHeightCm
// (see navyBodyFatPercentMale) — NULL when waist/neck/height aren't all
// present, same as every other field here: whatever the caller passes in is
// exactly what ends up on the row, clearing a stale value is as valid a save
// as setting one. Weight is written to its _override column, never _raw —
// the sync job owns raw, the UI owns override (see ARCHITECTURE.md §3).
// Height columns are left untouched.
func saveBodyMeasurement(ctx context.Context, db *sql.DB, userID int64, day string, weightKg, waistCm, neckCm *float64) error {
	heightCm, err := latestHeightCm(ctx, db, userID)
	if err != nil {
		return err
	}
	var bodyFatPct *float64
	if heightCm != nil && waistCm != nil && neckCm != nil {
		if pct, ok := navyBodyFatPercentMale(*waistCm, *neckCm, *heightCm); ok {
			bodyFatPct = &pct
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.ExecContext(ctx, `
		INSERT INTO body_measurement (user_id, day, weight_kg_override, waist_cm, neck_cm, body_fat_pct_calculated, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, day) DO UPDATE SET
			weight_kg_override      = excluded.weight_kg_override,
			waist_cm                = excluded.waist_cm,
			neck_cm                 = excluded.neck_cm,
			body_fat_pct_calculated = excluded.body_fat_pct_calculated,
			updated_at              = excluded.updated_at
	`, userID, day, weightKg, waistCm, neckCm, bodyFatPct, now, now)
	if err != nil {
		return fmt.Errorf("saving body measurement for %s: %w", day, err)
	}
	return nil
}

// latestPriorValue finds the most recent day before day with a non-null
// value for the given body_measurement column (or column expression, for
// weight's raw/override coalesce). column is always one of a fixed set of
// internal string literals this file controls — never request input — so
// building the query with fmt.Sprintf here carries no injection risk. userID
// still binds as a normal ? parameter, same as everywhere else.
func latestPriorValue(ctx context.Context, db *sql.DB, userID int64, day, column string) (*float64, error) {
	query := fmt.Sprintf(`
		SELECT %s FROM body_measurement
		WHERE user_id = ? AND day < ? AND %s IS NOT NULL
		ORDER BY day DESC LIMIT 1
	`, column, column)
	var v float64
	err := db.QueryRowContext(ctx, query, userID, day).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying latest prior %s before %s: %w", column, day, err)
	}
	return &v, nil
}

// bodyMeasurementStatDef configures one read-only stat tile sourced from
// v_body_measurement — weight/waist/neck/body-fat all share the same
// single-value-plus-14-day-graph shape buildStatTile (data.go) already gives
// every watch_daily_summary metric (e.g. Resting Heart Rate); this is that
// same shape for the one set of metrics living outside dashboardDailyRow.
// Column is always one of the fixed literals below, never request input —
// see fetchBodyMeasurementHistory's own comment on why building its query
// with fmt.Sprintf is still injection-safe.
type bodyMeasurementStatDef struct {
	Column   string
	Title    string
	Unit     string
	EmptyMsg string
}

var bodyMeasurementStatDefs = map[string]bodyMeasurementStatDef{
	"weight": {Column: "weight_kg", Title: "Weight", Unit: "kg", EmptyMsg: "Enter weight above"},
	"waist":  {Column: "waist_cm", Title: "Waist", Unit: "cm", EmptyMsg: "Enter waist above"},
	"neck":   {Column: "neck_cm", Title: "Neck", Unit: "cm", EmptyMsg: "Enter neck above"},
	"body_fat": {
		Column: "body_fat_pct", Title: "Body Fat %", Unit: "%",
		EmptyMsg: "Enter waist, neck & height above to calculate",
	},
}

// buildBodyMeasurementStatTile loads day's value for one bodyMeasurementStatDefs
// entry as a read-only stat tile — same collapsed-sparkline/expanded-chart
// shape every other metric tile uses (buildStatTile in data.go), but with
// its own small history query since v_body_measurement isn't part of
// dashboardDailyRow (that composite stays Google-Health/Cronometer only, see
// data.go's own doc comment). body_fat's value in particular is
// v_body_measurement.body_fat_pct: a smart scale's own direct reading
// (body_fat_pct_raw) if one's ever synced, else our own Navy-method estimate
// (body_fat_pct_calculated, written by saveBodyMeasurement).
//
// showAll swaps the 14-day chart for a line over every logged day the
// account has ("Show all").
func buildBodyMeasurementStatTile(ctx context.Context, db *sql.DB, userID int64, t views.TileData, kind string, day time.Time, expanded, showAll bool) (views.TileData, error) {
	def := bodyMeasurementStatDefs[kind]
	t.Kind = views.TileKindStat
	t.Title = def.Title
	t.Icon = "body"
	t.Category = "body"
	t.Unit = def.Unit
	t.ShowAll = showAll

	if showAll {
		d, err := fetchBodyMeasurementAllTime(ctx, db, userID, def)
		if err != nil {
			return t, err
		}
		if d == nil {
			t.Empty = true
			t.EmptyMsg = def.EmptyMsg
		}
		t.Detail = d
		return t, nil
	}

	// Always the last trendDays days, sparkline and chart alike — body
	// measurements move slowly enough that one week shows little trend.
	days := trendDays
	history, err := fetchBodyMeasurementHistory(ctx, db, userID, day, def.Column, days)
	if err != nil {
		return t, err
	}

	// Measurements aren't logged daily, so a day with no value of its own
	// still shows the most recent one in the window rather than an empty
	// tile; only a window with nothing at all is empty.
	for i := 0; i < days; i++ {
		d := day.AddDate(0, 0, -i)
		if v, ok := history[d.Format(dateLayout)]; ok {
			t.BigValue = fmt.Sprintf("%.1f", v)
			if i > 0 {
				t.Subtext = "Last logged " + d.Format("Jan 2")
			}
			break
		}
	}
	if t.BigValue == "" {
		t.Empty = true
		t.EmptyMsg = def.EmptyMsg
	}

	chart := buildBodyMeasurementChart(day, history, days)
	if chart != nil {
		t.SparklineValues = chart.Values
	}
	if expanded && chart != nil {
		t.Chart = chart
		t.BigValue = fmt.Sprintf("%.1f", chart.Average)
		t.ChartSubtext = fmt.Sprintf("%d-day average: %s %s", days, t.BigValue, def.Unit)
		t.Empty = false
	}
	return t, nil
}

// fetchBodyMeasurementAllTime returns def's column over every day the
// account ever logged it, as a line (only logged days, so gaps don't plot
// as zero) — nil when there's nothing. def.Column is a fixed internal
// literal, see fetchBodyMeasurementHistory.
func fetchBodyMeasurementAllTime(ctx context.Context, db *sql.DB, userID int64, def bodyMeasurementStatDef) (*views.DetailData, error) {
	query := fmt.Sprintf(`
		SELECT day, %s FROM v_body_measurement
		WHERE user_id = ? AND %s IS NOT NULL ORDER BY day
	`, def.Column, def.Column)
	rows, err := db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("querying all-time v_body_measurement %s: %w", def.Column, err)
	}
	defer rows.Close()
	d := &views.DetailData{Kind: "line", LineUnit: def.Unit}
	var total float64
	for rows.Next() {
		var day string
		var v float64
		if err := rows.Scan(&day, &v); err != nil {
			return nil, fmt.Errorf("scanning v_body_measurement row: %w", err)
		}
		label := day
		if t, err := time.Parse(dateLayout, day); err == nil {
			label = t.Format("Jan 2, 2006")
		}
		d.LineSamples = append(d.LineSamples, views.HRSample{TimeLabel: label, Bpm: v})
		total += v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	n := len(d.LineSamples)
	if n == 0 {
		return nil, nil
	}
	d.Stats = []views.StatItem{
		{Label: "Latest", Value: fmt.Sprintf("%.1f %s", d.LineSamples[n-1].Bpm, def.Unit)},
		{Label: "All-time average", Value: fmt.Sprintf("%.1f %s", total/float64(n), def.Unit)},
		{Label: "Entries", Value: fmt.Sprintf("%d", n)},
	}
	return d, nil
}

// buildBodyHistoryTile lists every day with a weight, waist or neck value
// (plus that day's body fat %),
// newest first — the long-term view the 14-day charts can't give. The
// template shows a page at a time behind "Show more".
func buildBodyHistoryTile(ctx context.Context, db *sql.DB, userID int64, t views.TileData) (views.TileData, error) {
	t.Kind = views.TileKindBodyHistory
	t.Title = "Body History"
	t.Icon = "body"
	t.Category = "body"

	rows, err := db.QueryContext(ctx, `
		SELECT day, weight_kg, waist_cm, neck_cm, body_fat_pct FROM v_body_measurement
		WHERE user_id = ? AND (weight_kg IS NOT NULL OR waist_cm IS NOT NULL OR neck_cm IS NOT NULL)
		ORDER BY day DESC
	`, userID)
	if err != nil {
		return t, fmt.Errorf("querying body history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r views.BodyHistoryRow
		var weight, waist, neck, bodyFat sql.NullFloat64
		if err := rows.Scan(&r.Day, &weight, &waist, &neck, &bodyFat); err != nil {
			return t, fmt.Errorf("scanning body history row: %w", err)
		}
		if weight.Valid {
			r.WeightKg = &weight.Float64
		}
		if waist.Valid {
			r.WaistCm = &waist.Float64
		}
		if neck.Valid {
			r.NeckCm = &neck.Float64
		}
		if bodyFat.Valid {
			r.BodyFatPct = &bodyFat.Float64
		}
		t.BodyHistory = append(t.BodyHistory, r)
	}
	return t, rows.Err()
}

// fetchBodyMeasurementHistory returns day->value of column (a
// v_body_measurement column) for the given number of days ending on day.
// column always comes from bodyMeasurementStatDefs, a fixed internal literal
// set — never request input — so building the query with fmt.Sprintf here
// carries no injection risk, same discipline latestPriorValue already applies.
func fetchBodyMeasurementHistory(ctx context.Context, db *sql.DB, userID int64, day time.Time, column string, days int) (map[string]float64, error) {
	start := day.AddDate(0, 0, -(days - 1)).Format(dateLayout)
	end := day.Format(dateLayout)
	query := fmt.Sprintf(`
		SELECT day, %s FROM v_body_measurement
		WHERE user_id = ? AND day BETWEEN ? AND ? AND %s IS NOT NULL
	`, column, column)
	rows, err := db.QueryContext(ctx, query, userID, start, end)
	if err != nil {
		return nil, fmt.Errorf("querying v_body_measurement %s for %s..%s: %w", column, start, end, err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var d string
		var v float64
		if err := rows.Scan(&d, &v); err != nil {
			return nil, fmt.Errorf("scanning v_body_measurement row: %w", err)
		}
		out[d] = v
	}
	return out, rows.Err()
}

// buildBodyMeasurementChart mirrors buildChart's (data.go) bar-chart
// shape over the given number of days, just keyed by a plain day->value map
// instead of dashboardDailyRow + an Extract func — not worth generalizing
// buildChart itself for the one table living outside dashboardDailyRow.
// Unlike buildChart, the average is divided by the number of days that
// actually have a value, not a flat day count — body measurements are filled
// in by hand, so a week with 3 of 7 days logged should average those 3, not
// silently treat the other 4 as zero.
func buildBodyMeasurementChart(day time.Time, history map[string]float64, days int) *views.ChartData {
	c := &views.ChartData{}
	var total float64
	var count int
	label := func(d time.Time) string { return d.Format("Mon") }
	if days > 7 {
		label = trendLabel
	}
	for i := days - 1; i >= 0; i-- {
		d := day.AddDate(0, 0, -i)
		v, ok := history[d.Format(dateLayout)]
		if ok {
			total += v
			count++
		}
		c.Labels = append(c.Labels, label(d))
		c.Values = append(c.Values, v)
	}
	if count == 0 {
		return nil
	}
	c.Total = total
	c.Average = total / float64(count)
	return c
}
