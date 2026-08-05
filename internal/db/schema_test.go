package db

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestSchemaExecutes catches SQL syntax mistakes at test time rather than
// when "healthd db init" runs this against a real database.
func TestSchemaExecutes(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Exec(Schema); err != nil {
		t.Fatalf("executing schema: %v", err)
	}

	wantTables := []string{
		"users",
		"web_session",
		"mcp_token",
		"user_profile",
		"sync_state",
		"watch_daily_summary",
		"watch_steps_hourly",
		"watch_heart_rate_intraday",
		"watch_sleep_session",
		"watch_sleep_stage",
		"watch_exercise_session",
		"watch_ecg_reading",
		"body_measurement",
		"cronometer_daily_nutrition",
		"cronometer_serving",
		"cronometer_exercise",
		"cronometer_biometric",
		"cronometer_note",
		"daily_journal",
		"daily_tag",
	}
	for _, name := range wantTables {
		var got string
		err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&got)
		if err != nil {
			t.Errorf("table %q missing after executing schema: %v", name, err)
		}
	}

	wantViews := []string{"v_body_measurement", "v_daily_overview"}
	for _, name := range wantViews {
		var got string
		err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'view' AND name = ?`, name).Scan(&got)
		if err != nil {
			t.Errorf("view %q missing after executing schema: %v", name, err)
		}
	}
}

// TestBodyMeasurementViewCoalesces exercises the raw/override resolution
// and the body-fat-preference rule (a direct raw reading wins over our own
// calculated estimate) end to end against a real SQLite engine.
func TestBodyMeasurementViewCoalesces(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Exec(Schema); err != nil {
		t.Fatalf("executing schema: %v", err)
	}

	_, err = conn.Exec(`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x')`)
	if err != nil {
		t.Fatalf("seeding test user: %v", err)
	}
	_, err = conn.Exec(`
		INSERT INTO body_measurement
			(user_id, day, weight_kg_raw, weight_kg_override, height_cm_raw, waist_cm, neck_cm, body_fat_pct_raw, body_fat_pct_calculated)
		VALUES
			(1, '2026-07-29', 80.0, 78.5, 180.0, 85.0, 38.0, NULL, 18.4)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	var weightKg, heightCm, bodyFatPct float64
	err = conn.QueryRow(`SELECT weight_kg, height_cm, body_fat_pct FROM v_body_measurement WHERE user_id = 1 AND day = '2026-07-29'`).
		Scan(&weightKg, &heightCm, &bodyFatPct)
	if err != nil {
		t.Fatalf("query view: %v", err)
	}

	if weightKg != 78.5 {
		t.Errorf("weight_kg = %v, want override value 78.5", weightKg)
	}
	if heightCm != 180.0 {
		t.Errorf("height_cm = %v, want raw value 180.0 (no override present)", heightCm)
	}
	if bodyFatPct != 18.4 {
		t.Errorf("body_fat_pct = %v, want calculated value 18.4 (no raw present)", bodyFatPct)
	}
}
