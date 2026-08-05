package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kardianos/service"
	"go.uber.org/zap"

	"github.com/sdhungan/Personal-Health-Data/internal/applog"
	"github.com/sdhungan/Personal-Health-Data/internal/config"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/web"
)

// defaultServiceName is the OS service name healthd installs itself under
// when --service-name isn't given. One service, not two: the sync
// scheduler and the web dashboard used to be registered separately (so one
// could restart without the other), but in practice they're fully
// order-dependent anyway — a user account can only be created through the
// dashboard, the MCP connector can't authenticate without that account
// existing, and sync has nothing to do until that account has connected a
// provider — so splitting the *processes* bought no real isolation, just
// two things to install/start/stop instead of one. See ARCHITECTURE.md §2.
const defaultServiceName = "healthDSafal"

// stopTimeout bounds how long Stop waits for the wrapped run function to
// return after its context is cancelled, so a hung sync/HTTP shutdown can
// never block the OS service manager's own stop sequence indefinitely.
const stopTimeout = 15 * time.Second

// hostedProgram adapts a blocking, context-driven run function
// (runForegroundCtx) to kardianos/service's Interface: Start must return
// within a few seconds (see Interface's doc comment), so the real work
// runs on its own goroutine, cancelled by Stop.
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

// newHostedService builds the kardianos/service registration for healthd's
// single OS service (systemd unit on Linux, Windows Service via
// kardianos/service). args is the exact argv the service manager
// re-invokes this same binary with — always including --root explicitly,
// since the account a service runs under (e.g. a Windows service account,
// or root/systemd) may not share the interactive user's home
// directory/default root.
func newHostedService(name string, args []string, run func(ctx context.Context) error) (service.Service, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolving executable path: %w", err)
	}
	cfg := &service.Config{
		Name:        name,
		DisplayName: "healthd",
		Description: "Syncs Google Health and Cronometer data and serves the healthd web dashboard and Claude MCP connector.",
		Executable:  exe,
		Arguments:   args,
	}
	return service.New(&hostedProgram{run: run}, cfg)
}

// validServiceActions are the only --action values healthd exposes — a
// subset of kardianos's own ControlAction list (which also allows
// "restart") kept deliberately small to match what ARCHITECTURE.md
// documents.
var validServiceActions = map[string]bool{"install": true, "start": true, "stop": true, "uninstall": true}

// runServiceAction handles the --action flag: lifecycle operations on
// healthd's OS service registration (systemd unit on Linux, Windows
// Service via kardianos/service — see ARCHITECTURE.md §2/§3). serviceName
// defaults to defaultServiceName ("healthDSafal") unless overridden via
// --service-name. googleClientSecretPath, if given, is baked into the
// installed service's re-invocation arguments so every start re-applies it,
// same as a manual run with --google-client-secret.
func runServiceAction(action, serviceName, googleClientSecretPath string) error {
	if !validServiceActions[action] {
		return fmt.Errorf("unknown --action %q (want one of install, start, stop, uninstall)", action)
	}

	args := []string{"--root", appPaths.Root(), "--service-name", serviceName}
	if googleClientSecretPath != "" {
		args = append(args, "--google-client-secret", googleClientSecretPath)
	}

	svc, err := newHostedService(serviceName, args, func(ctx context.Context) error {
		return runForegroundCtx(ctx, googleClientSecretPath)
	})
	if err != nil {
		return fmt.Errorf("configuring service %q: %w", serviceName, err)
	}
	if err := service.Control(svc, action); err != nil {
		return fmt.Errorf("%s service %q: %w", action, serviceName, err)
	}
	fmt.Printf("%s service %q: done\n", action, serviceName)
	return nil
}

// runHostedService is what actually runs when the OS service manager
// starts the installed service — see runRoot's service.Interactive check.
// It hands runForegroundCtx to kardianos's Run(), which performs the
// platform-specific handshake a hosted service needs (notably, on Windows,
// registering with the service control dispatcher within a few seconds of
// process start) before calling Start/Stop around it.
func runHostedService(serviceName, googleClientSecretPath string) error {
	svc, err := newHostedService(serviceName,
		[]string{"--root", appPaths.Root(), "--service-name", serviceName},
		func(ctx context.Context) error { return runForegroundCtx(ctx, googleClientSecretPath) },
	)
	if err != nil {
		return fmt.Errorf("configuring service %q: %w", serviceName, err)
	}
	return svc.Run()
}

// runForeground is the default entry point ("healthd" with no subcommand)
// when running interactively (a real terminal, not the OS service
// manager — see runRoot): builds its own signal-driven context and blocks
// until interrupted. runForegroundCtx holds everything healthd actually
// does and is also what an installed OS service runs (via
// runHostedService), driven by kardianos's own Start/Stop lifecycle
// instead of OS signals directly.
func runForeground(googleClientSecretPath string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runForegroundCtx(ctx, googleClientSecretPath)
}

