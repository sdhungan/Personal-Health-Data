package googlehealth

// kind classifies how a data type's timestamps work, which determines how
// to build a time-range filter for it (see developers.google.com/health/data-types).
type kind int

const (
	kindInterval kind = iota
	kindSample
	kindSession
	kindDailyAggregate
)

// dataType pairs a Google Health API data type identifier with its kind.
// This list is every type under the activity_and_fitness, sleep,
// health_metrics_and_measurements, ecg, and irn scopes — the scopes
// healthd actually requests (see internal/googleauth.Scopes). Nutrition,
// location, profile, and settings types are excluded since we don't
// request those scopes.
type dataType struct {
	Name string
	Kind kind
	// NoList means list() is rejected outright for this data type — confirmed
	// by the API's own error ("List is not supported for data type X, but the
	// following actions are supported: ..."). Only rollup/dailyRollUp work.
	NoList bool
}

var DataTypes = []dataType{
	{Name: "steps", Kind: kindInterval},
	{Name: "distance", Kind: kindInterval},
	{Name: "floors", Kind: kindInterval, NoList: true},
	{Name: "active-minutes", Kind: kindInterval},
	{Name: "active-zone-minutes", Kind: kindInterval},
	{Name: "activity-level", Kind: kindInterval},
	{Name: "altitude", Kind: kindInterval},
	{Name: "sedentary-period", Kind: kindInterval},
	{Name: "time-in-heart-rate-zone", Kind: kindInterval},
	{Name: "calories-in-heart-rate-zone", Kind: kindInterval, NoList: true},
	{Name: "total-calories", Kind: kindInterval, NoList: true},
	{Name: "active-energy-burned", Kind: kindInterval},
	{Name: "vo2-max", Kind: kindSample},
	{Name: "run-vo2-max", Kind: kindSample},
	{Name: "daily-vo2-max", Kind: kindDailyAggregate},
	{Name: "exercise", Kind: kindSession},
	// Confirmed to exist via the RPC reference (SwimLengthsData /
	// SwimLengthsDataRollupValue) but never observed against a real
	// response from this account, so its field shape isn't modeled in
	// values.go yet and DBSyncer doesn't sync it — see prerequisite.md.
	// Listed here so DumpToday can capture a real response to verify
	// against, the same "confirm before trusting" discipline every other
	// type in this file follows.
	{Name: "swim-lengths-data", Kind: kindSession},

	{Name: "body-fat", Kind: kindSample},
	{Name: "height", Kind: kindSample},
	{Name: "weight", Kind: kindSample},

	{Name: "sleep", Kind: kindSession},
	{Name: "daily-sleep-temperature-derivations", Kind: kindDailyAggregate},

	{Name: "heart-rate", Kind: kindSample},
	{Name: "heart-rate-variability", Kind: kindSample},
	{Name: "daily-heart-rate-variability", Kind: kindDailyAggregate},
	{Name: "daily-heart-rate-zones", Kind: kindDailyAggregate},
	{Name: "daily-resting-heart-rate", Kind: kindDailyAggregate},
	{Name: "electrocardiogram", Kind: kindSession},
	{Name: "irregular-rhythm-notification", Kind: kindSession},

	{Name: "blood-glucose", Kind: kindSample},
	{Name: "core-body-temperature", Kind: kindSample},
	{Name: "daily-oxygen-saturation", Kind: kindDailyAggregate},
	{Name: "oxygen-saturation", Kind: kindSample},
	{Name: "daily-respiratory-rate", Kind: kindDailyAggregate},
	{Name: "respiratory-rate-sleep-summary", Kind: kindDailyAggregate},
}

// RollUpDataTypes is the subset dailyRollUp is confirmed to support — either
// documented in the dailyRollUp response schema, or confirmed by a
// successful call, or (for daily-heart-rate-variability/
// daily-resting-heart-rate) confirmed EXCLUDED by the API's own error
// ("DailyRollup is not supported for data type X, ... supported: list,
// reconcile") and therefore left out here.
var RollUpDataTypes = []string{
	"steps",
	"heart-rate",
	"weight",
	"distance",
	"altitude",
	"active-minutes",
	"total-calories",
	"floors",
}
