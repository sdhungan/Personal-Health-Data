package web

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	dbpkg "github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

// TestFetchHourlyEveryColumnRunsInTileUnits runs every hourlyColumns
// expression against the real schema (a typo'd column only fails at query
// time) and checks the unit conversion and NULL handling on two of them.
func TestFetchHourlyEveryColumnRunsInTileUnits(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Exec(dbpkg.Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x');
		INSERT INTO watch_activity_hourly (user_id, day, hour, distance_m, active_minutes_light, active_minutes_vigorous)
		VALUES (1, '2026-11-05', 19, 1382.0, 4, 15), (1, '2026-11-05', 20, NULL, NULL, 20), (1, '2026-11-06', 0, 500.0, NULL, NULL)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	day := time.Date(2026, 11, 5, 0, 0, 0, 0, time.Local)

	for kind, col := range hourlyColumns {
		if _, err := fetchHourly(ctx, conn, 1, day, 1, col.expr); err != nil {
			t.Errorf("%s: %v", kind, err)
		}
	}
	km, _ := fetchHourly(ctx, conn, 1, day, 2, hourlyColumns["distance"].expr)
	if len(km) != 48 || km[19] != 1.382 || km[20] != 0 || km[24] != 0.5 {
		t.Errorf("distance km over 2 days: len %d, day1 19h/20h = %v/%v, day2 00h = %v; want 48, 1.382/0, 0.5", len(km), km[19], km[20], km[24])
	}
	active, _ := fetchHourly(ctx, conn, 1, day, 1, hourlyColumns["active_minutes"].expr)
	if active[19] != 19 || active[20] != 20 {
		t.Errorf("active minutes at 19h/20h = %v/%v, want 19/20 (levels summed, NULL as 0)", active[19], active[20])
	}
}

// TestHourlyTileBarsThenLine: up to 1W an hourly bar per hour of the range;
// from 2W a dotted line of daily totals that skips days without data; the
// per-day average divides by days with data only.
func TestHourlyTileBarsThenLine(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Exec(dbpkg.Schema); err != nil {
		t.Fatal(err)
	}
	// Two days with distance, 3 km and 1 km, inside any range ending 11-06.
	if _, err := conn.Exec(`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x');
		INSERT INTO watch_activity_hourly (user_id, day, hour, distance_m) VALUES
		(1, '2026-11-05', 9, 3000.0), (1, '2026-11-06', 12, 1000.0)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	day := time.Date(2026, 11, 6, 0, 0, 0, 0, time.Local)
	def := metricDefs["distance"]

	bars, err := hourlyTile(ctx, conn, 1, views.TileData{Empty: true}, def, day, 3, hourlyColumns["distance"])
	if err != nil {
		t.Fatal(err)
	}
	if bars.Chart == nil || len(bars.Chart.Values) != 72 || bars.Chart.Average != 2 {
		t.Fatalf("3-day tile: want 72 hourly bars averaging 2 km/day over the 2 days with data, got %+v", bars.Chart)
	}

	line, err := hourlyTile(ctx, conn, 1, views.TileData{Empty: true}, def, day, 14, hourlyColumns["distance"])
	if err != nil {
		t.Fatal(err)
	}
	if line.Detail == nil || len(line.Detail.LineSamples) != 2 || !line.Detail.LineZeroBased || !line.Detail.LineDots ||
		line.Detail.LineSamples[0].Bpm != 3 || line.Detail.LineSamples[1].Bpm != 1 {
		t.Fatalf("2W tile: want a zero-based dotted line of the 2 data days' totals (3, 1 km), got %+v", line.Detail)
	}
	if got := line.Detail.Stats[0].Value; got != "2.0 km per day" {
		t.Errorf("2W average = %q, want 2.0 km per day", got)
	}
	if bars.Chart.YMax != 0 {
		t.Errorf("distance y-axis fixed at %v, want sized to the data", bars.Chart.YMax)
	}

	// Minutes per hour: the hourly bars' y-axis is fixed at 60.
	if _, err := conn.Exec(`UPDATE watch_activity_hourly SET sedentary_minutes = 20`); err != nil {
		t.Fatal(err)
	}
	sed, err := hourlyTile(ctx, conn, 1, views.TileData{Empty: true}, metricDefs["sedentary"], day, 1, hourlyColumns["sedentary"])
	if err != nil {
		t.Fatal(err)
	}
	if sed.Chart == nil || sed.Chart.YMax != 60 {
		t.Errorf("1-day sedentary tile: want y-axis fixed at 60, got %+v", sed.Chart)
	}
}
