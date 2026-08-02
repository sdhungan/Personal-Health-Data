package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

// buildFoodLogTile loads day's cronometer_serving rows (see
// internal/cronometer's sync) as the food-log tile — collapsed shows just
// today, expanded shows the same 7-day range buildActivitiesTile uses for
// activities.
func buildFoodLogTile(ctx context.Context, db *sql.DB, userID int64, t views.TileData, day time.Time, expanded bool) (views.TileData, error) {
	t.Kind = views.TileKindFoodLog
	t.Title = "Food Log"
	t.Icon = "apple"
	t.Category = "nutrition"

	rangeStart := day
	if expanded {
		rangeStart = day.AddDate(0, 0, -6)
	}

	// Plain water servings are zero-calorie hydration logging, not food —
	// they just crowd out the actual meals in this list, so they're
	// excluded here (display only; the nutrition/energy tiles still read
	// from cronometer_daily_nutrition, Cronometer's own rollup, untouched
	// by this filter). Matches "Water" and comma-separated variants Cronometer's
	// food database uses ("Water, sparkling", "Water, tap, drinking") without
	// catching unrelated foods that merely start with "water" ("Watermelon").
	rows, err := db.QueryContext(ctx, `
		SELECT id, recorded_time, food_name, quantity_value, quantity_units, energy_kcal, protein_g, carbs_g, fat_g
		FROM cronometer_serving
		WHERE user_id = ? AND day BETWEEN ? AND ?
		  AND LOWER(TRIM(food_name)) != 'water'
		  AND LOWER(TRIM(food_name)) NOT LIKE 'water,%'
		ORDER BY recorded_time DESC
	`, userID, rangeStart.Format(dateLayout), day.Format(dateLayout))
	if err != nil {
		return t, fmt.Errorf("querying cronometer_serving: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var recordedTime, foodName string
		var quantityValue, energyKcal, proteinG, carbsG, fatG sql.NullFloat64
		var quantityUnits sql.NullString
		if err := rows.Scan(&id, &recordedTime, &foodName, &quantityValue, &quantityUnits, &energyKcal, &proteinG, &carbsG, &fatG); err != nil {
			return t, fmt.Errorf("scanning cronometer_serving row: %w", err)
		}

		s := views.ServingSummary{ID: id, FoodName: foodName, EnergyKcal: energyKcal.Float64}
		// recorded_time is stored "YYYY-MM-DDTHH:MM:SS" in the account's own
		// local wall-clock time (see internal/cronometer/sync_upsert.go's
		// recordedTimeOf) -- parsed as a plain layout, not RFC3339/UTC.
		if ts, err := time.Parse("2006-01-02T15:04:05", recordedTime); err == nil {
			s.TimeLabel = ts.Format("3:04 PM")
		}
		if quantityValue.Valid && quantityUnits.Valid {
			s.QuantityLabel = fmt.Sprintf("%.0f %s", quantityValue.Float64, quantityUnits.String)
		}
		if proteinG.Valid {
			v := proteinG.Float64
			s.ProteinG = &v
		}
		if carbsG.Valid {
			v := carbsG.Float64
			s.CarbsG = &v
		}
		if fatG.Valid {
			v := fatG.Float64
			s.FatG = &v
		}
		t.FoodLog = append(t.FoodLog, s)
	}
	return t, rows.Err()
}

// fetchFoodServingDetail loads one cronometer_serving row's full detail for
// the click-to-expand popup — the same handful of nutrients a nutrition
// label highlights, beyond the four already visible in the list. userID is
// part of the WHERE clause (not just an afterthought filter) precisely so
// one logged-in user can never fetch another user's serving by guessing/
// incrementing the id query parameter.
func fetchFoodServingDetail(ctx context.Context, db *sql.DB, userID, id int64) (*views.FoodServingDetail, error) {
	var d views.FoodServingDetail
	var recordedTime string
	var quantityValue sql.NullFloat64
	var quantityUnits sql.NullString
	var energyKcal sql.NullFloat64

	err := db.QueryRowContext(ctx, `
		SELECT recorded_time, food_name, quantity_value, quantity_units, energy_kcal,
		       protein_g, carbs_g, fat_g, fiber_g, sugars_g, saturated_g, sodium_mg, cholesterol_mg
		FROM cronometer_serving
		WHERE user_id = ? AND id = ?
	`, userID, id).Scan(&recordedTime, &d.FoodName, &quantityValue, &quantityUnits, &energyKcal,
		&d.ProteinG, &d.CarbsG, &d.FatG, &d.FiberG, &d.SugarsG, &d.SaturatedG, &d.SodiumMg, &d.CholesterolMg)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no food serving found for id %d", id)
	}
	if err != nil {
		return nil, fmt.Errorf("querying cronometer_serving id %d: %w", id, err)
	}

	d.EnergyKcal = energyKcal.Float64
	if ts, err := time.Parse("2006-01-02T15:04:05", recordedTime); err == nil {
		d.TimeLabel = ts.Format("3:04 PM")
	}
	if quantityValue.Valid && quantityUnits.Valid {
		d.QuantityLabel = fmt.Sprintf("%.0f %s", quantityValue.Float64, quantityUnits.String)
	}
	return &d, nil
}
