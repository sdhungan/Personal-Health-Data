// Package web is healthd's dashboard: an Echo server that reads from the
// encrypted database and renders templ components (internal/web/views),
// pushing updates over Datastar SSE instead of full page reloads.
package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/sdhungan/Personal-Health-Data/internal/cronometer"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/googlehealth"
	"github.com/sdhungan/Personal-Health-Data/internal/syncengine"
)

// checkpointInterval bounds how much would be lost to a crash while the
// server is running for a long stretch — see internal/db.Store.Checkpoint.
const checkpointInterval = 5 * time.Minute

// googleHealthSource/cronometerSource are the sync_state.source values the
// cron job (see internal/cli/googlehealthsync.go, cronometersync.go) and
// the dashboard's force-sync button both use — they must agree so a manual
// sync here is visible to, and visible from, the normal scheduled path.
const googleHealthSource = "google_health"
const cronometerSource = "cronometer"

// Server wires together the Echo app, the encrypted database, and the
// route handlers for healthd's web dashboard.
type Server struct {
	echo       *echo.Echo
	store      *db.Store
	DB         *sql.DB
	googleSync *googlehealth.DBSyncer
	syncState  *syncengine.SQLStore

	// cronometerSync is nil until credentials exist — either already saved
	// when the server started, or saved just now via the dashboard's
	// Cronometer login card (see handleCronometerLogin, which rebuilds
	// this field on success so a running server picks up a fresh login
	// without a restart). The key/paths are kept alongside it so that
	// rebuild can happen without re-deriving them from scratch.
	cronometerSync            *cronometer.DBSyncer
	cronometerKey             crypto.Key
	cronometerCredentialsPath string
	cronometerSessionPath     string

	// syncingDays tracks which days currently have a force-sync in
	// flight (see handleForceSync): only the specific day being synced is
	// serialized against a duplicate request, not the whole database —
	// WAL mode (internal/db.Store) already keeps reads/writes for other
	// days from blocking on each other.
	syncingDays sync.Map // day string -> struct{}
}

// New builds the Echo app and registers every route. store is used for
// the lifetime of the server (periodic checkpointing, and a final
// checkpoint+close on shutdown) — see Start. googleClient is an
// already-authenticated Google Health API client (see
// internal/googleauth.HTTPClient); it may be nil if Google auth hasn't
// been set up yet ("healthd auth google" not run), in which case the
// force-sync button reports a clear error instead of the dashboard
// failing to start. dbKey/cronometerCredentialsPath/cronometerSessionPath
// are what a Cronometer login (CLI or the dashboard's own login card)
// needs — cronometerSync starts nil if no credentials have been saved yet.
func New(store *db.Store, googleClient *http.Client, dbKey crypto.Key, cronometerCredentialsPath, cronometerSessionPath string) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	// Recover turns a panicking handler into a logged 500 response instead
	// of crashing the whole process — the "graceful handling of hard
	// crashes" middleware this package needs.
	e.Use(middleware.Recover())
	e.Use(middleware.Logger())

	s := &Server{
		echo: e, store: store, DB: store.DB(), syncState: &syncengine.SQLStore{DB: store.DB()},
		cronometerKey: dbKey, cronometerCredentialsPath: cronometerCredentialsPath, cronometerSessionPath: cronometerSessionPath,
	}
	if googleClient != nil {
		s.googleSync = &googlehealth.DBSyncer{Client: googlehealth.NewClient(googleClient), DB: store.DB()}
	}
	if syncer, err := cronometer.NewDBSyncer(store.DB(), dbKey, cronometerCredentialsPath, cronometerSessionPath); err == nil {
		s.cronometerSync = syncer
	}
	s.registerRoutes()
	return s
}

// Start runs the HTTP server on addr until ctx is canceled, then shuts
// down gracefully (in-flight requests get a grace period), checkpoints,
// and cleanly closes the database before returning. A best-effort final
// checkpoint also happens if the server exits some other way (e.g. a
// listener error) so a crash loses at most the last checkpoint interval,
// not the whole session.
func (s *Server) Start(ctx context.Context, addr string) error {
	serveErr := make(chan error, 1)
	go func() {
		if err := s.echo.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	ticker := time.NewTicker(checkpointInterval)
	defer ticker.Stop()

	var runErr error
loop:
	for {
		select {
		case err := <-serveErr:
			runErr = err
			break loop
		case <-ticker.C:
			if err := s.store.Checkpoint(); err != nil {
				fmt.Println("warning: checkpointing database:", err)
			}
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := s.echo.Shutdown(shutdownCtx); err != nil {
				runErr = fmt.Errorf("shutting down web server: %w", err)
			}
			cancel()
			break loop
		}
	}

	if err := s.store.Close(); err != nil {
		if runErr != nil {
			return fmt.Errorf("%w (also failed to close database: %v)", runErr, err)
		}
		return fmt.Errorf("closing database: %w", err)
	}
	return runErr
}
