// Package cli wires the healthd Cobra commands together. Every subcommand
// shares the same resolved *paths.Paths (set once in the root command's
// PersistentPreRunE) so the sync job, the server, and one-shot tools like
// "db decrypt" all agree on where healthd's state lives.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sdhungan/Personal-Health-Data/internal/paths"
)

var (
	rootFlag   string
	actionFlag string

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

	cmd.AddCommand(newSyncCmd())
	cmd.AddCommand(newAuthCmd())
	cmd.AddCommand(newDBCmd())
	cmd.AddCommand(newServeCmd())

	return cmd
}

func runRoot(cmd *cobra.Command, args []string) error {
	if actionFlag != "" {
		return runServiceAction(actionFlag)
	}
	return runForeground()
}
