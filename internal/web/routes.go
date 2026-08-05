package web

import (
	"io/fs"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
	"github.com/sdhungan/Personal-Health-Data/internal/webauth"
)

// registerRoutes wires every endpoint under views.APIPrefix (the same
// prefix every backend URL built in the views package resolves against —
// see views.APIURL) plus the page and static asset routes. Every route
// other than /login, /signup, and /static sits behind webauth's session
// middleware (see webauth.Middleware) — a missing/expired session redirects
// page requests to /login and 401s API requests.
func (s *Server) registerRoutes() {
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("embedding static assets: " + err.Error()) // can only fail if assets.go's embed directive is wrong
	}
	s.echo.StaticFS("/static", staticSub)

	s.echo.GET("/login", s.handleLoginPage)
	s.echo.POST("/login", s.handleLogin)
	s.echo.GET("/signup", s.handleSignupPage)
	s.echo.POST("/signup", s.handleSignup)

	auth := webauth.Middleware(s.DB, views.APIPrefix)

	s.echo.GET("/", s.handleIndex, auth)
	s.echo.POST("/logout", s.handleLogout, auth)
	s.echo.GET("/settings/google-client", s.handleGoogleClientSettingsPage, auth)
	s.echo.POST("/settings/google-client", s.handleGoogleClientUpload, auth)
	s.echo.POST("/settings/google-client/connect", s.handleGoogleClientConnectAccount, auth)
	s.echo.GET("/settings/account", s.handleAccountSettingsPage, auth)
	s.echo.POST("/settings/account/delete", s.handleAccountDelete, auth)
	s.echo.GET("/settings/mcp-connector", s.handleMCPConnectorPage, auth)
	s.echo.GET("/onboarding/connect", s.handleOnboardingPage, auth)
	s.echo.POST("/onboarding/connect/google", s.handleOnboardingConnectGoogle, auth)
	s.echo.POST("/onboarding/connect/cronometer", s.handleOnboardingConnectCronometer, auth)
	s.echo.POST("/onboarding/skip", s.handleOnboardingSkip, auth)

	api := s.echo.Group(views.APIPrefix, auth)
	api.GET("/view", s.handleView)
	api.GET("/tile", s.handleTile)
	api.GET("/activity", s.handleActivity)
	api.GET("/food-serving", s.handleFoodServing)
	api.POST("/sync", s.handleForceSync)
	api.POST("/journal", s.handleJournalSave)
	api.POST("/journal/beacon", s.handleJournalBeacon)
	api.POST("/body-measurement", s.handleBodyMeasurementSave)
	api.POST("/body-measurement/carry-forward", s.handleBodyMeasurementCarryForward)
	api.POST("/cronometer/login", s.handleCronometerLogin)
}
