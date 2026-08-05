// Command cronoverify is a throwaway diagnostic tool (ad hoc dev use only,
// same status as cmd/tmpinspect and cmd/cronodump — not part of the healthd
// product). It round-trips every write endpoint in internal/cronometer's
// Client (FindFood, CreateCustomFood, AddServing, DeleteEntries) against a
// real Cronometer account, since values.go marks them "PARTIALLY CONFIRMED"
// (find_food/add_food/get_foods confirmed 2026-08-05; add_serving's
// response type bug found and fixed the same day; delete_entries still
// unconfirmed — see values.go's own doc comment).
//
// Unlike cronodump (which deliberately re-implements raw requests to probe
// JSON shapes before values.go existed), this tool imports and calls
// internal/cronometer.Client's actual typed methods directly, since the
// point here is confirming those methods work as coded.
//
// Credentials are entered interactively at this program's own stdin/stdout
// — never passed as a flag, env var, or logged. Nothing is persisted to
// disk; this is a one-shot verification, not a session cache like
// cronodump's.
//
// It creates one throwaway custom food + serving entry in the real account
// and deletes both before exiting. On any failure after the food is
// created, it prints the food/serving IDs prominently so they can be
// removed by hand from the Cronometer app if automatic cleanup didn't run
// (add_food/find_food have no endpoint to remove a *food* itself, only
// diary *entries* — the throwaway custom food always needs manual removal
// from the Cronometer app/website, delete_entries or not).
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/sdhungan/Personal-Health-Data/internal/cronometer"
)

