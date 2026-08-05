// Package mcpserver exposes internal/cronometer's food search/logging
// actions as MCP tools over a local stdio transport — thin glue only, no
// business logic (the same role internal/web already plays over
// internal/cronometer/internal/googlehealth, just for an MCP client instead
// of a browser). See ARCHITECTURE.md's MCP connector section for why this
// exists and why it deliberately never calls an LLM itself: the calling
// Claude session already does all the food-identification/macro-estimation
// reasoning, using whatever chat subscription is already paid for — this
// package's only job is grounded search + authenticated writes against the
// real Cronometer account.
package mcpserver

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sdhungan/Personal-Health-Data/internal/cronometer"
)

// consumerInstructions is the server-level policy every cronometer_* tool's
// own description reinforces — surfaced to the connecting MCP client (e.g.
// Claude) as usage guidance for this whole tool set, not just one tool.
// This exists specifically to stop a described *dish* ("pasta with
// homemade arrabiata sauce and chicken/pork mince sausage") from being
// decomposed into its parts and searched/logged one ingredient at a time —
// the earlier behavior this replaces — in favor of one estimated custom
// food for the whole dish, confirmed with the user first. cronometer_search_food
// still comes first, but only for a single specific/branded item likely to
// already exist verbatim in Cronometer's database (a "Maltesers", a named
// branded product, a plain single ingredient like "a banana") — not as a
// per-ingredient decomposition step for a described meal.
const consumerInstructions = `Two different situations call for two different tools, and mixing them up is the most common mistake:

1. A SINGLE, SPECIFIC, LIKELY-BRANDED OR WELL-KNOWN ITEM ("a banana", "Maltesers", "a can of Coke", "a Snickers bar"):
   Call cronometer_search_food first. Prefer a real database match over cronometer_create_custom_food — these almost
   always already exist in Cronometer's database verbatim.

2. A DESCRIBED DISH OR MEAL (anything with more than one component, or a photo of a plated meal —
   "pasta with homemade arrabiata sauce and chicken/pork mince sausage", "chicken stir-fry with rice"):
   Do NOT decompose it into ingredients and search/log each one separately — that is the wrong approach even though
   cronometer_search_food would technically return matches for "pasta" or "sausage" individually. Instead:
     a. First restate what you understood (the ingredients/components you're seeing or were told) back to the user
        and wait for their confirmation or correction — especially important when working from a photo, since your
        read of what's actually in the dish is a guess until they confirm it. Do not call any Cronometer write tool
        (cronometer_create_custom_food or cronometer_log_serving) before this confirmation.
     b. Once confirmed, estimate the dish's nutrition yourself, as one whole item — you are the one doing the
        estimation here, not Cronometer's database.
     c. Call cronometer_create_custom_food exactly once, with a clear, specific name for the whole dish (e.g. "Pasta
        with homemade arrabiata sauce and chicken/pork mince sausage"), not once per ingredient.
     d. Then log it with cronometer_log_serving.
   The end result should be one diary entry for the dish, not several entries for its ingredients.`

