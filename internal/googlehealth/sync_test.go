package googlehealth

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/sdhungan/Personal-Health-Data/internal/db"
)

// fakeTransport routes requests to canned JSON bodies keyed by
// "list:<dataType>" or "rollup:<dataType>", parsed from the request URL.
// It does not actually honor the filter query parameter — tests choose
// fixture timestamps that unambiguously belong to the day under test
// instead, since the real API's filtering behavior is already covered by
// dump_test.go.
type fakeTransport struct {
	responses map[string]string
}

func (t *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	parts := strings.Split(req.URL.Path, "/")
	var dataType string
	for i, p := range parts {
		if p == "dataTypes" && i+1 < len(parts) {
			dataType = parts[i+1]
		}
	}
	key := "list:" + dataType
	if strings.Contains(req.URL.Path, "dataPoints:dailyRollUp") {
		key = "rollup:" + dataType
	}

	body, ok := t.responses[key]
	if !ok {
		body = "{}"
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(dbpkg.Schema); err != nil {
		t.Fatalf("applying schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x')`); err != nil {
		t.Fatalf("seeding test user: %v", err)
	}
	return db
}

// testDataResponses is a full day's worth of synthetic (structurally
// real, numerically made-up) fixture data for 2026-07-30, covering every
// data type SyncDay maps.
func testDataResponses() map[string]string {
	return map[string]string{
		"list:daily-resting-heart-rate": `{"dataPoints":[{"dataSource":{},"dailyRestingHeartRate":{"date":{"year":2026,"month":7,"day":30},"beatsPerMinute":"62"}}]}`,
		"list:daily-heart-rate-variability": `{"dataPoints":[{"dataSource":{},"dailyHeartRateVariability":{
			"date":{"year":2026,"month":7,"day":30},
			"averageHeartRateVariabilityMilliseconds":75.5,
			"nonRemHeartRateBeatsPerMinute":"58",
			"entropy":2.5,
			"deepSleepRootMeanSquareOfSuccessiveDifferencesMilliseconds":60.0
		}}]}`,
		"list:daily-oxygen-saturation": `{"dataPoints":[{"dataSource":{},"dailyOxygenSaturation":{
			"date":{"year":2026,"month":7,"day":30},
			"averagePercentage":96.0,"lowerBoundPercentage":93.0,"upperBoundPercentage":98.0,"standardDeviationPercentage":0.9
		}}]}`,
		"list:daily-respiratory-rate": `{"dataPoints":[{"dataSource":{},"dailyRespiratoryRate":{"date":{"year":2026,"month":7,"day":30},"breathsPerMinute":16.5}}]}`,
		"rollup:total-calories":       `{"rollupDataPoints":[{"civilStartTime":{"date":{"year":2026,"month":7,"day":30}},"civilEndTime":{"date":{"year":2026,"month":7,"day":31}},"totalCalories":{"kcalSum":2103.5}}]}`,
		"list:steps": `{"dataPoints":[
			{"dataSource":{},"steps":{"interval":{"startTime":"2026-07-30T10:00:00Z","endTime":"2026-07-30T11:00:00Z"},"count":"1200"}},
			{"dataSource":{},"steps":{"interval":{"startTime":"2026-07-30T11:00:00Z","endTime":"2026-07-30T12:00:00Z"},"count":"800"}}
		]}`,
		"list:distance": `{"dataPoints":[{"dataSource":{},"distance":{"interval":{"startTime":"2026-07-30T10:00:00Z","endTime":"2026-07-30T11:00:00Z"},"meters":"1500"}}]}`,
		"list:active-minutes": `{"dataPoints":[{"dataSource":{},"activeMinutes":{
			"interval":{"startTime":"2026-07-30T10:00:00Z","endTime":"2026-07-30T11:00:00Z"},
			"activeMinutesByActivityLevel":[{"activityLevel":"MODERATE","minutes":"20"},{"activityLevel":"VIGOROUS","minutes":"10"}]
		}}]}`,
		"rollup:floors":             `{"rollupDataPoints":[{"civilStartTime":{"date":{"year":2026,"month":7,"day":30}},"civilEndTime":{"date":{"year":2026,"month":7,"day":31}},"floors":{"floorsSum":8.0}}]}`,
		"list:altitude":             `{"dataPoints":[{"dataSource":{},"altitude":{"interval":{"startTime":"2026-07-30T10:00:00Z","endTime":"2026-07-30T11:00:00Z"},"gainMillimeters":"12000"}}]}`,
		"list:sedentary-period":     `{"dataPoints":[{"dataSource":{},"sedentaryPeriod":{"interval":{"startTime":"2026-07-30T14:00:00Z","endTime":"2026-07-30T15:30:00Z"}}}]}`,
		"list:active-energy-burned": `{"dataPoints":[{"dataSource":{},"activeEnergyBurned":{"interval":{"startTime":"2026-07-30T10:00:00Z","endTime":"2026-07-30T11:00:00Z"},"kcal":210.5}}]}`,
		"list:vo2-max":              `{"dataPoints":[{"dataSource":{},"vo2Max":{"sampleTime":{"physicalTime":"2026-07-30T12:00:00Z"},"vo2MaxMillilitersPerKilogramPerMinute":41.2}}]}`,
		"list:run-vo2-max":          `{"dataPoints":[{"dataSource":{},"runVo2Max":{"sampleTime":{"physicalTime":"2026-07-30T12:00:00Z"},"mlPerKgPerMin":42.8}}]}`,
		"list:daily-heart-rate-zones": `{"dataPoints":[{"dataSource":{},"dailyHeartRateZones":{
			"date":{"year":2026,"month":7,"day":30},
			"heartRateZones":[{"heartRateZoneType":"LIGHT","minBeatsPerMinute":"30","maxBeatsPerMinute":"120"}]
		}}]}`,
		"list:daily-sleep-temperature-derivations": `{"dataPoints":[{"dataSource":{},"dailySleepTemperatureDerivations":{
			"date":{"year":2026,"month":7,"day":30},
			"nightlyTemperatureCelsius":36.1,"baselineTemperatureCelsius":36.4,"relativeNightlyStddev30dCelsius":0.2
		}}]}`,
		"list:time-in-heart-rate-zone": `{"dataPoints":[
			{"dataSource":{},"timeInHeartRateZone":{"interval":{"startTime":"2026-07-30T10:00:00Z","endTime":"2026-07-30T10:30:00Z"},"heartRateZone":"MODERATE","durationMillis":"600000"}},
			{"dataSource":{},"timeInHeartRateZone":{"interval":{"startTime":"2026-07-30T10:30:00Z","endTime":"2026-07-30T10:40:00Z"},"heartRateZone":"MODERATE","durationMillis":"300000"}}
		]}`,
		"rollup:calories-in-heart-rate-zone": `{"rollupDataPoints":[{"civilStartTime":{"date":{"year":2026,"month":7,"day":30}},"civilEndTime":{"date":{"year":2026,"month":7,"day":31}},"caloriesInHeartRateZone":{"caloriesInHeartRateZones":[{"heartRateZone":"MODERATE","kcal":45.0}]}}]}`,
		"list:active-zone-minutes": `{"dataPoints":[
			{"dataSource":{},"activeZoneMinutes":{"interval":{"startTime":"2026-07-30T10:00:00Z","endTime":"2026-07-30T10:30:00Z"},"heartRateZone":"CARDIO","activeZoneMinutes":"15"}},
			{"dataSource":{},"activeZoneMinutes":{"interval":{"startTime":"2026-07-30T11:00:00Z","endTime":"2026-07-30T11:15:00Z"},"heartRateZone":"CARDIO","activeZoneMinutes":"5"}},
			{"dataSource":{},"activeZoneMinutes":{"interval":{"startTime":"2026-07-30T12:00:00Z","endTime":"2026-07-30T12:10:00Z"},"heartRateZone":"PEAK","activeZoneMinutes":"3"}}
		]}`,
		"list:activity-level": `{"dataPoints":[
			{"dataSource":{},"activityLevel":{"interval":{"startTime":"2026-07-30T08:00:00Z","endTime":"2026-07-30T09:00:00Z"},"activityLevelType":"SEDENTARY"}},
			{"dataSource":{},"activityLevel":{"interval":{"startTime":"2026-07-30T09:00:00Z","endTime":"2026-07-30T09:30:00Z"},"activityLevelType":"LIGHTLY_ACTIVE"}}
		]}`,
		"list:blood-glucose": `{"dataPoints":[
			{"dataSource":{},"bloodGlucose":{"sampleTime":{"physicalTime":"2026-07-30T07:00:00Z"},"bloodGlucoseMilligramsPerDeciliter":95.0,"measurementTiming":"FASTING"}},
			{"dataSource":{},"bloodGlucose":{"sampleTime":{"physicalTime":"2026-07-30T13:00:00Z"},"bloodGlucoseMilligramsPerDeciliter":115.0,"measurementTiming":"AFTER_MEAL"}}
		]}`,
		"list:core-body-temperature": `{"dataPoints":[
			{"dataSource":{},"coreBodyTemperature":{"sampleTime":{"physicalTime":"2026-07-30T03:00:00Z"},"temperatureCelsius":36.5}},
			{"dataSource":{},"coreBodyTemperature":{"sampleTime":{"physicalTime":"2026-07-30T06:00:00Z"},"temperatureCelsius":37.1}}
		]}`,
		"list:respiratory-rate-sleep-summary": `{"dataPoints":[{"dataSource":{},"respiratoryRateSleepSummary":{
			"sampleTime":{"physicalTime":"2026-07-30T08:30:00Z"},
			"deepSleepStats":{"breathsPerMinute":14.0},"lightSleepStats":{"breathsPerMinute":15.0},
			"remSleepStats":{"breathsPerMinute":16.0},"fullSleepStats":{"breathsPerMinute":15.0}
		}}]}`,
		"list:heart-rate": `{"dataPoints":[
			{"dataSource":{},"heartRate":{"sampleTime":{"physicalTime":"2026-07-30T10:15:00Z"},"beatsPerMinute":"70"}},
			{"dataSource":{},"heartRate":{"sampleTime":{"physicalTime":"2026-07-30T10:30:00Z"},"beatsPerMinute":"90"}}
		]}`,
		"list:sleep": `{"dataPoints":[{"dataSource":{},"sleep":{
			"interval":{"startTime":"2026-07-30T02:00:00Z","endTime":"2026-07-30T09:00:00Z"},
			"type":"STAGES",
			"stages":[
				{"startTime":"2026-07-30T02:00:00Z","endTime":"2026-07-30T03:00:00Z","type":"LIGHT"},
				{"startTime":"2026-07-30T03:00:00Z","endTime":"2026-07-30T05:00:00Z","type":"DEEP"}
			],
			"summary":{
				"minutesInSleepPeriod":"420","minutesAfterWakeUp":"0","minutesToFallAsleep":"5",
				"minutesAsleep":"400","minutesAwake":"20",
				"stagesSummary":[{"type":"LIGHT","minutes":"260","count":"5"},{"type":"DEEP","minutes":"140","count":"3"}]
			}
		}}]}`,
		"list:exercise": `{"dataPoints":[{"dataSource":{},"exercise":{
			"interval":{"startTime":"2026-07-30T12:00:00Z","endTime":"2026-07-30T12:30:00Z"},
			"exerciseType":"BIKING","displayName":"Bike","activeDuration":"1800s",
			"metricsSummary":{"caloriesKcal":150,"averageHeartRateBeatsPerMinute":"110","activeZoneMinutes":"15",
				"heartRateZoneDurations":{"lightTime":"600s","moderateTime":"900s","vigorousTime":"300s","peakTime":"0s"}}
		}}]}`,
		"list:electrocardiogram": `{"dataPoints":[{"dataSource":{},"electrocardiogram":{
			"interval":{"startTime":"2026-07-30T13:00:00Z"},
			"beatsPerMinuteAvg":"75","resultClassification":"NORMAL_SINUS_RHYTHM","waveformSamples":[1,2,3]
		}}]}`,
		"list:weight":   `{"dataPoints":[{"dataSource":{},"weight":{"sampleTime":{"physicalTime":"2026-07-30T12:00:00Z"},"kilograms":78.2}}]}`,
		"list:height":   `{"dataPoints":[{"dataSource":{},"height":{"sampleTime":{"physicalTime":"2026-07-30T12:00:00Z"},"meters":1.8}}]}`,
		"list:body-fat": `{"dataPoints":[{"dataSource":{},"bodyFat":{"sampleTime":{"physicalTime":"2026-07-30T12:00:00Z"},"percentage":18.5}}]}`,
	}
}

func newTestSyncer(t *testing.T, responses map[string]string) (*DBSyncer, *sql.DB) {
	t.Helper()
	client := NewClient(&http.Client{Transport: &fakeTransport{responses: responses}})
	db := newTestDB(t)
	return &DBSyncer{Client: client, DB: db, UserID: 1}, db
}

func TestSyncDayFullDayPopulatesEveryTable(t *testing.T) {
	syncer, db := newTestSyncer(t, testDataResponses())
	day := time.Date(2026, 7, 30, 0, 0, 0, 0, time.Local)

	hasData, err := syncer.SyncDay(context.Background(), day)
	if err != nil {
		t.Fatalf("SyncDay: %v", err)
	}
	if !hasData {
		t.Fatal("SyncDay reported hasData = false, want true")
	}

	// watch_daily_summary
	var stepsTotal, activeMinutes, floorsClimbed, sedentaryMin int64
	var distanceM, kcalBurnedGoogle, restingHR, hrMin, hrMax, hrAvg, hrvAvg, spo2Avg, spo2Min, respRate float64
	var altitudeGainM, activeEnergyKcal, vo2MaxSample, vo2MaxRunSample float64
	var sleepTempC, sleepTempBaselineC float64
	var bgAvg, bgMin, bgMax, cbtAvg, cbtMin, cbtMax float64
	var sleepMinutes int64
	err = db.QueryRow(`SELECT steps_total, distance_m, active_minutes, kcal_burned_google,
		resting_heart_rate_bpm, heart_rate_min_bpm, heart_rate_max_bpm, heart_rate_avg_bpm,
		hrv_avg_ms, spo2_avg_pct, spo2_min_pct, respiratory_rate_avg_bpm, sleep_duration_minutes,
		floors_climbed, sedentary_minutes, altitude_gain_m, active_energy_burned_kcal, vo2_max_sample, vo2_max_run_sample,
		sleep_temperature_c, sleep_temperature_baseline_c,
		blood_glucose_avg_mg_dl, blood_glucose_min_mg_dl, blood_glucose_max_mg_dl,
		core_body_temperature_avg_c, core_body_temperature_min_c, core_body_temperature_max_c
		FROM watch_daily_summary WHERE day = '2026-07-30'`).
		Scan(&stepsTotal, &distanceM, &activeMinutes, &kcalBurnedGoogle, &restingHR, &hrMin, &hrMax, &hrAvg,
			&hrvAvg, &spo2Avg, &spo2Min, &respRate, &sleepMinutes,
			&floorsClimbed, &sedentaryMin, &altitudeGainM, &activeEnergyKcal, &vo2MaxSample, &vo2MaxRunSample,
			&sleepTempC, &sleepTempBaselineC,
			&bgAvg, &bgMin, &bgMax, &cbtAvg, &cbtMin, &cbtMax)
	if err != nil {
		t.Fatalf("querying watch_daily_summary: %v", err)
	}
	if stepsTotal != 2000 {
		t.Errorf("steps_total = %d, want 2000", stepsTotal)
	}
	if distanceM != 1500 {
		t.Errorf("distance_m = %v, want 1500", distanceM)
	}
	if activeMinutes != 30 {
		t.Errorf("active_minutes = %d, want 30", activeMinutes)
	}
	if kcalBurnedGoogle != 2103.5 {
		t.Errorf("kcal_burned_google = %v, want 2103.5", kcalBurnedGoogle)
	}
	if restingHR != 62 {
		t.Errorf("resting_heart_rate_bpm = %v, want 62", restingHR)
	}
	if hrMin != 70 || hrMax != 90 || hrAvg != 80 {
		t.Errorf("heart_rate min/max/avg = %v/%v/%v, want 70/90/80", hrMin, hrMax, hrAvg)
	}
	if hrvAvg != 75.5 {
		t.Errorf("hrv_avg_ms = %v, want 75.5", hrvAvg)
	}
	if spo2Avg != 96.0 || spo2Min != 93.0 {
		t.Errorf("spo2 avg/min = %v/%v, want 96/93", spo2Avg, spo2Min)
	}
	if respRate != 16.5 {
		t.Errorf("respiratory_rate_avg_bpm = %v, want 16.5", respRate)
	}
	if sleepMinutes != 400 {
		t.Errorf("sleep_duration_minutes = %d, want 400", sleepMinutes)
	}
	if floorsClimbed != 8 {
		t.Errorf("floors_climbed = %d, want 8", floorsClimbed)
	}
	if sedentaryMin != 90 {
		t.Errorf("sedentary_minutes = %d, want 90", sedentaryMin)
	}
	if altitudeGainM != 12 {
		t.Errorf("altitude_gain_m = %v, want 12", altitudeGainM)
	}
	if activeEnergyKcal != 210.5 {
		t.Errorf("active_energy_burned_kcal = %v, want 210.5", activeEnergyKcal)
	}
	if vo2MaxSample != 41.2 {
		t.Errorf("vo2_max_sample = %v, want 41.2", vo2MaxSample)
	}
	if vo2MaxRunSample != 42.8 {
		t.Errorf("vo2_max_run_sample = %v, want 42.8", vo2MaxRunSample)
	}
	if sleepTempC != 36.1 || sleepTempBaselineC != 36.4 {
		t.Errorf("sleep temperature/baseline = %v/%v, want 36.1/36.4", sleepTempC, sleepTempBaselineC)
	}
	if bgAvg != 105 || bgMin != 95 || bgMax != 115 {
		t.Errorf("blood glucose avg/min/max = %v/%v/%v, want 105/95/115", bgAvg, bgMin, bgMax)
	}
	if cbtAvg != 36.8 || cbtMin != 36.5 || cbtMax != 37.1 {
		t.Errorf("core body temperature avg/min/max = %v/%v/%v, want 36.8/36.5/37.1", cbtAvg, cbtMin, cbtMax)
	}

	// watch_active_minutes_by_level / watch_active_zone_minutes_by_zone
	var moderateActiveMin, vigorousActiveMin int64
	if err := db.QueryRow(`SELECT minutes FROM watch_active_minutes_by_level WHERE day='2026-07-30' AND activity_level='MODERATE'`).Scan(&moderateActiveMin); err != nil {
		t.Fatalf("querying watch_active_minutes_by_level (MODERATE): %v", err)
	}
	if err := db.QueryRow(`SELECT minutes FROM watch_active_minutes_by_level WHERE day='2026-07-30' AND activity_level='VIGOROUS'`).Scan(&vigorousActiveMin); err != nil {
		t.Fatalf("querying watch_active_minutes_by_level (VIGOROUS): %v", err)
	}
	if moderateActiveMin != 20 || vigorousActiveMin != 10 {
		t.Errorf("active minutes by level MODERATE/VIGOROUS = %d/%d, want 20/10", moderateActiveMin, vigorousActiveMin)
	}
	var cardioAZM, peakAZM int64
	if err := db.QueryRow(`SELECT minutes FROM watch_active_zone_minutes_by_zone WHERE day='2026-07-30' AND zone_type='CARDIO'`).Scan(&cardioAZM); err != nil {
		t.Fatalf("querying watch_active_zone_minutes_by_zone (CARDIO): %v", err)
	}
	if err := db.QueryRow(`SELECT minutes FROM watch_active_zone_minutes_by_zone WHERE day='2026-07-30' AND zone_type='PEAK'`).Scan(&peakAZM); err != nil {
		t.Fatalf("querying watch_active_zone_minutes_by_zone (PEAK): %v", err)
	}
	if cardioAZM != 20 || peakAZM != 3 {
		t.Errorf("active zone minutes by zone CARDIO/PEAK = %d/%d, want 20/3 (15+5 from two intervals / 3)", cardioAZM, peakAZM)
	}

	// watch_activity_level_segment
	var segmentCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM watch_activity_level_segment WHERE day='2026-07-30'`).Scan(&segmentCount); err != nil {
		t.Fatalf("counting watch_activity_level_segment: %v", err)
	}
	if segmentCount != 2 {
		t.Errorf("activity level segment count = %d, want 2", segmentCount)
	}

	// watch_blood_glucose_sample / watch_core_body_temperature_sample
	var bgSampleCount, cbtSampleCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM watch_blood_glucose_sample`).Scan(&bgSampleCount); err != nil {
		t.Fatalf("counting watch_blood_glucose_sample: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM watch_core_body_temperature_sample`).Scan(&cbtSampleCount); err != nil {
		t.Fatalf("counting watch_core_body_temperature_sample: %v", err)
	}
	if bgSampleCount != 2 || cbtSampleCount != 2 {
		t.Errorf("blood glucose/core body temp sample counts = %d/%d, want 2/2", bgSampleCount, cbtSampleCount)
	}

	// watch_heart_rate_zone_definition / _minutes, watch_calories_by_zone
	var minBpm, maxBpm int64
	if err := db.QueryRow(`SELECT min_bpm, max_bpm FROM watch_heart_rate_zone_definition WHERE day='2026-07-30' AND zone_type='LIGHT'`).Scan(&minBpm, &maxBpm); err != nil {
		t.Fatalf("querying watch_heart_rate_zone_definition: %v", err)
	}
	if minBpm != 30 || maxBpm != 120 {
		t.Errorf("LIGHT zone min/max = %d/%d, want 30/120", minBpm, maxBpm)
	}
	var zoneMinutes float64
	if err := db.QueryRow(`SELECT minutes FROM watch_heart_rate_zone_minutes WHERE day='2026-07-30' AND zone_type='MODERATE'`).Scan(&zoneMinutes); err != nil {
		t.Fatalf("querying watch_heart_rate_zone_minutes: %v", err)
	}
	if zoneMinutes != 15 {
		t.Errorf("MODERATE zone minutes = %v, want 15 (10+5 from two intervals)", zoneMinutes)
	}
	var zoneKcal float64
	if err := db.QueryRow(`SELECT kcal FROM watch_calories_by_zone WHERE day='2026-07-30' AND zone_type='MODERATE'`).Scan(&zoneKcal); err != nil {
		t.Fatalf("querying watch_calories_by_zone: %v", err)
	}
	if zoneKcal != 45.0 {
		t.Errorf("MODERATE zone kcal = %v, want 45", zoneKcal)
	}

	// watch_sleep_session: per-stage respiratory rate, matched by sample time
	var deepResp, lightResp, remResp, fullResp float64
	if err := db.QueryRow(`SELECT deep_resp_rate_bpm, light_resp_rate_bpm, rem_resp_rate_bpm, full_resp_rate_bpm FROM watch_sleep_session WHERE day='2026-07-30'`).
		Scan(&deepResp, &lightResp, &remResp, &fullResp); err != nil {
		t.Fatalf("querying sleep session respiratory rate: %v", err)
	}
	if deepResp != 14.0 || lightResp != 15.0 || remResp != 16.0 || fullResp != 15.0 {
		t.Errorf("sleep session resp rates deep/light/rem/full = %v/%v/%v/%v, want 14/15/16/15", deepResp, lightResp, remResp, fullResp)
	}

	// watch_steps_hourly: two buckets, at whatever local hour the fixture
	// timestamps land on (portable across the test machine's timezone).
	hour1, _ := time.Parse(time.RFC3339, "2026-07-30T10:00:00Z")
	hour2, _ := time.Parse(time.RFC3339, "2026-07-30T11:00:00Z")
	var steps1, steps2 int64
	if err := db.QueryRow(`SELECT steps FROM watch_steps_hourly WHERE day='2026-07-30' AND hour=?`, hour1.In(time.Local).Hour()).Scan(&steps1); err != nil {
		t.Fatalf("querying hourly bucket 1: %v", err)
	}
	if err := db.QueryRow(`SELECT steps FROM watch_steps_hourly WHERE day='2026-07-30' AND hour=?`, hour2.In(time.Local).Hour()).Scan(&steps2); err != nil {
		t.Fatalf("querying hourly bucket 2: %v", err)
	}
	if steps1 != 1200 || steps2 != 800 {
		t.Errorf("hourly steps = %d, %d, want 1200, 800", steps1, steps2)
	}

	// watch_heart_rate_intraday
	var hrCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM watch_heart_rate_intraday`).Scan(&hrCount); err != nil {
		t.Fatalf("counting heart rate intraday: %v", err)
	}
	if hrCount != 2 {
		t.Errorf("heart rate intraday rows = %d, want 2", hrCount)
	}

	// watch_sleep_session + watch_sleep_stage
	var sleepID int64
	var isMain, minutesAsleepPeriod, minutesAwake, minutesLight, minutesDeep int64
	err = db.QueryRow(`SELECT id, is_main_sleep, duration_minutes, minutes_awake, minutes_light, minutes_deep
		FROM watch_sleep_session WHERE day='2026-07-30'`).
		Scan(&sleepID, &isMain, &minutesAsleepPeriod, &minutesAwake, &minutesLight, &minutesDeep)
	if err != nil {
		t.Fatalf("querying watch_sleep_session: %v", err)
	}
	if isMain != 1 {
		t.Errorf("is_main_sleep = %d, want 1 (only session of the day)", isMain)
	}
	if minutesAsleepPeriod != 420 || minutesAwake != 20 || minutesLight != 260 || minutesDeep != 140 {
		t.Errorf("sleep session fields = %d/%d/%d/%d, want 420/20/260/140", minutesAsleepPeriod, minutesAwake, minutesLight, minutesDeep)
	}
	var stageCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM watch_sleep_stage WHERE sleep_session_id = ?`, sleepID).Scan(&stageCount); err != nil {
		t.Fatalf("counting sleep stages: %v", err)
	}
	if stageCount != 2 {
		t.Errorf("sleep stage count = %d, want 2", stageCount)
	}

	// watch_exercise_session
	var exerciseType string
	var exerciseDuration int64
	var caloriesBurned float64
	err = db.QueryRow(`SELECT exercise_type, duration_minutes, calories_burned FROM watch_exercise_session WHERE day='2026-07-30'`).
		Scan(&exerciseType, &exerciseDuration, &caloriesBurned)
	if err != nil {
		t.Fatalf("querying watch_exercise_session: %v", err)
	}
	if exerciseType != "BIKING" || exerciseDuration != 30 || caloriesBurned != 150 {
		t.Errorf("exercise session = %q/%d/%v, want BIKING/30/150", exerciseType, exerciseDuration, caloriesBurned)
	}

	// watch_ecg_reading
	var classification string
	var ecgAvgHR float64
	if err := db.QueryRow(`SELECT classification, avg_heart_rate_bpm FROM watch_ecg_reading`).Scan(&classification, &ecgAvgHR); err != nil {
		t.Fatalf("querying watch_ecg_reading: %v", err)
	}
	if classification != "NORMAL_SINUS_RHYTHM" || ecgAvgHR != 75 {
		t.Errorf("ecg reading = %q/%v, want NORMAL_SINUS_RHYTHM/75", classification, ecgAvgHR)
	}

	// body_measurement
	var weightKg, heightCm, bodyFatPct float64
	err = db.QueryRow(`SELECT weight_kg_raw, height_cm_raw, body_fat_pct_raw FROM body_measurement WHERE day='2026-07-30'`).
		Scan(&weightKg, &heightCm, &bodyFatPct)
	if err != nil {
		t.Fatalf("querying body_measurement: %v", err)
	}
	if weightKg != 78.2 || heightCm != 180 || bodyFatPct != 18.5 {
		t.Errorf("body measurement = %v/%v/%v, want 78.2/180/18.5", weightKg, heightCm, bodyFatPct)
	}
}

func TestSyncDayEmptyDayReportsNoData(t *testing.T) {
	syncer, _ := newTestSyncer(t, map[string]string{}) // every call returns "{}"
	day := time.Date(2026, 7, 30, 0, 0, 0, 0, time.Local)

	hasData, err := syncer.SyncDay(context.Background(), day)
	if err != nil {
		t.Fatalf("SyncDay: %v", err)
	}
	if hasData {
		t.Error("SyncDay reported hasData = true for an entirely empty day, want false")
	}
}

func TestSyncDayIsIdempotentOnRepeatedRuns(t *testing.T) {
	syncer, db := newTestSyncer(t, testDataResponses())
	day := time.Date(2026, 7, 30, 0, 0, 0, 0, time.Local)

	for i := 0; i < 3; i++ {
		if _, err := syncer.SyncDay(context.Background(), day); err != nil {
			t.Fatalf("SyncDay iteration %d: %v", i, err)
		}
	}

	for table, want := range map[string]int{
		"watch_daily_summary":                1,
		"watch_sleep_session":                1,
		"watch_sleep_stage":                  2,
		"watch_exercise_session":             1,
		"watch_ecg_reading":                  1,
		"body_measurement":                   1,
		"watch_steps_hourly":                 2,
		"watch_active_minutes_by_level":      2, // MODERATE, VIGOROUS
		"watch_active_zone_minutes_by_zone":  2, // CARDIO, PEAK
		"watch_activity_level_segment":       2,
		"watch_blood_glucose_sample":         2,
		"watch_core_body_temperature_sample": 2,
	} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		if count != want {
			t.Errorf("%s has %d rows after 3 identical syncs, want %d (re-syncing must upsert, not duplicate)", table, count, want)
		}
	}
}

