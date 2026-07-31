package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/starfederation/datastar-go/datastar"

	"github.com/sdhungan/Personal-Health-Data/internal/syncengine"
	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
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

func buildDashboardData(ctx context.Context, db *sql.DB, day time.Time, view string, tileKinds []string) (views.DashboardData, error) {
	dayStr := day.Format(dateLayout)
	data := views.DashboardData{
		Day:      dayStr,
		DayLabel: dayLabel(day),
		PrevDay:  day.AddDate(0, 0, -1).Format(dateLayout),
		NextDay:  day.AddDate(0, 0, 1).Format(dateLayout),
		View:     view,
	}

	today, err := fetchDailySummaryRow(ctx, db, dayStr)
	if err != nil {
		return data, err
	}
	history, err := fetch7DayRows(ctx, db, day)
	if err != nil {
		return data, err
	}

	for _, kind := range tileKinds {
		t := views.TileData{ID: "tile-" + kind, Metric: kind, Expanded: kind == "steps"}
		switch {
		case kind == "activities":
			t, err = buildActivitiesTile(ctx, db, t, day, t.Expanded)
		case kind == "body":
			t, err = buildBodyTile(ctx, db, t, day)
		case kind == "hr_zones":
			t, err = buildHeartRateZonesTile(ctx, db, t, day, t.Expanded)
		default:
			if _, ok := metricDefs[kind]; !ok {
				continue
			}
			t, err = buildStatTile(ctx, db, t, kind, day, t.Expanded, today, history)
		}
		if err != nil {
			return data, err
		}
		data.Tiles = append(data.Tiles, t)
	}
	return data, nil
}

// handleIndex serves the full page on a normal (non-SSE) GET, always
// starting on the "data" view.
func (s *Server) handleIndex(c echo.Context) error {
	ctx := c.Request().Context()
	day := parseDay(c)

	data, err := buildDashboardData(ctx, s.DB, day, "data", DefaultTileKinds)
	if err != nil {
		return c.String(http.StatusInternalServerError, err.Error())
	}

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

	sse := datastar.NewSSE(c.Response(), c.Request())

	if view == "journal" {
		j, err := fetchJournal(ctx, s.DB, day.Format(dateLayout))
		if err != nil {
			return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
		}
		data := views.DashboardData{
			Day: day.Format(dateLayout), DayLabel: dayLabel(day),
			PrevDay: day.AddDate(0, 0, -1).Format(dateLayout), NextDay: day.AddDate(0, 0, 1).Format(dateLayout),
			View: "journal",
		}
		return sse.PatchElementTempl(views.JournalViewBody(data, j))
	}

	data, err := buildDashboardData(ctx, s.DB, day, "data", DefaultTileKinds)
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

	sse := datastar.NewSSE(c.Response(), c.Request())

	t := views.TileData{ID: "tile-" + kind, Metric: kind, Expanded: expanded}
	var err error
	switch {
	case kind == "activities":
		t, err = buildActivitiesTile(ctx, s.DB, t, day, expanded)
	case kind == "body":
		t, err = buildBodyTile(ctx, s.DB, t, day)
	default:
		if _, ok := metricDefs[kind]; !ok {
			return c.String(http.StatusNotFound, "unknown tile kind")
		}
		today, ferr := fetchDailySummaryRow(ctx, s.DB, day.Format(dateLayout))
		if ferr != nil {
			err = ferr
			break
		}
		history, ferr := fetch7DayRows(ctx, s.DB, day)
		if ferr != nil {
			err = ferr
			break
		}
		t, err = buildStatTile(ctx, s.DB, t, kind, day, expanded, today, history)
	}
	if err != nil {
		return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
	}
	return sse.PatchElementTempl(views.Tile(t, day.Format(dateLayout)))
}

