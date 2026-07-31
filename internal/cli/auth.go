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
	"github.com/sdhungan/Personal-Health-Data/internal/cronometer"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
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
// before saving anything, and encrypts them to CronometerCredentialsFile.
// This replaces config.yaml's plaintext cronometer.username/password as the
// long-term store (see cronometer-integration.md's "Credential handling" —
// same posture as the Google OAuth client secret, never plaintext at rest).
func newAuthCronometerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cronometer",
		Short: "Save Cronometer credentials for the sync job (encrypted at rest)",
		RunE: func(cmd *cobra.Command, args []string) error {
			key, err := crypto.LoadKey(appPaths.DBKeyFile())
			if err != nil {
				return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
			}

			fmt.Print("Cronometer email: ")
			reader := bufio.NewReader(os.Stdin)
			username, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("reading email: %w", err)
			}
			username = strings.TrimSpace(username)

			fmt.Print("Cronometer password (hidden): ")
			pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return fmt.Errorf("reading password: %w", err)
			}
			password := string(pwBytes)

			ctx := context.Background()
			if _, err := cronometer.NewClient().Login(ctx, username, password); err != nil {
				return fmt.Errorf("verifying credentials: %w", err)
			}

			creds := &cronometer.Credentials{Username: username, Password: password}
			if err := cronometer.SaveCredentials(appPaths.CronometerCredentialsFile(), key, creds); err != nil {
				return fmt.Errorf("saving credentials: %w", err)
			}

			fmt.Println("verified and saved — credentials written to", appPaths.CronometerCredentialsFile())
			return nil
		},
	}
}

func newAuthGoogleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "google",
		Short: "Run the OAuth2 flow for Google Health API access",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(appPaths.ConfigFile())
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			key, err := crypto.LoadKey(appPaths.DBKeyFile())
			if err != nil {
				return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
			}

			token, err := googleauth.RunConsentFlow(context.Background(), cfg)
			if err != nil {
				return fmt.Errorf("running Google consent flow: %w", err)
			}

			if err := googleauth.SaveToken(appPaths.GoogleOAuthFile(), key, token); err != nil {
				return fmt.Errorf("saving token: %w", err)
			}

			fmt.Println("authorized — tokens written to", appPaths.GoogleOAuthFile())
			return nil
		},
	}
}
