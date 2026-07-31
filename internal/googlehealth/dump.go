package googlehealth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// filterFor builds an AIP-160 time-range filter for dt covering [start, end).
//
// Found by probing the real API (see bin/google-health-dump/ and the
// commit history — every hypothesis below was confirmed or ruled out
// against a live account, not guessed):
//   - The filter's leading path segment DOES need the data type name after
//     all, but as the type's snake_case identifier (hyphens -> underscores),
//     never kebab-case and never camelCase. "heart-rate...", "heartRate...",
//     and bare "sample_time..." (no prefix at all) are all rejected —
//     "heart_rate.sample_time.physical_time..." is accepted.
//   - kindInterval/kindSample both accept a start/end range this way.
//   - kindSession types are inconsistent per type (sleep only filters on
//     interval.end_time; exercise accepts neither start nor end time
//     member at all) rather than sharing one rule, so we don't fight that:
//     list() unfiltered returns recent history by default (confirmed for
//     exercise, sleep, electrocardiogram, irregular-rhythm-notification),
//     and callers trim to the desired day client-side — the same approach
//     that already works for kindDailyAggregate.
func filterFor(dt dataType, start, end time.Time) string {
	startStr := start.UTC().Format(time.RFC3339)
	endStr := end.UTC().Format(time.RFC3339)
	name := strings.ReplaceAll(dt.Name, "-", "_")

	switch dt.Kind {
	case kindInterval:
		return fmt.Sprintf(`%s.interval.start_time >= "%s" AND %s.interval.start_time < "%s"`, name, startStr, name, endStr)
	case kindSample:
		return fmt.Sprintf(`%s.sample_time.physical_time >= "%s" AND %s.sample_time.physical_time < "%s"`, name, startStr, name, endStr)
	default: // kindSession, kindDailyAggregate: list unfiltered, trim client-side
		return ""
	}
}

// DumpToday is a diagnostic pass, not part of the eventual sync engine: it
// calls list() (and, for the types dailyRollUp documents, dailyRollUp())
// for every data type healthd's scopes cover, for today's date, and writes
// every response — success or error — as pretty-printed JSON under outDir.
// The point is to see, against a real account, what this API actually
// returns before committing to a watch_* table mapping.
func DumpToday(ctx context.Context, client *Client, outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output directory %s: %w", outDir, err)
	}

	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endOfDay := startOfDay.Add(24 * time.Hour)

	for _, dt := range DataTypes {
		if dt.NoList {
			continue
		}
		filter := filterFor(dt, startOfDay, endOfDay)
		data, err := client.ListDataPoints(ctx, dt.Name, filter, 10000)
		writeResult(outDir, dt.Name+".list.json", data, err)
	}

	civilStart := CivilDateTime{
		Date: Date{Year: startOfDay.Year(), Month: int(startOfDay.Month()), Day: startOfDay.Day()},
	}
	civilEnd := CivilDateTime{
		Date: Date{Year: endOfDay.Year(), Month: int(endOfDay.Month()), Day: endOfDay.Day()},
	}
	for _, name := range RollUpDataTypes {
		data, err := client.DailyRollUp(ctx, name, civilStart, civilEnd)
		writeResult(outDir, name+".dailyrollup.json", data, err)
	}

	return nil
}

func writeResult(outDir, filename string, data json.RawMessage, callErr error) {
	path := filepath.Join(outDir, filename)

	var payload []byte
	switch {
	case callErr == nil:
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, data, "", "  "); err == nil {
			payload = pretty.Bytes()
		} else {
			payload = data
		}
	default:
		var apiErr *APIError
		if errors.As(callErr, &apiErr) {
			var prettyBody bytes.Buffer
			body := any(json.RawMessage(apiErr.Body))
			if json.Indent(&prettyBody, apiErr.Body, "", "  ") != nil {
				body = string(apiErr.Body)
			} else {
				body = json.RawMessage(prettyBody.Bytes())
			}
			payload, _ = json.MarshalIndent(map[string]any{
				"error_status_code": apiErr.StatusCode,
				"error_body":        body,
			}, "", "  ")
		} else {
			payload, _ = json.MarshalIndent(map[string]any{"error": callErr.Error()}, "", "  ")
		}
	}

	if err := os.WriteFile(path, payload, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "warning: writing", path, "failed:", err)
		return
	}
	fmt.Println("wrote", path)
}
