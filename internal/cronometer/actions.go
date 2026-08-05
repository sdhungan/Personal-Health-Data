package cronometer

import (
	"context"
	"fmt"
	"time"
)

// maxFoodSearchResults caps how many find_food candidates get resolved to
// full nutrient profiles (via GetFoods) and returned — keeps the payload
// something an LLM caller can reason over in one turn, and avoids a wasted
// GetFoods round trip for matches nobody will look at anyway.
const maxFoodSearchResults = 8

// NutrientProfile is the nutrition-label subset of Cronometer's ~64-column
// nutrient dictionary (see nutrients.go's NutritionAmounts for the full
// set) — the same handful internal/web/foodlog.go's FoodServingDetail
// already surfaces to a human on the click-to-detail popup. Used both to
// summarize a food search candidate's or logged serving's nutrients, and to
// define a new custom food's profile. Pointer fields distinguish "not
// provided/not tracked" from "zero," same convention as NutritionAmounts.
type NutrientProfile struct {
	EnergyKcal    *float64
	ProteinG      *float64
	CarbsG        *float64
	FatG          *float64
	FiberG        *float64
	SugarsG       *float64
	SaturatedG    *float64
	SodiumMg      *float64
	CholesterolMg *float64
}

// nutrientProfileFromAmounts extracts NutrientProfile's subset out of a
// full NutritionAmounts (e.g. the result of nutritionAmountsFromFood).
func nutrientProfileFromAmounts(n *NutritionAmounts) NutrientProfile {
	return NutrientProfile{
		EnergyKcal:    n.EnergyKcal,
		ProteinG:      n.ProteinG,
		CarbsG:        n.CarbsG,
		FatG:          n.FatG,
		FiberG:        n.FiberG,
		SugarsG:       n.SugarsG,
		SaturatedG:    n.SaturatedG,
		SodiumMg:      n.SodiumMg,
		CholesterolMg: n.CholesterolMg,
	}
}

// toFoodNutrients converts p into the ID/amount pairs Client.CreateCustomFood
// expects, reusing the exact CONFIRMED nutrient IDs nutrients.go's
// nutritionSetters already maps (208 energy, 203 protein, 205 carbs, 204
// fat, 291 fiber, 269 sugars, 606 saturated, 307 sodium, 601 cholesterol —
// see that file), just in the reverse (name -> ID) direction. Fields left
// nil are omitted rather than sent as zero, so an unset field doesn't
// silently claim "confirmed zero" for that nutrient.
func (p NutrientProfile) toFoodNutrients() []FoodNutrient {
	var out []FoodNutrient
	add := func(id int, v *float64) {
		if v != nil {
			out = append(out, FoodNutrient{ID: id, Amount: *v})
		}
	}
	add(208, p.EnergyKcal)
	add(203, p.ProteinG)
	add(205, p.CarbsG)
	add(204, p.FatG)
	add(291, p.FiberG)
	add(269, p.SugarsG)
	add(606, p.SaturatedG)
	add(307, p.SodiumMg)
	add(601, p.CholesterolMg)
	return out
}

// FoodCandidate is one search_food result: enough for an LLM to judge
// nutritional fit and compute a gram amount from a described meal or photo,
// without dumping Cronometer's full ~64-column nutrient dictionary.
type FoodCandidate struct {
	FoodID   int64
	Name     string
	Measures []FoodMeasure
	Per100g  NutrientProfile
}

