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
	TileKindGoal       TileKind = "goal"
)

// TileData is a generic tile. Not every field applies to every kind —
// e.g. Chart only applies to TileKindStat, Activities only to
// TileKindActivities.
type TileData struct {
	ID     string // unique DOM id, e.g. "tile-steps"
	Kind   TileKind
	Metric string // the data-fetch identifier for the /api/tile?kind= query — e.g. "steps", "calories", "activities", "goal". Distinct from Kind, which only selects how to render, not which metric/endpoint this tile is.
	Title  string
	Icon   string // key into the icon() templ component

	Expanded bool

	// TileKindStat / TileKindSleep
	BigValue string
	Unit     string
	Subtext  string
	Chart    *ChartData
	Empty    bool
	EmptyMsg string

	// TileKindActivities
	Activities []ActivitySummary

	// TileKindGoal
	GoalMessage string
	GoalPct     float64 // 0..1, how full the goal-progress glass is
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
