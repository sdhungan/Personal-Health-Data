package web

import (
	"io"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

// handleGoogleClientSettingsPage serves the app-wide Google OAuth client
// upload form (see views.GoogleClientSettingsPage's doc comment — this is
// not a per-user credential). Reachable by any logged-in account; there's
// no admin/role system yet to gate this further. ?uploaded=1 (set by the
// redirect at the end of handleGoogleClientUpload) shows an explicit
// success confirmation — without it, replacing an already-configured
// client with a new file redirected back to a page that just said "a
// client is configured" either way, which looked identical to before and
// gave no sign the upload actually took effect.
func (s *Server) handleGoogleClientSettingsPage(c echo.Context) error {
	_, err := s.googleClientJSON()
	configured := err == nil

	message := ""
	if configured && c.QueryParam("uploaded") == "1" {
		message = "Uploaded — every account's \"Connect Google Health\" now uses this client."
	}

	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return views.GoogleClientSettingsPage(configured, message, "").Render(c.Request().Context(), c.Response())
}

// handleGoogleClientUpload validates and saves an uploaded client_secret
// JSON, replacing whatever was configured before — see
// googleauth.SaveClientJSON for the validation (rejects a file that
// doesn't actually parse as a Google OAuth client JSON before it can break
// every account's Google connection).
func (s *Server) handleGoogleClientUpload(c echo.Context) error {
	ctx := c.Request().Context()

	renderErr := func(msg string) error {
		_, err := s.googleClientJSON()
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		return views.GoogleClientSettingsPage(err == nil, "", msg).Render(ctx, c.Response())
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
