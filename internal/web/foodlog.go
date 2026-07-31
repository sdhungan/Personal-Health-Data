package web

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

// buildFoodLogTile loads day's cronometer_serving rows (see
// internal/cronometer's sync) as the food-log tile — collapsed shows just
// today, expanded shows the same 7-day range buildActivitiesTile uses for
// activities.
func buildFoodLogTile(ctx context.Context, db *sql.DB, t views.TileData, day time.Time, expanded bool) (views.TileData, error) {
	t.Kind = views.TileKindFoodLog
	t.Title = "Food Log"
	t.Icon = "apple"
	t.Category = "nutrition"

	rangeStart := day
	if expanded {
		rangeStart = day.AddDate(0, 0, -6)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT recorded_time, food_name, quantity_value, quantity_units, energy_kcal, protein_g, carbs_g, fat_g
		FROM cronometer_serving
		WHERE day BETWEEN ? AND ?
		ORDER BY recorded_time DESC
	`, rangeStart.Format(dateLayout), day.Format(dateLayout))
	if err != nil {
		return t, fmt.Errorf("querying cronometer_serving: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var recordedTime, foodName string
		var quantityValue, energyKcal, proteinG, carbsG, fatG sql.NullFloat64
		var quantityUnits sql.NullString
		if err := rows.Scan(&recordedTime, &foodName, &quantityValue, &quantityUnits, &energyKcal, &proteinG, &carbsG, &fatG); err != nil {
			return t, fmt.Errorf("scanning cronometer_serving row: %w", err)
		}

		s := views.ServingSummary{FoodName: foodName, EnergyKcal: energyKcal.Float64}
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
