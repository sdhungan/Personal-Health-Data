package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/googlehealth"
	"github.com/sdhungan/Personal-Health-Data/internal/syncengine"
	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

// maxFirstBulkSyncDays caps a single explicit-range bulk sync for an
// account with no google_health sync_state history yet (see
// handleGoogleBulkSync) — a big historical pull is many real Google Health
// API calls per day (handleForceSync's own doc comment: 15-20+ seconds for
// one day), so an unbounded range risks a very long-running background
// goroutine for one click. Once an account has any history, the "catch up
// to today" path has no such cap — see handleGoogleClientSettingsPage's
// hasHistory check.
const maxFirstBulkSyncDays = 30

// validateBulkSyncRange checks and normalizes a first-time bulk sync's
// requested (start, end) range against now: end can't be before start,
// end is clamped down to now rather than syncing days that haven't happened
// yet, and the whole range (inclusive of both ends) can't exceed
// maxFirstBulkSyncDays — the error message is exactly the one shown on the
// settings page, telling the user to split a longer backfill into multiple
// smaller ranges instead.
func validateBulkSyncRange(start, end, now time.Time) (time.Time, time.Time, error) {
	if end.Before(start) {
		return start, end, errors.New("end date can't be before the start date")
	}
	if end.After(now) {
		end = now
	}
	if days := int(end.Sub(start).Hours()/24) + 1; days > maxFirstBulkSyncDays {
		return start, end, fmt.Errorf(
			"that range is %d days — the maximum is %d per bulk sync. Big bulk syncs are hard on the API; run this again with additional %d-day-or-less ranges instead",
			days, maxFirstBulkSyncDays, maxFirstBulkSyncDays)
	}
	return start, end, nil
}

// handleGoogleClientSettingsPage serves the app-wide Google OAuth client
// status (see views.GoogleClientSettingsPage's doc comment — the client
// itself is not a per-user credential) alongside the *current* account's
// own Google Health connection status and a "Connect Google Health"
// action — connecting isn't onboarding-only, same as Cronometer's ongoing
// account-settings card. The upload form only appears when
// GoogleClientLockedByFlag is false: once --google-client-secret has
// successfully configured the client for this run, the dashboard is no
// longer a way to change it (see GoogleClientLockedByFlag's doc comment).
// Reachable by any logged-in account; there's no admin/role system yet to
// gate the (now conditional) upload half of this page further. ?uploaded=1
// (set by the redirect at the end of handleGoogleClientUpload) shows an
// explicit success confirmation — without it, replacing an
// already-configured client with a new file redirected back to a page
// that just said "a client is configured" either way, which looked
// identical to before and gave no sign the upload actually took effect.
func (s *Server) handleGoogleClientSettingsPage(c echo.Context) error {
	userID := webauth.CurrentUserID(c)
	_, err := s.googleClientJSON()
	configured := err == nil

	message := ""
	if configured && c.QueryParam("uploaded") == "1" {
		message = "Uploaded — every account's \"Connect Google Health\" now uses this client."
	}

	if configured && c.QueryParam("connected") == "1" {
		message = "Connected — Google Health data will be included in your next sync."
	}

	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return views.GoogleClientSettingsPage(configured, GoogleClientLockedByFlag, message, "", webauth.CurrentUsername(c), s.userGoogleSyncer(userID) != nil, s.googleBulkSyncStatus(c.Request().Context(), userID)).
		Render(c.Request().Context(), c.Response())
}

// googleBulkSyncStatus builds the settings page's bulk-sync section: any
// in-flight or just-finished run for userID (see Server.bulkSyncs), plus
// whether this account has any google_health sync_state history at all —
// the switch between the one-click "catch up to today" form and the
// date-range picker (see views.GoogleBulkSyncStatus's doc comment).
func (s *Server) googleBulkSyncStatus(ctx context.Context, userID int64) views.GoogleBulkSyncStatus {
	status := views.GoogleBulkSyncStatus{Today: time.Now().Format(dateLayout)}
	if v, ok := s.bulkSyncs.Load(userID); ok {
		status.Running, status.Summary, status.Err = v.(*bulkSyncStatus).snapshot()
	}
	state := &syncengine.SQLStore{DB: s.DB, UserID: userID}
	if _, ok, err := state.LatestDay(ctx, googleHealthSource); err == nil {
		status.HasHistory = ok
	}
	return status
}

