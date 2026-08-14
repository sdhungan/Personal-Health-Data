package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/starfederation/datastar-go/datastar"

	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/syncengine"
	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

func parseDay(c echo.Context) time.Time {
	s := c.QueryParam("day")
	if s == "" {
		return time.Now()
	}
	t, err := time.ParseInLocation(dateLayout, s, time.Local)
	if err != nil {
		return time.Now()
	}
	return t
}

// defaultExpandedKind reports whether kind should render already-expanded
// on a fresh dashboard load, rather than requiring a click every time — the
// handful of tiles worth seeing in full by default.
func defaultExpandedKind(kind string) bool {
	switch kind {
	case "steps", "heart_rate", "body", "sleep":
		return true
	default:
		return false
	}
}

func dayLabel(day time.Time) string {
	now := time.Now()
	d := day.Format(dateLayout)
	switch d {
	case now.Format(dateLayout):
		return "Today"
	case now.AddDate(0, 0, -1).Format(dateLayout):
		return "Yesterday"
	case now.AddDate(0, 0, 1).Format(dateLayout):
		return "Tomorrow"
	default:
		return day.Format("Mon, Jan 2")
	}
}

func buildDashboardData(ctx context.Context, db *sql.DB, userID int64, day time.Time, view string, tileKinds []string, cronometerConnected bool) (views.DashboardData, error) {
	dayStr := day.Format(dateLayout)
	data := views.DashboardData{
		Day:                 dayStr,
		DayLabel:            dayLabel(day),
		PrevDay:             day.AddDate(0, 0, -1).Format(dateLayout),
		NextDay:             day.AddDate(0, 0, 1).Format(dateLayout),
		Today:               time.Now().Format(dateLayout),
		View:                view,
		CronometerConnected: cronometerConnected,
		MissingByCategory:   map[string][]string{},
	}

	today, err := fetchDailySummaryRow(ctx, db, userID, dayStr)
	if err != nil {
		return data, err
	}
	history, err := fetch7DayRows(ctx, db, userID, day)
	if err != nil {
		return data, err
	}

	for _, kind := range tileKinds {
		t := views.TileData{ID: "tile-" + kind, Metric: kind, Expanded: defaultExpandedKind(kind)}
		switch {
		case kind == "activities":
			t, err = buildActivitiesTile(ctx, db, userID, t, day, t.Expanded)
		case kind == "body":
			t, err = buildBodyTile(ctx, db, userID, t, day)
		case kind == "weight" || kind == "waist" || kind == "neck" || kind == "body_fat":
			t, err = buildBodyMeasurementStatTile(ctx, db, userID, t, kind, day, t.Expanded)
		case kind == "hr_zones":
			t, err = buildHeartRateZonesTile(ctx, db, userID, t, day, t.Expanded)
		case kind == "active_minutes_by_level":
			t, err = buildActiveMinutesByLevelTile(ctx, db, userID, t, day, t.Expanded)
		case kind == "active_zone_minutes_by_zone":
			t, err = buildActiveZoneMinutesByZoneTile(ctx, db, userID, t, day, t.Expanded)
		case kind == "activity_level":
			t, err = buildActivityLevelSegmentTile(ctx, db, userID, t, day, t.Expanded)
		case kind == "food_log":
			t, err = buildFoodLogTile(ctx, db, userID, t, day, t.Expanded)
		default:
			if _, ok := metricDefs[kind]; !ok {
				continue
			}
			t, err = buildStatTile(ctx, db, userID, t, kind, day, t.Expanded, today, history)
		}
		if err != nil {
			return data, err
		}
		if shouldHideEmptyTile(t) {
			if t.Title != "" {
				data.MissingByCategory[t.Category] = append(data.MissingByCategory[t.Category], t.Title)
			}
			continue
		}
		data.Tiles = append(data.Tiles, t)
	}
	return data, nil
}

