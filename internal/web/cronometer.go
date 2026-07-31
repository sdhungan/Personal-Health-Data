package web

import (
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/starfederation/datastar-go/datastar"

	"github.com/sdhungan/Personal-Health-Data/internal/cronometer"
	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

// cronometerConnected reports whether Cronometer credentials are on file —
// drives the Nutrition section's account card (login form vs. connected
// status) and whether the Sync button's force-sync also covers Cronometer.
func (s *Server) cronometerConnected() bool {
	return s.cronometerSync != nil
}

// handleCronometerLogin answers the Cronometer account card's "Connect"
// button: verifies the given credentials with a real login (same as
// "healthd auth cronometer") before saving anything, then encrypts and
// saves them, and rebuilds s.cronometerSync so a running server picks up
// the login immediately — no restart needed for the next Sync click to
// start covering Cronometer too.
func (s *Server) handleCronometerLogin(c echo.Context) error {
	ctx := c.Request().Context()

	var signals struct {
		Username string `json:"cronousername"`
		Password string `json:"cronopassword"`
	}
	readErr := datastar.ReadSignals(c.Request(), &signals)

	sse := datastar.NewSSE(c.Response(), c.Request())
	if readErr != nil {
		return sse.PatchElementTempl(views.ErrorFragment("reading request: " + readErr.Error()))
	}

	username := strings.TrimSpace(signals.Username)
	if username == "" || signals.Password == "" {
		return sse.PatchElementTempl(views.CronometerAccountCard(false, "Email and password are required."))
	}

	if _, err := cronometer.NewClient().Login(ctx, username, signals.Password); err != nil {
		return sse.PatchElementTempl(views.CronometerAccountCard(false, "Login failed: "+err.Error()))
	}

	creds := &cronometer.Credentials{Username: username, Password: signals.Password}
	if err := cronometer.SaveCredentials(s.cronometerCredentialsPath, s.cronometerKey, creds); err != nil {
		return sse.PatchElementTempl(views.CronometerAccountCard(false, "Saving credentials failed: "+err.Error()))
	}

	syncer, err := cronometer.NewDBSyncer(s.DB, s.cronometerKey, s.cronometerCredentialsPath, s.cronometerSessionPath)
	if err != nil {
		return sse.PatchElementTempl(views.CronometerAccountCard(false, "Saved, but failed to activate: "+err.Error()))
	}
	s.cronometerSync = syncer

	return sse.PatchElementTempl(views.CronometerAccountCard(true, ""))
}
