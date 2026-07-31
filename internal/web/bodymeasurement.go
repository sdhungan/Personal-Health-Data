package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

// buildBodyTile loads day's Body Measurements tile — the always-visible
// weight/waist/neck entry form (see views.BodyMeasurementData).
func buildBodyTile(ctx context.Context, db *sql.DB, t views.TileData, day time.Time) (views.TileData, error) {
	t.Kind = views.TileKindBody
	t.Title = "Body Measurements"
	t.Icon = "body"
	t.Category = "body"

	b, err := fetchBodyMeasurement(ctx, db, day.Format(dateLayout))
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
func queryBodyMeasurementRow(ctx context.Context, db *sql.DB, day string) (weightKg, waistCm, neckCm *float64, err error) {
	var weightRaw, weightOverride, waist, neck sql.NullFloat64
	dbErr := db.QueryRowContext(ctx, `
		SELECT weight_kg_raw, weight_kg_override, waist_cm, neck_cm
		FROM body_measurement WHERE day = ?
	`, day).Scan(&weightRaw, &weightOverride, &waist, &neck)
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

// fetchBodyMeasurement loads day's effective weight/waist/neck plus whether
// any earlier day has at least one field "Carry forward" could pull from.
func fetchBodyMeasurement(ctx context.Context, db *sql.DB, day string) (*views.BodyMeasurementData, error) {
	weightKg, waistCm, neckCm, err := queryBodyMeasurementRow(ctx, db, day)
	if err != nil {
		return nil, err
	}
	b := &views.BodyMeasurementData{Day: day, WeightKg: weightKg, WaistCm: waistCm, NeckCm: neckCm}

	if weightKg == nil || waistCm == nil || neckCm == nil {
		var priorCount int
		if err := db.QueryRowContext(ctx, `
			SELECT count(*) FROM body_measurement
			WHERE day < ? AND (weight_kg_override IS NOT NULL OR weight_kg_raw IS NOT NULL
			                    OR waist_cm IS NOT NULL OR neck_cm IS NOT NULL)
		`, day).Scan(&priorCount); err != nil {
			return nil, fmt.Errorf("checking prior body_measurement rows before %s: %w", day, err)
		}
		b.HasPriorDay = priorCount > 0
	}
	return b, nil
}

// saveBodyMeasurement upserts day's manually-entered weight/waist/neck.
// Weight is written to weight_kg_override, never weight_kg_raw — the sync
// job owns raw, the UI owns override (see ARCHITECTURE.md §3).
func saveBodyMeasurement(ctx context.Context, db *sql.DB, day string, weightKg, waistCm, neckCm *float64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := db.ExecContext(ctx, `
		INSERT INTO body_measurement (day, weight_kg_override, waist_cm, neck_cm, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(day) DO UPDATE SET
			weight_kg_override = excluded.weight_kg_override,
			waist_cm           = excluded.waist_cm,
			neck_cm            = excluded.neck_cm,
			updated_at         = excluded.updated_at
	`, day, weightKg, waistCm, neckCm, now, now)
	if err != nil {
		return fmt.Errorf("saving body measurement for %s: %w", day, err)
	}
	return nil
}

// carryForwardBodyMeasurement fills whichever of today's weight/waist/neck
// are currently empty from the most recent earlier day that has a value for
// that specific field — handled independently per field, since e.g. weight
// might have last been logged two days ago while waist was logged five days
// ago; there's no assumption that "yesterday" specifically has the data.
func carryForwardBodyMeasurement(ctx context.Context, db *sql.DB, day string) error {
	weightKg, waistCm, neckCm, err := queryBodyMeasurementRow(ctx, db, day)
	if err != nil {
		return err
	}
	if weightKg == nil {
		if v, err := latestPriorValue(ctx, db, day, "COALESCE(weight_kg_override, weight_kg_raw)"); err != nil {
			return err
		} else {
			weightKg = v
		}
	}
	if waistCm == nil {
		if v, err := latestPriorValue(ctx, db, day, "waist_cm"); err != nil {
			return err
		} else {
			waistCm = v
		}
	}
	if neckCm == nil {
		if v, err := latestPriorValue(ctx, db, day, "neck_cm"); err != nil {
			return err
		} else {
			neckCm = v
		}
	}
	return saveBodyMeasurement(ctx, db, day, weightKg, waistCm, neckCm)
}

// latestPriorValue finds the most recent day before day with a non-null
// value for the given body_measurement column (or column expression, for
// weight's raw/override coalesce). column is always one of a fixed set of
// internal string literals this file controls — never request input — so
// building the query with fmt.Sprintf here carries no injection risk.
func latestPriorValue(ctx context.Context, db *sql.DB, day, column string) (*float64, error) {
	query := fmt.Sprintf(`
		SELECT %s FROM body_measurement
		WHERE day < ? AND %s IS NOT NULL
		ORDER BY day DESC LIMIT 1
	`, column, column)
	var v float64
	err := db.QueryRowContext(ctx, query, day).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying latest prior %s before %s: %w", column, day, err)
	}
	return &v, nil
}