// shouldHideEmptyTile reports whether t has no data at all for the day and
// should be omitted from the dashboard entirely — rather than rendered as
// an empty "No X recorded" placeholder — so the page only shows metrics
// this account/watch actually produces. Body Measurements is exempt: it's
// an input form, not a read-only stat, so it always needs to stay
// reachable even before anything's been entered.
func shouldHideEmptyTile(t views.TileData) bool {
	switch t.Kind {
	case views.TileKindBody:
		return false
	case views.TileKindActivities:
		return len(t.Activities) == 0
	case views.TileKindFoodLog:
		return len(t.FoodLog) == 0
	default:
		return t.Empty
	}
}

// handleIndex serves the full page on a normal (non-SSE) GET, always
// starting on the "data" view.
func (s *Server) handleIndex(c echo.Context) error {
	ctx := c.Request().Context()
	day := parseDay(c)
	userID := webauth.CurrentUserID(c)

	data, err := buildDashboardData(ctx, s.DB, userID, day, "data", DefaultTileKinds, s.cronometerConnected(userID))
	if err != nil {
		return c.String(http.StatusInternalServerError, err.Error())
	}
	data.Username = webauth.CurrentUsername(c)

	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return views.Page(data).Render(ctx, c.Response())
}

// handleView answers day-navigation and data/journal tab switches by
// patching #view-body via SSE.
func (s *Server) handleView(c echo.Context) error {
	ctx := c.Request().Context()
	day := parseDay(c)
	view := c.QueryParam("view")
	if view != "journal" {
		view = "data"
	}
	userID := webauth.CurrentUserID(c)

	sse := datastar.NewSSE(c.Response(), c.Request())

	if view == "journal" {
		j, err := fetchJournal(ctx, s.DB, userID, day.Format(dateLayout))
		if err != nil {
			return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
		}
		data := views.DashboardData{
			Day: day.Format(dateLayout), DayLabel: dayLabel(day),
			PrevDay: day.AddDate(0, 0, -1).Format(dateLayout), NextDay: day.AddDate(0, 0, 1).Format(dateLayout),
			Today: time.Now().Format(dateLayout),
			View:  "journal",
		}
		return sse.PatchElementTempl(views.JournalViewBody(data, j))
	}

	data, err := buildDashboardData(ctx, s.DB, userID, day, "data", DefaultTileKinds, s.cronometerConnected(userID))
	if err != nil {
		return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
	}
	return sse.PatchElementTempl(views.DataViewBody(data))
}

// handleTile answers a tile's expand/collapse button by patching that one
// tile via SSE, identified by its own id — nothing else on the page moves.
func (s *Server) handleTile(c echo.Context) error {
	ctx := c.Request().Context()
	day := parseDay(c)
	kind := c.QueryParam("kind")
	expanded := c.QueryParam("expanded") == "true"
	userID := webauth.CurrentUserID(c)

	sse := datastar.NewSSE(c.Response(), c.Request())

	t := views.TileData{ID: "tile-" + kind, Metric: kind, Expanded: expanded}
	var err error
	switch {
	case kind == "activities":
		t, err = buildActivitiesTile(ctx, s.DB, userID, t, day, expanded)
	case kind == "body":
		t, err = buildBodyTile(ctx, s.DB, userID, t, day)
	case kind == "weight" || kind == "waist" || kind == "neck" || kind == "body_fat":
		t, err = buildBodyMeasurementStatTile(ctx, s.DB, userID, t, kind, day, expanded)
	// hr_zones was missing from this switch even before this pass (found
	// and flagged as an out-of-scope bug earlier this session) — fixed here
	// since active_minutes_by_level/active_zone_minutes_by_zone/
	// activity_level need the exact same wiring added right next to it.
	case kind == "hr_zones":
		t, err = buildHeartRateZonesTile(ctx, s.DB, userID, t, day, expanded)
	case kind == "active_minutes_by_level":
		t, err = buildActiveMinutesByLevelTile(ctx, s.DB, userID, t, day, expanded)
	case kind == "active_zone_minutes_by_zone":
		t, err = buildActiveZoneMinutesByZoneTile(ctx, s.DB, userID, t, day, expanded)
	case kind == "activity_level":
		t, err = buildActivityLevelSegmentTile(ctx, s.DB, userID, t, day, expanded)
	case kind == "food_log":
		t, err = buildFoodLogTile(ctx, s.DB, userID, t, day, expanded)
	default:
		if _, ok := metricDefs[kind]; !ok {
			return c.String(http.StatusNotFound, "unknown tile kind")
		}
		today, ferr := fetchDailySummaryRow(ctx, s.DB, userID, day.Format(dateLayout))
		if ferr != nil {
			err = ferr
			break
		}
		history, ferr := fetch7DayRows(ctx, s.DB, userID, day)
		if ferr != nil {
			err = ferr
			break
		}
		t, err = buildStatTile(ctx, s.DB, userID, t, kind, day, expanded, today, history)
	}
	if err != nil {
		return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
	}
	return sse.PatchElementTempl(views.Tile(t, day.Format(dateLayout)))
}

