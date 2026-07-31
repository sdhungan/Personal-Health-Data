package cronometer

import (
	"context"
	"database/sql"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/sdhungan/Personal-Health-Data/internal/db"
)

// Fixture values below are synthetic (structurally matched to the real
// 2026-07-31 dump's field names/shapes — see cmd/cronodump and
// cronometer-integration.md — but with fabricated numbers), never the
// user's real captured data.

const fixtureLogin = `{"result":"SUCCESS","id":42,"sessionKey":"tok-abc","timezone":"UTC"}`

const fixtureDiary = `{
	"summary": {"burned": {"total": 2500.5}},
	"info": {"complete": true, "day": "2026-01-15"},
	"diary": [
		{"type":"Serving","day":"2026-01-15","time":"08:00:00","servingId":1001,"foodId":501,"measureId":1,"grams":150},
		{"type":"Exercise","day":"2026-01-15","time":"09:00:00","source":"Fitbit","exerciseId":9001,"name":"Fitbit Activity","minutes":30,"calories":-200},
		{"type":"Exercise","day":"2026-01-15","time":"10:00:00","exerciseId":9002,"name":"Yoga","minutes":45,"calories":150},
		{"type":"Biometric","day":"2026-01-15","source":"Fitbit","biometricId":8001,"metricId":3,"unitId":5,"amount":60},
		{"type":"Biometric","day":"2026-01-15","time":"07:30:00","biometricId":8002,"metricId":1,"unitId":1,"amount":70.5}
	]
}`

const fixtureFoods = `{"foods":[{"id":501,"name":"Test Oatmeal","source":"Custom",
	"measures":[{"id":1,"name":"g","value":1},{"id":2,"name":"cup","value":150}],
	"nutrients":[{"id":208,"amount":389},{"id":203,"amount":16.9}]}]}`

const fixtureNutritionScores = `{"scores":[{"title":"All Targets","components":[
	{"nutrientId":208,"amount":600},{"nutrientId":203,"amount":25},{"nutrientId":-1205,"amount":50}
]}]}`

const fixtureMetrics = `{"metrics":[
	{"id":1,"name":"Weight","units":[{"id":1,"name":"kg"}]},
	{"id":3,"name":"Heart Rate","units":[{"id":5,"name":"bpm"}]}
]}`

// pathTransport routes each request to a canned body keyed by URL path.
type pathTransport struct {
	responses map[string]string
	calls     map[string]int
}

