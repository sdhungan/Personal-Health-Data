package cli

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/config"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/googlehealth"
	"github.com/sdhungan/Personal-Health-Data/internal/syncengine"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

// runGoogleHealthSyncOnce opens the encrypted database once, then fans out
// one short-lived goroutine per user who has connected Google Health (has
// a saved OAuth token under their own per-user path — see
// ARCHITECTURE.md's multi-user section), each running one
// day-completeness sync pass (internal/syncengine.RunDay) and exiting.
// This is deliberately independent of who — if anyone — is currently
// logged into the dashboard: the scheduler's only job is keeping data
// synced, on its own schedule, for every account that's connected a
// provider (see ARCHITECTURE.md). Used by both "healthd sync" (a single
// manual pass) and the scheduler in service.go (repeated on a timer).
func runGoogleHealthSyncOnce(ctx context.Context) error {
	cfg, err := config.Load(appPaths.ConfigFile())
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

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

	userIDs, err := allUserIDs(ctx, store.DB())
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}

	// The OAuth client JSON is one app-wide secret (see
	// ARCHITECTURE.md's multi-user section), not per-user — load it once
	// rather than per goroutine below. Missing entirely means no account
	// could possibly sync yet, so skip the whole pass with one clear
	// message instead of repeating the same failure per user.
	clientJSON, err := googleauth.LoadClientJSON(appPaths.GoogleClientSecretFile(), key)
	if err != nil {
		fmt.Fprintln(os.Stderr, "google_health sync skipped:", err)
		return nil
	}

	var wg sync.WaitGroup
	for _, userID := range userIDs {
		tokenPath := appPaths.UserGoogleOAuthFile(userID)
		if _, err := os.Stat(tokenPath); err != nil {
			continue // this user hasn't connected Google Health yet — nothing to sync
		}

		wg.Add(1)
		go func(userID int64) {
			defer wg.Done()
			if err := syncGoogleHealthForUser(ctx, store.DB(), cfg, clientJSON, userID, tokenPath); err != nil {
				fmt.Fprintf(os.Stderr, "google_health sync error (user %d): %v\n", userID, err)
			}
		}(userID)
	}
	wg.Wait()

	fmt.Println("google_health sync complete")
	return nil
}

func syncGoogleHealthForUser(ctx context.Context, conn *sql.DB, cfg *config.Config, clientJSON []byte, userID int64, tokenPath string) error {
	userKey, err := webauth.CredentialKey(appPaths, userID)
	if err != nil {
		return fmt.Errorf("loading credential key: %w", err)
	}
	httpClient, err := googleauth.HTTPClient(ctx, tokenPath, userKey, clientJSON, cfg.Google.CallbackPort)
	if err != nil {
		return fmt.Errorf("building authenticated Google Health client: %w", err)
	}

	syncer := &googlehealth.DBSyncer{Client: googlehealth.NewClient(httpClient), DB: conn, UserID: userID}
	stateStore := &syncengine.SQLStore{DB: conn, UserID: userID}
	return syncengine.RunDay(ctx, "google_health", stateStore, syncer, time.Now())
}