// handleForceSync answers the dashboard's "Sync" button: an on-demand,
// unconditional sync of one day's data from every connected source
// (Google Health always, Cronometer too once its account card shows
// connected) — the manual override to the day-completeness engine's
// normal pending/partial-only automatic sync (internal/syncengine), useful
// both for filling in a day the automatic pass gave up on and for testing
// without waiting for the cron schedule. The two sources run concurrently
// (see runSource below) rather than one after the other, since they're
// deliberately independent (schema.sql: "a Cronometer outage/breakage
// never touches watch data or vice versa") and there's no reason to make
// the button wait twice as long.
//
// A full day's sync takes many real API calls (heart-rate alone can be
// thousands of samples) and can run 15-20+ seconds, so this must not hold
// the request/DB up for that whole time: it kicks the sync off in a
// goroutine (using its own background context, so it keeps running and
// keeps writing even if the browser navigates away or the SSE connection
// drops), immediately morphs the Sync button into a spinner, and only
// morphs the full view back in once the sync actually finishes. Only the
// specific (user, day) pair being synced is serialized (via s.syncingDays)
// — a duplicate click just re-shows the spinner rather than starting a
// second concurrent sync of the same day; every other day (or other
// user's same day) keeps working normally, and WAL mode (internal/db.Store)
// keeps normal reads from ever blocking on this write.
func (s *Server) handleForceSync(c echo.Context) error {
	reqCtx := c.Request().Context()
	day := parseDay(c)
	dayStr := day.Format(dateLayout)
	view := c.QueryParam("view")
	if view != "journal" {
		view = "data"
	}
	userID := webauth.CurrentUserID(c)
	syncKey := fmt.Sprintf("%d/%s", userID, dayStr)

	sse := datastar.NewSSE(c.Response(), c.Request())

	googleSync := s.userGoogleSyncer(userID)
	if googleSync == nil {
		return sse.PatchElementTempl(views.ErrorFragment(`Google Health isn't authorized yet — connect it from the onboarding/account page first.`))
	}

	if _, alreadySyncing := s.syncingDays.LoadOrStore(syncKey, struct{}{}); alreadySyncing {
		return sse.PatchElementTempl(views.SyncButton(dayStr, view, true))
	}

	if err := sse.PatchElementTempl(views.SyncButton(dayStr, view, true)); err != nil {
		s.syncingDays.Delete(syncKey)
		return err
	}

	type syncResult struct {
		hasData bool
		err     error
	}
	// runSource syncs one source's day and records the resulting
	// sync_state status — today always stays "partial" regardless of what
	// was found, since it's still in progress by definition and a manual
	// sync doesn't change that (see internal/syncengine.RunDay's doc
	// comment on why today is never auto-promoted).
	runSource := func(bgCtx context.Context, source string, syncer syncengine.DaySyncer, state *syncengine.SQLStore) syncResult {
		hasData, err := syncer.SyncDay(bgCtx, day)
		if err == nil {
			status := syncengine.StatusComplete
			if !hasData {
				status = syncengine.StatusMissing
			}
			if dayStr == time.Now().Format(dateLayout) {
				status = syncengine.StatusPartial
			}
			if serr := state.EnsurePending(bgCtx, source, dayStr); serr == nil {
				_ = state.SetStatus(bgCtx, source, dayStr, status)
			}
		}
		return syncResult{hasData, err}
	}

	done := make(chan syncResult, 1)
	go func() {
		defer s.syncingDays.Delete(syncKey)
		bgCtx := context.Background()
		state := &syncengine.SQLStore{DB: s.DB, UserID: userID}

		var wg sync.WaitGroup
		var googleRes syncResult
		wg.Add(1)
		go func() {
			defer wg.Done()
			googleRes = runSource(bgCtx, googleHealthSource, googleSync, state)
		}()

		var cronoRes syncResult
		cronometerSync := s.userCronometerSyncer(userID)
		cronoConnected := cronometerSync != nil
		if cronoConnected {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cronoRes = runSource(bgCtx, cronometerSource, cronometerSync, state)
			}()
		}
		wg.Wait()

		// Cronometer failing is reported (stderr) but never blocks the
		// button on Google Health's own result — same non-fatal posture
		// internal/cli/sync.go already takes for the scheduled pass.
		if cronoConnected && cronoRes.err != nil {
			fmt.Fprintln(os.Stderr, "cronometer sync error:", cronoRes.err)
		}
		done <- googleRes
	}()

	select {
	case res := <-done:
		if res.err != nil {
			if googleauth.IsInvalidGrant(res.err) {
				// The refresh token behind the cached syncer is permanently
				// dead (expired/revoked at Google, or the token file was
				// already deleted by savingTokenSource.Token() on this same
				// error) — evict it so userGoogleSyncer stops reporting
				// "connected" everywhere else (settings page, onboarding)
				// and the reconnect button reappears without a service
				// restart.
				s.setGoogleSyncer(userID, nil)
				return sse.PatchElementTempl(views.ErrorFragment("Google Health access expired or was revoked — reconnect it from Settings → Google OAuth client."))
			}
			return sse.PatchElementTempl(views.ErrorFragment("force sync failed: " + res.err.Error()))
		}
		return s.patchCurrentView(sse, context.Background(), userID, day, dayStr, view)
	case <-reqCtx.Done():
		// The client disconnected/navigated away — the goroutine above
		// keeps running against context.Background() and will finish the
		// write regardless; there's just no one left here to show the
		// result to.
		return nil
	}
}

