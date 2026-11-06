// Package views holds every templ component healthd's dashboard renders,
// plus the plain-Go view-model structs they take as parameters. Nothing
// here touches the database directly — internal/web builds these structs
// from query results and hands them to a component to render.
package views

import "time"

// DashboardData is everything the main dashboard page needs: which day is
// selected and the tiles to lay out in the grid.
type DashboardData struct {
	Day      string // "YYYY-MM-DD"
	DayLabel string // "Today", "Yesterday", or a formatted date
	PrevDay  string
	NextDay  string
	Today    string     // "YYYY-MM-DD" for the real current day — lets DayNav's "Today" button jump straight there regardless of which day is selected, distinct from Day (the day being viewed)
	View     string     // "data" or "journal" — which tab is active
	Range    TrendRange // the range selector's choice; zero (no selector shown) outside the data view
	Tiles    []TileData

	// MissingByCategory lists, per dashboard section, the titles of tiles
	// that had no data for this day and were filtered out of Tiles (see
	// shouldHideEmptyTile in handlers.go) — rendered as one compact
	// EmptySummaryTile per section instead of either a full-size empty tile
	// per metric or the section leaving an inconsistent gap by vanishing
	// silently.
	MissingByCategory map[string][]string

	// Username is the logged-in account's name, shown in the header
	// alongside the log-out control (see webauth.CurrentUsername).
	Username string

	// CronometerConnected reports whether encrypted Cronometer credentials
	// are on file (see internal/cronometer.LoadCredentials) — drives
	// whether the Nutrition section's account card shows the login form or
	// a connected status. CronometerMessage carries a login error/status to
	// show alongside it, when set.
	CronometerConnected bool
	CronometerMessage   string
}

// TrendRange is one choice of the range selector (see RangeNav): how many
// days every trend chart and sparkline covers, ending on the selected day.
// Key is both its button label and its ?range= value.
type TrendRange struct {
	Key  string
	Days int
}

var TrendRanges = []TrendRange{{"1D", 1}, {"3D", 3}, {"1W", 7}, {"2W", 14}, {"1M", 30}, {"3M", 91}, {"6M", 182}, {"1Y", 365}}

// GoogleBulkSyncStatus drives the "Bulk sync" section of
// GoogleClientSettingsPage. HasHistory selects which form is shown: a
// one-click "catch up to today" button once this account has any prior
// google_health sync_state history, or a date-range picker (capped at 30
// days server-side) for a brand-new connection with none yet. Running means
// a bulk sync is currently executing in the background (see
// Server.handleGoogleBulkSync) — the page shows a spinner-ish message and
// auto-refreshes instead of a form. Summary/Err report the outcome of the
// most recent run once it's no longer Running.
type GoogleBulkSyncStatus struct {
	HasHistory bool
	Running    bool
	Summary    string
	Err        string
	Today      string // "YYYY-MM-DD", today's date — the range picker's default/max
}

// TileKind selects which body a tile renders.
type TileKind string

const (
	TileKindStat        TileKind = "stat"
	TileKindSleep       TileKind = "sleep"
	TileKindActivities  TileKind = "activities"
	TileKindBody        TileKind = "body"
	TileKindFoodLog     TileKind = "food_log"
	TileKindBodyHistory TileKind = "body_history"
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
	Width    int // saved expanded width in twelfths of the row (2-12, 12 = full row); 0 = the tile's built-in default
	Height   int // saved expanded height in px (snapped to 20); 0 = the tile's built-in default

	// TileKindStat / TileKindSleep
	BigValue        string
	Unit            string
	Subtext         string
	SparklineValues []float64   // trendDays-day trend values, rendered small when collapsed (see renderSparklineSVG)
	RingPct         *float64    // 0..1 progress ring drawn next to BigValue (steps only); nil means no ring
	Chart           *ChartData  // expanded bar chart — either an hourly-for-the-day view (steps) or a 14-day trend (everything else with no finer granularity)
	ChartSubtext    string      // label above Chart, set by whichever builder populated it (e.g. "Total today" vs "14-day average")
	Detail          *DetailData // expanded rich detail view (heart-rate intraday line, sleep stage timeline) — takes priority over Chart when set
	Empty           bool
	EmptyMsg        string

	// TileKindActivities
	Activities []ActivitySummary

	// TileKindBody
	Body *BodyMeasurementData

	// Body measurement stat tiles: all-time line instead of the 14-day chart
	ShowAll bool

	// TileKindBodyHistory, newest first
	BodyHistory []BodyHistoryRow

	// TileKindFoodLog
	FoodLog []ServingSummary
}

