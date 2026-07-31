package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/robfig/cron/v3"

	"github.com/sdhungan/Personal-Health-Data/internal/config"
)

// runServiceAction handles the --action flag: lifecycle operations on the
// OS service registration (systemd unit on Linux, Windows Service via
// kardianos/service). Not implemented yet — this just wires up the shape
// described in ARCHITECTURE.md §2/§3.
func runServiceAction(action string) error {
	switch action {
	case "install":
		fmt.Println("TODO: register healthd as an OS service under", appPaths.ServiceDir())
	case "start":
		fmt.Println("TODO: start the installed healthd service and its internal sync scheduler")
	case "stop":
		fmt.Println("TODO: stop the installed healthd service")
	case "uninstall":
		fmt.Println("TODO: remove the installed healthd service and its schedule")
	default:
		return fmt.Errorf("unknown --action %q (want one of install, start, stop, uninstall)", action)
	}
	return nil
}

// runForeground is the default entry point ("healthd" with no subcommand):
// runs the internal sync scheduler (robfig/cron, see ARCHITECTURE.md §3)
// in the foreground, logging straight to stdout/stderr, until interrupted.
func runForeground() error {
	fmt.Println("healthd starting in foreground mode")
	fmt.Println("root:", appPaths.Root())

	cfg, err := config.Load(appPaths.ConfigFile())
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runSync := func() {
		if err := runGoogleHealthSyncOnce(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "google_health sync error:", err)
		}
		if err := runCronometerSyncOnce(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "cronometer sync error:", err)
		}
	}

	scheduler := cron.New()
	spec := fmt.Sprintf("@every %dm", cfg.SyncIntervalMinutes)
	if _, err := scheduler.AddFunc(spec, runSync); err != nil {
		return fmt.Errorf("scheduling sync job (%s): %w", spec, err)
	}
	scheduler.Start()
	defer scheduler.Stop()

	fmt.Printf("sync scheduler running every %d minutes (Ctrl+C to stop)\n", cfg.SyncIntervalMinutes)
	fmt.Println("TODO: start the Echo+Datastar server alongside this scheduler")

	// Run once immediately rather than waiting for the first tick, so
	// starting healthd always syncs right away.
	runSync()

	<-ctx.Done()
	fmt.Println("shutting down")
	return nil
}
