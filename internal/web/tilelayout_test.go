package web

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	_ "modernc.org/sqlite"

	dbpkg "github.com/sdhungan/Personal-Health-Data/internal/db"
)

// TestMoveTileTakesTargetSlotAndPersists checks the drag-and-drop rule
// (forward lands after the target, backward lands before it) and that
// size saves don't disturb the saved order.
func TestMoveTileTakesTargetSlotAndPersists(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1) // one :memory: database, not one per pooled connection
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Exec(dbpkg.Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	heart := func() []string {
		order, _, err := fetchTileLayout(ctx, conn, 1)
		if err != nil {
			t.Fatal(err)
		}
		return slices.DeleteFunc(order, func(k string) bool {
			return !slices.Contains([]string{"heart_rate", "resting_hr", "hrv"}, k)
		})
	}

	if err := saveTileSize(ctx, conn, 1, "hrv", tileSize{Width: 3, Height: 300}); err != nil {
		t.Fatal(err)
	}
	if err := moveTile(ctx, conn, 1, "hrv", "heart_rate"); err != nil { // backward: before target
		t.Fatal(err)
	}
	if got, want := heart(), []string{"hrv", "heart_rate", "resting_hr"}; !slices.Equal(got, want) {
		t.Errorf("after moving hrv onto heart_rate: got %v, want %v", got, want)
	}
	if err := moveTile(ctx, conn, 1, "hrv", "heart_rate"); err != nil { // forward: after target
		t.Fatal(err)
	}
	if got, want := heart(), []string{"heart_rate", "hrv", "resting_hr"}; !slices.Equal(got, want) {
		t.Errorf("after moving hrv back onto heart_rate: got %v, want %v", got, want)
	}

	_, sizes, err := fetchTileLayout(ctx, conn, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sizes["hrv"], (tileSize{Width: 3, Height: 300}); got != want {
		t.Errorf("hrv size = %+v, want %+v (moves must not clear it)", got, want)
	}
	if err := saveTileSize(ctx, conn, 1, "hrv", tileSize{Width: 13}); err == nil {
		t.Error("saveTileSize(width 13): expected an error, widths are 2-12")
	}
}

// TestSaveTileExpandedPersists checks a saved open/closed state comes back
// from fetchTileLayout and survives a later size save.
func TestSaveTileExpandedPersists(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Exec(dbpkg.Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO users (id, username, password_hash) VALUES (1, 'test', 'x')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := saveTileExpanded(ctx, conn, 1, "steps", false); err != nil { // steps defaults to open
		t.Fatal(err)
	}
	if err := saveTileSize(ctx, conn, 1, "steps", tileSize{Width: 6}); err != nil {
		t.Fatal(err)
	}
	_, sizes, err := fetchTileLayout(ctx, conn, 1)
	if err != nil {
		t.Fatal(err)
	}
	if e := sizes["steps"].Expanded; e == nil || *e {
		t.Errorf("steps expanded = %v, want saved false", e)
	}
	if e := sizes["calories"].Expanded; e != nil {
		t.Errorf("calories expanded = %v, want nil (never toggled)", *e)
	}
}