// SearchFood wraps FindFood + GetFoods (see client.go's FindFood doc
// comment: "resolve a candidate match here, then GetFoods for its full
// per-100g nutrient profile") — the primary path for logging a food, so
// most items match a real Cronometer food and never need CreateCustomFood.
// limit caps the number of candidates resolved to full profiles; <= 0 or >
// maxFoodSearchResults falls back to maxFoodSearchResults.
func (s *DBSyncer) SearchFood(ctx context.Context, query string, limit int) ([]FoodCandidate, error) {
	if limit <= 0 || limit > maxFoodSearchResults {
		limit = maxFoodSearchResults
	}

	results, err := withRetry(ctx, s, func(sess *Session) ([]FoodSearchResult, error) {
		return s.Client.FindFood(ctx, sess, query)
	})
	if err != nil {
		return nil, fmt.Errorf("searching Cronometer foods for %q: %w", query, err)
	}
	if len(results) > limit {
		results = results[:limit]
	}
	if len(results) == 0 {
		return nil, nil
	}

	ids := make([]int64, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}
	foods, err := withRetry(ctx, s, func(sess *Session) ([]Food, error) {
		return s.Client.GetFoods(ctx, sess, ids)
	})
	if err != nil {
		return nil, fmt.Errorf("resolving nutrient profiles for %q: %w", query, err)
	}
	byID := indexFoods(foods)

	candidates := make([]FoodCandidate, 0, len(results))
	for _, r := range results {
		food, ok := byID[r.ID]
		if !ok {
			// find_food returned an ID get_foods couldn't resolve (observed
			// with some non-database sources) — skip rather than return a
			// candidate with no nutrient data an LLM could act on.
			continue
		}
		candidates = append(candidates, FoodCandidate{
			FoodID:   food.ID,
			Name:     food.Name,
			Measures: food.Measures,
			Per100g:  nutrientProfileFromAmounts(nutritionAmountsFromFood(food.Nutrients, 100)),
		})
	}
	return candidates, nil
}

// CreateCustomFood defines a new food in the account's own Cronometer
// database from an estimated per-100g nutrient profile (fallback for when
// SearchFood finds no reasonable match), then immediately resolves its
// assigned measure ID via GetFoods — Client.CreateCustomFood's own response
// only returns a food ID, not a measure ID.
func (s *DBSyncer) CreateCustomFood(ctx context.Context, name string, per100g NutrientProfile) (foodID, measureID int64, err error) {
	nutrients := per100g.toFoodNutrients()

	foodID, err = withRetry(ctx, s, func(sess *Session) (int64, error) {
		return s.Client.CreateCustomFood(ctx, sess, name, "g", 1.0, nutrients)
	})
	if err != nil {
		return 0, 0, fmt.Errorf("creating custom food %q: %w", name, err)
	}

	foods, err := withRetry(ctx, s, func(sess *Session) ([]Food, error) {
		return s.Client.GetFoods(ctx, sess, []int64{foodID})
	})
	if err != nil {
		return foodID, 0, fmt.Errorf("resolving measure for new food %q (id %d): %w", name, foodID, err)
	}
	if len(foods) == 0 || len(foods[0].Measures) == 0 {
		return foodID, 0, fmt.Errorf("new food %q (id %d) has no resolvable measure", name, foodID)
	}
	return foodID, foods[0].Measures[0].ID, nil
}

// LoggedServing describes one Serving diary entry after logging or
// fetching it — echoes back what was actually recorded so a caller doesn't
// need a separate round trip to confirm.
type LoggedServing struct {
	ServingID int64
	FoodName  string
	Grams     float64
	Day       string
	Time      string
	Nutrients NutrientProfile // scaled to Grams, not per-100g
}

// LogServing wraps AddServing. day/timeStr default to "now" in the
// account's own Cronometer timezone (Session.Timezone, populated on
// login) — not the server process's local zone, since a diary day is
// inherently scoped to the account's own zone, not wherever this binary
// happens to be running. Also fetches the food's name and scales its
// per-100g nutrients to grams so the result can echo back what was
// actually logged.
func (s *DBSyncer) LogServing(ctx context.Context, foodID, measureID int64, grams float64, day, timeStr string) (LoggedServing, error) {
	if err := s.ensureSession(ctx); err != nil {
		return LoggedServing{}, err
	}

	loc := s.timezoneLocation()
	now := time.Now().In(loc)
	if day == "" {
		day = now.Format(dateLayout)
	}
	if timeStr == "" {
		timeStr = now.Format("15:04:05")
	}

	foods, err := withRetry(ctx, s, func(sess *Session) ([]Food, error) {
		return s.Client.GetFoods(ctx, sess, []int64{foodID})
	})
	if err != nil {
		return LoggedServing{}, fmt.Errorf("resolving food %d before logging: %w", foodID, err)
	}
	if len(foods) == 0 {
		return LoggedServing{}, fmt.Errorf("food %d not found", foodID)
	}
	food := foods[0]

	if measureID == 0 {
		if len(food.Measures) == 0 {
			return LoggedServing{}, fmt.Errorf("food %d (%q) has no measures to default to — pass measureID explicitly", foodID, food.Name)
		}
		measureID = food.Measures[0].ID
	}

	servingID, err := withRetry(ctx, s, func(sess *Session) (int64, error) {
		return s.Client.AddServing(ctx, sess, ServingEntry{
			Day: day, Time: timeStr, FoodID: foodID, MeasureID: measureID, Grams: grams,
		})
	})
	if err != nil {
		return LoggedServing{}, fmt.Errorf("logging %q: %w", food.Name, err)
	}

	return LoggedServing{
		ServingID: servingID,
		FoodName:  food.Name,
		Grams:     grams,
		Day:       day,
		Time:      timeStr,
		Nutrients: nutrientProfileFromAmounts(nutritionAmountsFromFood(food.Nutrients, grams)),
	}, nil
}

