package cronometer

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
)

// nutritionColumns lists the 64 nutrient columns cronometer_daily_nutrition
// and cronometer_serving both have (schema.sql: the two tables share the
// exact same nutrient dictionary at daily vs. per-entry granularity), in
// NutritionAmounts field order. Keep this, (*NutritionAmounts).values(),
// and nutrients.go's struct field order in sync by hand if either changes.
var nutritionColumns = []string{
	"energy_kcal", "caffeine_mg", "water_g", "b1_mg", "b2_mg", "b3_mg", "b5_mg", "b6_mg", "b12_ug",
	"biotin_ug", "choline_mg", "folate_ug", "vitamin_a_ug", "vitamin_c_mg", "vitamin_d_iu", "vitamin_e_mg", "vitamin_k_ug",
	"calcium_mg", "chromium_ug", "copper_mg", "fluoride_ug", "iodine_ug", "iron_mg", "magnesium_mg", "manganese_mg",
	"phosphorus_mg", "potassium_mg", "selenium_ug", "sodium_mg", "zinc_mg",
	"carbs_g", "fiber_g", "fructose_g", "galactose_g", "glucose_g", "lactose_g", "maltose_g", "starch_g", "sucrose_g",
	"sugars_g", "net_carbs_g", "added_sugars_g", "allulose_g", "sugar_alcohol_g",
	"fat_g", "cholesterol_mg", "monounsaturated_g", "polyunsaturated_g", "saturated_g", "trans_fat_g", "omega3_g", "omega6_g",
	"protein_g", "cystine_g", "histidine_g", "isoleucine_g", "leucine_g", "lysine_g", "methionine_g",
	"phenylalanine_g", "threonine_g", "tryptophan_g", "tyrosine_g", "valine_g",
}

func (n *NutritionAmounts) values() []any {
	return []any{
		n.EnergyKcal, n.CaffeineMg, n.WaterG, n.B1Mg, n.B2Mg, n.B3Mg, n.B5Mg, n.B6Mg, n.B12Ug,
		n.BiotinUg, n.CholineMg, n.FolateUg, n.VitaminAUg, n.VitaminCMg, n.VitaminDIu, n.VitaminEMg, n.VitaminKUg,
		n.CalciumMg, n.ChromiumUg, n.CopperMg, n.FluorideUg, n.IodineUg, n.IronMg, n.MagnesiumMg, n.ManganeseMg,
		n.PhosphorusMg, n.PotassiumMg, n.SeleniumUg, n.SodiumMg, n.ZincMg,
		n.CarbsG, n.FiberG, n.FructoseG, n.GalactoseG, n.GlucoseG, n.LactoseG, n.MaltoseG, n.StarchG, n.SucroseG,
		n.SugarsG, n.NetCarbsG, n.AddedSugarsG, n.AlluloseG, n.SugarAlcoholG,
		n.FatG, n.CholesterolMg, n.MonounsaturatedG, n.PolyunsaturatedG, n.SaturatedG, n.TransFatG, n.Omega3G, n.Omega6G,
		n.ProteinG, n.CystineG, n.HistidineG, n.IsoleucineG, n.LeucineG, n.LysineG, n.MethionineG,
		n.PhenylalanineG, n.ThreonineG, n.TryptophanG, n.TyrosineG, n.ValineG,
	}
}

func nutritionPlaceholders() string {
	ph := make([]string, len(nutritionColumns))
	for i := range ph {
		ph[i] = "?"
	}
	return strings.Join(ph, ", ")
}

func nutritionCoalesceSet(table string) string {
	parts := make([]string, len(nutritionColumns))
	for i, c := range nutritionColumns {
		parts[i] = fmt.Sprintf("%s = COALESCE(excluded.%s, %s.%s)", c, c, table, c)
	}
	return strings.Join(parts, ",\n\t\t\t")
}

// upsertDailyNutrition writes one day's Cronometer-computed nutrition
// totals. Sync is the sole writer of these columns (these tables have no
// raw/override split — see cronometer-integration.md), so overwrite-on-fetch
// via COALESCE(excluded.x, table.x) is correct, same pattern as
// internal/googlehealth/sync_upsert.go's upsertDailySummary.
func upsertDailyNutrition(ctx context.Context, db *sql.DB, userID int64, day string, completed bool, kcalBurned float64, amounts *NutritionAmounts) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	completedInt := 0
	if completed {
		completedInt = 1
	}
	var kcalBurnedArg any
	if kcalBurned != 0 {
		kcalBurnedArg = kcalBurned
	}

	query := fmt.Sprintf(`
		INSERT INTO cronometer_daily_nutrition (user_id, day, completed, kcal_burned_cronometer, %s, synced_at)
		VALUES (?, ?, ?, ?, %s, ?)
		ON CONFLICT(user_id, day) DO UPDATE SET
			completed = excluded.completed,
			kcal_burned_cronometer = COALESCE(excluded.kcal_burned_cronometer, cronometer_daily_nutrition.kcal_burned_cronometer),
			%s,
			synced_at = excluded.synced_at
	`, strings.Join(nutritionColumns, ", "), nutritionPlaceholders(), nutritionCoalesceSet("cronometer_daily_nutrition"))

	args := []any{userID, day, completedInt, kcalBurnedArg}
	args = append(args, amounts.values()...)
	args = append(args, now)

	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("upserting cronometer_daily_nutrition for %s: %w", day, err)
	}
	return nil
}

