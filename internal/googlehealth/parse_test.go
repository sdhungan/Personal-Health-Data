package googlehealth

import (
	"encoding/json"
	"testing"
)

func TestValueKeyCoversAllDataTypes(t *testing.T) {
	for _, dt := range DataTypes {
		if ValueKey(dt.Name) == "" {
			t.Errorf("ValueKey(%q) = \"\", every entry in DataTypes needs a mapping", dt.Name)
		}
	}
}

func TestPBInt64UnmarshalsStringAndNumber(t *testing.T) {
	var s PBInt64
	if err := json.Unmarshal([]byte(`"71"`), &s); err != nil {
		t.Fatalf("unmarshal string form: %v", err)
	}
	if s != 71 {
		t.Errorf("string form = %d, want 71", s)
	}

	var n PBInt64
	if err := json.Unmarshal([]byte(`42`), &n); err != nil {
		t.Fatalf("unmarshal number form: %v", err)
	}
	if n != 42 {
		t.Errorf("number form = %d, want 42", n)
	}
}

func TestPBDurationUnmarshalsSecondsString(t *testing.T) {
	var d PBDuration
	if err := json.Unmarshal([]byte(`"1088.400s"`), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := 1088*1e9 + 400*1e6 // 1088.400 seconds in nanoseconds
	if int64(d) != int64(want) {
		t.Errorf("PBDuration = %d ns, want %d ns", int64(d), int64(want))
	}
}

// The envelope fixtures below are hand-written, structurally matching real
// Google Health API responses observed while building this package
// (field names, nesting, and value types), but with made-up numbers — no
// real personal health data is committed to the repo as a fixture.

func TestExtractValuesStripsMetadataDailyRestingHeartRate(t *testing.T) {
	raw := json.RawMessage(`{
		"dataPoints": [
			{
				"name": "users/123/dataTypes/daily-resting-heart-rate/dataPoints/abc",
				"dataSource": {"recordingMethod": "DERIVED", "device": {"displayName": "Pixel Watch 3"}, "platform": "FITBIT"},
				"dailyRestingHeartRate": {
					"date": {"year": 2026, "month": 7, "day": 30},
					"beatsPerMinute": "62",
					"dailyRestingHeartRateMetadata": {"calculationMethod": "WITH_SLEEP"}
				}
			},
			{
				"name": "users/123/dataTypes/daily-resting-heart-rate/dataPoints/def",
				"dataSource": {},
				"dailyRestingHeartRate": {
					"date": {"year": 2026, "month": 7, "day": 29},
					"beatsPerMinute": "65",
					"dailyRestingHeartRateMetadata": {"calculationMethod": "WITH_SLEEP"}
				}
			}
		]
	}`)

	points, err := ExtractValues[DailyRestingHeartRate](raw, "dailyRestingHeartRate")
	if err != nil {
		t.Fatalf("ExtractValues: %v", err)
	}
	if len(points) != 2 {
		t.Fatalf("len(points) = %d, want 2", len(points))
	}
	if points[0].BeatsPerMinute != 62 || points[0].Date.Day != 30 {
		t.Errorf("points[0] = %+v, want BeatsPerMinute=62 Date.Day=30", points[0])
	}
	if points[1].BeatsPerMinute != 65 || points[1].Date.Day != 29 {
		t.Errorf("points[1] = %+v, want BeatsPerMinute=65 Date.Day=29", points[1])
	}
	if points[0].Metadata.CalculationMethod != "WITH_SLEEP" {
		t.Errorf("Metadata.CalculationMethod = %q, want WITH_SLEEP", points[0].Metadata.CalculationMethod)
	}
}

func TestExtractValuesSkipsPointsMissingTheKey(t *testing.T) {
	raw := json.RawMessage(`{
		"dataPoints": [
			{"name": "n1", "dataSource": {}, "weight": {"sampleTime": {"physicalTime": "2026-07-30T08:00:00Z"}, "kilograms": 78.5}},
			{"name": "n2", "dataSource": {}}
		]
	}`)

	points, err := ExtractValues[Weight](raw, "weight")
	if err != nil {
		t.Fatalf("ExtractValues: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("len(points) = %d, want 1 (the point without a weight value should be skipped)", len(points))
	}
	if points[0].Kilograms != 78.5 {
		t.Errorf("Kilograms = %v, want 78.5", points[0].Kilograms)
	}
}

func TestExtractValuesElectrocardiogramDropsWaveform(t *testing.T) {
	raw := json.RawMessage(`{
		"dataPoints": [
			{
				"name": "n1",
				"dataSource": {},
				"electrocardiogram": {
					"interval": {"startTime": "2026-07-30T08:00:00Z"},
					"beatsPerMinuteAvg": "87",
					"resultClassification": "NORMAL_SINUS_RHYTHM",
					"waveformSamples": [0, -16, -32, -56, -100]
				}
			}
		]
	}`)

	points, err := ExtractValues[Electrocardiogram](raw, "electrocardiogram")
	if err != nil {
		t.Fatalf("ExtractValues: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("len(points) = %d, want 1", len(points))
	}
	if points[0].BeatsPerMinuteAvg != 87 {
		t.Errorf("BeatsPerMinuteAvg = %d, want 87", points[0].BeatsPerMinuteAvg)
	}
	if points[0].ResultClassification != "NORMAL_SINUS_RHYTHM" {
		t.Errorf("ResultClassification = %q, want NORMAL_SINUS_RHYTHM", points[0].ResultClassification)
	}
	// Electrocardiogram has no Go field for waveformSamples at all — this
	// is really a compile-time guarantee, but exercising the real decode
	// path confirms unmarshal doesn't choke on the extra field either.
}

func TestExtractValuesSleepStagesAndSummary(t *testing.T) {
	raw := json.RawMessage(`{
		"dataPoints": [
			{
				"name": "n1",
				"dataSource": {},
				"sleep": {
					"interval": {"startTime": "2026-07-29T22:15:00Z", "endTime": "2026-07-30T05:23:00Z"},
					"type": "STAGES",
					"stages": [
						{"startTime": "2026-07-29T22:15:00Z", "endTime": "2026-07-29T22:23:00Z", "type": "AWAKE"},
						{"startTime": "2026-07-29T22:23:00Z", "endTime": "2026-07-29T22:34:00Z", "type": "LIGHT"}
					],
					"summary": {
						"minutesInSleepPeriod": "428",
						"minutesAsleep": "413",
						"minutesAwake": "15",
						"stagesSummary": [
							{"type": "AWAKE", "minutes": "15", "count": "3"},
							{"type": "LIGHT", "minutes": "227", "count": "11"}
						]
					}
				}
			}
		]
	}`)

	points, err := ExtractValues[Sleep](raw, "sleep")
	if err != nil {
		t.Fatalf("ExtractValues: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("len(points) = %d, want 1", len(points))
	}
	s := points[0]
	if len(s.Stages) != 2 || s.Stages[0].Type != "AWAKE" || s.Stages[1].Type != "LIGHT" {
		t.Errorf("Stages = %+v, want 2 stages AWAKE then LIGHT", s.Stages)
	}
	if s.Summary.MinutesAsleep != 413 || s.Summary.MinutesAwake != 15 {
		t.Errorf("Summary = %+v, want MinutesAsleep=413 MinutesAwake=15", s.Summary)
	}
	if len(s.Summary.StagesSummary) != 2 || s.Summary.StagesSummary[0].Minutes != 15 {
		t.Errorf("StagesSummary = %+v", s.Summary.StagesSummary)
	}
}

func TestExtractRollupValuesTotalCalories(t *testing.T) {
	raw := json.RawMessage(`{
		"rollupDataPoints": [
			{
				"civilStartTime": {"date": {"year": 2026, "month": 7, "day": 31}},
				"civilEndTime": {"date": {"year": 2026, "month": 8, "day": 1}},
				"totalCalories": {"kcalSum": 1847.3}
			}
		]
	}`)

	points, err := ExtractRollupValues[TotalCaloriesRollup](raw, "totalCalories")
	if err != nil {
		t.Fatalf("ExtractRollupValues: %v", err)
	}
	if len(points) != 1 {
		t.Fatalf("len(points) = %d, want 1", len(points))
	}
	if points[0].Value.KcalSum != 1847.3 {
		t.Errorf("KcalSum = %v, want 1847.3", points[0].Value.KcalSum)
	}
	if points[0].CivilStart.Day != 31 || points[0].CivilEnd.Day != 1 {
		t.Errorf("CivilStart/CivilEnd = %+v/%+v, want Day 31/1", points[0].CivilStart, points[0].CivilEnd)
	}
}

func TestExtractValuesEmptyResponseIsEmptyNotError(t *testing.T) {
	points, err := ExtractValues[Steps](json.RawMessage(`{}`), "steps")
	if err != nil {
		t.Fatalf("ExtractValues on empty object: %v", err)
	}
	if len(points) != 0 {
		t.Errorf("len(points) = %d, want 0", len(points))
	}
}
