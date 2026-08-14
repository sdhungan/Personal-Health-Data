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
// body_measurement: weight and height both prefer a manual override over a
// connected scale's raw reading (body_measurement's existing raw/override
// split); waist/neck have no upstream source at all, so they're read
// directly.
func queryBodyMeasurementRow(ctx context.Context, db *sql.DB, userID int64, day string) (weightKg, heightCm, waistCm, neckCm *float64, err error) {
	var weightRaw, weightOverride, heightRaw, heightOverride, waist, neck sql.NullFloat64
	dbErr := db.QueryRowContext(ctx, `
		SELECT weight_kg_raw, weight_kg_override, height_cm_raw, height_cm_override, waist_cm, neck_cm
		FROM body_measurement WHERE user_id = ? AND day = ?
	`, userID, day).Scan(&weightRaw, &weightOverride, &heightRaw, &heightOverride, &waist, &neck)
	if dbErr != nil && !errors.Is(dbErr, sql.ErrNoRows) {
		return nil, nil, nil, nil, fmt.Errorf("querying body_measurement for %s: %w", day, dbErr)
	}
	if weightOverride.Valid {
		v := weightOverride.Float64
		weightKg = &v
	} else if weightRaw.Valid {
		v := weightRaw.Float64
		weightKg = &v
	}
	if heightOverride.Valid {
		v := heightOverride.Float64
		heightCm = &v
	} else if heightRaw.Valid {
		v := heightRaw.Float64
		heightCm = &v
	}
	if waist.Valid {
		v := waist.Float64
		waistCm = &v
	}
	if neck.Valid {
		v := neck.Float64
		neckCm = &v
	}
	return weightKg, heightCm, waistCm, neckCm, nil
}