// handleGoogleClientUpload validates and saves an uploaded client_secret
// JSON, replacing whatever was configured before — see
// googleauth.SaveClientJSON for the validation (rejects a file that
// doesn't actually parse as a Google OAuth client JSON before it can break
// every account's Google connection). Rejected outright once
// GoogleClientLockedByFlag is true — the dashboard's upload form is
// already hidden in that case, but the handler refuses independently of
// the UI too, since --google-client-secret is meant to be the only place
// this changes once it's successfully set that way (see
// GoogleClientLockedByFlag's doc comment).
func (s *Server) handleGoogleClientUpload(c echo.Context) error {
	if GoogleClientLockedByFlag {
		return c.String(http.StatusForbidden,
			"the Google OAuth client is configured via --google-client-secret and can't be changed from the dashboard — restart the service with a different path to replace it")
	}

	ctx := c.Request().Context()
	userID := webauth.CurrentUserID(c)

	renderErr := func(msg string) error {
		_, err := s.googleClientJSON()
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		return views.GoogleClientSettingsPage(err == nil, GoogleClientLockedByFlag, "", msg, webauth.CurrentUsername(c), s.userGoogleSyncer(userID) != nil, s.googleBulkSyncStatus(ctx, userID)).Render(ctx, c.Response())
	}

	fileHeader, err := c.FormFile("client_json")
	if err != nil {
		return renderErr("Choose a file to upload.")
	}
	file, err := fileHeader.Open()
	if err != nil {
		return renderErr("Reading uploaded file failed: " + err.Error())
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return renderErr("Reading uploaded file failed: " + err.Error())
	}

	if err := googleauth.SaveClientJSON(s.Paths.GoogleClientSecretFile(), s.RootKey, data); err != nil {
		return renderErr(err.Error())
	}

	return c.Redirect(http.StatusFound, "/settings/google-client?uploaded=1")
}

// handleGoogleClientConnectAccount starts the OAuth consent flow (see
// startGoogleConnect in auth.go — the same one onboarding uses) for the
// currently logged-in account, triggered from this settings page instead
// of /onboarding/connect, and redirects the browser there right away — the
// flow itself finishes asynchronously, landing back on this page (with
// ?connected=1) once Google's own redirect reaches the local callback
// listener.
func (s *Server) handleGoogleClientConnectAccount(c echo.Context) error {
	userID := webauth.CurrentUserID(c)

	authURL, err := s.startGoogleConnect(userID, "/settings/google-client?connected=1")
	if err != nil {
		_, clientErr := s.googleClientJSON()
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		return views.GoogleClientSettingsPage(clientErr == nil, GoogleClientLockedByFlag, "", err.Error(), webauth.CurrentUsername(c), false, s.googleBulkSyncStatus(c.Request().Context(), userID)).Render(c.Request().Context(), c.Response())
	}
	return c.Redirect(http.StatusFound, authURL)
}