// Diary returns everything logged as food (Type=="Serving" only, matching
// buildFoodLogTile's existing scope in internal/web/foodlog.go) for day
// (defaults to today in the account's own timezone), resolved against
// Cronometer's LIVE API rather than the locally-synced cronometer_serving
// table — the local table only updates on the next scheduled sync (every
// ~30 min, and only while the separate `healthd` scheduler process happens
// to be running; `healthd mcp` doesn't sync anything itself), so a serving
// logged moments earlier in the same MCP session wouldn't appear yet,
// defeating the "check for duplicates before logging" use this exists for.
func (s *DBSyncer) Diary(ctx context.Context, day string) ([]LoggedServing, error) {
	if err := s.ensureSession(ctx); err != nil {
		return nil, err
	}
	if day == "" {
		day = time.Now().In(s.timezoneLocation()).Format(dateLayout)
	}

	diary, err := withRetry(ctx, s, func(sess *Session) (*DiaryResponse, error) {
		return s.Client.GetDiary(ctx, sess, day)
	})
	if err != nil {
		return nil, fmt.Errorf("fetching diary for %s: %w", day, err)
	}

	var servings []DiaryEntry
	for _, e := range diary.Diary {
		if e.Type == "Serving" {
			servings = append(servings, e)
		}
	}
	if len(servings) == 0 {
		return nil, nil
	}

	foods, err := withRetry(ctx, s, func(sess *Session) ([]Food, error) {
		return s.Client.GetFoods(ctx, sess, uniqueFoodIDs(servings))
	})
	if err != nil {
		return nil, fmt.Errorf("resolving foods for %s: %w", day, err)
	}
	byID := indexFoods(foods)

	out := make([]LoggedServing, 0, len(servings))
	for _, e := range servings {
		food, ok := byID[e.FoodID]
		name := "unknown food"
		var nutrients NutrientProfile
		if ok {
			name = food.Name
			nutrients = nutrientProfileFromAmounts(nutritionAmountsFromFood(food.Nutrients, e.Grams))
		}
		out = append(out, LoggedServing{
			ServingID: e.ServingID,
			FoodName:  name,
			Grams:     e.Grams,
			Day:       e.Day,
			Time:      e.Time,
			Nutrients: nutrients,
		})
	}
	return out, nil
}

// DeleteServing wraps DeleteEntries for one entry — an "undo last log"
// affordance for correcting a mistaken cronometer_log_serving call.
func (s *DBSyncer) DeleteServing(ctx context.Context, ref DiaryEntryRef) error {
	_, err := withRetry(ctx, s, func(sess *Session) (struct{}, error) {
		return struct{}{}, s.Client.DeleteEntries(ctx, sess, []DiaryEntryRef{ref})
	})
	if err != nil {
		return fmt.Errorf("deleting serving %d: %w", ref.ServingID, err)
	}
	return nil
}

// ensureSession logs in if s doesn't already have a cached session — needed
// before timezoneLocation can reflect the account's real zone rather than
// falling back to UTC, since Session.Timezone is only populated on login.
func (s *DBSyncer) ensureSession(ctx context.Context) error {
	if s.session != nil {
		return nil
	}
	return s.login(ctx)
}

// timezoneLocation resolves the account's own Cronometer timezone (cached
// on s.session after login) to a *time.Location for formatting "now" into
// a diary day/time — falls back to UTC if the session isn't loaded yet or
// the zone name isn't one Go's tzdata recognizes, rather than erroring a
// logging call over a cosmetic default.
func (s *DBSyncer) timezoneLocation() *time.Location {
	if s.session == nil || s.session.Timezone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(s.session.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}