// handleForceSync answers the dashboard's "Sync" button: an on-demand,
// unconditional sync of one day's Google Health data — the manual
// override to the day-completeness engine's normal pending/partial-only
// automatic sync (internal/syncengine), useful both for filling in a day
// the automatic pass gave up on and for testing without waiting for the
// cron schedule.
//
// A full day's sync takes many real API calls (heart-rate alone can be
// thousands of samples) and can run 15-20+ seconds, so this must not hold
// the request/DB up for that whole time: it kicks the sync off in a
// goroutine (using its own background context, so it keeps running and
// keeps writing even if the browser navigates away or the SSE connection
// drops), immediately morphs the Sync button into a spinner, and only
// morphs the full view back in once the sync actually finishes. Only the
// specific day being synced is serialized (via s.syncingDays) — a
// duplicate click just re-shows the spinner rather than starting a second
// concurrent sync of the same day; every other day keeps working
// normally, and WAL mode (internal/db.Store) keeps normal reads from ever
// blocking on this write.
func (s *Server) handleForceSync(c echo.Context) error {
	reqCtx := c.Request().Context()
	day := parseDay(c)
	dayStr := day.Format(dateLayout)
	view := c.QueryParam("view")
	if view != "journal" {
		view = "data"
	}

	sse := datastar.NewSSE(c.Response(), c.Request())

	if s.googleSync == nil {
		return sse.PatchElementTempl(views.ErrorFragment(`Google Health isn't authorized yet — run "healthd auth google" first.`))
	}

	if _, alreadySyncing := s.syncingDays.LoadOrStore(dayStr, struct{}{}); alreadySyncing {
		return sse.PatchElementTempl(views.SyncButton(dayStr, view, true))
	}

	if err := sse.PatchElementTempl(views.SyncButton(dayStr, view, true)); err != nil {
		s.syncingDays.Delete(dayStr)
		return err
	}

	type syncResult struct {
		hasData bool
		err     error
	}
	done := make(chan syncResult, 1)
	go func() {
		defer s.syncingDays.Delete(dayStr)
		bgCtx := context.Background()

		hasData, err := s.googleSync.SyncDay(bgCtx, day)
		if err == nil {
			// Today must stay "partial" regardless — it's still in
			// progress by definition, and a manual sync doesn't change
			// that (see internal/syncengine.RunDay's doc comment on why
			// today is never auto-promoted).
			status := syncengine.StatusComplete
			if !hasData {
				status = syncengine.StatusMissing
			}
			if dayStr == time.Now().Format(dateLayout) {
				status = syncengine.StatusPartial
			}
			if serr := s.syncState.EnsurePending(bgCtx, googleHealthSource, dayStr); serr == nil {
				_ = s.syncState.SetStatus(bgCtx, googleHealthSource, dayStr, status)
			}
		}
		done <- syncResult{hasData, err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			return sse.PatchElementTempl(views.ErrorFragment("force sync failed: " + res.err.Error()))
		}
		return s.patchCurrentView(sse, context.Background(), day, dayStr, view)
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
func (s *Server) patchCurrentView(sse *datastar.ServerSentEventGenerator, ctx context.Context, day time.Time, dayStr, view string) error {
	if view == "journal" {
		j, err := fetchJournal(ctx, s.DB, dayStr)
		if err != nil {
			return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
		}
		data := views.DashboardData{
			Day: dayStr, DayLabel: dayLabel(day),
			PrevDay: day.AddDate(0, 0, -1).Format(dateLayout), NextDay: day.AddDate(0, 0, 1).Format(dateLayout),
			View: "journal",
		}
		return sse.PatchElementTempl(views.JournalViewBody(data, j))
	}

	data, err := buildDashboardData(ctx, s.DB, day, "data", DefaultTileKinds)
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

	sse := datastar.NewSSE(c.Response(), c.Request())
	detail, err := fetchActivityDetail(c.Request().Context(), s.DB, id)
	if err != nil {
		return sse.PatchElementTempl(views.ActivityDetailError(err.Error()))
	}
	return sse.PatchElementTempl(views.ActivityDetailFragment(*detail))
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
	if err := saveJournal(ctx, s.DB, day, signals.JournalContent); err != nil {
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
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return c.NoContent(http.StatusBadRequest)
	}
	if err := saveJournal(c.Request().Context(), s.DB, day, string(body)); err != nil {
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

	// Datastar sends a number-input's bound signal as a bare JSON number
	// (e.g. 82.5) when it has a value, but as an empty string once the
	// field is cleared — json.RawMessage plus parseOptionalFloatRaw handles
	// both shapes (and a JSON null, just in case) rather than assuming one.
	var signals struct {
		BMWeight json.RawMessage `json:"bmweight"`
		BMWaist  json.RawMessage `json:"bmwaist"`
		BMNeck   json.RawMessage `json:"bmneck"`
	}
	readErr := datastar.ReadSignals(c.Request(), &signals)

	sse := datastar.NewSSE(c.Response(), c.Request())
	if readErr != nil {
		return sse.PatchElementTempl(views.ErrorFragment("reading request: " + readErr.Error()))
	}

	weight := parseOptionalFloatRaw(signals.BMWeight)
	waist := parseOptionalFloatRaw(signals.BMWaist)
	neck := parseOptionalFloatRaw(signals.BMNeck)
	if err := saveBodyMeasurement(ctx, s.DB, day, weight, waist, neck); err != nil {
		return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
	}
	return s.patchBodyTile(sse, ctx, day, "Saved "+time.Now().Format("15:04:05"))
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

	sse := datastar.NewSSE(c.Response(), c.Request())
	if err := carryForwardBodyMeasurement(ctx, s.DB, day); err != nil {
		return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
	}
	return s.patchBodyTile(sse, ctx, day, "Carried forward from a previous day")
}

// patchBodyTile re-fetches and patches #tile-body after a save/carry-forward
// — the same "rebuild the one tile, patch it" shape handleTile uses.
func (s *Server) patchBodyTile(sse *datastar.ServerSentEventGenerator, ctx context.Context, day, savedAt string) error {
	dayTime, err := time.ParseInLocation(dateLayout, day, time.Local)
	if err != nil {
		dayTime = time.Now()
	}
	t := views.TileData{ID: "tile-body", Metric: "body"}
	t, err = buildBodyTile(ctx, s.DB, t, dayTime)
	if err != nil {
		return sse.PatchElementTempl(views.ErrorFragment(err.Error()))
	}
	if t.Body != nil {
		t.Body.SavedAt = savedAt
	}
	return sse.PatchElementTempl(views.Tile(t, day))
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
