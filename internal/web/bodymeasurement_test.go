package web

import (
	"math"
	"testing"
)

// TestNavyBodyFatPercentMale checks against a known reference point (34in
// waist, 15in neck, 70in height -> ~11.05% body fat, a standard Navy-method
// worked example) converted to the centimeters our schema actually stores,
// to catch a units mismatch (see navyBodyFatPercentMale's own doc comment —
// this function once silently accepted cm against inch-calibrated
// constants).
func TestNavyBodyFatPercentMale(t *testing.T) {
	const cmPerInch = 2.54
	waistCm := 34 * cmPerInch
	neckCm := 15 * cmPerInch
	heightCm := 70 * cmPerInch

	pct, ok := navyBodyFatPercentMale(waistCm, neckCm, heightCm)
	if !ok {
		t.Fatal("navyBodyFatPercentMale: expected ok=true")
	}
	const want = 11.05
	if math.Abs(pct-want) > 0.2 {
		t.Errorf("navyBodyFatPercentMale(34in, 15in, 70in) = %.2f%%, want ~%.2f%%", pct, want)
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
