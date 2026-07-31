package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sdhungan/Personal-Health-Data/internal/config"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication with upstream data sources",
	}
	cmd.AddCommand(newAuthGoogleCmd())
	return cmd
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
