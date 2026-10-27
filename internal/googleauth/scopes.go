package googleauth

// Scopes healthd requests. Google Health API scopes are all Restricted and
// split into a separate read/write pair per data category (see
// https://developers.google.com/health/scopes); healthd only ever reads,
// and only the categories the watch_* schema (internal/db/schema.sql)
// models: activity/fitness, sleep, the broad health-metrics-and-
// measurements bucket (heart rate, HRV, SpO2, VO2 max, weight, height,
// body fat), ECG, irregular rhythm notifications, and location (only used
// for an exercise's GPS route, see googlehealth.ExportExerciseTcx).
// Nutrition, profile, and settings scopes are deliberately not requested —
// nutrition comes from Cronometer in this system, and the rest isn't
// modeled. A token granted before a scope was added here doesn't have it:
// the account has to reconnect Google Health to grant it.
var Scopes = []string{
	"https://www.googleapis.com/auth/googlehealth.location.readonly",
	"https://www.googleapis.com/auth/googlehealth.activity_and_fitness.readonly",
	"https://www.googleapis.com/auth/googlehealth.sleep.readonly",
	"https://www.googleapis.com/auth/googlehealth.health_metrics_and_measurements.readonly",
	"https://www.googleapis.com/auth/googlehealth.ecg.readonly",
	"https://www.googleapis.com/auth/googlehealth.irn.readonly",
}
