package cli

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/cronometer"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/syncengine"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

// runCronometerSyncOnce opens the encrypted database once, then fans out
// one short-lived goroutine per user who has connected Cronometer (has
// saved credentials under their own per-user path), each running one
// day-completeness sync pass and exiting. Mirrors
// runGoogleHealthSyncOnce (googlehealthsync.go) — the two sources are
// deliberately independent (schema.sql: "a Cronometer outage/breakage
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

	userIDs, err := allUserIDs(ctx, store.DB())
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}

	var wg sync.WaitGroup
	for _, userID := range userIDs {
		credPath := appPaths.UserCronometerCredentialsFile(userID)
		if _, err := os.Stat(credPath); err != nil {
			continue // this user hasn't connected Cronometer yet — nothing to sync
		}

		wg.Add(1)
		go func(userID int64) {
			defer wg.Done()
			if err := syncCronometerForUser(ctx, store.DB(), userID, credPath); err != nil {
				fmt.Fprintf(os.Stderr, "cronometer sync error (user %d): %v\n", userID, err)
			}
		}(userID)
	}
	wg.Wait()

	fmt.Println("cronometer sync complete")
	return nil
}

func syncCronometerForUser(ctx context.Context, conn *sql.DB, userID int64, credPath string) error {
	userKey, err := webauth.CredentialKey(appPaths, userID)
	if err != nil {
		return fmt.Errorf("loading credential key: %w", err)
	}

	syncer, err := cronometer.NewDBSyncer(conn, userID, userKey, credPath, appPaths.UserCronometerSessionFile(userID))
	if err != nil {
		return err
	}
	stateStore := &syncengine.SQLStore{DB: conn, UserID: userID}
	return syncengine.RunDay(ctx, "cronometer", stateStore, syncer, time.Now())
}