// handleGoogleBulkSync starts a background Google Health bulk sync for the
// current account: a one-click "catch up to today" pass (reusing
// syncengine.RunDay's own backfill-and-resolve logic, forced immediately
// instead of waiting for the next scheduler tick) once this account has any
// google_health sync_state history, or an explicit start/end date range —
// capped at maxFirstBulkSyncDays — for a brand-new connection with none
// yet. Only one bulk sync per user runs at a time, tracked via
// Server.bulkSyncs (same "duplicate click just re-shows in-flight state"
// posture as handleForceSync's syncingDays); the run itself happens in a
// goroutine against context.Background() so it survives the request/browser
// tab, same reasoning as handleForceSync.
func (s *Server) handleGoogleBulkSync(c echo.Context) error {
	userID := webauth.CurrentUserID(c)
	ctx := c.Request().Context()

	renderErr := func(msg string) error {
		bulk := s.googleBulkSyncStatus(ctx, userID)
		bulk.Err = msg
		_, clientErr := s.googleClientJSON()
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		return views.GoogleClientSettingsPage(clientErr == nil, GoogleClientLockedByFlag, "", "", webauth.CurrentUsername(c), s.userGoogleSyncer(userID) != nil, bulk).Render(ctx, c.Response())
	}

	googleSync := s.userGoogleSyncer(userID)
	if googleSync == nil {
		return renderErr("Connect Google Health above first.")
	}

	state := &syncengine.SQLStore{DB: s.DB, UserID: userID}
	_, hasHistory, err := state.LatestDay(ctx, googleHealthSource)
	if err != nil {
		return renderErr(err.Error())
	}

	var start, end time.Time
	if !hasHistory {
		start, err = time.ParseInLocation(dateLayout, c.FormValue("start"), time.Local)
		if err != nil {
			return renderErr("Choose a valid start date.")
		}
		end, err = time.ParseInLocation(dateLayout, c.FormValue("end"), time.Local)
		if err != nil {
			return renderErr("Choose a valid end date.")
		}
		start, end, err = validateBulkSyncRange(start, end, time.Now())
		if err != nil {
			return renderErr(err.Error())
		}
	}

	status := &bulkSyncStatus{running: true}
	if existing, loaded := s.bulkSyncs.LoadOrStore(userID, status); loaded {
		if running, _, _ := existing.(*bulkSyncStatus).snapshot(); running {
			return renderErr("A bulk sync is already running for this account — wait for it to finish first.")
		}
		s.bulkSyncs.Store(userID, status)
	}

	if hasHistory {
		go s.runGoogleCatchUpSync(userID, googleSync, status)
	} else {
		go s.runGoogleRangeSync(userID, googleSync, start, end, status)
	}

	return c.Redirect(http.StatusFound, "/settings/google-client")
}

// runGoogleCatchUpSync walks every single day from this account's earliest
// ever-tracked google_health day (syncengine.SQLStore.EarliestDay — "the
// first day of viewing") through today, so a gap anywhere in that history —
// not just at the tail end — gets filled in. This deliberately does more
// work than syncengine.RunDay's own automatic pass: RunDay only ever
// backfills forward from the single latest tracked day and resolves the
// pending/partial chain right below today, so a hole stranded further back
// (e.g. this account's very first explicit bulk sync only ever covered a
// recent window, or the scheduler was down for a stretch) would never
// self-heal on its own — see the doc comment on backfillPendingDays in
// syncengine/engine.go. A day already resolved (StatusComplete or
// StatusMissing) is left untouched, same as RunDay, so re-running this
// costs nothing beyond one cheap status lookup per already-good day.
func (s *Server) runGoogleCatchUpSync(userID int64, syncer *googlehealth.DBSyncer, status *bulkSyncStatus) {
	bgCtx := context.Background()
	state := &syncengine.SQLStore{DB: s.DB, UserID: userID}
	s.Logger.Info("google_health bulk sync (catch-up) started", zap.Int64("user_id", userID))

	// finish wraps status.finish (which only feeds the settings page's own
	// polling UI) so every return path — success or failure — also leaves
	// a trace in the persistent log, same reasoning as the scheduler's own
	// per-user logging (internal/cli/googlehealthsync.go): this used to be
	// visible only to whoever happened to be looking at the settings page
	// at that exact moment, nothing after the fact.
	finish := func(summary string, err error) {
		if err != nil {
			s.Logger.Error("google_health bulk sync (catch-up) failed", zap.Int64("user_id", userID),
				zap.Bool("auth_expired", googleauth.IsInvalidGrant(err)), zap.Error(err))
		} else {
			s.Logger.Info("google_health bulk sync (catch-up) complete", zap.Int64("user_id", userID), zap.String("summary", summary))
		}
		status.finish(summary, err)
	}

	earliestStr, ok, err := state.EarliestDay(bgCtx, googleHealthSource)
	if err != nil {
		finish("", err)
		return
	}
	if !ok {
		finish("No history to catch up yet.", nil)
		return
	}
	earliest, err := time.ParseInLocation(dateLayout, earliestStr, time.Local)
	if err != nil {
		finish("", fmt.Errorf("parsing earliest tracked day %q: %w", earliestStr, err))
		return
	}

	now := time.Now()
	todayStr := now.Format(dateLayout)
	skipped, synced, missing := 0, 0, 0
	for d := earliest; !d.After(now); d = d.AddDate(0, 0, 1) {
		dayStr := d.Format(dateLayout)

		existing, hasRow, serr := state.Status(bgCtx, googleHealthSource, dayStr)
		if serr != nil {
			finish(fmt.Sprintf("Stopped at %s: %v", dayStr, serr), serr)
			return
		}
		if hasRow && existing != syncengine.StatusPending && existing != syncengine.StatusPartial {
			skipped++
			continue // already resolved (complete or confirmed missing) — leave it
		}

		hasData, err := syncer.SyncDay(bgCtx, d)
		if err != nil {
			if googleauth.IsInvalidGrant(err) {
				s.setGoogleSyncer(userID, nil)
			}
			finish(fmt.Sprintf("Stopped at %s after covering %d day(s): %v", dayStr, skipped+synced+missing, err), err)
			return
		}

		newStatus := syncengine.StatusComplete
		if !hasData {
			newStatus = syncengine.StatusMissing
			missing++
		} else {
			synced++
		}
		if dayStr == todayStr {
			newStatus = syncengine.StatusPartial
		}
		s.recordSyncState(bgCtx, state, googleHealthSource, dayStr, newStatus)
	}

	finish(fmt.Sprintf(
		"Checked every day since %s: %d already up to date, %d newly synced, %d had no data.",
		earliestStr, skipped, synced, missing), nil)
}

