package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kardianos/service"

	"github.com/sdhungan/Personal-Health-Data/internal/config"
)

// defaultServiceName is the OS service name the root "healthd" scheduler
// installs itself under when --service-name isn't given. serve.go derives
// its own default ("healthDSafal-web") from this for the dashboard's
// separate service registration (see ARCHITECTURE.md §2).
const defaultServiceName = "healthDSafal"

// stopTimeout bounds how long Stop waits for the wrapped run function to
// return after its context is cancelled, so a hung sync/HTTP shutdown can
// never block the OS service manager's own stop sequence indefinitely.
const stopTimeout = 15 * time.Second

// hostedProgram adapts a blocking, context-driven run function
// (runForegroundCtx, runServeForegroundCtx) to kardianos/service's
// Interface: Start must return within a few seconds (see Interface's doc
// comment), so the real work runs on its own goroutine, cancelled by Stop.
type hostedProgram struct {
	run    func(ctx context.Context) error
	cancel context.CancelFunc
	done   chan error
}

func (p *hostedProgram) Start(_ service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan error, 1)
	go func() { p.done <- p.run(ctx) }()
	return nil
}

func (p *hostedProgram) Stop(_ service.Service) error {
	if p.cancel != nil {
		p.cancel()
	}
	select {
	case err := <-p.done:
		if err != nil {
			fmt.Fprintln(os.Stderr, "service stopped with error:", err)
		}
	case <-time.After(stopTimeout):
		fmt.Fprintf(os.Stderr, "service did not shut down within %s of being asked to stop\n", stopTimeout)
	}
	return nil
}

// newHostedService builds the kardianos/service registration for one of
// healthd's two independently installable services (see ARCHITECTURE.md §2:
// the bare scheduler and "serve"'s dashboard are registered separately so
// one can restart without touching the other). args is the exact argv the
// service manager re-invokes this same binary with — always including
// --root explicitly, since the account a service runs under (e.g. a Windows
// service account, or root/systemd) may not share the interactive user's
// home directory/default root.
func newHostedService(name, displayName, description string, args []string, run func(ctx context.Context) error) (service.Service, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolving executable path: %w", err)
	}
	cfg := &service.Config{
		Name:        name,
		DisplayName: displayName,
		Description: description,
		Executable:  exe,
		Arguments:   args,
	}
	return service.New(&hostedProgram{run: run}, cfg)
}

// validServiceActions are the only --action values healthd exposes on
// either the root command or "serve" — a subset of kardianos's own
// ControlAction list (which also allows "restart") kept deliberately small
// to match what ARCHITECTURE.md documents.
var validServiceActions = map[string]bool{"install": true, "start": true, "stop": true, "uninstall": true}

// runServiceAction handles the root command's --action flag: lifecycle
// operations on the scheduler's OS service registration (systemd unit on
// Linux, Windows Service via kardianos/service — see ARCHITECTURE.md §2/§3).
// serviceName defaults to defaultServiceName ("healthDSafal") unless
// overridden via --service-name.
func runServiceAction(action, serviceName string) error {
	if !validServiceActions[action] {
		return fmt.Errorf("unknown --action %q (want one of install, start, stop, uninstall)", action)
	}

	svc, err := newHostedService(
		serviceName,
		"healthd sync scheduler",
		"Syncs Google Health and Cronometer data into healthd's encrypted database.",
		[]string{"--root", appPaths.Root(), "--service-name", serviceName},
		runForegroundCtx,
	)
	if err != nil {
		return fmt.Errorf("configuring service %q: %w", serviceName, err)
	}
	if err := service.Control(svc, action); err != nil {
		return fmt.Errorf("%s service %q: %w", action, serviceName, err)
	}
	fmt.Printf("%s service %q: done\n", action, serviceName)
	return nil
}

// runHostedScheduler is what actually runs when the OS service manager
// starts the installed scheduler service — see runRoot's service.Interactive
// check. It hands runForegroundCtx to kardianos's Run(), which performs
// the platform-specific handshake a hosted service needs (notably, on
// Windows, registering with the service control dispatcher within a few
// seconds of process start) before calling Start/Stop around it.
func runHostedScheduler(serviceName string) error {
	svc, err := newHostedService(
		serviceName,
		"healthd sync scheduler",
		"Syncs Google Health and Cronometer data into healthd's encrypted database.",
		[]string{"--root", appPaths.Root(), "--service-name", serviceName},
		runForegroundCtx,
	)
	if err != nil {
		return fmt.Errorf("configuring service %q: %w", serviceName, err)
	}
	return svc.Run()
}

// runForeground is the default entry point ("healthd" with no subcommand)
// when running interactively (a real terminal, not the OS service
// manager — see runRoot): builds its own signal-driven context and blocks
// until interrupted. runForegroundCtx holds the actual scheduler body and
// is also what an installed OS service runs (via runHostedScheduler),
// driven by kardianos's own Start/Stop lifecycle instead of OS signals
// directly.
func runForeground() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runForegroundCtx(ctx)
}

// runForegroundCtx runs the internal sync scheduler until ctx is cancelled,
// logging straight to stdout/stderr. The schedule is always a fixed
// interval (see ARCHITECTURE.md §3), so a plain time.Ticker does the job —
// no need for a cron-expression parser.
func runForegroundCtx(ctx context.Context) error {
	fmt.Println("healthd starting in foreground mode")
	fmt.Println("root:", appPaths.Root())

	cfg, err := config.Load(appPaths.ConfigFile())
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	runSync := func() {
		if err := runGoogleHealthSyncOnce(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "google_health sync error:", err)
		}
		if err := runCronometerSyncOnce(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "cronometer sync error:", err)
		}
	}

	ticker := time.NewTicker(time.Duration(cfg.SyncIntervalMinutes) * time.Minute)
	defer ticker.Stop()

	fmt.Printf("sync scheduler running every %d minutes (Ctrl+C to stop)\n", cfg.SyncIntervalMinutes)
	fmt.Println("TODO: start the Echo+Datastar server alongside this scheduler")

	// Run once immediately rather than waiting for the first tick, so
	// starting healthd always syncs right away.
	runSync()

	for {
		select {
		case <-ticker.C:
			runSync()
		case <-ctx.Done():
			fmt.Println("shutting down")
			return nil
		}
	}
}
