package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
)

// newGoogleClientCmd is the headless alternative to the dashboard's
// /settings/google-client upload form — sets the one app-wide Google OAuth
// client JSON every account's "Connect Google Health" button uses (see
// ARCHITECTURE.md's multi-user section). Not tied to any one user account,
// unlike "healthd auth google --user".
func newGoogleClientCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "google-client", Short: "Manage the app-wide Google OAuth client credentials"}
	cmd.AddCommand(newGoogleClientSetCmd())
	return cmd
}

func newGoogleClientSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <path-to-client-secret.json>",
		Short: "Upload/replace the Google OAuth client JSON downloaded from Google Cloud Console",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("reading %s: %w", args[0], err)
			}

			key, err := crypto.LoadKey(appPaths.DBKeyFile())
			if err != nil {
				return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
			}

			if err := googleauth.SaveClientJSON(appPaths.GoogleClientSecretFile(), key, data); err != nil {
				return fmt.Errorf("saving Google OAuth client: %w", err)
			}

			fmt.Println("saved — every account's \"Connect Google Health\" now uses this client")
			return nil
		},
	}
}
