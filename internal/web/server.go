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
	"os"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/sdhungan/Personal-Health-Data/internal/config"
	"github.com/sdhungan/Personal-Health-Data/internal/cronometer"
	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
	"github.com/sdhungan/Personal-Health-Data/internal/db"
	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/googlehealth"
	"github.com/sdhungan/Personal-Health-Data/internal/paths"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
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
// route handlers for healthd's web dashboard. It serves multiple accounts
// out of one shared database (see ARCHITECTURE.md's multi-user section) —
// every request is gated behind webauth's session middleware, and every
// query threads the authenticated caller's user id through explicitly.
type Server struct {
	echo   *echo.Echo
	store  *db.Store
	DB     *sql.DB
	Paths  *paths.Paths
	Config *config.Config

	// RootKey is the root DB encryption key (crypto.LoadKey(Paths.DBKeyFile())).
	// It decrypts the one app-wide Google OAuth client JSON (see
	// GoogleClientJSON) — a genuinely different secret from any per-user
	// credential, which is why it uses the root key rather than a
	// per-user one (see internal/paths.GoogleClientSecretFile).
	RootKey crypto.Key

	// googleSyncers/cronoSyncers cache one DBSyncer per user, built lazily
	// on first use from that user's own per-user credential files (see
	// internal/paths's UserGoogleOAuthFile/UserCronometerCredentialsFile).
	// A cached nil entry means "checked, this user hasn't connected this
	// provider" — distinguished from "never checked" via the map's ok
	// return, so a user who never connects isn't re-probed the filesystem
	// on every single request. setGoogleSyncer/setCronometerSyncer
	// overwrite the cache on a fresh connect so a running server picks up
	// a new login immediately, no restart needed.
	googleMu      sync.Mutex
	googleSyncers map[int64]*googlehealth.DBSyncer
	cronoMu       sync.Mutex
	cronoSyncers  map[int64]*cronometer.DBSyncer

	// syncingDays tracks which (user, day) pairs currently have a
	// force-sync in flight (see handleForceSync): only that specific pair
	// is serialized against a duplicate request, not the whole database —
	// WAL mode (internal/db.Store) already keeps reads for other days (or
	// other users) from blocking on each other.
	syncingDays sync.Map // "userID/day" string -> struct{}
}

// New builds the Echo app and registers every route. store is used for the
// lifetime of the server (periodic checkpointing + session cleanup, and a
// final checkpoint+close on shutdown) — see Start. p and cfg are what
// resolving any user's per-user Google/Cronometer credentials needs on
// demand (see googleSyncers/cronoSyncers above) — nothing is pre-loaded at
// startup, since which users exist and which providers they've connected
// can change at any time via the dashboard's own signup/onboarding flow.
func New(store *db.Store, p *paths.Paths, cfg *config.Config, rootKey crypto.Key) *Server {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	// Recover turns a panicking handler into a logged 500 response instead
	// of crashing the whole process — the "graceful handling of hard
	// crashes" middleware this package needs.
	e.Use(middleware.Recover())
	e.Use(middleware.Logger())

	s := &Server{
		echo: e, store: store, DB: store.DB(), Paths: p, Config: cfg, RootKey: rootKey,
		googleSyncers: map[int64]*googlehealth.DBSyncer{},
		cronoSyncers:  map[int64]*cronometer.DBSyncer{},
	}
	s.registerRoutes()
	return s
}

// googleClientJSON loads the app-wide Google OAuth client JSON (see
// RootKey's doc comment). Returns googleauth.ErrMissingClientCredentials
// if none has been uploaded yet — callers show that as an actionable
// message, not a generic error.
func (s *Server) googleClientJSON() ([]byte, error) {
	return googleauth.LoadClientJSON(s.Paths.GoogleClientSecretFile(), s.RootKey)
}

// userGoogleSyncer returns userID's Google Health syncer, building and
// caching it on first use. nil means this user hasn't connected Google
// Health (or their token file is unreadable) — callers must handle that,
// not treat it as an error.
func (s *Server) userGoogleSyncer(userID int64) *googlehealth.DBSyncer {
	s.googleMu.Lock()
	defer s.googleMu.Unlock()
	if syncer, ok := s.googleSyncers[userID]; ok {
		return syncer
	}
	syncer := s.buildGoogleSyncer(userID)
	s.googleSyncers[userID] = syncer
	return syncer
}

func (s *Server) buildGoogleSyncer(userID int64) *googlehealth.DBSyncer {
	tokenPath := s.Paths.UserGoogleOAuthFile(userID)
	if _, err := os.Stat(tokenPath); err != nil {
		return nil
	}
	key, err := webauth.CredentialKey(s.Paths, userID)
	if err != nil {
		return nil
	}
	clientJSON, err := s.googleClientJSON()
	if err != nil {
		return nil
	}
	client, err := googleauth.HTTPClient(context.Background(), tokenPath, key, clientJSON, s.Config.Google.CallbackPort)
	if err != nil {
		return nil
	}
	return &googlehealth.DBSyncer{Client: googlehealth.NewClient(client), DB: s.DB, UserID: userID}
}

// setGoogleSyncer overwrites the cached syncer for userID — called right
// after a successful web-triggered Google connect (see routes.go) so the
// next force-sync click picks it up without a restart.
func (s *Server) setGoogleSyncer(userID int64, syncer *googlehealth.DBSyncer) {
	s.googleMu.Lock()
	s.googleSyncers[userID] = syncer
	s.googleMu.Unlock()
}

// userCronometerSyncer mirrors userGoogleSyncer for Cronometer.
func (s *Server) userCronometerSyncer(userID int64) *cronometer.DBSyncer {
	s.cronoMu.Lock()
	defer s.cronoMu.Unlock()
	if syncer, ok := s.cronoSyncers[userID]; ok {
		return syncer
	}
	syncer := s.buildCronometerSyncer(userID)
	s.cronoSyncers[userID] = syncer
	return syncer
}

func (s *Server) buildCronometerSyncer(userID int64) *cronometer.DBSyncer {
	credPath := s.Paths.UserCronometerCredentialsFile(userID)
	if _, err := os.Stat(credPath); err != nil {
		return nil
	}
	key, err := webauth.CredentialKey(s.Paths, userID)
	if err != nil {
		return nil
	}
	syncer, err := cronometer.NewDBSyncer(s.DB, userID, key, credPath, s.Paths.UserCronometerSessionFile(userID))
	if err != nil {
		return nil
	}
	return syncer
}

// setCronometerSyncer overwrites the cached syncer for userID — called
// right after a successful Cronometer login (see cronometer.go's
// handleCronometerLogin) so a running server picks up the login
// immediately, no restart needed for the next Sync click to cover it.
func (s *Server) setCronometerSyncer(userID int64, syncer *cronometer.DBSyncer) {
	s.cronoMu.Lock()
	s.cronoSyncers[userID] = syncer
	s.cronoMu.Unlock()
}

// cronometerConnected reports whether userID has Cronometer credentials on
// file — drives the Nutrition section's account card (login form vs.
// connected status) and whether the Sync button's force-sync also covers
// Cronometer.
func (s *Server) cronometerConnected(userID int64) bool {
	return s.userCronometerSyncer(userID) != nil
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
			// Opportunistic, not on its own goroutine/timer — piggybacking
			// on the checkpoint tick is enough to keep web_session from
			// growing unbounded; a few minutes' delay in sweeping an
			// already-rejected expired session is harmless (see
			// webauth.LookupSession, which refuses expired rows itself).
			if err := webauth.CleanupExpired(ctx, s.DB); err != nil {
				fmt.Println("warning: cleaning up expired sessions:", err)
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
