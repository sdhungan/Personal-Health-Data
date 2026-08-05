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
	return views.GoogleClientSettingsPage(configured, GoogleClientLockedByFlag, message, "", webauth.CurrentUsername(c), s.userGoogleSyncer(userID) != nil).
		Render(c.Request().Context(), c.Response())
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
		return views.GoogleClientSettingsPage(err == nil, GoogleClientLockedByFlag, "", msg, webauth.CurrentUsername(c), s.userGoogleSyncer(userID) != nil).Render(ctx, c.Response())
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
		return views.GoogleClientSettingsPage(clientErr == nil, GoogleClientLockedByFlag, "", err.Error(), webauth.CurrentUsername(c), false).Render(c.Request().Context(), c.Response())
	}
	return c.Redirect(http.StatusFound, authURL)
}