// runGoogleRangeSync backfills one explicit day range for an account with
// no google_health sync_state history yet — syncengine.RunDay's own
// backfill only ever reaches back to yesterday on a source's first run
// (see that package's backfillPendingDays), so a real historical pull needs
// its own day-by-day loop instead, mirroring handleForceSync's per-day
// status bookkeeping.
func (s *Server) runGoogleRangeSync(userID int64, syncer *googlehealth.DBSyncer, start, end time.Time, status *bulkSyncStatus) {
	bgCtx := context.Background()
	state := &syncengine.SQLStore{DB: s.DB, UserID: userID}
	todayStr := time.Now().Format(dateLayout)
	s.Logger.Info("google_health bulk sync (range) started", zap.Int64("user_id", userID),
		zap.String("start", start.Format(dateLayout)), zap.String("end", end.Format(dateLayout)))

	// See runGoogleCatchUpSync's identical finish wrapper for why.
	finish := func(summary string, err error) {
		if err != nil {
			s.Logger.Error("google_health bulk sync (range) failed", zap.Int64("user_id", userID),
				zap.Bool("auth_expired", googleauth.IsInvalidGrant(err)), zap.Error(err))
		} else {
			s.Logger.Info("google_health bulk sync (range) complete", zap.Int64("user_id", userID), zap.String("summary", summary))
		}
		status.finish(summary, err)
	}

	synced, missing := 0, 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		dayStr := d.Format(dateLayout)
		hasData, err := syncer.SyncDay(bgCtx, d)
		if err != nil {
			if googleauth.IsInvalidGrant(err) {
				s.setGoogleSyncer(userID, nil)
			}
			finish(fmt.Sprintf("Stopped at %s after syncing %d day(s): %v", dayStr, synced, err), err)
			return
		}

		syncStatus := syncengine.StatusComplete
		if !hasData {
			syncStatus = syncengine.StatusMissing
			missing++
		} else {
			synced++
		}
		if dayStr == todayStr {
			syncStatus = syncengine.StatusPartial
		}
		s.recordSyncState(bgCtx, state, googleHealthSource, dayStr, syncStatus)
	}

	finish(fmt.Sprintf("Synced %s through %s: %d day(s) had data, %d had none.", start.Format(dateLayout), end.Format(dateLayout), synced, missing), nil)
}
