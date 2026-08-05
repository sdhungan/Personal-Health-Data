// Package cli wires the healthd Cobra commands together. Every subcommand
// shares the same resolved *paths.Paths (set once in the root command's
// PersistentPreRunE) so the sync job, the server, and one-shot tools like
// "db decrypt" all agree on where healthd's state lives.
package cli

import (
	"fmt"
	"os"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/sdhungan/Personal-Health-Data/internal/paths"
)

var (
	rootFlag           string
	actionFlag         string
	serviceNameFlag    string
	googleClientSecret string

	appPaths *paths.Paths
)

// Execute runs the healthd command tree and exits the process on error.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "healthd",
		Short:         "healthd owns your personal health data end to end: sync, storage, and dashboard.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			p, err := paths.Resolve(rootFlag)
			if err != nil {
				return fmt.Errorf("resolving root directory: %w", err)
			}
			appPaths = p
			return nil
		},
		RunE: runRoot,
	}

	cmd.PersistentFlags().StringVar(&rootFlag, "root", "", `root directory for all healthd state (default "~/.healthd")`)
	cmd.Flags().StringVar(&actionFlag, "action", "", "service lifecycle action: install, start, stop, uninstall")
	cmd.Flags().StringVar(&serviceNameFlag, "service-name", "", fmt.Sprintf(
		"OS service name to install/start/stop/uninstall healthd under (default %q)", defaultServiceName))
	cmd.Flags().StringVar(&googleClientSecret, "google-client-secret", "",
		"path to the Google OAuth client_secret_*.json downloaded from Google Cloud Console — if given and valid, this is the ONLY way to (re)configure the app-wide Google OAuth client (see web.GoogleClientLockedByFlag); if empty or invalid, that's logged as a warning and /settings/google-client's upload form stays available as a fallback instead")

	cmd.AddCommand(newSyncCmd())
	cmd.AddCommand(newAuthCmd())
	cmd.AddCommand(newDBCmd())
	cmd.AddCommand(newUserCmd())
	cmd.AddCommand(newMCPCmd())

	return cmd
}

func runRoot(cmd *cobra.Command, args []string) error {
	name := serviceNameFlag
	if name == "" {
		name = defaultServiceName
	}
	if actionFlag != "" {
		return runServiceAction(actionFlag, name, googleClientSecret)
	}
	// service.Interactive() is false when this process was launched by the
	// OS service manager (systemd/sysv on Linux, the SCM on Windows) rather
	// than run directly from a terminal — see runHostedService's doc
	// comment for why that path needs kardianos's own Run() instead of
	// runForeground()'s plain signal handling.
	if !service.Interactive() {
		return runHostedService(name, googleClientSecret)
	}
	return runForeground(googleClientSecret)
}
