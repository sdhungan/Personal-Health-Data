package web

import (
	"io"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

// handleGoogleClientSettingsPage serves the app-wide Google OAuth client
// upload form (see views.GoogleClientSettingsPage's doc comment — the
// client itself is not a per-user credential) alongside the *current*
// account's own Google Health connection status and a "Connect Google
// Health" action — connecting isn't onboarding-only, same as Cronometer's
// ongoing account-settings card; someone who fixes the client JSON here
// (the exact situation this page exists for) can connect their own account
// right after, without navigating back to /onboarding/connect by hand.
// Reachable by any logged-in account; there's no admin/role system yet to
// gate the client-upload half of this page further. ?uploaded=1 (set by
// the redirect at the end of handleGoogleClientUpload) shows an explicit
// success confirmation — without it, replacing an already-configured
// client with a new file redirected back to a page that just said "a
// client is configured" either way, which looked identical to before and
// gave no sign the upload actually took effect.
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
	return views.GoogleClientSettingsPage(configured, message, "", webauth.CurrentUsername(c), s.userGoogleSyncer(userID) != nil).
		Render(c.Request().Context(), c.Response())
}

// handleGoogleClientUpload validates and saves an uploaded client_secret
// JSON, replacing whatever was configured before — see
// googleauth.SaveClientJSON for the validation (rejects a file that
// doesn't actually parse as a Google OAuth client JSON before it can break
// every account's Google connection).
func (s *Server) handleGoogleClientUpload(c echo.Context) error {
	ctx := c.Request().Context()
	userID := webauth.CurrentUserID(c)

	renderErr := func(msg string) error {
		_, err := s.googleClientJSON()
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		return views.GoogleClientSettingsPage(err == nil, "", msg, webauth.CurrentUsername(c), s.userGoogleSyncer(userID) != nil).Render(ctx, c.Response())
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

// handleGoogleClientConnectAccount runs the OAuth consent flow (see
// connectGoogle in auth.go — the same one onboarding uses) for the
// currently logged-in account, triggered from this settings page instead
// of /onboarding/connect, and returns here either way.
func (s *Server) handleGoogleClientConnectAccount(c echo.Context) error {
	ctx := c.Request().Context()
	userID := webauth.CurrentUserID(c)

	if err := s.connectGoogle(ctx, userID); err != nil {
		_, clientErr := s.googleClientJSON()
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		return views.GoogleClientSettingsPage(clientErr == nil, "", err.Error(), webauth.CurrentUsername(c), false).Render(ctx, c.Response())
	}
	return c.Redirect(http.StatusFound, "/settings/google-client?connected=1")
}
