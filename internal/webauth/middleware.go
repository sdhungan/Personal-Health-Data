package webauth

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// CookieName is the browser cookie holding the raw session token (see
// sessions.go — only its SHA-256 is ever persisted).
const CookieName = "healthd_session"

// userIDContextKey/usernameContextKey are where Middleware stashes the
// authenticated user's identity on success, for handlers to read via
// CurrentUserID/CurrentUsername.
const userIDContextKey = "webauth_user_id"
const usernameContextKey = "webauth_username"

// SetCookie sets the session cookie in the response. SameSite=Lax is a
// practical default CSRF mitigation for a same-origin app like this one
// without needing a separate token scheme. Secure is only set when the
// request itself arrived over TLS — healthd has no built-in TLS
// termination and is documented as a localhost/LAN tool, so forcing Secure
// unconditionally would silently break the cookie over plain HTTP.
func SetCookie(c echo.Context, token string) {
	c.SetCookie(&http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(SessionDuration.Seconds()),
		HttpOnly: true,
		Secure:   c.Request().TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie removes the session cookie (logout).
func ClearCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.Request().TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

// CurrentUserID returns the authenticated user id Middleware set on this
// request's context. Only meaningful on routes behind Middleware.
func CurrentUserID(c echo.Context) int64 {
	id, _ := c.Get(userIDContextKey).(int64)
	return id
}

// CurrentUsername returns the authenticated username Middleware set on
// this request's context. Only meaningful on routes behind Middleware.
func CurrentUsername(c echo.Context) string {
	name, _ := c.Get(usernameContextKey).(string)
	return name
}

// Middleware gates every route it's applied to behind a valid session
// cookie: missing or expired -> redirect page (GET) requests to
// /login?next=<original path>, or 401 requests under apiPrefix (Datastar
// POST/SSE calls can't follow a redirect usefully). A valid session touches
// its inactivity window (see LookupSession) and stores the user id on the
// Echo context for downstream handlers via CurrentUserID.
func Middleware(db *sql.DB, apiPrefix string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			cookie, err := c.Cookie(CookieName)
			var token string
			if err == nil {
				token = cookie.Value
			}

			userID, username, ok, err := LookupSession(c.Request().Context(), db, token)
			if err != nil {
				return err
			}
			if !ok {
				if strings.HasPrefix(c.Path(), apiPrefix) {
					return c.String(http.StatusUnauthorized, "session expired")
				}
				return c.Redirect(http.StatusFound, "/login?next="+c.Request().URL.Path)
			}

			c.Set(userIDContextKey, userID)
			c.Set(usernameContextKey, username)
			return next(c)
		}
	}
}
