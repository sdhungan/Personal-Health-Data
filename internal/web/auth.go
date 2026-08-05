package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/sdhungan/Personal-Health-Data/internal/cronometer"
	"github.com/sdhungan/Personal-Health-Data/internal/googleauth"
	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

// handleLoginPage serves the login form (see webauth.Middleware's redirect
// for how a request ends up here with ?next= set).
func (s *Server) handleLoginPage(c echo.Context) error {
	info := ""
	if c.QueryParam("deleted") == "1" {
		info = "Your account and all of its data have been permanently deleted."
	}
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return views.LoginPage("", info, c.QueryParam("next")).Render(c.Request().Context(), c.Response())
}

// handleLogin verifies username/password, creates a session, sets the
// cookie, and redirects to next (defaulting to the dashboard root) — a
// plain form POST, not a Datastar/SSE exchange, since a login redirect is
// a full navigation either way.
func (s *Server) handleLogin(c echo.Context) error {
	ctx := c.Request().Context()
	username := c.FormValue("username")
	password := c.FormValue("password")
	next := c.FormValue("next")

	user, err := webauth.Authenticate(ctx, s.DB, username, password)
	if err != nil {
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		return views.LoginPage("Invalid username or password.", "", next).Render(ctx, c.Response())
	}

	token, err := webauth.CreateSession(ctx, s.DB, user.ID)
	if err != nil {
		return err
	}
	webauth.SetCookie(c, token)

	if next == "" || !strings.HasPrefix(next, "/") {
		next = "/"
	}
	return c.Redirect(http.StatusFound, next)
}

// handleSignupPage serves the account-creation form.
func (s *Server) handleSignupPage(c echo.Context) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return views.SignupPage("").Render(c.Request().Context(), c.Response())
}

// handleSignup creates a new account, derives+caches its credential-
// encryption key (see webauth.CreateUser), logs the new user in
// immediately (no separate login step needed right after signing up), and
// sends them to the onboarding page to connect Google Health/Cronometer.
func (s *Server) handleSignup(c echo.Context) error {
	ctx := c.Request().Context()
	username := c.FormValue("username")
	password := c.FormValue("password")
	confirm := c.FormValue("confirm")

	renderErr := func(msg string) error {
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		return views.SignupPage(msg).Render(ctx, c.Response())
	}

	if password != confirm {
		return renderErr("Passwords do not match.")
	}

	user, err := webauth.CreateUser(ctx, s.DB, s.Paths, username, password)
	if err != nil {
		return renderErr(err.Error())
	}

	token, err := webauth.CreateSession(ctx, s.DB, user.ID)
	if err != nil {
		return err
	}
	webauth.SetCookie(c, token)

	return c.Redirect(http.StatusFound, "/onboarding/connect")
}

// handleLogout deletes the current session (best-effort — a missing/
// already-expired token is not an error) and clears the cookie.
func (s *Server) handleLogout(c echo.Context) error {
	if cookie, err := c.Cookie(webauth.CookieName); err == nil {
		_ = webauth.DeleteSession(c.Request().Context(), s.DB, cookie.Value)
	}
	webauth.ClearCookie(c)
	return c.Redirect(http.StatusFound, "/login")
}

// handleOnboardingPage serves the post-signup "connect your accounts" step
// — also freely revisitable any time later at /onboarding/connect (e.g. to
// connect a provider that was skipped at signup).
func (s *Server) handleOnboardingPage(c echo.Context) error {
	userID := webauth.CurrentUserID(c)
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	return views.OnboardingPage(
		s.userGoogleSyncer(userID) != nil, "",
		s.cronometerConnected(userID), "",
	).Render(c.Request().Context(), c.Response())
}

// handleOnboardingConnectGoogle triggers connectGoogle from the onboarding
// page and returns there afterward either way — /onboarding/connect stays
// freely revisitable, so this is also how a provider skipped at signup
// gets connected later.
func (s *Server) handleOnboardingConnectGoogle(c echo.Context) error {
	ctx := c.Request().Context()
	userID := webauth.CurrentUserID(c)

	if err := s.connectGoogle(ctx, userID); err != nil {
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		return views.OnboardingPage(false, err.Error(), s.cronometerConnected(userID), "").Render(ctx, c.Response())
	}
	return c.Redirect(http.StatusFound, "/onboarding/connect")
}

