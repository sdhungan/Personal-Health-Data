package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func TestParseRange(t *testing.T) {
	e := echo.New()

	rec := httptest.NewRecorder()
	if r := parseRange(e.NewContext(httptest.NewRequest(http.MethodGet, "/?range=3M", nil), rec)); r.Days != 91 {
		t.Fatalf("?range=3M: got %+v", r)
	}
	if got := rec.Header().Get("Set-Cookie"); got == "" {
		t.Fatal("?range= didn't remember the choice in a cookie")
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: rangeCookie, Value: "1Y"})
	if r := parseRange(e.NewContext(req, httptest.NewRecorder())); r.Days != 365 {
		t.Fatalf("cookie 1Y: got %+v", r)
	}

	if r := parseRange(e.NewContext(httptest.NewRequest(http.MethodGet, "/?range=bogus", nil), httptest.NewRecorder())); r.Days != trendDays {
		t.Fatalf("bogus range: got %+v, want the default", r)
	}
}

func TestTrendLineSkipsMissingDays(t *testing.T) {
	day := time.Date(2026, 11, 6, 0, 0, 0, 0, time.Local)
	have := map[string]float64{"2026-11-01": 60, "2026-11-06": 70}
	value := func(d time.Time) (float64, bool) { v, ok := have[d.Format(dateLayout)]; return v, ok }

	d := trendLine(day, 40, value, fmtNoDec, "bpm")
	if d == nil || len(d.LineSamples) != 2 || d.LineSamples[1].ValueLabel != "70 bpm" || d.Stats[0].Value != "65 bpm" {
		t.Fatalf("got %+v", d)
	}
	if trendLine(day, 3, value, fmtNoDec, "bpm") == nil {
		t.Fatal("today's value is within a 3-day range")
	}
	if trendLine(day.AddDate(0, 0, -1), 3, value, fmtNoDec, "bpm") != nil {
		t.Fatal("no value in Nov 3–5, want nil")
	}
}
