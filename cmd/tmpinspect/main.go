package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", `C:/Users/meror/AppData/Local/Temp/claude/G--Repos-Personal-Health-Data/bd0526ff-4dc3-46bd-9618-ac8bf331180c/scratchpad/dbinspect/inspect.db`)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	fmt.Println("=== watch_exercise_session (last 10) ===")
	rows, err := db.Query(`SELECT id, day, exercise_type, start_time, end_time, duration_minutes, calories_burned, avg_heart_rate_bpm, max_heart_rate_bpm, distance_m FROM watch_exercise_session ORDER BY day DESC, start_time DESC LIMIT 10`)
	if err != nil {
		log.Fatal(err)
	}
	printRows(rows)

	fmt.Println("\n=== cronometer_exercise (last 10) ===")
	rows2, err := db.Query(`SELECT id, day, recorded_time, exercise_name, group_name, minutes, calories_burned FROM cronometer_exercise ORDER BY day DESC, recorded_time DESC LIMIT 10`)
	if err != nil {
		log.Fatal(err)
	}
	printRows(rows2)

	fmt.Println("\n=== body_measurement (last 10) ===")
	rows3, err := db.Query(`SELECT day, weight_kg_raw, weight_kg_override, height_cm_raw, height_cm_override, waist_cm, neck_cm, hip_cm, body_fat_pct_raw, body_fat_pct_calculated FROM body_measurement ORDER BY day DESC LIMIT 10`)
	if err != nil {
		log.Fatal(err)
	}
	printRows(rows3)

	fmt.Println("\n=== watch_daily_summary for 2026-07-30 ===")
	rows4, err := db.Query(`SELECT day, steps_total, total_calories, heart_rate_avg_bpm, heart_rate_min_bpm, heart_rate_max_bpm FROM watch_daily_summary WHERE day IN ('2026-07-29','2026-07-30','2026-07-31')`)
	if err != nil {
		log.Fatal(err)
	}
	printRows(rows4)

	fmt.Println("\n=== watch_heart_rate_intraday count by day ===")
	rows5, err := db.Query(`SELECT substr(recorded_at,1,10) as d, count(*) FROM watch_heart_rate_intraday GROUP BY d ORDER BY d DESC LIMIT 10`)
	if err != nil {
		log.Fatal(err)
	}
	printRows(rows5)

	fmt.Println("\n=== max(day) per table ===")
	for _, t := range []string{"watch_daily_summary", "watch_exercise_session", "cronometer_exercise", "body_measurement", "daily_journal"} {
		var d sql.NullString
		_ = db.QueryRow(fmt.Sprintf(`SELECT max(day) FROM %s`, t)).Scan(&d)
		fmt.Printf("%s: %v\n", t, d)
	}

	fmt.Println("\n=== watch_exercise_session for 2026-07-30 (all cols) ===")
	rows6, err := db.Query(`SELECT id, day, exercise_type, start_time, end_time, duration_minutes, calories_burned, avg_heart_rate_bpm, max_heart_rate_bpm, distance_m FROM watch_exercise_session WHERE day = '2026-07-30' ORDER BY start_time`)
	if err != nil {
		log.Fatal(err)
	}
	printRows(rows6)

	fmt.Println("\n=== heart rate intraday sample count in each exercise window on 2026-07-30 ===")
	rows7, err := db.Query(`
		SELECT e.id, e.start_time, e.end_time,
		       (SELECT count(*) FROM watch_heart_rate_intraday hr WHERE hr.recorded_at BETWEEN e.start_time AND e.end_time) as hr_samples
		FROM watch_exercise_session e WHERE e.day = '2026-07-30'
	`)
	if err != nil {
		log.Fatal(err)
	}
	printRows(rows7)

	fmt.Println("\n=== row counts ===")
	for _, t := range []string{"watch_daily_summary", "watch_exercise_session", "watch_heart_rate_intraday", "watch_steps_hourly", "watch_sleep_session", "body_measurement", "cronometer_exercise", "cronometer_daily_nutrition"} {
		var n int
		_ = db.QueryRow(fmt.Sprintf(`SELECT count(*) FROM %s`, t)).Scan(&n)
		fmt.Printf("%s: %d\n", t, n)
	}
}

func printRows(rows *sql.Rows) {
	defer rows.Close()
	cols, _ := rows.Columns()
	n := len(cols)
	fmt.Println(cols)
	for rows.Next() {
		vals := make([]interface{}, n)
		ptrs := make([]interface{}, n)
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			log.Fatal(err)
		}
		fmt.Println(vals)
	}
}