func main() {
	query := flag.String("query", "banana", "food name to search for via find_food")
	flag.Parse()

	if err := run(context.Background(), *query); err != nil {
		fmt.Fprintln(os.Stderr, "FAIL:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, query string) error {
	client := cronometer.NewClient()

	fmt.Print("Cronometer email: ")
	reader := bufio.NewReader(os.Stdin)
	email, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("reading email: %w", err)
	}
	email = strings.TrimSpace(email)

	fmt.Print("Cronometer password (hidden): ")
	pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("reading password: %w", err)
	}

	sess, err := client.Login(ctx, email, string(pwBytes))
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	fmt.Printf("PASS  login (user_id=%d, timezone=%s)\n", sess.UserID, sess.Timezone)

	// ---- find_food ----
	results, err := client.FindFood(ctx, sess, query)
	if err != nil {
		return fmt.Errorf("find_food: %w", err)
	}
	if len(results) == 0 || results[0].ID == 0 {
		return fmt.Errorf("find_food returned no usable results for %q — FoodSearchResult's fields may be wrong: %+v", query, results)
	}
	fmt.Printf("PASS  find_food (%d results, first: id=%d name=%q measureId=%d)\n", len(results), results[0].ID, results[0].Name, results[0].MeasureID)

	// ---- add_food (throwaway custom food) ----
	foodName := fmt.Sprintf("healthd-mcp-verify-%d", time.Now().Unix())
	nutrients := []cronometer.FoodNutrient{
		{ID: 208, Amount: 100}, // energy_kcal
		{ID: 203, Amount: 5},   // protein_g
	}
	foodID, err := client.CreateCustomFood(ctx, sess, foodName, "g", 1.0, nutrients)
	if err != nil {
		return fmt.Errorf("add_food: %w", err)
	}
	if foodID == 0 {
		return fmt.Errorf("add_food returned id=0 with no error — treating as a failure, not a pass")
	}
	fmt.Printf("PASS  add_food (created %q, id=%d)\n", foodName, foodID)

	// From here on, a failure should print manual-cleanup instructions,
	// since the food now exists in the real account whether or not the
	// rest of this verification succeeds. servingID==0 means "nothing to
	// delete_entries yet" (a real serving id is never 0).
	cleanup := func(servingID, measureID int64, grams float64) {
		if servingID == 0 {
			return
		}
		if err := client.DeleteEntries(ctx, sess, []cronometer.DiaryEntryRef{
			{ServingID: servingID, FoodID: foodID, MeasureID: measureID, Grams: grams},
		}); err != nil {
			fmt.Printf("MANUAL CLEANUP NEEDED: failed to delete serving %d (food id %d, %q): %v\n", servingID, foodID, foodName, err)
			return
		}
		fmt.Printf("PASS  delete_entries (removed serving %d)\n", servingID)
	}

	// ---- get_foods (resolve the new food's measure ID) ----
	foods, err := client.GetFoods(ctx, sess, []int64{foodID})
	if err != nil {
		return fmt.Errorf("get_foods on new food %d: %w — MANUAL CLEANUP NEEDED for food id %d (%q)", foodID, err, foodID, foodName)
	}
	if len(foods) == 0 || len(foods[0].Measures) == 0 {
		return fmt.Errorf("get_foods returned no usable measure for new food %d — MANUAL CLEANUP NEEDED for food id %d (%q)", foodID, foodID, foodName)
	}
	measureID := foods[0].Measures[0].ID
	fmt.Printf("PASS  get_foods on new food (measureId=%d resolved immediately, no propagation delay observed)\n", measureID)

	// ---- add_serving ----
	// Zero-padded day format (dateLayout in sync.go, same as GetDiary) —
	// CONFIRMED 2026-08-05 to be accepted; see ServingEntry.Day's doc
	// comment for why the originally-DOCUMENTED non-padded claim was wrong.
	now := time.Now()
	day := now.Format("2006-01-02")
	timeStr := now.Format("15:04:05")
	servingID, err := client.AddServing(ctx, sess, cronometer.ServingEntry{
		Day: day, Time: timeStr, FoodID: foodID, MeasureID: measureID, Grams: 100,
	})
	if err != nil {
		return fmt.Errorf("add_serving: %w — MANUAL CLEANUP NEEDED for food id %d (%q); it may have logged despite this error, check your diary for %s", err, foodID, foodName, day)
	}
	if servingID == 0 {
		return fmt.Errorf("add_serving returned serving id 0 — MANUAL CLEANUP NEEDED for food id %d (%q); it may have logged despite this, check your diary for %s", foodID, foodName, day)
	}
	fmt.Printf("PASS  add_serving (day %q; serving id=%d)\n", day, servingID)

	// ---- get_diary (confirm the serving is actually visible) ----
	diary, err := client.GetDiary(ctx, sess, day)
	if err != nil {
		cleanup(servingID, measureID, 100)
		return fmt.Errorf("get_diary after add_serving: %w", err)
	}
	found := false
	for _, e := range diary.Diary {
		if e.Type == "Serving" && e.FoodID == foodID {
			found = true
			break
		}
	}
	if !found {
		cleanup(servingID, measureID, 100)
		return fmt.Errorf("logged serving for food %d not found in get_diary for %s — add_serving may not have actually persisted", foodID, day)
	}
	fmt.Println("PASS  get_diary shows the logged serving")

	// ---- delete_entries (cleanup) ----
	cleanup(servingID, measureID, 100)

	// ---- get_diary again (confirm removal) ----
	diary2, err := client.GetDiary(ctx, sess, day)
	if err != nil {
		return fmt.Errorf("get_diary after delete_entries: %w", err)
	}
	for _, e := range diary2.Diary {
		if e.Type == "Serving" && e.FoodID == foodID {
			return fmt.Errorf("serving for food %d still present after delete_entries — MANUAL CLEANUP NEEDED for food id %d (%q)", foodID, foodID, foodName)
		}
	}
	fmt.Println("PASS  get_diary confirms the serving is gone")

	fmt.Println()
	fmt.Println("All write endpoints verified against the real account. Remaining manual step: delete the throwaway custom food")
	fmt.Printf("(%q, id=%d) from Cronometer's food database — add_food/find_food have no endpoint to remove a *food* itself, only diary *entries*.\n", foodName, foodID)
	return nil
}
