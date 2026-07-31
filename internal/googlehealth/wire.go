package googlehealth

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// PBInt64 decodes a protobuf int64 field. Google's JSON encoding represents
// int64 as a string to avoid precision loss in JavaScript clients —
// confirmed against real responses (e.g. dailyRestingHeartRate's
// beatsPerMinute came back as "71", not 71). Accepts a bare number too, in
// case a given field is ever emitted unquoted.
type PBInt64 int64

func (v *PBInt64) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("parsing int64 string %q: %w", s, err)
		}
		*v = PBInt64(n)
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*v = PBInt64(n)
	return nil
}

func (v PBInt64) MarshalJSON() ([]byte, error) { return json.Marshal(int64(v)) }

// PBFloat64 decodes a protobuf double/float field. Google's JSON encoding
// represents NaN/Infinity/-Infinity as quoted strings — confirmed against
// real data: an early baselineTemperatureCelsius reading, before 30 days
// of history exist to compute a baseline, comes back as "NaN" while
// ordinary readings are plain unquoted numbers.
type PBFloat64 float64

func (v *PBFloat64) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		switch s {
		case "NaN":
			*v = PBFloat64(math.NaN())
		case "Infinity":
			*v = PBFloat64(math.Inf(1))
		case "-Infinity":
			*v = PBFloat64(math.Inf(-1))
		default:
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return fmt.Errorf("parsing float string %q: %w", s, err)
			}
			*v = PBFloat64(f)
		}
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*v = PBFloat64(f)
	return nil
}

func (v PBFloat64) MarshalJSON() ([]byte, error) {
	switch f := float64(v); {
	case math.IsNaN(f):
		return json.Marshal("NaN")
	case math.IsInf(f, 1):
		return json.Marshal("Infinity")
	case math.IsInf(f, -1):
		return json.Marshal("-Infinity")
	default:
		return json.Marshal(f)
	}
}

// PBDuration decodes a protobuf Duration, which Google's JSON encoding
// represents as a string like "1080s" or "1088.400s" — confirmed against
// real exercise data (activeDuration, heartRateZoneDurations.*).
type PBDuration time.Duration

func (d *PBDuration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	s = strings.TrimSuffix(s, "s")
	secs, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("parsing duration %q: %w", s, err)
	}
	*d = PBDuration(secs * float64(time.Second))
	return nil
}

func (d PBDuration) Duration() time.Duration { return time.Duration(d) }

// Date is a calendar day with no time-of-day component (protobuf
// google.type.Date), used by every daily-aggregate data type.
type Date struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

// Time returns Date as midnight UTC on that day — a convenience for
// sorting/bucketing, not a claim that the measurement happened at 00:00.
func (d Date) Time() time.Time {
	return time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.UTC)
}

// ObservationTimeInterval is the [start, end) window an interval-kind data
// point (steps, distance, active-minutes, ...) covers. Google's response
// also includes civil-time/UTC-offset fields alongside these; healthd
// doesn't model them — the physical start/end timestamps are enough to
// bucket data by day and hour ourselves.
type ObservationTimeInterval struct {
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
}

func (i ObservationTimeInterval) Start() (time.Time, error) {
	return time.Parse(time.RFC3339, i.StartTime)
}
func (i ObservationTimeInterval) End() (time.Time, error) { return time.Parse(time.RFC3339, i.EndTime) }

// ObservationSampleTime is the single instant a sample-kind data point
// (heart rate, weight, ...) was recorded at.
type ObservationSampleTime struct {
	PhysicalTime string `json:"physicalTime"`
}

func (s ObservationSampleTime) Time() (time.Time, error) {
	return time.Parse(time.RFC3339, s.PhysicalTime)
}

// SessionTimeInterval is the [start, end) window a session-kind data point
// (sleep, exercise, ECG) covers. Confirmed from real data that not every
// session type populates both ends — ECG's interval only ever has a
// startTime.
type SessionTimeInterval struct {
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime,omitempty"`
}

func (i SessionTimeInterval) Start() (time.Time, error) { return time.Parse(time.RFC3339, i.StartTime) }
func (i SessionTimeInterval) End() (time.Time, error)   { return time.Parse(time.RFC3339, i.EndTime) }
