package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/sdhungan/Personal-Health-Data/internal/config"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/cronometer"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication with upstream data sources",
	}
	cmd.AddCommand(newAuthGoogleCmd())
	cmd.AddCommand(newAuthCronometerCmd())
	return cmd
}

// newAuthCronometerCmd prompts for the Cronometer username/password
// (hidden input, same term.ReadPassword pattern "healthd db init" uses for
// the encryption passphrase — see db.go), verifies them with a real login
// before saving anything, and encrypts them under --user's own per-user
// path/key (the same credential-encryption key their account password
// derived at signup — see internal/webauth). This is the headless
// alternative to the dashboard's own onboarding/account-settings flow, for
// setups where opening a browser first isn't convenient.
func newAuthCronometerCmd() *cobra.Command {
	var username string

	cmd := &cobra.Command{
		Use:   "cronometer",
		Short: "Save Cronometer credentials for the sync job (encrypted at rest)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			dbKey, err := crypto.LoadKey(appPaths.DBKeyFile())
			if err != nil {
				return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
			}
			store, err := db.Open(appPaths.DBFile(), appPaths.DBWorkingFile(), dbKey)
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

			fmt.Print("Cronometer email: ")
			reader := bufio.NewReader(os.Stdin)
			cronoUsername, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("reading email: %w", err)
			}
			cronoUsername = strings.TrimSpace(cronoUsername)

			fmt.Print("Cronometer password (hidden): ")
			pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return fmt.Errorf("reading password: %w", err)
			}
			password := string(pwBytes)

			if _, err := cronometer.NewClient().Login(ctx, cronoUsername, password); err != nil {
				return fmt.Errorf("verifying credentials: %w", err)
			}

			if err := appPaths.EnsureUserDir(userID); err != nil {
				return fmt.Errorf("creating per-user directories: %w", err)
			}
			creds := &cronometer.Credentials{Username: cronoUsername, Password: password}
			credPath := appPaths.UserCronometerCredentialsFile(userID)
			if err := cronometer.SaveCredentials(credPath, userKey, creds); err != nil {
				return fmt.Errorf("saving credentials: %w", err)
			}

			fmt.Println("verified and saved — credentials written to", credPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&username, "user", "", "healthd account username to attach these credentials to (required)")
	return cmd
}

func newAuthGoogleCmd() *cobra.Command {
	var username string

	cmd := &cobra.Command{
		Use:   "google",
		Short: "Run the OAuth2 flow for Google Health API access",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			cfg, err := config.Load(appPaths.ConfigFile())
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			dbKey, err := crypto.LoadKey(appPaths.DBKeyFile())
			if err != nil {
				return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
			}
			store, err := db.Open(appPaths.DBFile(), appPaths.DBWorkingFile(), dbKey)
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

			clientJSON, err := googleauth.LoadClientJSON(appPaths.GoogleClientSecretFile(), dbKey)
			if err != nil {
				return err
			}
			token, err := googleauth.RunConsentFlow(ctx, clientJSON, cfg.Google.CallbackPort)
			if err != nil {
				return fmt.Errorf("running Google consent flow: %w", err)
			}

			if err := appPaths.EnsureUserDir(userID); err != nil {
				return fmt.Errorf("creating per-user directories: %w", err)
			}
			tokenPath := appPaths.UserGoogleOAuthFile(userID)
			if err := googleauth.SaveToken(tokenPath, userKey, token); err != nil {
				return fmt.Errorf("saving token: %w", err)
			}

			fmt.Println("authorized — tokens written to", tokenPath)
			return nil
		},
	}

	cmd.Flags().StringVar(&username, "user", "", "healthd account username to attach this Google Health authorization to (required)")
	return cmd
}
