// Package healthdata is the shared domain model for watch/Google Health
// data — one Go struct per watch_* table (see internal/db/schema.sql's
// "Watch / Google Health API data" section for the full shape rationale).
// It deliberately imports nothing from this module (stdlib only), so
// internal/googlehealth (writing these), internal/web (reading these), and
// any future consumer can all depend on it without risking an import
// cycle between themselves.
//
// This is a domain layer, not a DB-scanning or wire-format layer: no
// database/sql types (sql.NullFloat64 etc.) and no JSON tags tying it to
// Google's API shape (that's internal/googlehealth/values.go's job).
// Pointer fields distinguish "not found for this day/point" (nil) from
// "found, value is zero" (non-nil) — the same convention
// internal/googlehealth's sync code already used before this package
// existed.
//
// Every type below is one of five fixed shapes (see schema.sql):
// DailyScalar (a field on DailySummary), DailyByCategory, Hourly, Timeline,
// or SegmentTimeline — plus the session types, which are deliberately
// bespoke. Types sharing a shape (e.g. HeartRateZoneMinutes and
// CaloriesByZone are both DailyByCategory) are kept as separate concrete
// structs rather than one generic type: they persist to different tables
// and represent different metrics, so a shared type would blur which one a
// function is actually handling — consistency of pattern, not forced
// literal reuse.
package healthdata

import "time"

// DailySummary mirrors watch_daily_summary — one row per calendar day.
// ActiveMinutesTotal is a denormalized convenience total; the
// light/moderate/vigorous breakdown lives in ActiveMinutesByLevel (same
// relationship StepsTotal has to HourlySteps).
type DailySummary struct {
	Day string

	StepsTotal         *int64
	DistanceM          *float64
	FloorsClimbed      *int64
	AltitudeGainM      *float64
	SedentaryMinutes   *int64
	ActiveMinutesTotal *int64

	KcalBurnedGoogle       *float64
	ActiveEnergyBurnedKcal *float64

	RestingHeartRateBpm *float64
	HeartRateMinBpm     *float64
	HeartRateMaxBpm     *float64
	HeartRateAvgBpm     *float64
	HrvAvgMs            *float64

	Vo2Max          *float64
	Vo2MaxSample    *float64
	Vo2MaxRunSample *float64

	Spo2AvgPct *float64
	Spo2MinPct *float64

	RespiratoryRateAvgBpm *float64

	// Computed by the sync job from the same Timeline fetch that populates
	// BloodGlucoseSample for the day — not a separate "last sample" query.
	BloodGlucoseAvgMgDl *float64
	BloodGlucoseMinMgDl *float64
	BloodGlucoseMaxMgDl *float64

	// Same pattern, from the fetch that populates CoreBodyTemperatureSample.
	CoreBodyTemperatureAvgC *float64
	CoreBodyTemperatureMinC *float64
	CoreBodyTemperatureMaxC *float64

	SleepDurationMinutes *int64

	SleepTemperatureC             *float64
	SleepTemperatureBaselineC     *float64
	SleepTemperatureDeviation30dC *float64

	SourceSyncedAt string
	RawPayload     map[string]string // dataTypeName -> raw JSON, see internal/googlehealth/sync.go
}

// ActiveMinutesByLevel is one (day, activity level) row — DailyByCategory
// shape, mirrors watch_active_minutes_by_level. Level is one of
// LIGHT/MODERATE/VIGOROUS (ActiveMinutes.ActivityLevel).
type ActiveMinutesByLevel struct {
	Day     string
	Level   string
	Minutes int64
}

// ActiveZoneMinutesByZone is one (day, heart-rate zone) row —
// DailyByCategory shape, mirrors watch_active_zone_minutes_by_zone.
// ZoneType is one of FAT_BURN/CARDIO/PEAK — the same domain
// HeartRateZoneMinutes and HeartRateZoneDefinition use.
type ActiveZoneMinutesByZone struct {
	Day      string
	ZoneType string
	Minutes  int64
}

// HeartRateZoneMinutes is one (day, heart-rate zone) row — DailyByCategory
// shape, mirrors watch_heart_rate_zone_minutes (actual time spent in each
// zone, the time-series counterpart to HeartRateZoneDefinition's
// thresholds).
type HeartRateZoneMinutes struct {
	Day      string
	ZoneType string
	Minutes  float64
}

