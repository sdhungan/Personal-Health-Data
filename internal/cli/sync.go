package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sdhungan/Personal-Health-Data/internal/config"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/googlehealth"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

func newSyncCmd() *cobra.Command {
	var dumpTodayDir string
	var dumpTodayUser string

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Run one ingestion pass immediately (Google Health + Cronometer)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dumpTodayDir != "" {
				return runDumpToday(dumpTodayDir, dumpTodayUser)
			}

			if err := runGoogleHealthSyncOnce(context.Background()); err != nil {
				return err
			}
			// Cronometer is deliberately independent of Google Health (see
			// schema.sql) — a failure here (e.g. "healthd auth cronometer"
			// never run) is logged, not fatal, so it never blocks the
			// Google Health pass that already succeeded above.
			if err := runCronometerSyncOnce(context.Background()); err != nil {
				fmt.Fprintln(os.Stderr, "cronometer sync error:", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dumpTodayDir, "dump-today", "",
		"diagnostic mode: instead of syncing into the database, fetch every known Google Health data type for today and write the raw JSON responses to this directory")
	cmd.Flags().StringVar(&dumpTodayUser, "user", "",
		"healthd account username whose Google Health authorization to use (required with --dump-today)")

	return cmd
}

// runDumpToday is a precursor to the real sync engine (see
// googlehealth.DumpToday's doc comment): it fetches today's data for every
// data type healthd's scopes cover and dumps the raw responses to disk so
// we can see what a real account actually returns before finalizing the
// watch_* table mapping.
func runDumpToday(outDir, username string) error {
	ctx := context.Background()

	cfg, err := config.Load(appPaths.ConfigFile())
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	rootKey, err := crypto.LoadKey(appPaths.DBKeyFile())
	if err != nil {
		return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
	}

	store, err := db.Open(appPaths.DBFile(), appPaths.DBWorkingFile(), rootKey)
	if err != nil {
		return fmt.Errorf("opening database (run \"healthd db init\" first?): %w", err)
	}
	defer func() {
		if cerr := store.Close(); cerr != nil {
			fmt.Fprintln(os.Stderr, "warning: closing database:", cerr)
		}
	}()

	userID, err := resolveUserID(ctx, store.DB(), username)
	if err != nil {
		return err
	}
	userKey, err := webauth.CredentialKey(appPaths, userID)
	if err != nil {
		return fmt.Errorf("loading credential key for %q: %w", username, err)
	}

	clientJSON, err := googleauth.LoadClientJSON(appPaths.GoogleClientSecretFile(), rootKey)
	if err != nil {
		return err
	}
	httpClient, err := googleauth.HTTPClient(ctx, appPaths.UserGoogleOAuthFile(userID), userKey, clientJSON, cfg.Google.CallbackPort)
	if err != nil {
		return fmt.Errorf("building authenticated Google Health client (run \"healthd auth google --user %s\" first?): %w", username, err)
	}

	client := googlehealth.NewClient(httpClient)
	fmt.Println("fetching today's data for", len(googlehealth.DataTypes), "data types into", outDir)
	return googlehealth.DumpToday(ctx, client, outDir)
}
