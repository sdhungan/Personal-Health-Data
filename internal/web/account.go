package web

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

// handleAccountSettingsPage serves the per-account settings page (see
// views.AccountSettingsPage).
func (s *Server) handleAccountSettingsPage(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return views.AccountSettingsPage(webauth.CurrentUsername(c), "").Render(c.Request().Context(), c.Response())
}

// handleAccountDelete permanently deletes the currently-authenticated
// account and every row of its data (see webauth.DeleteUser) — self-service
// only, always scoped to whoever webauth.CurrentUserID resolves this request
// to, never a target id read from the request itself. Re-verifies the
// account password first (the same real check handleLogin uses) so this
// destructive action can't be triggered by, say, a stray autofilled form
// submit — the confirm() dialog in the template is a UX nudge, not the
// actual guard.
func (s *Server) handleAccountDelete(c echo.Context) error {
	ctx := c.Request().Context()
	userID := webauth.CurrentUserID(c)
	username := webauth.CurrentUsername(c)

	renderErr := func(msg string) error {
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		return views.AccountSettingsPage(username, msg).Render(ctx, c.Response())
	}

	if _, err := webauth.Authenticate(ctx, s.DB, username, c.FormValue("password")); err != nil {
		return renderErr("Incorrect password.")
	}

	if err := webauth.DeleteUser(ctx, s.DB, s.Paths, userID); err != nil {
		return renderErr("Deleting account failed: " + err.Error())
	}

	// DeleteUser already removed every web_session row for this user
	// (including the one backing this very request's cookie) as part of its
	// transaction — only the browser-side cookie and the in-memory syncer
	// caches (never persisted, so DeleteUser can't reach them) need clearing
	// here.
	s.googleMu.Lock()
	delete(s.googleSyncers, userID)
	s.googleMu.Unlock()
	s.cronoMu.Lock()
	delete(s.cronoSyncers, userID)
	s.cronoMu.Unlock()
	webauth.ClearCookie(c)

	return c.Redirect(http.StatusFound, "/login?deleted=1")
}
