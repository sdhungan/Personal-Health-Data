package googlehealth

import (
	"encoding/json"
	"fmt"
)

// listEnvelope is the outer shape every list() response shares: an
// optional "dataPoints" array where each element carries a "name" and a
// "dataSource" (device/platform/recordingMethod — genuinely irrelevant for
// a single-Pixel-Watch personal tool) alongside exactly one data-type-
// specific value keyed by that type's camelCase field name.
type listEnvelope struct {
	DataPoints []map[string]json.RawMessage `json:"dataPoints"`
}

// ExtractValues strips the dataSource/name wrapper from a list() response
// and decodes each point's value (found under key) into T, discarding
// everything else. This is the metadata-stripping step: what comes back is
// just the measurements, ready to insert or hand to a UI.
func ExtractValues[T any](raw json.RawMessage, key string) ([]T, error) {
	var env listEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decoding list envelope: %w", err)
	}

	out := make([]T, 0, len(env.DataPoints))
	for _, point := range env.DataPoints {
		valueRaw, ok := point[key]
		if !ok {
			continue // e.g. a point carrying only dataSource/name, no value
		}
		var v T
		if err := json.Unmarshal(valueRaw, &v); err != nil {
			return nil, fmt.Errorf("decoding %q value: %w", key, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// SourcedValue pairs a decoded data point with an opaque key identifying
// which data source (device/platform/recording method) reported it — see
// ExtractValuesWithSource.
type SourcedValue[T any] struct {
	Source string
	Value  T
}

// ExtractValuesWithSource is ExtractValues, but keeps each point's
// dataSource (dropped everywhere else — see values.go's package doc) as an
// opaque grouping key instead of discarding it. Needed wherever more than
// one device can report the same metric for the same period (e.g. a
// phone's own step sensor running alongside a watch): summing every point
// regardless of source double-counts in exactly that case, so callers with
// more than one real-world source need to deduplicate per source before
// summing rather than blindly summing everything list() returns.
func ExtractValuesWithSource[T any](raw json.RawMessage, key string) ([]SourcedValue[T], error) {
	var env listEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decoding list envelope: %w", err)
	}

	out := make([]SourcedValue[T], 0, len(env.DataPoints))
	for _, point := range env.DataPoints {
		valueRaw, ok := point[key]
		if !ok {
			continue // e.g. a point carrying only dataSource/name, no value
		}
		var v T
		if err := json.Unmarshal(valueRaw, &v); err != nil {
			return nil, fmt.Errorf("decoding %q value: %w", key, err)
		}
		out = append(out, SourcedValue[T]{Source: string(point["dataSource"]), Value: v})
	}
	return out, nil
}

// rollupEnvelope is the outer shape every dailyRollUp() response shares.
type rollupEnvelope struct {
	RollupDataPoints []map[string]json.RawMessage `json:"rollupDataPoints"`
}

// RollupPoint pairs a dailyRollUp value with the civil day range it covers.
type RollupPoint[T any] struct {
	CivilStart Date
	CivilEnd   Date
	Value      T
}

// ExtractRollupValues is ExtractValues for dailyRollUp() responses, which
// wrap civilStartTime/civilEndTime around the value instead of
// dataSource/name.
func ExtractRollupValues[T any](raw json.RawMessage, key string) ([]RollupPoint[T], error) {
	var env rollupEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decoding rollup envelope: %w", err)
	}

	out := make([]RollupPoint[T], 0, len(env.RollupDataPoints))
	for _, point := range env.RollupDataPoints {
		var rp RollupPoint[T]

		if startRaw, ok := point["civilStartTime"]; ok {
			var cdt CivilDateTime
			if err := json.Unmarshal(startRaw, &cdt); err != nil {
				return nil, fmt.Errorf("decoding civilStartTime: %w", err)
			}
			rp.CivilStart = cdt.Date
		}
		if endRaw, ok := point["civilEndTime"]; ok {
			var cdt CivilDateTime
			if err := json.Unmarshal(endRaw, &cdt); err != nil {
				return nil, fmt.Errorf("decoding civilEndTime: %w", err)
			}
			rp.CivilEnd = cdt.Date
		}

		valueRaw, ok := point[key]
		if !ok {
			continue
		}
		if err := json.Unmarshal(valueRaw, &rp.Value); err != nil {
			return nil, fmt.Errorf("decoding %q rollup value: %w", key, err)
		}
		out = append(out, rp)
	}
	return out, nil
}