// patchCurrentView re-renders and patches the data or journal view for
// day — the "reload the page on the day the sync happened on" behavior
// once a force-sync completes.
func (s *Server) patchCurrentView(sse *datastar.ServerSentEventGenerator, ctx context.Context, userID int64, day time.Time, dayStr, view string) error {
	if view == "journal" {
		j, err := fetchJournal(ctx, s.DB, userID, dayStr)
		if err != nil {
			return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
		}
		data := views.DashboardData{
			Day: dayStr, DayLabel: dayLabel(day),
			PrevDay: day.AddDate(0, 0, -1).Format(dateLayout), NextDay: day.AddDate(0, 0, 1).Format(dateLayout),
			Today: time.Now().Format(dateLayout),
			View:  "journal",
		}
		return sse.PatchElementTempl(views.JournalViewBody(data, j))
	}

	data, err := buildDashboardData(ctx, s.DB, userID, day, "data", DefaultTileKinds, s.cronometerConnected(userID))
	if err != nil {
		return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
	}
	return sse.PatchElementTempl(views.DataViewBody(data))
}

// handleActivity answers a click on an activity list item, patching the
// detail overlay's content (visibility is toggled client-side by the
// $activityOpen signal the button also sets).
func (s *Server) handleActivity(c echo.Context) error {
	id, err := parseInt64(c.QueryParam("id"))
	if err != nil {
		return c.String(http.StatusBadRequest, "invalid activity id")
	}
	userID := webauth.CurrentUserID(c)

	sse := datastar.NewSSE(c.Response(), c.Request())
	detail, err := fetchActivityDetail(c.Request().Context(), s.DB, userID, id)
	if err != nil {
		return sse.PatchElementTempl(views.ActivityDetailError(err.Error()))
	}
	return sse.PatchElementTempl(views.ActivityDetailFragment(*detail))
}

