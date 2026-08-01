package views

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// APIPrefix is where every backend endpoint these templates reference
// lives. This is the single point of truth for that prefix — route
// registration (internal/web/routes.go) and every URL built here both
// derive from it, so they can never drift apart.
const APIPrefix = "/api"

// APIURL builds a backend URL from a path relative to the API group, e.g.
// APIURL("tile", nil) -> "/api/tile". This is the one helper every
// Datastar attribute in this package goes through instead of hardcoding
// "/api/..." directly — per the project convention, if the path is "bla"
// the endpoint always resolves to the API group plus "bla".
func APIURL(path string, query url.Values) string {
	p := APIPrefix + "/" + strings.TrimPrefix(path, "/")
	if len(query) > 0 {
		return p + "?" + query.Encode()
	}
	return p
}

func viewURL(day, view string) string {
	return APIURL("view", url.Values{"day": {day}, "view": {view}})
}

// dayPickerChangeExpr builds the date-picker input's change-handler
// expression. Unlike the other nav URLs, the day segment can't be baked in
// at render time — it's whatever the user just picked — so this leaves it
// as a Datastar expression that reads the input's own value (`el.value`)
// at click time instead of a Go-side string.
func dayPickerChangeExpr(view string) string {
	return "@get('" + APIURL("view", url.Values{"view": {view}}) + "&day=' + el.value)"
}

func tileURL(kind, day string, expanded bool) string {
	return APIURL("tile", url.Values{"kind": {kind}, "day": {day}, "expanded": {fmt.Sprintf("%t", expanded)}})
}

func syncURL(day, view string) string {
	return APIURL("sync", url.Values{"day": {day}, "view": {view}})
}

func activityURL(id int64) string {
	return APIURL("activity", url.Values{"id": {fmt.Sprintf("%d", id)}})
}

func foodServingURL(id int64) string {
	return APIURL("food-serving", url.Values{"id": {fmt.Sprintf("%d", id)}})
}

func journalSaveURL(day string) string {
	return APIURL("journal", url.Values{"day": {day}})
}

// journalBeaconURL is a plain (non-SSE) endpoint for the beforeunload
// best-effort save: navigator.sendBeacon fires a request the browser
// doesn't wait for a response to, so it can't participate in the normal
// Datastar SSE exchange.
func journalBeaconURL(day string) string {
	return APIURL("journal/beacon", url.Values{"day": {day}})
}

func bodyMeasurementSaveURL(day string) string {
	return APIURL("body-measurement", url.Values{"day": {day}})
}

func bodyMeasurementCarryForwardURL(day string) string {
	return APIURL("body-measurement/carry-forward", url.Values{"day": {day}})
}

func cronometerLoginURL() string {
	return APIURL("cronometer/login", nil)
}

func boolJS(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// initialSignals is the data-signals bootstrap for the outer <html>
// element on a full page load.
func initialSignals(day string) string {
	b, _ := json.Marshal(map[string]any{"day": day, "view": "data", "detailOpen": false})
	return string(b)
}

// getExpr/postExpr build the Datastar expression a data-on:* attribute
// runs, e.g. getExpr(tileURL(...)) -> "@get('/api/tile?...')".
func getExpr(url string) string  { return "@get('" + url + "')" }
func postExpr(url string) string { return "@post('" + url + "')" }
