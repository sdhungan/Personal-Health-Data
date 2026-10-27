package views

import (
	"context"
	"strings"
	"testing"
)

// TestJournalBeaconScriptIsValid guards the save-on-close script: templ
// doesn't interpolate inside <script>, so a { expr } there ships to the
// browser verbatim as a JavaScript syntax error and the beforeunload save
// silently never registers. The URL must come in through an attribute.
func TestJournalBeaconScriptIsValid(t *testing.T) {
	var b strings.Builder
	if err := JournalView(DashboardData{}, JournalData{Day: "2026-10-27"}).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	if !strings.Contains(html, `data-beacon-url="`+journalBeaconURL("2026-10-27")+`"`) {
		t.Errorf("textarea has no data-beacon-url for the day:\n%s", html)
	}
	if strings.Contains(html, "journalBeaconURL") || strings.Contains(html, "{ j.") {
		t.Errorf("uninterpolated templ expression shipped in the page:\n%s", html)
	}
}