// CaloriesByZone is one (day, heart-rate zone) row — DailyByCategory shape,
// mirrors watch_calories_by_zone. INFERRED field shape (see
// internal/googlehealth/values.go's confidence-level convention).
type CaloriesByZone struct {
	Day      string
	ZoneType string
	Kcal     float64
}

// HeartRateZoneDefinition is one (day, heart-rate zone) row — a
// definition, not a measurement (see schema.sql) — mirrors
// watch_heart_rate_zone_definition.
type HeartRateZoneDefinition struct {
	Day      string
	ZoneType string
	MinBpm   int64
	MaxBpm   int64
}

// HourlySteps is one (day, hour) bucket — Hourly shape, mirrors
// watch_steps_hourly. DistanceM/Calories are schema columns that exist but
// currently always come back NULL for every account tested so far (see
// prerequisite.md) — kept here so a future confirmed source can populate
// them without a struct change.
type HourlySteps struct {
	Day       string
	Hour      int
	Steps     *int64
	DistanceM *float64
	Calories  *float64
}

// HeartRateSample is one exact-timestamp reading — Timeline shape, mirrors
// watch_heart_rate_intraday (a rolling cache, not permanent history — see
// its schema comment).
type HeartRateSample struct {
	RecordedAt time.Time
	Bpm        float64
}

// BloodGlucoseSample is one exact-timestamp reading — Timeline shape,
// mirrors watch_blood_glucose_sample.
type BloodGlucoseSample struct {
	RecordedAt        time.Time
	MgDl              float64
	MeasurementSource string
	MeasurementTiming string
	MealType          string
	Specimen          string
}

// CoreBodyTemperatureSample is one exact-timestamp reading — Timeline
// shape, mirrors watch_core_body_temperature_sample.
type CoreBodyTemperatureSample struct {
	RecordedAt          time.Time
	Celsius             float64
	MeasurementLocation string
}

// ActivityLevelSegment is one span of a single activity level — SegmentTimeline
// shape, mirrors watch_activity_level_segment. Level is one of
// SEDENTARY/LIGHTLY_ACTIVE/MODERATELY_ACTIVE/VERY_ACTIVE
// (ActivityLevel.ActivityLevelType).
type ActivityLevelSegment struct {
	Day       string
	Level     string
	StartTime time.Time
	EndTime   time.Time
}

// SleepSession is one sleep session (usually one "main sleep" per day, naps
// can add more) — a session type, deliberately bespoke rather than one of
// the five shapes. Mirrors watch_sleep_session; its stage-level detail is
// SleepStage below (a SegmentTimeline nested inside the session).
type SleepSession struct {
	ID              int64
	Day             string
	StartTime       time.Time
	EndTime         time.Time
	IsMainSleep     bool
	DurationMinutes *int64
	EfficiencyPct   *float64

	MinutesAwake *int64
	MinutesLight *int64
	MinutesDeep  *int64
	MinutesRem   *int64

	// Per-stage respiratory rate (from respiratory-rate-sleep-summary,
	// matched to whichever session's interval contains its sample time).
	DeepRespRateBpm  *float64
	LightRespRateBpm *float64
	RemRespRateBpm   *float64
	FullRespRateBpm  *float64
}

// SleepStage is one stage-timeline segment within a SleepSession —
// SegmentTimeline shape, mirrors watch_sleep_stage. StageType is one of
// AWAKE/LIGHT/DEEP/REM.
type SleepStage struct {
	SleepSessionID int64
	StageType      string
	StartTime      time.Time
	EndTime        time.Time
}

// ExerciseSession is one workout — a session type, deliberately bespoke.
// Mirrors watch_exercise_session. Its heart-rate detail comes from
// correlating HeartRateSample's Timeline against StartTime/EndTime, not a
// separate table.
type ExerciseSession struct {
	ID              int64
	Day             string
	ExerciseType    string
	StartTime       time.Time
	EndTime         time.Time
	DurationMinutes *int64
	CaloriesBurned  *float64
	AvgHeartRateBpm *float64
	MaxHeartRateBpm *float64
	DistanceM       *float64
}

// ECGReading is one ECG reading or irregular-rhythm notification — an
// event-log type, deliberately bespoke. Mirrors watch_ecg_reading; Type
// discriminates "ecg" vs "irregular_rhythm". Only summary/classification
// is kept, never raw waveform data (see schema.sql).
type ECGReading struct {
	ID              int64
	Type            string
	RecordedAt      time.Time
	Classification  string
	AvgHeartRateBpm *float64
}