// DetailData is an expanded tile's rich detail view, for metrics with
// finer-than-daily data actually available (heart rate intraday samples,
// sleep stage timeline) — as opposed to Chart, which is either an
// hourly-for-the-day bar chart (steps) or a 14-day trend (every metric with
// no source finer than one value per day).
type DetailData struct {
	Kind        string // "line" (heart rate) or "stages" (sleep)
	Message     string // empty-state / info text, e.g. "only cached for the last 3 days"
	Stats       []StatItem
	LineSamples []HRSample // Kind == "line"
	LineUnit    string     // Kind == "line": tooltip unit, e.g. "bpm"
	// LineZeroBased: y-axis from 0 like the bar charts (amounts: kcal, km),
	// instead of heart rate's min-10..max+10.
	LineZeroBased bool
	// LineDots: a dot on every sample, however many (the hourly activity
	// lines); otherwise only up to maxDotSamples.
	LineDots  bool
	StageRows []StageRow // Kind == "stages": one timeline per row (sleep: main sleep + each nap)
}

// StageRow is one segmented timeline bar with its label, e.g.
// "Nap · 1:43 PM – 2:52 PM".
type StageRow struct {
	Label  string
	Stages []StageSegment
	// Lanes set means a lane chart over Start..End (a lane per segment type,
	// clock times along the bottom — see renderLaneChartSVG); none means a
	// flat segmented bar.
	Lanes      []Lane
	Start, End time.Time
}

// StatItem is one small label/value stat shown above a Detail view (e.g.
// Min/Avg/Max heart rate, or Awake/Light/Deep/REM sleep minutes).
type StatItem struct {
	Label string
	Value string
	Sub   string // optional second line under Value, e.g. a sleep stage's share "23%"
}

// StageSegment is one sleep stage's span within its session, or one
// activity-level span within a day, expressed as a 0..1 fraction of the
// total duration so it can be drawn as a segment of a fixed-width timeline
// bar regardless of the session's/day's actual length. TimeLabel is the
// caller's pre-formatted actual clock-time range (the renderer only has the
// fraction, not the underlying time.Time) shown on hover alongside Type.
type StageSegment struct {
	Type      string // AWAKE | LIGHT | DEEP | REM, or SEDENTARY | LIGHTLY_ACTIVE | MODERATELY_ACTIVE | VERY_ACTIVE
	StartPct  float64
	EndPct    float64
	TimeLabel string
}

// BodyHistoryRow is one day in the Body History list.
type BodyHistoryRow struct {
	Day                                   string
	WeightKg, WaistCm, NeckCm, BodyFatPct *float64
}

// BodyMeasurementData is the editable weight/waist/neck form for one
// day. Weight reflects body_measurement's raw/override split
// (a connected scale writes raw, this form writes override); waist/neck have
// no upstream source at all, so the form writes them directly.
type BodyMeasurementData struct {
	Day      string
	WeightKg *float64
	WaistCm  *float64
	NeckCm   *float64
	SavedAt  string
	Error    string
}

// ChartData is a small 14-day bar chart, rendered as inline SVG (see
// chart.go) so no client-side charting library is needed.
type ChartData struct {
	Labels  []string
	Values  []float64
	Goal    *float64
	Average float64
	Total   float64
	// Unit captions the y-axis (e.g. "km"); ValueLabels, when set, is each
	// bar's tooltip text, formatted with its unit (e.g. "5.6 km", "6h 5m").
	Unit        string
	ValueLabels []string
	// YMax fixes the y-axis top (minutes per hour: 60); 0 sizes it to the data.
	YMax float64
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

	// GPS route, only for sessions recorded with GPS: RouteJSON is
	// [[lat,lng],...] for static/activity-map.js, RouteError why it's
	// missing (e.g. location access not granted).
	HasGPS     bool
	RouteJSON  string
	RouteError string
}

type HRSample struct {
	TimeLabel  string
	Bpm        float64
	ValueLabel string // tooltip text; empty means the bare number plus the chart's unit
}

// ServingSummary is one row in the food-log tile — a single
// cronometer_serving entry. QuantityLabel is pre-formatted ("150 g", "1
// cup") since quantity_units is only known when Cronometer's own food
// lookup succeeded (see internal/cronometer's sync — unresolved foods have
// no quantity/units, only grams-implied nutrients). ID drives the click-to-detail
// popup (see FoodServingDetail), the same pattern ActivitySummary.ID does.
type ServingSummary struct {
	ID            int64
	TimeLabel     string
	FoodName      string
	QuantityLabel string
	EnergyKcal    float64
	ProteinG      *float64
	CarbsG        *float64
	FatG          *float64
}

// FoodServingDetail is the full overlay view for one cronometer_serving row
// — the same click-to-detail popup pattern ActivityDetail uses, showing a
// handful of the most commonly-relevant nutrients beyond the four already
// visible in the food-log list, when Cronometer's food lookup provided them.
type FoodServingDetail struct {
	FoodName      string
	TimeLabel     string
	QuantityLabel string
	EnergyKcal    float64
	ProteinG      *float64
	CarbsG        *float64
	FatG          *float64
	FiberG        *float64
	SugarsG       *float64
	SaturatedG    *float64
	SodiumMg      *float64
	CholesterolMg *float64
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
