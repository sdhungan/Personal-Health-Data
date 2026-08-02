package web

import (
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/starfederation/datastar-go/datastar"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

// handleCronometerLogin answers the Cronometer account card's "Connect"
// button (dashboard settings, not the onboarding flow — see auth.go's
// handleOnboardingConnectCronometer for that variant): verifies the given
// credentials with a real login before saving anything (see
// connectCronometer in auth.go, shared by both entry points), then
// encrypts and saves them under this user's own per-user path/key and
// rebuilds their syncer so a running server picks up the login immediately
// — no restart needed for the next Sync click to start covering Cronometer
// too.
func (s *Server) handleCronometerLogin(c echo.Context) error {
	ctx := c.Request().Context()
	userID := webauth.CurrentUserID(c)

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
	if err := s.connectCronometer(ctx, userID, username, signals.Password); err != nil {
		return sse.PatchElementTempl(views.CronometerAccountCard(false, err.Error()))
	}
	return sse.PatchElementTempl(views.CronometerAccountCard(true, ""))
}
