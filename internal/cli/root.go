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
	rootFlag        string
	actionFlag      string
	serviceNameFlag string

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
		"OS service name to install/start/stop/uninstall the sync scheduler under (default %q)", defaultServiceName))

	cmd.AddCommand(newSyncCmd())
	cmd.AddCommand(newAuthCmd())
	cmd.AddCommand(newDBCmd())
	cmd.AddCommand(newServeCmd())
	cmd.AddCommand(newUserCmd())
	cmd.AddCommand(newGoogleClientCmd())
	cmd.AddCommand(newMCPCmd())

	return cmd
}

func runRoot(cmd *cobra.Command, args []string) error {
	name := serviceNameFlag
	if name == "" {
		name = defaultServiceName
	}
	if actionFlag != "" {
		return runServiceAction(actionFlag, name)
	}
	// service.Interactive() is false when this process was launched by the
	// OS service manager (systemd/sysv on Linux, the SCM on Windows) rather
	// than run directly from a terminal — see runHostedScheduler's doc
	// comment for why that path needs kardianos's own Run() instead of
	// runForeground()'s plain signal handling.
	if !service.Interactive() {
		return runHostedScheduler(name)
	}
	return runForeground()
}
