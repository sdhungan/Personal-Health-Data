package web

import (
	"math"
	"testing"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

// TestNavyBodyFatPercentMale checks against a worked example from a
// published physiology course covering this exact DoDI 1308.3 equation
// (31in waist, 17in neck, 70in height -> 6.1% body fat, see
// https://med.libretexts.org/Courses/Irvine_Valley_College/Physiology_Labs_at_Home/03:_Anthropometrics/3.02:_Part_B-_Circumference_Measures/3.2.04:_Part_B4-_The_U.S._Navy_body_fat_estimation_formula)
// converted to the centimeters our schema actually stores, to catch a units
// mismatch (see navyBodyFatPercentMale's own doc comment — this function
// once silently accepted cm against inch-calibrated constants).
func TestNavyBodyFatPercentMale(t *testing.T) {
	const cmPerInch = 2.54
	waistCm := 31 * cmPerInch
	neckCm := 17 * cmPerInch
	heightCm := 70 * cmPerInch

	pct, ok := navyBodyFatPercentMale(waistCm, neckCm, heightCm)
	if !ok {
		t.Fatal("navyBodyFatPercentMale: expected ok=true")
	}
	const want = 6.1
	if math.Abs(pct-want) > 0.1 {
		t.Errorf("navyBodyFatPercentMale(31in, 17in, 70in) = %.2f%%, want ~%.2f%%", pct, want)
	}
}

func TestNavyBodyFatPercentMaleRejectsWaistNotBiggerThanNeck(t *testing.T) {
	if _, ok := navyBodyFatPercentMale(38, 40, 178); ok {
		t.Error("navyBodyFatPercentMale: expected ok=false when waist <= neck")
	}
}

func TestNavyBodyFatPercentMaleIncreasesWithWaist(t *testing.T) {
	small, ok := navyBodyFatPercentMale(80, 38, 178)
	if !ok {
		t.Fatal("navyBodyFatPercentMale(80, ...): expected ok=true")
	}
	large, ok := navyBodyFatPercentMale(100, 38, 178)
	if !ok {
		t.Fatal("navyBodyFatPercentMale(100, ...): expected ok=true")
	}
	if large <= small {
		t.Errorf("expected body fat %% to increase with waist: waist=80 -> %.2f%%, waist=100 -> %.2f%%", small, large)
	}
}

// TestBuildBodyMeasurementChartAveragesOnlyLoggedDays checks that a week
// with gaps (body measurements are hand-entered, not synced) averages only
// the days that actually have a value, rather than dividing by a flat 7 and
// silently treating missing days as zero.
func TestBuildBodyMeasurementChartAveragesOnlyLoggedDays(t *testing.T) {
	day, err := time.ParseInLocation(dateLayout, "2026-08-14", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	// Only 2 of the 7 days (day and day-1) have a value; the rest are gaps.
	history := map[string]float64{
		"2026-08-14": 90,
		"2026-08-13": 100,
	}
	chart := buildBodyMeasurementChart(day, history)
	if chart == nil {
		t.Fatal("buildBodyMeasurementChart: expected a chart, got nil")
	}
	const wantAvg = 95.0 // (90+100)/2, not /7
	if chart.Average != wantAvg {
		t.Errorf("chart.Average = %v, want %v", chart.Average, wantAvg)
	}
}

func TestBuildBodyMeasurementChartNilWhenNoData(t *testing.T) {
	day, err := time.ParseInLocation(dateLayout, "2026-08-14", time.Local)
	if err != nil {
		t.Fatal(err)
	}
	if chart := buildBodyMeasurementChart(day, map[string]float64{}); chart != nil {
		t.Errorf("buildBodyMeasurementChart with no data: expected nil, got %+v", chart)
	}
}

// TestShouldHideEmptyTileBodyDerivedKindsStayVisible checks that
// weight/waist/neck/body_fat never fold into the "No data today" summary
// even with nothing recorded today — unlike every other empty stat tile —
// since collapsing them away would also hide the only way to reach their
// "Expand" button and see the past week's trend.
func TestShouldHideEmptyTileBodyDerivedKindsStayVisible(t *testing.T) {
	for _, kind := range bodyDerivedTileKinds {
		empty := views.TileData{Kind: views.TileKindStat, Metric: kind, Empty: true}
		if shouldHideEmptyTile(empty) {
			t.Errorf("shouldHideEmptyTile(%q, Empty=true) = true, want false (should always stay visible)", kind)
		}
	}
}

func TestShouldHideEmptyTileOtherStatsStillHide(t *testing.T) {
	empty := views.TileData{Kind: views.TileKindStat, Metric: "blood_glucose", Empty: true}
	if !shouldHideEmptyTile(empty) {
		t.Error("shouldHideEmptyTile(blood_glucose, Empty=true) = false, want true (unrelated empty stats still hide)")
	}
}