func TestSyncDayPreservesFieldsNotReturnedThisRun(t *testing.T) {
	full := testDataResponses()
	syncer, db := newTestSyncer(t, full)
	day := time.Date(2026, 7, 30, 0, 0, 0, 0, time.Local)

	if _, err := syncer.SyncDay(context.Background(), day); err != nil {
		t.Fatalf("first SyncDay: %v", err)
	}

	// Second sync: steps data type now returns nothing (e.g. transient
	// gap in that one endpoint) — steps_total must NOT be wiped out by
	// the otherwise-successful re-sync.
	partial := testDataResponses()
	delete(partial, "list:steps")
	syncer.Client = NewClient(&http.Client{Transport: &fakeTransport{responses: partial}})
	if _, err := syncer.SyncDay(context.Background(), day); err != nil {
		t.Fatalf("second SyncDay: %v", err)
	}

	var stepsTotal int64
	if err := db.QueryRow(`SELECT steps_total FROM watch_daily_summary WHERE day='2026-07-30'`).Scan(&stepsTotal); err != nil {
		t.Fatalf("querying steps_total: %v", err)
	}
	if stepsTotal != 2000 {
		t.Errorf("steps_total = %d after a re-sync where that endpoint returned nothing, want 2000 (preserved)", stepsTotal)
	}
}