// New builds an MCP server exposing every cronometer_* tool, bound to one
// already-constructed syncer (see internal/cli/mcp.go — one DBSyncer per
// process, scoped to a single --user). Tool names are prefixed
// "cronometer_" since this process will very plausibly run alongside other
// MCP servers in the same Claude Code/Desktop session.
func New(syncer *cronometer.DBSyncer) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "healthd-cronometer", Version: "0.1.0"}, &mcp.ServerOptions{
		Instructions: consumerInstructions,
	})
	r := &registrar{syncer: syncer}

	mcp.AddTool(s, &mcp.Tool{
		Name: "cronometer_search_food",
		Description: "Search Cronometer's own food database by name — for a SINGLE, SPECIFIC, likely-branded or " +
			"well-known item only (a named product, a plain single ingredient). Do NOT use this to decompose a " +
			"described dish/meal into its ingredients and search each one — see this server's own instructions for " +
			"the dish-vs-single-item policy; a multi-component dish should become one cronometer_create_custom_food " +
			"call instead, never several cronometer_search_food + cronometer_log_serving calls per ingredient. " +
			"Returns up to `limit` candidates with per-100g nutrients and available measures so you can judge the " +
			"best nutritional match and compute the right gram amount.",
	}, r.searchFood)

	mcp.AddTool(s, &mcp.Tool{
		Name: "cronometer_log_serving",
		Description: "Log a serving of a food already identified via cronometer_search_food (or " +
			"cronometer_create_custom_food) to the Cronometer diary. For a described dish (as opposed to a single " +
			"item matched via search), only call this after the user has confirmed the ingredients/description you " +
			"understood and after you've already created one custom food for the whole dish — never before that " +
			"confirmation, and never once per ingredient. grams is the actual amount consumed — the measure is " +
			"mostly cosmetic bookkeeping, not what controls the logged quantity. day/time default to right now in " +
			"the account's own Cronometer timezone if omitted; meal (breakfast/lunch/dinner/snack) can be given " +
			"instead of an explicit time to pick a sensible default — ask the user which meal this was if it's not " +
			"obvious from context. Also refreshes the healthd dashboard's local mirror for that day, so it shows up " +
			"there without a manual sync.",
	}, r.logServing)

	mcp.AddTool(s, &mcp.Tool{
		Name: "cronometer_create_custom_food",
		Description: "Defines a new food in the account's own Cronometer database with the nutrient values you " +
			"estimate (per 100g of the food), then returns its food_id/measure_id so you can log it via " +
			"cronometer_log_serving exactly like a real database match. This is the PRIMARY tool for a described " +
			"dish/meal with more than one component — name it for the whole dish (e.g. \"Pasta with homemade " +
			"arrabiata sauce and chicken/pork mince sausage\"), called exactly once per dish, not once per " +
			"ingredient — see this server's own instructions. Only call this after the user has confirmed the " +
			"ingredients/description you understood, particularly when working from a photo. Also the fallback for " +
			"a single item cronometer_search_food found no reasonable match for. Don't use this for a common " +
			"single item that almost certainly already exists in Cronometer's database — search for that instead.",
	}, r.createCustomFood)

	mcp.AddTool(s, &mcp.Tool{
		Name: "cronometer_get_diary",
		Description: "Fetches everything already logged as food in Cronometer's diary for a day (defaults to " +
			"today in the account's own timezone), straight from Cronometer's live API. Use this before logging " +
			"something to check for likely duplicates, or to answer 'what have I eaten today/on a given day' — it " +
			"reflects entries logged seconds ago via cronometer_log_serving in this same conversation.",
	}, r.getDiary)

	mcp.AddTool(s, &mcp.Tool{
		Name: "cronometer_delete_serving",
		Description: "Removes one diary entry previously logged via cronometer_log_serving — use for correcting a " +
			"mistaken log ('undo that', 'I meant the other one'), not for arbitrary diary cleanup. Needs the " +
			"serving_id/food_id/measure_id/grams/day cronometer_log_serving returned when the entry was created. " +
			"Also refreshes the healthd dashboard's local mirror for that day.",
	}, r.deleteServing)

	return s
}

// registrar holds the one syncer this process's tools all act through.
// DBSyncer's session/metrics fields aren't safe for concurrent use (see
// ARCHITECTURE.md's MCP connector section) — an MCP client can legitimately
// fire concurrent tool calls, so every call is serialized here rather than
// inside cronometer itself, keeping the sync path's existing
// single-goroutine contract untouched.
type registrar struct {
	mu     sync.Mutex
	syncer *cronometer.DBSyncer
}

type nutrients struct {
	EnergyKcal    *float64 `json:"energy_kcal,omitempty" jsonschema:"energy in kcal per 100g"`
	ProteinG      *float64 `json:"protein_g,omitempty" jsonschema:"protein in grams per 100g"`
	CarbsG        *float64 `json:"carbs_g,omitempty" jsonschema:"total carbohydrate in grams per 100g"`
	FatG          *float64 `json:"fat_g,omitempty" jsonschema:"total fat in grams per 100g"`
	FiberG        *float64 `json:"fiber_g,omitempty" jsonschema:"dietary fiber in grams per 100g"`
	SugarsG       *float64 `json:"sugars_g,omitempty" jsonschema:"sugars in grams per 100g"`
	SaturatedG    *float64 `json:"saturated_g,omitempty" jsonschema:"saturated fat in grams per 100g"`
	SodiumMg      *float64 `json:"sodium_mg,omitempty" jsonschema:"sodium in milligrams per 100g"`
	CholesterolMg *float64 `json:"cholesterol_mg,omitempty" jsonschema:"cholesterol in milligrams per 100g"`
}

func nutrientsFromProfile(p cronometer.NutrientProfile) nutrients {
	return nutrients{
		EnergyKcal: p.EnergyKcal, ProteinG: p.ProteinG, CarbsG: p.CarbsG, FatG: p.FatG,
		FiberG: p.FiberG, SugarsG: p.SugarsG, SaturatedG: p.SaturatedG,
		SodiumMg: p.SodiumMg, CholesterolMg: p.CholesterolMg,
	}
}

func (n nutrients) toProfile() cronometer.NutrientProfile {
	return cronometer.NutrientProfile{
		EnergyKcal: n.EnergyKcal, ProteinG: n.ProteinG, CarbsG: n.CarbsG, FatG: n.FatG,
		FiberG: n.FiberG, SugarsG: n.SugarsG, SaturatedG: n.SaturatedG,
		SodiumMg: n.SodiumMg, CholesterolMg: n.CholesterolMg,
	}
}

