package googlehealth

import (
	"strings"
	"testing"
	"time"
)

func TestFilterForByKind(t *testing.T) {
	start := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	tests := []struct {
		name    string
		dt      dataType
		want    []string
		mustNot []string
	}{
		{
			// Confirmed against the real API: the type name prefix is
			// required, but must be snake_case, never kebab-case.
			name: "interval single-word",
			dt:   dataType{Name: "steps", Kind: kindInterval},
			want: []string{"steps.interval.start_time >=", "steps.interval.start_time <", "2026-07-31T00:00:00Z", "2026-08-01T00:00:00Z"},
		},
		{
			name:    "interval hyphenated becomes snake_case",
			dt:      dataType{Name: "active-minutes", Kind: kindInterval},
			want:    []string{"active_minutes.interval.start_time >="},
			mustNot: []string{"active-minutes.interval", "activeMinutes."},
		},
		{
			name:    "sample hyphenated becomes snake_case",
			dt:      dataType{Name: "heart-rate", Kind: kindSample},
			want:    []string{"heart_rate.sample_time.physical_time >=", "heart_rate.sample_time.physical_time <"},
			mustNot: []string{"heart-rate.sample_time", "heartRate."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterFor(tt.dt, start, end)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("filterFor(%v) = %q, missing substring %q", tt.dt, got, want)
				}
			}
			for _, notWant := range tt.mustNot {
				if strings.Contains(got, notWant) {
					t.Errorf("filterFor(%v) = %q, must not contain %q", tt.dt, got, notWant)
				}
			}
		})
	}
}

func TestFilterForSessionAndDailyAggregateAreUnfiltered(t *testing.T) {
	start := time.Now()
	end := start.Add(24 * time.Hour)

	for _, dt := range []dataType{
		{Name: "sleep", Kind: kindSession},
		{Name: "exercise", Kind: kindSession},
		{Name: "daily-vo2-max", Kind: kindDailyAggregate},
	} {
		if got := filterFor(dt, start, end); got != "" {
			t.Errorf("filterFor(%v) = %q, want empty string (unfiltered)", dt, got)
		}
	}
}

func TestDumpTodaySkipsListForUnsupportedTypes(t *testing.T) {
	noListConfirmed := map[string]bool{
		"floors":                      true,
		"total-calories":              true,
		"calories-in-heart-rate-zone": true,
	}
	for _, dt := range DataTypes {
		if noListConfirmed[dt.Name] && !dt.NoList {
			t.Errorf("%s: NoList = false, want true (list() is confirmed unsupported for this type)", dt.Name)
		}
	}
}

func TestRollUpDataTypesExcludesConfirmedUnsupported(t *testing.T) {
	unsupported := map[string]bool{
		"daily-heart-rate-variability": true,
		"daily-resting-heart-rate":     true,
	}
	for _, name := range RollUpDataTypes {
		if unsupported[name] {
			t.Errorf("RollUpDataTypes contains %q, which the API confirmed does not support dailyRollUp", name)
		}
	}
}
