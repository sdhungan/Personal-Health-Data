package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/cronometer"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/syncengine"
)

// runCronometerSyncOnce opens the encrypted database, runs one
// day-completeness sync pass for the cronometer source (see
// internal/syncengine.RunDay), and cleanly closes the database afterwards.
// Mirrors runGoogleHealthSyncOnce (googlehealthsync.go) — the two sources
// are deliberately independent (schema.sql: "a Cronometer outage/breakage
// never touches watch data or vice versa"), so callers should treat a
// Cronometer failure as non-fatal to an overall sync pass, same as this
// package already does in sync.go/service.go.
func runCronometerSyncOnce(ctx context.Context) error {
	key, err := crypto.LoadKey(appPaths.DBKeyFile())
	if err != nil {
		return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
	}

	store, err := db.Open(appPaths.DBFile(), appPaths.DBWorkingFile(), key)
	if err != nil {
		return fmt.Errorf("opening database (run \"healthd db init\" first?): %w", err)
	}
	defer func() {
		if cerr := store.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "warning: closing database:", cerr)
		}
	}()

	syncer, err := cronometer.NewDBSyncer(store.DB(), key, appPaths.CronometerCredentialsFile(), appPaths.CronometerSessionFile())
	if err != nil {
		return err
	}
	stateStore := &syncengine.SQLStore{DB: store.DB()}

	if err := syncengine.RunDay(ctx, "cronometer", stateStore, syncer, time.Now()); err != nil {
		return fmt.Errorf("running cronometer sync: %w", err)
	}
	fmt.Println("cronometer sync complete")
	return nil
}
