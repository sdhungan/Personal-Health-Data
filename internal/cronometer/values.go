package cronometer

// Typed values for the Cronometer mobile API's response shapes. Everything
// in this file is CONFIRMED — decoded from a real response against a live
// account on 2026-07-31 (see cmd/cronodump and bin/cronometer-dump), not
// guessed from the reference client's README. Fields the dump showed but
// this package has no use for are deliberately omitted (e.g. diary
// "messages"/"fasts", food "barcodes"/"foodTags").
//
// One exception: no "Note" diary entry type was observed in the dump — the
// day sampled simply may not have had one logged. Whether Cronometer's
// notes feature is reachable via this API at all is still unconfirmed;
// cronometer_note stays unpopulated until that's settled against a real
// account that has a note logged on some day.

// ---- get_diary ----

type DiaryResponse struct {
	Summary DiarySummary `json:"summary"`
	Diary   []DiaryEntry `json:"diary"`
	Info    DiaryInfo    `json:"info"`
}

// DiarySummary.Burned.Total is Cronometer's own total daily-expenditure
// estimate (BMR + tracked activity + food thermic effect) — confirmed by
// arithmetic against a real response (bmr_kcal + tracker_activity_kcal +
// tef_kcal + exercise_kcal summed to burned.total exactly). This is the
// figure cronometer_daily_nutrition.kcal_burned_cronometer wants.
type DiarySummary struct {
	Burned DiaryBurned `json:"burned"`
}

type DiaryBurned struct {
	Total float64 `json:"total"`
}

// DiaryInfo.Complete mirrors cronometer_daily_nutrition.completed exactly
// (schema comment: "mirrors Cronometer's own 'diary day marked complete'
// flag").
type DiaryInfo struct {
	Complete bool   `json:"complete"`
	Day      string `json:"day"`
}

// DiaryEntry is deliberately flat across all three observed Type values
// ("Serving", "Exercise", "Biometric") rather than three separate structs —
// their fields don't collide, and callers already switch on Type before
// reading the type-specific fields below.
type DiaryEntry struct {
	Type   string `json:"type"`
	Day    string `json:"day"`
	Time   string `json:"time"`   // "HH:MM:SS", absent on some entries
	Source string `json:"source"` // e.g. "Fitbit" for auto-synced entries; empty for hand-entered ones

	// Serving
	ServingID     int64   `json:"servingId"`
	FoodID        int64   `json:"foodId"`
	MeasureID     int64   `json:"measureId"`
	Grams         float64 `json:"grams"`
	TranslationID int64   `json:"translationId"`

	// Exercise. Calories is observed negative (a budget deduction); take
	// its absolute value when storing into calories_burned.
	ExerciseID int64   `json:"exerciseId"`
	Name       string  `json:"name"`
	Minutes    float64 `json:"minutes"`
	Calories   float64 `json:"calories"`

	// Biometric. MetricID/UnitID are looked up against GetMetrics' catalog
	// to resolve human-readable metric/unit names.
	BiometricID int64   `json:"biometricId"`
	MetricID    int     `json:"metricId"`
	UnitID      int     `json:"unitId"`
	Amount      float64 `json:"amount"`
}

// ---- get_foods ----

type Food struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Source    string         `json:"source"`
	Measures  []FoodMeasure  `json:"measures"`
	Nutrients []FoodNutrient `json:"nutrients"` // amounts are per 100g
}

type FoodMeasure struct {
	ID    int64   `json:"id"`
	Name  string  `json:"name"`
	Value float64 `json:"value"` // grams per one of this measure
}

type FoodNutrient struct {
	ID     int     `json:"id"`
	Amount float64 `json:"amount"`
}

// ---- get_nutrition_scores ----

type NutritionScoresResponse struct {
	Scores []ScoreCategory `json:"scores"`
}

type ScoreCategory struct {
	Title      string           `json:"title"`
	Components []ScoreComponent `json:"components"`
}

// ScoreComponent.NutrientID may be negative (e.g. -1205 for Net Carbs) —
// same convention the reference client's NUTRIENT_IDS map uses.
type ScoreComponent struct {
	NutrientID int     `json:"nutrientId"`
	Amount     float64 `json:"amount"`
}

// AllTargetsComponents returns the "All Targets" category's components —
// every nutrient the account currently tracks, each with its consumed
// amount for the day the scores were fetched for. A nutrient the account
// doesn't track (no target set) simply doesn't appear here.
func (r *NutritionScoresResponse) AllTargetsComponents() []ScoreComponent {
	for _, c := range r.Scores {
		if c.Title == "All Targets" {
			return c.Components
		}
	}
	return nil
}

// ---- Write endpoints (find_food, add_food, add_serving, delete_entries) ----
//
// PARTIALLY CONFIRMED against a real account on 2026-08-05 (via
// cmd/cronoverify) — find_food (this struct, below), add_food, and
// get_foods-immediately-after-add_food all round-tripped clean. add_serving
// initially failed to decode (the DOCUMENTED guess of a string-typed
// response "id" was wrong — it's a JSON number, see client.go's AddServing
// doc comment); that's now fixed but not yet re-verified end to end.
// delete_entries has never actually been exercised (the verification run
// that would have reached it failed one step earlier, at add_serving's
// decode error) — treat DeleteEntries/DiaryEntryRef as still DOCUMENTED,
// not CONFIRMED, until a clean cmd/cronoverify pass reaches it.
//
// Originally transcribed from rwestergren/cronometer-api-mcp (the same
// reference project this package's read side was reverse-engineered from —
// see client.go's package comment).

type FoodSearchResult struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	MeasureID     int64  `json:"measureId"`
	TranslationID int64  `json:"translationId"`
}

// ---- get_metrics ----

type Metric struct {
	ID    int          `json:"id"`
	Name  string       `json:"name"`
	Units []MetricUnit `json:"units"`
}

type MetricUnit struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// UnitName returns the display name for unitID within m's own unit list,
// or "" if m has no such unit.
func (m Metric) UnitName(unitID int) string {
	for _, u := range m.Units {
		if u.ID == unitID {
			return u.Name
		}
	}
	return ""
}