type measureOut struct {
	MeasureID int64   `json:"measure_id"`
	Name      string  `json:"name"`
	Grams     float64 `json:"grams"`
}

func measuresOut(ms []cronometer.FoodMeasure) []measureOut {
	out := make([]measureOut, len(ms))
	for i, m := range ms {
		out[i] = measureOut{MeasureID: m.ID, Name: m.Name, Grams: m.Value}
	}
	return out
}

type servingOut struct {
	ServingID int64     `json:"serving_id"`
	FoodName  string    `json:"food_name"`
	Grams     float64   `json:"grams"`
	Day       string    `json:"day"`
	Time      string    `json:"time"`
	Nutrients nutrients `json:"nutrients"`
}

func servingOutFrom(l cronometer.LoggedServing) servingOut {
	return servingOut{
		ServingID: l.ServingID, FoodName: l.FoodName, Grams: l.Grams,
		Day: l.Day, Time: l.Time, Nutrients: nutrientsFromProfile(l.Nutrients),
	}
}

// ---- cronometer_search_food ----

type searchFoodIn struct {
	Query string `json:"query" jsonschema:"food name to search Cronometer's database for"`
	Limit int    `json:"limit,omitempty" jsonschema:"max candidates to return (default/cap 8)"`
}

type foodCandidateOut struct {
	FoodID   int64        `json:"food_id"`
	Name     string       `json:"name"`
	Measures []measureOut `json:"measures"`
	Per100g  nutrients    `json:"per_100g"`
}

type searchFoodOut struct {
	Candidates []foodCandidateOut `json:"candidates"`
}

func (r *registrar) searchFood(ctx context.Context, _ *mcp.CallToolRequest, in searchFoodIn) (*mcp.CallToolResult, searchFoodOut, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	candidates, err := r.syncer.SearchFood(ctx, in.Query, in.Limit)
	if err != nil {
		return nil, searchFoodOut{}, fmt.Errorf("cronometer_search_food: %w", err)
	}

	out := searchFoodOut{Candidates: make([]foodCandidateOut, len(candidates))}
	for i, c := range candidates {
		out.Candidates[i] = foodCandidateOut{
			FoodID: c.FoodID, Name: c.Name,
			Measures: measuresOut(c.Measures), Per100g: nutrientsFromProfile(c.Per100g),
		}
	}
	return nil, out, nil
}

// ---- cronometer_log_serving ----

type logServingIn struct {
	FoodID    int64   `json:"food_id" jsonschema:"the food_id from cronometer_search_food or cronometer_create_custom_food"`
	MeasureID int64   `json:"measure_id,omitempty" jsonschema:"one of the measure_ids the food's search/create result listed; defaults to the food's first measure if omitted"`
	Grams     float64 `json:"grams" jsonschema:"actual amount consumed, in grams"`
	Day       string  `json:"day,omitempty" jsonschema:"YYYY-MM-DD; defaults to today in the account's own timezone"`
	Time      string  `json:"time,omitempty" jsonschema:"HH:MM:SS; defaults to right now in the account's own timezone. Takes priority over meal if both are given."`
	Meal      string  `json:"meal,omitempty" jsonschema:"one of breakfast, lunch, dinner, snack — used only to pick a sensible default time when time isn't given. Cronometer buckets diary entries into meal groups purely by time of day against the account's own settings, not an explicit per-entry field, so this is a best-effort default (Cronometer's own stock boundaries), not a guarantee for an account with customized meal times. Prefer asking the user which meal this was rather than guessing."`
}

// mealDefaultTimes maps a casual meal name to a default HH:MM:SS, used only
// when the caller didn't give an explicit time — see logServingIn.Meal's
// jsonschema description for why this is a best-effort default rather than
// something add_serving itself understands.
var mealDefaultTimes = map[string]string{
	"breakfast": "08:00:00",
	"lunch":     "12:30:00",
	"dinner":    "19:00:00",
	"snack":     "15:30:00",
}

func (r *registrar) logServing(ctx context.Context, _ *mcp.CallToolRequest, in logServingIn) (*mcp.CallToolResult, servingOut, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	timeStr := in.Time
	if timeStr == "" {
		if t, ok := mealDefaultTimes[strings.ToLower(in.Meal)]; ok {
			timeStr = t
		}
	}

	// MeasureID==0 means "use the food's first measure" — resolved inside
	// DBSyncer.LogServing itself (it already fetches the food), not here.
	logged, err := r.syncer.LogServing(ctx, in.FoodID, in.MeasureID, in.Grams, in.Day, timeStr)
	if err != nil {
		return nil, servingOut{}, fmt.Errorf("cronometer_log_serving: %w", err)
	}
	r.syncDashboard(ctx, logged.Day)
	return nil, servingOutFrom(logged), nil
}

