package googlehealth

import (
	"fmt"
	"strings"
	"time"
)

// intervalDayFilter and sampleDayFilter build the same confirmed-working
// filter shape as dump.go's filterFor (snake_case type name, both bounds),
// scoped to exactly one calendar day.
func intervalDayFilter(dataTypeName string, day time.Time) string {
	start, end := dayBounds(day)
	name := strings.ReplaceAll(dataTypeName, "-", "_")
	return fmt.Sprintf(`%s.interval.start_time >= "%s" AND %s.interval.start_time < "%s"`,
		name, start.UTC().Format(time.RFC3339), name, end.UTC().Format(time.RFC3339))
}

func sampleDayFilter(dataTypeName string, day time.Time) string {
	start, end := dayBounds(day)
	name := strings.ReplaceAll(dataTypeName, "-", "_")
	return fmt.Sprintf(`%s.sample_time.physical_time >= "%s" AND %s.sample_time.physical_time < "%s"`,
		name, start.UTC().Format(time.RFC3339), name, end.UTC().Format(time.RFC3339))
}

// sleepDayFilter matches sleep sessions that END on day (sleep is keyed by
// wake-up day). Sleep's filter accepts interval.end_time only, not
// start_time (confirmed against the live API 2026-10-27, e.g. 2026-08-20
// returned exactly that morning's night plus a nap). Unfiltered list()
// only returns the newest page, so it can't reach older days.
func sleepDayFilter(day time.Time) string {
	start, end := dayBounds(day)
	return fmt.Sprintf(`sleep.interval.end_time >= "%s" AND sleep.interval.end_time < "%s"`,
		start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339))
}
