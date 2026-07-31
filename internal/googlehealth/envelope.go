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