// handleFoodServing answers a click on a food-log list item, the same
// detail-overlay pattern handleActivity uses (see #detail-panel/$detailOpen
// in layout.templ).
func (s *Server) handleFoodServing(c echo.Context) error {
	id, err := parseInt64(c.QueryParam("id"))
	if err != nil {
		return c.String(http.StatusBadRequest, "invalid food serving id")
	}
	userID := webauth.CurrentUserID(c)

	sse := datastar.NewSSE(c.Response(), c.Request())
	detail, err := fetchFoodServingDetail(c.Request().Context(), s.DB, userID, id)
	if err != nil {
		return sse.PatchElementTempl(views.FoodServingDetailError(err.Error()))
	}
	return sse.PatchElementTempl(views.FoodServingDetailFragment(*detail))
}

// handleJournalSave is the Datastar-driven autosave/manual-save path: it
// reads the current signals (including the textarea's bound content),
// saves, and patches back just the status + preview fragments.
func (s *Server) handleJournalSave(c echo.Context) error {
	ctx := c.Request().Context()
	day := c.QueryParam("day")
	if day == "" {
		day = time.Now().Format(dateLayout)
	}
	userID := webauth.CurrentUserID(c)

	// ReadSignals consumes the request body, so it must happen before
	// NewSSE — constructing the SSE generator first leaves nothing left to
	// read (confirmed the hard way: it fails with "invalid Read on closed
	// Body").
	var signals struct {
		JournalContent string `json:"journalcontent"`
	}
	readErr := datastar.ReadSignals(c.Request(), &signals)

	sse := datastar.NewSSE(c.Response(), c.Request())
	if readErr != nil {
		return sse.PatchElementTempl(views.JournalStatusFragment(views.JournalData{Day: day, Error: "reading request: " + readErr.Error()}))
	}

	j := views.JournalData{Day: day, AutoSave: true, Content: signals.JournalContent}
	if err := saveJournal(ctx, s.DB, userID, day, signals.JournalContent); err != nil {
		j.Error = err.Error()
	} else {
		j.SavedAt = time.Now().Format("15:04:05")
		j.ContentHTML = renderMarkdown(signals.JournalContent)
	}

	if err := sse.PatchElementTempl(views.JournalStatusFragment(j)); err != nil {
		return err
	}
	return sse.PatchElementTempl(views.JournalPreviewFragment(j))
}

// handleJournalBeacon is the plain (non-SSE) counterpart used by
// navigator.sendBeacon on page unload — the browser doesn't process a
// response in that case, so this just saves and returns 204.
func (s *Server) handleJournalBeacon(c echo.Context) error {
	day := c.QueryParam("day")
	if day == "" {
		day = time.Now().Format(dateLayout)
	}
	userID := webauth.CurrentUserID(c)
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.NoContent(http.StatusBadRequest)
	}
	if err := saveJournal(c.Request().Context(), s.DB, userID, day, string(body)); err != nil {
		return c.NoContent(http.StatusInternalServerError)
	}
	return c.NoContent(http.StatusNoContent)
}

func parseInt64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// handleBodyMeasurementSave answers the Body Measurements tile's per-field
// on-change save, mirroring handleJournalSave's read-signals-then-SSE
// pattern.
func (s *Server) handleBodyMeasurementSave(c echo.Context) error {
	ctx := c.Request().Context()
	day := c.QueryParam("day")
	if day == "" {
		day = time.Now().Format(dateLayout)
	}
	userID := webauth.CurrentUserID(c)

	// Datastar sends a number-input's bound signal as a bare JSON number
	// (e.g. 82.5) when it has a value, but as an empty string once the
	// field is cleared — json.RawMessage plus parseOptionalFloatRaw handles
	// both shapes (and a JSON null, just in case) rather than assuming one.
	var signals struct {
		BMWeight json.RawMessage `json:"bmweight"`
		BMHeight json.RawMessage `json:"bmheight"`
		BMWaist  json.RawMessage `json:"bmwaist"`
		BMNeck   json.RawMessage `json:"bmneck"`
	}
	readErr := datastar.ReadSignals(c.Request(), &signals)

	sse := datastar.NewSSE(c.Response(), c.Request())
	if readErr != nil {
		return sse.PatchElementTempl(views.ErrorFragment("reading request: " + readErr.Error()))
	}

	weight := parseOptionalFloatRaw(signals.BMWeight)
	height := parseOptionalFloatRaw(signals.BMHeight)
	waist := parseOptionalFloatRaw(signals.BMWaist)
	neck := parseOptionalFloatRaw(signals.BMNeck)
	if err := saveBodyMeasurement(ctx, s.DB, userID, day, weight, height, waist, neck); err != nil {
		return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
	}
	return s.patchBodyTile(sse, ctx, userID, day, "Saved "+time.Now().Format("15:04:05"))
}

