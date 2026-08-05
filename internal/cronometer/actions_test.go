package cronometer

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// Fixture values below are synthetic (structurally matched to real response
// shapes confirmed via cmd/cronoverify — see cronometer-integration.md —
// but with fabricated numbers), never the user's real captured data.

const fixtureFindFood = `{"foods":[
	{"id":501,"name":"Test Oatmeal","measureId":1,"translationId":0},
	{"id":777,"name":"Unresolvable Food","measureId":1,"translationId":0}
]}`

const fixtureAddFood = `{"id":9001}`

const fixtureNewFood = `{"foods":[{"id":9001,"name":"My Custom Food","source":"Custom",
	"measures":[{"id":5001,"name":"g","value":1}],
	"nutrients":[{"id":208,"amount":250},{"id":203,"amount":10}]}]}`

const fixtureAddServing = `{"id":12345}`

func newTestSyncer(t *testing.T, transport http.RoundTripper) *DBSyncer {
	t.Helper()
	return &DBSyncer{
		Client:      &Client{HTTP: &http.Client{Transport: transport}},
		DB:          newTestDB(t),
		UserID:      1,
		SessionPath: filepath.Join(t.TempDir(), "session.json.enc"),
		credentials: &Credentials{Username: "u", Password: "p"},
	}
}

func TestSearchFoodResolvesCandidatesAndSkipsUnresolvable(t *testing.T) {
	transport := &pathTransport{responses: map[string]string{
		"/api/v2/login":     fixtureLogin,
		"/api/v2/find_food": fixtureFindFood,
		"/api/v2/get_foods": fixtureFoods, // only resolves food 501, not 777
	}}
	s := newTestSyncer(t, transport)

	candidates, err := s.SearchFood(context.Background(), "oatmeal", 0)
	if err != nil {
		t.Fatalf("SearchFood: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1 (id 777 has no get_foods match and should be skipped)", len(candidates))
	}
	c := candidates[0]
	if c.FoodID != 501 || c.Name != "Test Oatmeal" {
		t.Errorf("candidate = (%d, %q), want (501, Test Oatmeal)", c.FoodID, c.Name)
	}
	if c.Per100g.EnergyKcal == nil || *c.Per100g.EnergyKcal != 389 {
		t.Errorf("Per100g.EnergyKcal = %v, want 389", c.Per100g.EnergyKcal)
	}
	if c.Per100g.ProteinG == nil || *c.Per100g.ProteinG != 16.9 {
		t.Errorf("Per100g.ProteinG = %v, want 16.9", c.Per100g.ProteinG)
	}
	if len(c.Measures) != 2 {
		t.Errorf("len(Measures) = %d, want 2", len(c.Measures))
	}
}

func TestSearchFoodLimitsCandidateCount(t *testing.T) {
	transport := &pathTransport{responses: map[string]string{
		"/api/v2/login":     fixtureLogin,
		"/api/v2/find_food": fixtureFindFood, // 2 results
		"/api/v2/get_foods": fixtureFoods,
	}}
	s := newTestSyncer(t, transport)

	candidates, err := s.SearchFood(context.Background(), "oatmeal", 1)
	if err != nil {
		t.Fatalf("SearchFood: %v", err)
	}
	if len(candidates) > 1 {
		t.Errorf("len(candidates) = %d, want <= 1 with limit=1", len(candidates))
	}
}

func TestCreateCustomFoodResolvesMeasureID(t *testing.T) {
	transport := &pathTransport{responses: map[string]string{
		"/api/v2/login":     fixtureLogin,
		"/api/v2/add_food":  fixtureAddFood,
		"/api/v2/get_foods": fixtureNewFood,
	}}
	s := newTestSyncer(t, transport)

	kcal, protein := 250.0, 10.0
	foodID, measureID, err := s.CreateCustomFood(context.Background(), "My Custom Food", NutrientProfile{
		EnergyKcal: &kcal, ProteinG: &protein,
	})
	if err != nil {
		t.Fatalf("CreateCustomFood: %v", err)
	}
	if foodID != 9001 {
		t.Errorf("foodID = %d, want 9001", foodID)
	}
	if measureID != 5001 {
		t.Errorf("measureID = %d, want 5001 (resolved via get_foods, not returned by add_food itself)", measureID)
	}
}

func TestLogServingDefaultsDayAndTime(t *testing.T) {
	transport := &pathTransport{responses: map[string]string{
		"/api/v2/login":       fixtureLogin,
		"/api/v2/get_foods":   fixtureFoods,
		"/api/v2/add_serving": fixtureAddServing,
	}}
	s := newTestSyncer(t, transport)

	logged, err := s.LogServing(context.Background(), 501, 1, 150, "", "")
	if err != nil {
		t.Fatalf("LogServing: %v", err)
	}
	if logged.ServingID != 12345 {
		t.Errorf("ServingID = %d, want 12345", logged.ServingID)
	}
	if logged.FoodName != "Test Oatmeal" {
		t.Errorf("FoodName = %q, want Test Oatmeal", logged.FoodName)
	}
	if logged.Day == "" || logged.Time == "" {
		t.Error("Day/Time were not defaulted")
	}
	// 150g of a 389kcal/100g food -> 583.5kcal, same scaling SyncDay's upsert already relies on.
	if logged.Nutrients.EnergyKcal == nil || *logged.Nutrients.EnergyKcal != 583.5 {
		t.Errorf("Nutrients.EnergyKcal = %v, want 583.5", logged.Nutrients.EnergyKcal)
	}
}