func (t *pathTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.calls == nil {
		t.calls = map[string]int{}
	}
	t.calls[req.URL.Path]++
	body, ok := t.responses[req.URL.Path]
	if !ok {
		body = `{}`
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
	return db
}

func TestSyncDayHappyPath(t *testing.T) {
	transport := &pathTransport{responses: map[string]string{
		"/api/v2/login":                fixtureLogin,
		"/api/v2/get_diary":            fixtureDiary,
		"/api/v2/get_foods":            fixtureFoods,
		"/api/v2/get_nutrition_scores": fixtureNutritionScores,
		"/api/v2/get_metrics":          fixtureMetrics,
	}}
	db := newTestDB(t)

	s := &DBSyncer{
		Client:      &Client{HTTP: &http.Client{Transport: transport}},
		DB:          db,
		SessionPath: filepath.Join(t.TempDir(), "session.json.enc"),
		credentials: &Credentials{Username: "u", Password: "p"},
	}

	day, err := time.Parse(dateLayout, "2026-01-15")
	if err != nil {
		t.Fatalf("parsing test day: %v", err)
	}

	hasData, err := s.SyncDay(context.Background(), day)
	if err != nil {
		t.Fatalf("SyncDay: %v", err)
	}
	if !hasData {
		t.Error("hasData = false, want true")
	}

	// cronometer_serving: one row, scaled to 150g from a per-100g profile.
	var foodName, quantityUnits string
	var quantityValue, energyKcal, proteinG float64
	if err := db.QueryRow(`SELECT food_name, quantity_value, quantity_units, energy_kcal, protein_g FROM cronometer_serving WHERE day = ?`, "2026-01-15").
		Scan(&foodName, &quantityValue, &quantityUnits, &energyKcal, &proteinG); err != nil {
		t.Fatalf("querying cronometer_serving: %v", err)
	}
	if foodName != "Test Oatmeal" || quantityValue != 150 || quantityUnits != "g" || energyKcal != 583.5 || math.Abs(proteinG-25.35) > 1e-9 {
		t.Errorf("cronometer_serving row = (%q, %v, %q, %v, %v), want (Test Oatmeal, 150, g, 583.5, 25.35)",
			foodName, quantityValue, quantityUnits, energyKcal, proteinG)
	}

	// cronometer_exercise: only the non-Fitbit entry.
	var exerciseCount int
	if err := db.QueryRow(`SELECT count(*) FROM cronometer_exercise WHERE day = ?`, "2026-01-15").Scan(&exerciseCount); err != nil {
		t.Fatalf("counting cronometer_exercise: %v", err)
	}
	if exerciseCount != 1 {
		t.Errorf("cronometer_exercise count = %d, want 1 (Fitbit-sourced entry should be filtered)", exerciseCount)
	}
	var exerciseName string
	var minutes, calories float64
	if err := db.QueryRow(`SELECT exercise_name, minutes, calories_burned FROM cronometer_exercise WHERE day = ?`, "2026-01-15").
		Scan(&exerciseName, &minutes, &calories); err != nil {
		t.Fatalf("querying cronometer_exercise: %v", err)
	}
	if exerciseName != "Yoga" || minutes != 45 || calories != 150 {
		t.Errorf("cronometer_exercise row = (%q, %v, %v), want (Yoga, 45, 150)", exerciseName, minutes, calories)
	}

	// cronometer_biometric: only the non-Fitbit entry, metric/unit resolved by name.
	var biometricCount int
	if err := db.QueryRow(`SELECT count(*) FROM cronometer_biometric WHERE day = ?`, "2026-01-15").Scan(&biometricCount); err != nil {
		t.Fatalf("counting cronometer_biometric: %v", err)
	}
	if biometricCount != 1 {
		t.Errorf("cronometer_biometric count = %d, want 1 (Fitbit-sourced entry should be filtered)", biometricCount)
	}
	var metric, unit string
	var amount float64
	if err := db.QueryRow(`SELECT metric, unit, amount FROM cronometer_biometric WHERE day = ?`, "2026-01-15").
		Scan(&metric, &unit, &amount); err != nil {
		t.Fatalf("querying cronometer_biometric: %v", err)
	}
	if metric != "Weight" || unit != "kg" || amount != 70.5 {
		t.Errorf("cronometer_biometric row = (%q, %q, %v), want (Weight, kg, 70.5)", metric, unit, amount)
	}

	// cronometer_daily_nutrition: completed flag, kcal_burned_cronometer from
	// the diary summary, tracked nutrients from nutrition_scores.
	var completed int
	var kcalBurned, dailyEnergy, dailyProtein, netCarbs float64
	if err := db.QueryRow(`SELECT completed, kcal_burned_cronometer, energy_kcal, protein_g, net_carbs_g FROM cronometer_daily_nutrition WHERE day = ?`, "2026-01-15").
		Scan(&completed, &kcalBurned, &dailyEnergy, &dailyProtein, &netCarbs); err != nil {
		t.Fatalf("querying cronometer_daily_nutrition: %v", err)
	}
	if completed != 1 || kcalBurned != 2500.5 || dailyEnergy != 600 || dailyProtein != 25 || netCarbs != 50 {
		t.Errorf("cronometer_daily_nutrition row = (%v, %v, %v, %v, %v), want (1, 2500.5, 600, 25, 50)",
			completed, kcalBurned, dailyEnergy, dailyProtein, netCarbs)
	}

	if transport.calls["/api/v2/login"] != 1 {
		t.Errorf("login calls = %d, want 1", transport.calls["/api/v2/login"])
	}
}

// countingTransport returns responses[i] for the i-th call to path,
// looping/clamping to the last entry once exhausted — used to simulate a
// session that expires mid-sync and recovers after re-login.
type countingTransport struct {
	responses map[string][]string
	calls     map[string]int
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.calls == nil {
		t.calls = map[string]int{}
	}
	path := req.URL.Path
	idx := t.calls[path]
	t.calls[path]++

	bodies := t.responses[path]
	body := `{}`
	if len(bodies) > 0 {
		if idx >= len(bodies) {
			idx = len(bodies) - 1
		}
		body = bodies[idx]
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func TestSyncDayRetriesOnceAfterSessionExpiry(t *testing.T) {
	emptyDiary := `{"summary":{"burned":{"total":0}},"info":{"complete":false,"day":"2026-01-16"},"diary":[]}`
	transport := &countingTransport{responses: map[string][]string{
		"/api/v2/login":                {fixtureLogin, fixtureLogin},
		"/api/v2/get_diary":            {`{"result":"FAIL","error":"Token Authorization failed"}`, emptyDiary},
		"/api/v2/get_nutrition_scores": {fixtureNutritionScores},
	}}
	db := newTestDB(t)

	s := &DBSyncer{
		Client:      &Client{HTTP: &http.Client{Transport: transport}},
		DB:          db,
		SessionPath: filepath.Join(t.TempDir(), "session.json.enc"),
		credentials: &Credentials{Username: "u", Password: "p"},
		session:     &Session{UserID: 1, Token: "stale", Timezone: "UTC"}, // pre-seeded, stale
	}

	day, err := time.Parse(dateLayout, "2026-01-16")
	if err != nil {
		t.Fatalf("parsing test day: %v", err)
	}

	if _, err := s.SyncDay(context.Background(), day); err != nil {
		t.Fatalf("SyncDay: %v", err)
	}

	if transport.calls["/api/v2/get_diary"] != 2 {
		t.Errorf("get_diary calls = %d, want 2 (one FAIL, one retry after re-login)", transport.calls["/api/v2/get_diary"])
	}
	if transport.calls["/api/v2/login"] != 1 {
		t.Errorf("login calls = %d, want 1 (re-login triggered by the FAIL)", transport.calls["/api/v2/login"])
	}
	if s.session.Token != "tok-abc" {
		t.Errorf("session token after retry = %q, want refreshed token tok-abc", s.session.Token)
	}
}