// runForegroundCtx runs healthd as one process until ctx is cancelled: the
// web dashboard (including the MCP connector, mounted as one of its
// routes — see internal/web/mcp.go) as the blocking main loop, and the
// sync scheduler as a lightweight ticker goroutine sharing the same
// database connection and the same shutdown signal. One store, opened
// once, handed to both — never two independent *db.Store opened in the
// same process (see runGoogleHealthSyncOnce's doc comment: a second Store
// closing itself on every tick would fight the first over the same
// on-disk working file). Every intermediate startup/shutdown step is
// logged (see internal/applog) to <root>/logs/healthd.log — this is the
// only place any of this gets logged once running as a hosted OS service,
// which has no attached console for fmt.Println to reach.
func runForegroundCtx(ctx context.Context, googleClientSecretPath string) error {
	logger, closeLog, err := applog.New(appPaths.LogsDir(), service.Interactive())
	if err != nil {
		return fmt.Errorf("setting up logging: %w", err)
	}
	defer closeLog()

	logger.Info("healthd starting", zap.String("root", appPaths.Root()))

	cfg, err := config.Load(appPaths.ConfigFile())
	if err != nil {
		logger.Error("loading config failed", zap.Error(err))
		return fmt.Errorf("loading config: %w", err)
	}
	logger.Info("config loaded", zap.Int("port", cfg.Port), zap.Int("sync_interval_minutes", cfg.SyncIntervalMinutes))

	key, err := crypto.LoadKey(appPaths.DBKeyFile())
	if err != nil {
		logger.Error("loading key material failed", zap.Error(err))
		return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
	}
	logger.Info("key material loaded")

	// --google-client-secret is optional (see §5): if given and valid,
	// it's saved as the app-wide Google OAuth client and becomes the *only*
	// way to change it for the rest of this run (web.GoogleClientLockedByFlag
	// locks out the dashboard's upload form — see its own doc comment for
	// why: this was an explicit request to stop the client secret from
	// silently drifting between "whatever's on the command line" and
	// "whatever was last uploaded through a browser"). An empty path, or
	// one that fails to read/validate, is treated as "not given" rather
	// than a fatal startup error — logged as a warning, not silently
	// swallowed, so a typo'd path doesn't look identical to deliberately
	// omitting it. When not locked, the dashboard's onboarding/settings
	// upload form stays available as the fallback so a missing or bad flag
	// value never leaves an account with zero way to configure it.
	web.GoogleClientLockedByFlag = false
	switch {
	case googleClientSecretPath == "":
		logger.Info("no --google-client-secret given; Google OAuth client configurable from the dashboard")
	default:
		clientJSON, readErr := os.ReadFile(googleClientSecretPath)
		if readErr != nil {
			logger.Warn("--google-client-secret path could not be read; ignoring it, dashboard upload stays available",
				zap.String("path", googleClientSecretPath), zap.Error(readErr))
			break
		}
		if saveErr := googleauth.SaveClientJSON(appPaths.GoogleClientSecretFile(), key, clientJSON); saveErr != nil {
			logger.Warn("--google-client-secret did not parse as a valid Google OAuth client JSON; ignoring it, dashboard upload stays available",
				zap.String("path", googleClientSecretPath), zap.Error(saveErr))
			break
		}
		web.GoogleClientLockedByFlag = true
		logger.Info("Google OAuth client configured from --google-client-secret; dashboard upload locked",
			zap.String("path", googleClientSecretPath))
	}

	store, err := db.Open(appPaths.DBFile(), appPaths.DBWorkingFile(), key)
	if err != nil {
		logger.Error("opening database failed", zap.Error(err))
		return fmt.Errorf("opening database (run \"healthd db init\" first?): %w", err)
	}
	logger.Info("database opened")

	// Every user's Google/Cronometer client is resolved lazily, per user,
	// from their own per-user credential files (see web.Server's
	// googleSyncers/cronoSyncers cache) — nothing to pre-load here. key is
	// also handed to the server as its RootKey, to decrypt the one
	// app-wide Google OAuth client JSON uploaded via /settings/google-client.
	srv := web.New(store, appPaths, cfg, key)

	runSync := func() {
		if err := runGoogleHealthSyncOnce(ctx, store.DB()); err != nil {
			logger.Error("google_health sync error", zap.Error(err))
		}
		if err := runCronometerSyncOnce(ctx, store.DB()); err != nil {
			logger.Error("cronometer sync error", zap.Error(err))
		}
	}
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		// A plain time.Ticker, not a cron-expression library: the schedule
		// is always a fixed interval (see ARCHITECTURE.md §3).
		ticker := time.NewTicker(time.Duration(cfg.SyncIntervalMinutes) * time.Minute)
		defer ticker.Stop()

		runSync() // sync once immediately rather than waiting for the first tick
		for {
			select {
			case <-ticker.C:
				runSync()
			case <-ctx.Done():
				logger.Info("sync scheduler stopping")
				return
			}
		}
	}()
	logger.Info("sync scheduler running", zap.Int("interval_minutes", cfg.SyncIntervalMinutes))

	addr := fmt.Sprintf(":%d", cfg.Port)
	logger.Info("web dashboard and MCP connector listening", zap.String("addr", "http://localhost"+addr))

	startErr := srv.Start(ctx, addr)
	if startErr != nil {
		logger.Error("web server stopped with error", zap.Error(startErr))
	} else {
		logger.Info("web server stopped")
	}

	// Wait for the scheduler goroutine to actually stop (it may be
	// mid-sync when ctx is cancelled) before closing the one connection
	// they both share — closing it any earlier would race an in-flight
	// sync query against Store.Close's checkpoint+close.
	<-schedulerDone

	if err := store.Close(); err != nil {
		logger.Error("closing database failed", zap.Error(err))
		if startErr != nil {
			return fmt.Errorf("%w (also failed to close database: %v)", startErr, err)
		}
		return fmt.Errorf("closing database: %w", err)
	}
	logger.Info("healthd shut down cleanly")
	return startErr
}
