package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sdhungan/Personal-Health-Data/internal/cronometer"
	"github.com/sdhungan/Personal-Health-Data/internal/syncengine"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

// runCronometerSyncOnce fans out one short-lived goroutine per user who has
// connected Cronometer (has saved credentials under their own per-user
// path), each running one day-completeness sync pass and exiting. Mirrors
// runGoogleHealthSyncOnce (googlehealthsync.go) — the two sources are
// deliberately independent (schema.sql: "a Cronometer outage/breakage
// never touches watch data or vice versa"), so callers should treat a
// Cronometer failure as non-fatal to an overall sync pass, same as this
// package already does in sync.go/service.go. conn is always the caller's
// own already-open database connection — see runGoogleHealthSyncOnce's doc
// comment for why this function never opens (or closes) one itself.
// logger is never nil — see runGoogleHealthSyncOnce's doc comment on the
// same default/one-shot-CLI exception.
func runCronometerSyncOnce(ctx context.Context, conn *sql.DB, logger *zap.Logger) error {
	userIDs, err := allUserIDs(ctx, conn)
	if err != nil {
		return fmt.Errorf("listing users: %w", err)
	}

	// Collected and returned, and also logged per-user right here — same
	// reasoning as runGoogleHealthSyncOnce's own comment on this loop.
	var (
		mu   sync.Mutex
		errs []error
	)
	var wg sync.WaitGroup
	for _, userID := range userIDs {
		credPath := appPaths.UserCronometerCredentialsFile(userID)
		if _, err := os.Stat(credPath); err != nil {
			continue // this user hasn't connected Cronometer yet — nothing to sync
		}

		wg.Add(1)
		go func(userID int64) {
			defer wg.Done()
			start := time.Now()
			if err := syncCronometerForUser(ctx, conn, userID, credPath); err != nil {
				logger.Error("cronometer sync failed", zap.Int64("user_id", userID), zap.Error(err))
				mu.Lock()
				errs = append(errs, fmt.Errorf("user %d: %w", userID, err))
				mu.Unlock()
				return
			}
			logger.Info("cronometer sync ok", zap.Int64("user_id", userID), zap.Duration("elapsed", time.Since(start)))
		}(userID)
	}
	wg.Wait()

	return errors.Join(errs...)
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
