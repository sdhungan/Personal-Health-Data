package web

import (
	"io/fs"

	"github.com/sdhungan/Personal-Health-Data/internal/web/views"
)

// registerRoutes wires every endpoint under views.APIPrefix (the same
// prefix every backend URL built in the views package resolves against —
// see views.APIURL) plus the page and static asset routes.
func (s *Server) registerRoutes() {
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("embedding static assets: " + err.Error()) // can only fail if assets.go's embed directive is wrong
	}
	s.echo.StaticFS("/static", staticSub)

	s.echo.GET("/", s.handleIndex)

	api := s.echo.Group(views.APIPrefix)
	api.GET("/view", s.handleView)
	api.GET("/tile", s.handleTile)
	api.GET("/activity", s.handleActivity)
	api.POST("/sync", s.handleForceSync)
	api.POST("/journal", s.handleJournalSave)
	api.POST("/journal/beacon", s.handleJournalBeacon)
	api.POST("/body-measurement", s.handleBodyMeasurementSave)
	api.POST("/body-measurement/carry-forward", s.handleBodyMeasurementCarryForward)
	api.POST("/cronometer/login", s.handleCronometerLogin)
}