// handleBodyMeasurementCarryForward answers the "Carry forward" button:
// fills whichever of today's weight/waist/neck are empty from the most
// recent earlier day that has a value for that specific field.
func (s *Server) handleBodyMeasurementCarryForward(c echo.Context) error {
	ctx := c.Request().Context()
	day := c.QueryParam("day")
	if day == "" {
		day = time.Now().Format(dateLayout)
	}
	userID := webauth.CurrentUserID(c)

	sse := datastar.NewSSE(c.Response(), c.Request())
	if err := carryForwardBodyMeasurement(ctx, s.DB, userID, day); err != nil {
		return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
	}
	return s.patchBodyTile(sse, ctx, userID, day, "Carried forward from a previous day")
}

// bodyDerivedTileKinds are every read-only stat tile whose value comes from
// the same body_measurement row the Body Measurements form just saved —
// patchBodyTile refreshes all of them alongside the form itself.
var bodyDerivedTileKinds = []string{"weight", "waist", "neck", "body_fat"}

// patchBodyTile re-fetches and patches #tile-body, then each of
// bodyDerivedTileKinds — the same "rebuild the one tile, patch it" shape
// handleTile uses, just repeated since weight/waist/neck/body_fat are all
// derived from the same fields this save just touched. If a derived tile was
// hidden entirely on the last full page/day-view load (nothing to show yet
// — see shouldHideEmptyTile), this patch targets a #tile-<kind> that doesn't
// exist in the DOM and silently no-ops; it only reappears on the next full
// view load. Not worth restructuring this handler's per-tile patches into a
// full buildDashboardData rebuild just for that one-time transition.
func (s *Server) patchBodyTile(sse *datastar.ServerSentEventGenerator, ctx context.Context, userID int64, day, savedAt string) error {
	dayTime, err := time.ParseInLocation(dateLayout, day, time.Local)
	if err != nil {
		dayTime = time.Now()
	}
	t := views.TileData{ID: "tile-body", Metric: "body"}
	t, err = buildBodyTile(ctx, s.DB, userID, t, dayTime)
	if err != nil {
		return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
	}
	if t.Body != nil {
		t.Body.SavedAt = savedAt
	}
	if err := sse.PatchElementTempl(views.Tile(t, day)); err != nil {
		return err
	}

	for _, kind := range bodyDerivedTileKinds {
		dt := views.TileData{ID: "tile-" + kind, Metric: kind}
		dt, err = buildBodyMeasurementStatTile(ctx, s.DB, userID, dt, kind, dayTime, false)
		if err != nil {
			return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
		}
		if err := sse.PatchElementTempl(views.Tile(dt, day)); err != nil {
			return err
		}
	}
	return nil
}

// parseOptionalFloatRaw converts a Datastar signal value that may arrive as
// a JSON number, a JSON string (numeric or blank), or null/absent into an
// optional float64 — blank/null means "clear this field, write NULL".
func parseOptionalFloatRaw(raw json.RawMessage) *float64 {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == `""` {
		return nil
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return &f
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		str = strings.TrimSpace(str)
		if str == "" {
			return nil
		}
		if v, err := strconv.ParseFloat(str, 64); err == nil {
			return &v
		}
	}
	return nil
}