// ---- cronometer_create_custom_food ----

type createCustomFoodIn struct {
	Name string `json:"name" jsonschema:"a clear, specific name for the new food"`
	nutrients
}

type createCustomFoodOut struct {
	FoodID    int64  `json:"food_id"`
	MeasureID int64  `json:"measure_id"`
	Name      string `json:"name"`
}

func (r *registrar) createCustomFood(ctx context.Context, _ *mcp.CallToolRequest, in createCustomFoodIn) (*mcp.CallToolResult, createCustomFoodOut, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	foodID, measureID, err := r.syncer.CreateCustomFood(ctx, in.Name, in.nutrients.toProfile())
	if err != nil {
		return nil, createCustomFoodOut{}, fmt.Errorf("cronometer_create_custom_food: %w", err)
	}
	return nil, createCustomFoodOut{FoodID: foodID, MeasureID: measureID, Name: in.Name}, nil
}

// ---- cronometer_get_diary ----

type getDiaryIn struct {
	Day string `json:"day,omitempty" jsonschema:"YYYY-MM-DD; defaults to today in the account's own timezone"`
}

type getDiaryOut struct {
	Day      string       `json:"day"`
	Servings []servingOut `json:"servings"`
}

func (r *registrar) getDiary(ctx context.Context, _ *mcp.CallToolRequest, in getDiaryIn) (*mcp.CallToolResult, getDiaryOut, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	servings, err := r.syncer.Diary(ctx, in.Day)
	if err != nil {
		return nil, getDiaryOut{}, fmt.Errorf("cronometer_get_diary: %w", err)
	}

	out := getDiaryOut{Day: in.Day, Servings: make([]servingOut, len(servings))}
	for i, s := range servings {
		out.Servings[i] = servingOutFrom(s)
		if out.Day == "" {
			out.Day = s.Day
		}
	}
	return nil, out, nil
}

// ---- cronometer_delete_serving ----

type deleteServingIn struct {
	ServingID int64   `json:"serving_id" jsonschema:"the serving_id cronometer_log_serving returned"`
	FoodID    int64   `json:"food_id" jsonschema:"the food_id cronometer_log_serving was called with"`
	MeasureID int64   `json:"measure_id" jsonschema:"the measure_id cronometer_log_serving was called with"`
	Grams     float64 `json:"grams" jsonschema:"the grams cronometer_log_serving was called with"`
	Day       string  `json:"day" jsonschema:"the day cronometer_log_serving returned (YYYY-MM-DD) — needed to refresh the local dashboard mirror for the right day after deleting"`
}

type deleteServingOut struct {
	Deleted bool `json:"deleted"`
}

func (r *registrar) deleteServing(ctx context.Context, _ *mcp.CallToolRequest, in deleteServingIn) (*mcp.CallToolResult, deleteServingOut, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	err := r.syncer.DeleteServing(ctx, cronometer.DiaryEntryRef{
		ServingID: in.ServingID, FoodID: in.FoodID, MeasureID: in.MeasureID, Grams: in.Grams,
	})
	if err != nil {
		return nil, deleteServingOut{}, fmt.Errorf("cronometer_delete_serving: %w", err)
	}
	r.syncDashboard(ctx, in.Day)
	return nil, deleteServingOut{Deleted: true}, nil
}

// syncDashboard best-effort refreshes the local cronometer_serving/
// cronometer_daily_nutrition mirror for day right after a write, so the
// healthd dashboard reflects a food logged/deleted through this connector
// without waiting for the next scheduled sync (which only runs while the
// separate `healthd` scheduler process happens to be running, at whatever
// interval config.yaml sets — potentially tens of minutes away). This is
// Cronometer-only, matching what actually changed — never touches Google
// Health. A sync failure here is logged, not returned as a tool error: the
// write to Cronometer itself already succeeded, which is the part that
// actually matters; a stale local mirror is a lesser, self-healing problem
// (the next real sync corrects it) and shouldn't make Claude think the food
// wasn't logged.
func (r *registrar) syncDashboard(ctx context.Context, day string) {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: write to Cronometer succeeded but couldn't parse day %q to refresh the dashboard mirror: %v\n", day, err)
		return
	}
	if _, err := r.syncer.SyncDay(ctx, t); err != nil {
		fmt.Fprintf(os.Stderr, "warning: writing to Cronometer succeeded but refreshing the local dashboard mirror for %s failed: %v\n", day, err)
	}
}