// fetchBodyMeasurement loads day's effective weight/height/waist/neck plus
// whether any earlier day has at least one field "Carry forward" could pull
// from.
func fetchBodyMeasurement(ctx context.Context, db *sql.DB, userID int64, day string) (*views.BodyMeasurementData, error) {
	weightKg, heightCm, waistCm, neckCm, err := queryBodyMeasurementRow(ctx, db, userID, day)
	if err != nil {
		return nil, err
	}
	b := &views.BodyMeasurementData{Day: day, WeightKg: weightKg, HeightCm: heightCm, WaistCm: waistCm, NeckCm: neckCm}

	if weightKg == nil || heightCm == nil || waistCm == nil || neckCm == nil {
		var priorCount int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM body_measurement
			WHERE user_id = ? AND day < ? AND (weight_kg_override IS NOT NULL OR weight_kg_raw IS NOT NULL
			                    OR height_cm_override IS NOT NULL OR height_cm_raw IS NOT NULL
			                    OR waist_cm IS NOT NULL OR neck_cm IS NOT NULL)
		`, userID, day).Scan(&priorCount); err != nil {
			return nil, fmt.Errorf("checking prior body_measurement rows before %s: %w", day, err)
		}
		b.HasPriorDay = priorCount > 0
	}
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

// saveBodyMeasurement upserts day's manually-entered weight/height/waist/neck
// and recomputes body_fat_pct_calculated from whatever the resulting row has
// (see navyBodyFatPercentMale) — NULL when waist/neck/height aren't all
// present, same as every other field here: whatever the caller passes in is
// exactly what ends up on the row, clearing a stale value is as valid a save
// as setting one. Weight/height are written to their _override columns,
// never _raw — the sync job owns raw, the UI owns override (see
// ARCHITECTURE.md §3).
func saveBodyMeasurement(ctx context.Context, db *sql.DB, userID int64, day string, weightKg, heightCm, waistCm, neckCm *float64) error {
	var bodyFatPct *float64
	if heightCm != nil && waistCm != nil && neckCm != nil {
		if pct, ok := navyBodyFatPercentMale(*waistCm, *neckCm, *heightCm); ok {
			bodyFatPct = &pct
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.ExecContext(ctx, `
		INSERT INTO body_measurement (user_id, day, weight_kg_override, height_cm_override, waist_cm, neck_cm, body_fat_pct_calculated, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, day) DO UPDATE SET
			weight_kg_override      = excluded.weight_kg_override,
			height_cm_override      = excluded.height_cm_override,
			waist_cm                = excluded.waist_cm,
			neck_cm                 = excluded.neck_cm,
			body_fat_pct_calculated = excluded.body_fat_pct_calculated,
			updated_at              = excluded.updated_at
	`, userID, day, weightKg, heightCm, waistCm, neckCm, bodyFatPct, now, now)
	if err != nil {
		return fmt.Errorf("saving body measurement for %s: %w", day, err)
	}
	return nil
}

// carryForwardBodyMeasurement fills whichever of today's
// weight/height/waist/neck are currently empty from the most recent earlier
// day that has a value for that specific field — handled independently per
// field, since e.g. weight might have last been logged two days ago while
// waist was logged five days ago; there's no assumption that "yesterday"
// specifically has the data.
func carryForwardBodyMeasurement(ctx context.Context, db *sql.DB, userID int64, day string) error {
	weightKg, heightCm, waistCm, neckCm, err := queryBodyMeasurementRow(ctx, db, userID, day)
	if err != nil {
		return err
	}
	if weightKg == nil {
		if v, err := latestPriorValue(ctx, db, userID, day, "COALESCE(weight_kg_override, weight_kg_raw)"); err != nil {
			return err
		} else {
			weightKg = v
		}
	}
	if heightCm == nil {
		if v, err := latestPriorValue(ctx, db, userID, day, "COALESCE(height_cm_override, height_cm_raw)"); err != nil {
			return err
		} else {
			heightCm = v
		}
	}
	if waistCm == nil {
		if v, err := latestPriorValue(ctx, db, userID, day, "waist_cm"); err != nil {
			return err
		} else {
			waistCm = v
		}
	}
	if neckCm == nil {
		if v, err := latestPriorValue(ctx, db, userID, day, "neck_cm"); err != nil {
			return err
		} else {
			neckCm = v
		}
	}
	return saveBodyMeasurement(ctx, db, userID, day, weightKg, heightCm, waistCm, neckCm)
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
// single-value-plus-7-day-graph shape buildStatTile (data.go) already gives
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
func buildBodyMeasurementStatTile(ctx context.Context, db *sql.DB, userID int64, t views.TileData, kind string, day time.Time, expanded bool) (views.TileData, error) {
	def := bodyMeasurementStatDefs[kind]
	t.Kind = views.TileKindStat
	t.Title = def.Title
	t.Icon = "body"
	t.Category = "body"
	t.Unit = def.Unit

	history, err := fetchBodyMeasurementHistory(ctx, db, userID, day, def.Column)
	if err != nil {
		return t, err
	}

	dayStr := day.Format(dateLayout)
	if v, ok := history[dayStr]; ok {
		t.BigValue = fmt.Sprintf("%.1f", v)
	} else {
		t.Empty = true
		t.EmptyMsg = def.EmptyMsg
	}

	chart := buildBodyMeasurementChart(day, history)
	if chart != nil {
		t.SparklineValues = chart.Values
	}
	if expanded && chart != nil {
		t.Chart = chart
		t.BigValue = fmt.Sprintf("%.1f", chart.Average)
		t.ChartSubtext = fmt.Sprintf("7-day average: %s %s", t.BigValue, def.Unit)
		t.Empty = false
	}
	return t, nil
}

// fetchBodyMeasurementHistory returns day->value of column (a
// v_body_measurement column) for the 7 days ending on day. column always
// comes from bodyMeasurementStatDefs, a fixed internal literal set — never
// request input — so building the query with fmt.Sprintf here carries no
// injection risk, same discipline latestPriorValue already applies.
func fetchBodyMeasurementHistory(ctx context.Context, db *sql.DB, userID int64, day time.Time, column string) (map[string]float64, error) {
	start := day.AddDate(0, 0, -6).Format(dateLayout)
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

// buildBodyMeasurementChart mirrors buildChart's (data.go) 7-day bar-chart
// shape, just keyed by a plain day->value map instead of dashboardDailyRow +
// an Extract func — not worth generalizing buildChart itself for the one
// table living outside dashboardDailyRow. Unlike buildChart, the average is
// divided by the number of days that actually have a value, not a flat 7 —
// body measurements are filled in by hand, so a week with 3 of 7 days
// logged should average those 3, not silently treat the other 4 as zero.
func buildBodyMeasurementChart(day time.Time, history map[string]float64) *views.ChartData {
	c := &views.ChartData{}
	var total float64
	var count int
	for i := 6; i >= 0; i-- {
		d := day.AddDate(0, 0, -i)
		v, ok := history[d.Format(dateLayout)]
		if ok {
			total += v
			count++
		}
		c.Labels = append(c.Labels, d.Format("Mon"))
		c.Values = append(c.Values, v)
	}
	if count == 0 {
		return nil
	}
	c.Total = total
	c.Average = total / float64(count)
	return c
}