// connectGoogle runs the same local-browser OAuth consent flow "healthd
// auth google" uses (internal/googleauth.RunConsentFlow), triggered from
// the dashboard instead of a terminal — this only works because healthd's
// browser and server are expected to be on the same machine (see
// ARCHITECTURE.md §5). It blocks the request until the user approves (or
// the flow times out), then saves the resulting token encrypted under this
// user's own per-user path/key and activates their syncer immediately.
// Shared by handleOnboardingConnectGoogle and
// handleGoogleClientConnectAccount (settings.go) — connecting Google
// Health isn't onboarding-only, same reasoning connectCronometer already
// follows for Cronometer's ongoing account-settings card.
func (s *Server) connectGoogle(ctx context.Context, userID int64) error {
	clientJSON, err := s.googleClientJSON()
	if err != nil {
		return fmt.Errorf("%w. Visit /settings/google-client to upload one", err)
	}
	token, err := googleauth.RunConsentFlow(ctx, clientJSON, s.Config.Google.CallbackPort)
	if err != nil {
		return fmt.Errorf("connecting Google Health failed: %w", err)
	}

	if err := s.Paths.EnsureUserDir(userID); err != nil {
		return fmt.Errorf("connecting Google Health failed: %w", err)
	}
	key, err := webauth.CredentialKey(s.Paths, userID)
	if err != nil {
		return fmt.Errorf("connecting Google Health failed: %w", err)
	}
	if err := googleauth.SaveToken(s.Paths.UserGoogleOAuthFile(userID), key, token); err != nil {
		return fmt.Errorf("connecting Google Health failed: %w", err)
	}

	s.setGoogleSyncer(userID, s.buildGoogleSyncer(userID)) // build now, using the token just saved, so it's picked up immediately
	return nil
}

// handleOnboardingConnectCronometer verifies the given Cronometer
// credentials with a real login (same as "healthd auth cronometer") before
// saving anything, encrypts and saves them under this user's own per-user
// path/key, and activates their syncer immediately — same verify-then-save
// logic as the dashboard's own Cronometer account card
// (handleCronometerLogin), just redirecting back to the onboarding page
// instead of returning an SSE patch.
func (s *Server) handleOnboardingConnectCronometer(c echo.Context) error {
	ctx := c.Request().Context()
	userID := webauth.CurrentUserID(c)
	username := strings.TrimSpace(c.FormValue("crono_username"))
	password := c.FormValue("crono_password")

	renderErr := func(msg string) error {
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
		return views.OnboardingPage(s.userGoogleSyncer(userID) != nil, "", false, msg).Render(ctx, c.Response())
	}

	if err := s.connectCronometer(ctx, userID, username, password); err != nil {
		return renderErr(err.Error())
	}
	return c.Redirect(http.StatusFound, "/onboarding/connect")
}

// connectCronometer holds the verify-then-save-then-activate logic shared
// by handleOnboardingConnectCronometer (plain redirect) and
// handleCronometerLogin (SSE, the dashboard's ongoing account-settings
// card) — only their response mechanism differs.
func (s *Server) connectCronometer(ctx context.Context, userID int64, username, password string) error {
	if username == "" || password == "" {
		return errors.New("email and password are required")
	}
	if _, err := cronometer.NewClient().Login(ctx, username, password); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	if err := s.Paths.EnsureUserDir(userID); err != nil {
		return err
	}
	key, err := webauth.CredentialKey(s.Paths, userID)
	if err != nil {
		return err
	}
	creds := &cronometer.Credentials{Username: username, Password: password}
	if err := cronometer.SaveCredentials(s.Paths.UserCronometerCredentialsFile(userID), key, creds); err != nil {
		return fmt.Errorf("saving credentials failed: %w", err)
	}

	syncer, err := cronometer.NewDBSyncer(s.DB, userID, key, s.Paths.UserCronometerCredentialsFile(userID), s.Paths.UserCronometerSessionFile(userID))
	if err != nil {
		return fmt.Errorf("saved, but failed to activate: %w", err)
	}
	s.setCronometerSyncer(userID, syncer)
	return nil
}

// handleOnboardingSkip is the "Continue to Dashboard" action — onboarding
// has no required state of its own beyond whatever providers were actually
// connected, so this is just a redirect; /onboarding/connect stays freely
// revisitable later to connect anything skipped now.
func (s *Server) handleOnboardingSkip(c echo.Context) error {
	return c.Redirect(http.StatusFound, "/")
}
