package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/config"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/googlehealth"
	"github.com/sdhungan/Personal-Health-Data/internal/syncengine"
)

// runGoogleHealthSyncOnce opens the encrypted database, runs one
// day-completeness sync pass for the google_health source (see
// internal/syncengine.RunDay), and cleanly closes the database
// afterwards. Used by both "healthd sync" (a single manual pass) and the
// scheduler in service.go (repeated on a timer).
func runGoogleHealthSyncOnce(ctx context.Context) error {
	cfg, err := config.Load(appPaths.ConfigFile())
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	key, err := crypto.LoadKey(appPaths.DBKeyFile())
	if err != nil {
		return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
	}

	httpClient, err := googleauth.HTTPClient(ctx, appPaths.GoogleOAuthFile(), key, cfg)
	if err != nil {
		return fmt.Errorf("building authenticated Google Health client (run \"healthd auth google\" first?): %w", err)
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

	syncer := &googlehealth.DBSyncer{Client: googlehealth.NewClient(httpClient), DB: store.DB()}
	stateStore := &syncengine.SQLStore{DB: store.DB()}

	if err := syncengine.RunDay(ctx, "google_health", stateStore, syncer, time.Now()); err != nil {
		return fmt.Errorf("running google_health sync: %w", err)
	}
	fmt.Println("google_health sync complete")
	return nil
}
