package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sdhungan/Personal-Health-Data/internal/config"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/web"
)

func newServeCmd() *cobra.Command {
	var action string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the web dashboard (Echo + Datastar)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if action != "" {
				return runServeServiceAction(action)
			}
			return runServeForeground()
		},
	}

	cmd.Flags().StringVar(&action, "action", "",
		"service lifecycle action for the web dashboard: install, start, stop, uninstall (empty runs in the terminal)")

	return cmd
}

// runServeServiceAction mirrors the root command's --action handling (see
// service.go) but for the web dashboard specifically — the two are
// registered as separate OS services so one can be restarted or disabled
// without touching the other.
func runServeServiceAction(action string) error {
	switch action {
	case "install":
		fmt.Println("TODO: register the web dashboard as an OS service under", appPaths.ServiceDir())
	case "start":
		fmt.Println("TODO: start the installed web dashboard service")
	case "stop":
		fmt.Println("TODO: stop the installed web dashboard service")
	case "uninstall":
		fmt.Println("TODO: remove the installed web dashboard service")
	default:
		return fmt.Errorf("unknown --action %q (want one of install, start, stop, uninstall)", action)
	}
	return nil
}

// runServeForeground opens the encrypted database and runs the web
// dashboard in the terminal until interrupted.
func runServeForeground() error {
	cfg, err := config.Load(appPaths.ConfigFile())
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	key, err := crypto.LoadKey(appPaths.DBKeyFile())
	if err != nil {
		return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
	}

	store, err := db.Open(appPaths.DBFile(), appPaths.DBWorkingFile(), key)
	if err != nil {
		return fmt.Errorf("opening database (run \"healthd db init\" first?): %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The dashboard's force-sync button needs an authenticated Google
	// Health client too, but a missing/broken Google auth setup shouldn't
	// stop the whole dashboard from starting — the button just reports a
	// clear error in that case (see web.Server's googleSync nil check).
	var googleClient *http.Client
	if gc, gcErr := googleauth.HTTPClient(ctx, appPaths.GoogleOAuthFile(), key, cfg); gcErr != nil {
		fmt.Fprintln(os.Stderr, "warning: Google Health client unavailable (force-sync will be disabled):", gcErr)
	} else {
		googleClient = gc
	}

	srv := web.New(store, googleClient, key, appPaths.CronometerCredentialsFile(), appPaths.CronometerSessionFile())

	addr := fmt.Sprintf(":%d", cfg.Port)
	fmt.Println("web dashboard listening on http://localhost" + addr)
	fmt.Println("(Ctrl+C to stop)")

	return srv.Start(ctx, addr)
}
