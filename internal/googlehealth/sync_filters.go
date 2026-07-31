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