// recordedTimeOf combines a diary entry's day and optional "HH:MM:SS" time
// (absent on several real Biometric entries — confirmed 2026-07-31 dump)
// into recorded_time's stored value. Cronometer's time is the account's own
// local wall clock; no confirmed UTC offset field exists to normalize it.
func recordedTimeOf(day, timeStr string) string {
	if timeStr == "" {
		timeStr = "00:00:00"
	}
	return day + "T" + timeStr
}

// upsertServings replaces day's cronometer_serving rows entirely (delete
// then insert) — servings have no natural external key to upsert against,
// and sync is the sole writer, so a full day's replacement on every fetch
// is correct and simpler than tracking Cronometer's own servingId.
func upsertServings(ctx context.Context, db *sql.DB, userID int64, day string, entries []DiaryEntry, foodByID map[int64]Food) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM cronometer_serving WHERE user_id = ? AND day = ?`, userID, day); err != nil {
		return fmt.Errorf("clearing existing servings for %s: %w", day, err)
	}

	query := fmt.Sprintf(`
		INSERT INTO cronometer_serving (user_id, day, recorded_time, food_name, category, quantity_value, quantity_units, %s)
		VALUES (?, ?, ?, ?, ?, ?, ?, %s)
	`, strings.Join(nutritionColumns, ", "), nutritionPlaceholders())

	for _, e := range entries {
		food, ok := foodByID[e.FoodID]

		name := fmt.Sprintf("food #%d", e.FoodID)
		amounts := &NutritionAmounts{}
		var quantityValue any
		var quantityUnits any
		if ok {
			name = food.Name
			amounts = nutritionAmountsFromFood(food.Nutrients, e.Grams)
			for _, m := range food.Measures {
				if m.ID == e.MeasureID && m.Value > 0 {
					quantityValue = e.Grams / m.Value
					quantityUnits = m.Name
					break
				}
			}
		}

		// category: Cronometer's food.Category is a numeric ID (confirmed
		// 2026-07-31 dump) with no confirmed name catalog captured yet —
		// left NULL rather than storing a meaningless number.
		args := []any{userID, day, recordedTimeOf(day, e.Time), name, nil, quantityValue, quantityUnits}
		args = append(args, amounts.values()...)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("inserting serving (food_id=%d) for %s: %w", e.FoodID, day, err)
		}
	}

	return tx.Commit()
}

func deleteServingsForDay(ctx context.Context, db *sql.DB, userID int64, day string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM cronometer_serving WHERE user_id = ? AND day = ?`, userID, day)
	return err
}

// upsertExercises replaces day's cronometer_exercise rows (delete then
// insert, same reasoning as upsertServings). group_name is left NULL: the
// only Exercise diary entries seen in the real dump were Fitbit-sourced
// (filtered out before this is called — see DBSyncer.SyncDay), so a
// hand-logged entry's real field shape, including whether it carries any
// group/category field at all, is INFERRED from Serving's shape, not
// confirmed.
func upsertExercises(ctx context.Context, db *sql.DB, userID int64, day string, entries []DiaryEntry) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM cronometer_exercise WHERE user_id = ? AND day = ?`, userID, day); err != nil {
		return fmt.Errorf("clearing existing exercises for %s: %w", day, err)
	}

	for _, e := range entries {
		// Calories observed negative in real (Fitbit-sourced) data — a
		// budget deduction, not a signed quantity; store the magnitude.
		calories := math.Abs(e.Calories)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cronometer_exercise (user_id, day, recorded_time, exercise_name, minutes, calories_burned)
			VALUES (?, ?, ?, ?, ?, ?)
		`, userID, day, recordedTimeOf(day, e.Time), e.Name, nullIfZero(e.Minutes), nullIfZero(calories)); err != nil {
			return fmt.Errorf("inserting exercise (id=%d) for %s: %w", e.ExerciseID, day, err)
		}
	}

	return tx.Commit()
}

func deleteExercisesForDay(ctx context.Context, db *sql.DB, userID int64, day string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM cronometer_exercise WHERE user_id = ? AND day = ?`, userID, day)
	return err
}

// upsertBiometrics replaces day's cronometer_biometric rows (delete then
// insert, same reasoning as upsertServings). metrics resolves each entry's
// numeric metricId/unitId to the human-readable names schema.sql's
// metric/unit TEXT columns want.
func upsertBiometrics(ctx context.Context, db *sql.DB, userID int64, day string, entries []DiaryEntry, metrics []Metric) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM cronometer_biometric WHERE user_id = ? AND day = ?`, userID, day); err != nil {
		return fmt.Errorf("clearing existing biometrics for %s: %w", day, err)
	}

	metricByID := make(map[int]Metric, len(metrics))
	for _, m := range metrics {
		metricByID[m.ID] = m
	}

	for _, e := range entries {
		metricName := fmt.Sprintf("metric #%d", e.MetricID)
		var unitName any
		if m, ok := metricByID[e.MetricID]; ok {
			metricName = m.Name
			if u := m.UnitName(e.UnitID); u != "" {
				unitName = u
			}
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cronometer_biometric (user_id, day, recorded_time, metric, unit, amount)
			VALUES (?, ?, ?, ?, ?, ?)
		`, userID, day, recordedTimeOf(day, e.Time), metricName, unitName, e.Amount); err != nil {
			return fmt.Errorf("inserting biometric (id=%d) for %s: %w", e.BiometricID, day, err)
		}
	}

	return tx.Commit()
}

func deleteBiometricsForDay(ctx context.Context, db *sql.DB, userID int64, day string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM cronometer_biometric WHERE user_id = ? AND day = ?`, userID, day)
	return err
}

func nullIfZero(v float64) any {
	if v == 0 {
		return nil
	}
	return v
}