func TestLogServingUsesExplicitDayAndTime(t *testing.T) {
	transport := &pathTransport{responses: map[string]string{
		"/api/v2/login":       fixtureLogin,
		"/api/v2/get_foods":   fixtureFoods,
		"/api/v2/add_serving": fixtureAddServing,
	}}
	s := newTestSyncer(t, transport)

	logged, err := s.LogServing(context.Background(), 501, 1, 150, "2026-02-01", "08:30:00")
	if err != nil {
		t.Fatalf("LogServing: %v", err)
	}
	if logged.Day != "2026-02-01" || logged.Time != "08:30:00" {
		t.Errorf("Day/Time = (%q, %q), want (2026-02-01, 08:30:00)", logged.Day, logged.Time)
	}
}

func TestDiaryFiltersToServingsOnly(t *testing.T) {
	transport := &pathTransport{responses: map[string]string{
		"/api/v2/login":     fixtureLogin,
		"/api/v2/get_diary": fixtureDiary, // 1 Serving, 2 Exercise, 2 Biometric
		"/api/v2/get_foods": fixtureFoods,
	}}
	s := newTestSyncer(t, transport)

	servings, err := s.Diary(context.Background(), "2026-01-15")
	if err != nil {
		t.Fatalf("Diary: %v", err)
	}
	if len(servings) != 1 {
		t.Fatalf("len(servings) = %d, want 1 (Exercise/Biometric entries must be excluded)", len(servings))
	}
	if servings[0].FoodName != "Test Oatmeal" {
		t.Errorf("FoodName = %q, want Test Oatmeal", servings[0].FoodName)
	}
}

// deleteTransport serves get_diary (POST, DeleteServing/FindDiaryEntryRaw's
// lookup step) and asserts+answers the delete_entries DELETE request —
// regression coverage for a real live bug (2026-08-05): the original
// implementation sent a hand-reconstructed {servingId, foodId, measureId,
// grams} object, which Cronometer's real API rejected with "Not able to
// deserialize data provided"; the fix re-fetches and forwards the diary
// entry's own exact raw JSON instead (see FindDiaryEntryRaw/DeleteEntries's
// doc comments) and adds the v3-specific headers the reference
// implementation sends that the original code was missing.
type deleteTransport struct {
	deleteCalls   int
	deleteBody    []byte
	deleteHeaders http.Header
}

func (t *deleteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodDelete {
		t.deleteCalls++
		t.deleteHeaders = req.Header
		if req.Body != nil {
			t.deleteBody, _ = io.ReadAll(req.Body)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}, nil
	}
	// get_diary (POST /api/v2/get_diary) — served from the shared fixtureDiary,
	// whose one Serving entry has servingId 1001 (see sync_test.go).
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixtureDiary)), Header: make(http.Header), Request: req}, nil
}

func TestDeleteServing(t *testing.T) {
	transport := &deleteTransport{}
	s := newTestSyncer(t, transport)
	s.session = &Session{UserID: 42, Token: "tok-abc"} // DeleteEntries needs a session already loaded; it doesn't go through withRetry's lazy login

	err := s.DeleteServing(context.Background(), "2026-01-15", 1001)
	if err != nil {
		t.Fatalf("DeleteServing: %v", err)
	}
	if transport.deleteCalls != 1 {
		t.Errorf("delete calls = %d, want 1", transport.deleteCalls)
	}

	// The request body must be the diary entry's own raw shape, not a
	// hand-reconstructed subset — assert on the wrapper and a couple of
	// fields get_diary's fixture entry has that the old minimal object
	// didn't (e.g. "day"), rather than a brittle full-string comparison.
	body := string(transport.deleteBody)
	for _, want := range []string{`"diaryEntries"`, `"servingId":1001`, `"day":"2026-01-15"`, `"time":"08:00:00"`} {
		if !strings.Contains(body, want) {
			t.Errorf("delete request body = %s, missing %q", body, want)
		}
	}

	if got := transport.deleteHeaders.Get("x-crono-app-os"); got != "android" {
		t.Errorf("x-crono-app-os header = %q, want \"android\"", got)
	}
	if got := transport.deleteHeaders.Get("content-type"); got != "application/json; charset=utf-8" {
		t.Errorf("content-type header = %q, want \"application/json; charset=utf-8\"", got)
	}
}

func TestDeleteServingErrorsWhenServingNotFoundInDiary(t *testing.T) {
	transport := &deleteTransport{}
	s := newTestSyncer(t, transport)
	s.session = &Session{UserID: 42, Token: "tok-abc"}

	err := s.DeleteServing(context.Background(), "2026-01-15", 999999)
	if err == nil {
		t.Fatal("DeleteServing with an unknown serving id: expected an error, got nil")
	}
	if transport.deleteCalls != 0 {
		t.Errorf("delete calls = %d, want 0 (should never reach the delete step without finding the entry first)", transport.deleteCalls)
	}
}
