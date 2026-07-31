// Package views holds every templ component healthd's dashboard renders,
// plus the plain-Go view-model structs they take as parameters. Nothing
// here touches the database directly — internal/web builds these structs
// from query results and hands them to a component to render.
package views

// DashboardData is everything the main dashboard page needs: which day is
// selected and the tiles to lay out in the grid.
type DashboardData struct {
	Day      string // "YYYY-MM-DD"
	DayLabel string // "Today", "Yesterday", or a formatted date
	PrevDay  string
	NextDay  string
	View     string // "data" or "journal" — which tab is active
	Tiles    []TileData
}

// TileKind selects which body a tile renders.
type TileKind string

const (
	TileKindStat       TileKind = "stat"
	TileKindSleep      TileKind = "sleep"
	TileKindActivities TileKind = "activities"
	TileKindBody       TileKind = "body"
)

// TileData is a generic tile. Not every field applies to every kind —
// e.g. Chart only applies to TileKindStat, Activities only to
// TileKindActivities.
type TileData struct {
	ID       string // unique DOM id, e.g. "tile-steps"
	Kind     TileKind
	Metric   string // the data-fetch identifier for the /api/tile?kind= query — e.g. "steps", "calories", "activities". Distinct from Kind, which only selects how to render, not which metric/endpoint this tile is.
	Category string // dashboard section this tile belongs in: "activity", "heart", "body", "sleep", "respiratory"
	Title    string
	Icon     string // key into the iconFor() templ component

	Expanded bool

	// TileKindStat / TileKindSleep
	BigValue        string
	Unit            string
	Subtext         string
	SparklineValues []float64   // 7-day trend values, rendered small when collapsed (see renderSparklineSVG)
	RingPct         *float64    // 0..1 progress ring drawn next to BigValue (steps only); nil means no ring
	Chart           *ChartData  // expanded bar chart — either an hourly-for-the-day view (steps) or a 7-day trend (everything else with no finer granularity)
	ChartSubtext    string      // label above Chart, set by whichever builder populated it (e.g. "Total today" vs "7-day average")
	Detail          *DetailData // expanded rich detail view (heart-rate intraday line, sleep stage timeline) — takes priority over Chart when set
	Empty           bool
	EmptyMsg        string

	// TileKindActivities
	Activities []ActivitySummary

	// TileKindBody
	Body *BodyMeasurementData
}

// DetailData is an expanded tile's rich detail view, for metrics with
// finer-than-daily data actually available (heart rate intraday samples,
// sleep stage timeline) — as opposed to Chart, which is either an
// hourly-for-the-day bar chart (steps) or a 7-day trend (every metric with
// no source finer than one value per day).
type DetailData struct {
	Kind        string // "line" (heart rate) or "stages" (sleep)
	Message     string // empty-state / info text, e.g. "only cached for the last 3 days"
	Stats       []StatItem
	LineSamples []HRSample     // Kind == "line"
	Stages      []StageSegment // Kind == "stages"
	RangeLabel  string         // Kind == "stages": formatted session start – end
}

// StatItem is one small label/value stat shown above a Detail view (e.g.
// Min/Avg/Max heart rate, or Awake/Light/Deep/REM sleep minutes).
type StatItem struct {
	Label string
	Value string
}

// StageSegment is one sleep stage's span within its session, expressed as a
// 0..1 fraction of the session's total duration so it can be drawn as a
// segment of a fixed-width timeline bar regardless of the session's actual
// length.
type StageSegment struct {
	Type     string // AWAKE | LIGHT | DEEP | REM
	StartPct float64
	EndPct   float64
}

// BodyMeasurementData is the editable weight/waist/neck form for one day.
// Weight reflects body_measurement's raw/override split (a connected scale
// writes raw, this form writes override); waist/neck have no upstream
// source at all, so the form writes them directly.
type BodyMeasurementData struct {
	Day         string
	WeightKg    *float64
	WaistCm     *float64
	NeckCm      *float64
	HasPriorDay bool // whether "Carry forward" has anything to offer
	SavedAt     string
	Error       string
}

// ChartData is a small 7-day bar chart, rendered as inline SVG (see
// chart.go) so no client-side charting library is needed.
type ChartData struct {
	Labels  []string
	Values  []float64
	Goal    *float64
	Average float64
	Total   float64
}

// ActivitySummary is one row in the activities tile / list.
type ActivitySummary struct {
	ID              int64
	Type            string
	StartLabel      string
	DurationMinutes int64
	CaloriesBurned  float64
	AvgHeartRateBpm *float64
}

// ActivityDetail is the full overlay view for one activity: its summary
// plus the heart-rate samples recorded during its interval.
type ActivityDetail struct {
	ActivitySummary
	Day              string
	StartTimeLabel   string
	EndTimeLabel     string
	HeartRateSamples []HRSample
}

type HRSample struct {
	TimeLabel string
	Bpm       float64
}

// JournalData is the state of the journal editor for one day.
type JournalData struct {
	Day         string
	Content     string
	ContentHTML string // rendered markdown preview
	AutoSave    bool
	SavedAt     string
	Error       string
}
