package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/sdhungan/Personal-Health-Data/internal/config"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/web"
)

// defaultServeServiceName is the OS service name the web dashboard installs
// itself under when --service-name isn't given — deliberately distinct from
// defaultServiceName (service.go) since the scheduler and the dashboard are
// registered as two independent OS services (see ARCHITECTURE.md §2).
const defaultServeServiceName = defaultServiceName + "-web"

func newServeCmd() *cobra.Command {
	var action string
	var serviceName string
	var googleClientSecret string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the web dashboard (Echo + Datastar)",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := serviceName
			if name == "" {
				name = defaultServeServiceName
			}
			if action != "" {
				return runServeServiceAction(action, name, googleClientSecret)
			}
			// See runRoot's identical check: false here means this process
			// was launched by the OS service manager, not a terminal.
			if !service.Interactive() {
				return runHostedServe(name, googleClientSecret)
			}
			return runServeForeground(googleClientSecret)
		},
	}

	cmd.Flags().StringVar(&action, "action", "",
		"service lifecycle action for the web dashboard: install, start, stop, uninstall (empty runs in the terminal)")
	cmd.Flags().StringVar(&serviceName, "service-name", "", fmt.Sprintf(
		"OS service name to install/start/stop/uninstall the web dashboard under (default %q)", defaultServeServiceName))
	cmd.Flags().StringVar(&googleClientSecret, "google-client-secret", "",
		"optional path to the Google OAuth client_secret_*.json downloaded from Google Cloud Console — if given, re-saved as the app-wide client on every startup; if omitted, falls back to whatever's already configured via \"google-client set\" or /settings/google-client (or none yet, if neither has happened)")

	return cmd
}

// runServeServiceAction mirrors the root command's --action handling (see
// service.go) but for the web dashboard specifically — the two are
// registered as separate OS services so one can be restarted or disabled
// without touching the other. googleClientSecret, if given, is baked into
// the installed service's re-invocation arguments so every start of the
// hosted service re-applies it, same as a manual "serve --google-client-secret".
func runServeServiceAction(action, serviceName, googleClientSecret string) error {
	if !validServiceActions[action] {
		return fmt.Errorf("unknown --action %q (want one of install, start, stop, uninstall)", action)
	}

	args := []string{"serve", "--root", appPaths.Root(), "--service-name", serviceName}
	if googleClientSecret != "" {
		args = append(args, "--google-client-secret", googleClientSecret)
	}

	svc, err := newHostedService(
		serviceName,
		"healthd web dashboard",
		"Serves the healthd web dashboard (Echo + Datastar) over HTTP.",
		args,
		func(ctx context.Context) error { return runServeForegroundCtx(ctx, googleClientSecret) },
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

// runHostedServe is what actually runs when the OS service manager starts
// the installed web dashboard service — mirrors runHostedScheduler (see
// service.go) but for the dashboard's HTTP server instead of the sync
// scheduler.
func runHostedServe(serviceName, googleClientSecret string) error {
	svc, err := newHostedService(
		serviceName,
		"healthd web dashboard",
		"Serves the healthd web dashboard (Echo + Datastar) over HTTP.",
		[]string{"serve", "--root", appPaths.Root(), "--service-name", serviceName},
		func(ctx context.Context) error { return runServeForegroundCtx(ctx, googleClientSecret) },
	)
	if err != nil {
		return fmt.Errorf("configuring service %q: %w", serviceName, err)
	}
	return svc.Run()
}

// runServeForeground opens the encrypted database and runs the web
// dashboard in the terminal until interrupted, building its own
// signal-driven context. See runServeForegroundCtx for the actual body,
// also used by the hosted-service path (runHostedServe).
func runServeForeground(googleClientSecretPath string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runServeForegroundCtx(ctx, googleClientSecretPath)
}

// runServeForegroundCtx opens the encrypted database and runs the web
// dashboard until ctx is cancelled. googleClientSecretPath, if non-empty,
// is re-saved as the app-wide Google OAuth client on every startup (see
// googleauth.SaveClientJSON) — a convenience so a fresh --root doesn't
// require a separate "google-client set" step, not a hard requirement: an
// empty path just means "leave whatever's already configured (or
// unconfigured) alone," same as before this flag existed. The dashboard's
// /settings/google-client page (upload form + a "Connect Google Health"
// action for the current account, see that page's own doc comment) is the
// primary way to fix a missing/broken client without restarting the
// server at all.
func runServeForegroundCtx(ctx context.Context, googleClientSecretPath string) error {
	cfg, err := config.Load(appPaths.ConfigFile())
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	key, err := crypto.LoadKey(appPaths.DBKeyFile())
	if err != nil {
		return fmt.Errorf("loading key material (run \"healthd db init\" first?): %w", err)
	}

	if googleClientSecretPath != "" {
		clientJSON, err := os.ReadFile(googleClientSecretPath)
		if err != nil {
			return fmt.Errorf("reading --google-client-secret %s: %w", googleClientSecretPath, err)
		}
		if err := googleauth.SaveClientJSON(appPaths.GoogleClientSecretFile(), key, clientJSON); err != nil {
			return fmt.Errorf("saving Google OAuth client from --google-client-secret %s: %w", googleClientSecretPath, err)
		}
	}

	store, err := db.Open(appPaths.DBFile(), appPaths.DBWorkingFile(), key)
	if err != nil {
		return fmt.Errorf("opening database (run \"healthd db init\" first?): %w", err)
	}

	// Every user's Google/Cronometer client is resolved lazily, per user,
	// from their own per-user credential files (see web.Server's
	// googleSyncers/cronoSyncers cache) — nothing to pre-load here. key is
	// also handed to the server as its RootKey, to decrypt the one
	// app-wide Google OAuth client JSON uploaded via /settings/google-client.
	srv := web.New(store, appPaths, cfg, key)

	addr := fmt.Sprintf(":%d", cfg.Port)
	fmt.Println("web dashboard listening on http://localhost" + addr)
	fmt.Println("(Ctrl+C to stop)")

	return srv.Start(ctx, addr)
}
