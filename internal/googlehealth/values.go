package googlehealth

// Typed values for each data type's payload, for use with ExtractValues /
// ExtractRollupValues. Every field name and shape below falls into one of
// three confidence levels, noted per type:
//
//   - CONFIRMED: decoded from a real response against a live account while
//     building this (see the dump this package's dump.go produces).
//   - DOCUMENTED: taken from Google's own proto reference
//     (developers.google.com/health/reference/rpc/google.devicesandservices.health.v4)
//     but not yet observed in a real response from this account.
//   - INFERRED: no documentation found; guessed by symmetry with a
//     confirmed/documented sibling type. Treat as a starting point, not
//     a guarantee — verify against a real response before relying on it.
//
// Deliberately dropped everywhere: dataSource (device/platform/recording
// method) and the opaque point "name" — see envelope.go. Also dropped:
// Electrocardiogram's waveformSamples (a large raw signal array; storing
// only the summary/classification was the schema's intent from the start,
// see internal/db/schema.sql's watch_ecg_reading comment) and the
// civilTime/UTC-offset fields alongside every interval/sample time (the
// physical timestamp is enough to bucket by day/hour ourselves).

// ---- CONFIRMED (daily aggregates) ----

type DailyHeartRateZones struct {
	Date           Date            `json:"date"`
	HeartRateZones []HeartRateZone `json:"heartRateZones"`
}

type HeartRateZone struct {
	Type              string  `json:"heartRateZoneType"`
	MinBeatsPerMinute PBInt64 `json:"minBeatsPerMinute"`
	MaxBeatsPerMinute PBInt64 `json:"maxBeatsPerMinute"`
}

type DailyRestingHeartRate struct {
	Date           Date    `json:"date"`
	BeatsPerMinute PBInt64 `json:"beatsPerMinute"`
	Metadata       struct {
		CalculationMethod string `json:"calculationMethod"`
	} `json:"dailyRestingHeartRateMetadata"`
}

type DailyOxygenSaturation struct {
	Date                        Date      `json:"date"`
	AveragePercentage           PBFloat64 `json:"averagePercentage"`
	LowerBoundPercentage        PBFloat64 `json:"lowerBoundPercentage"`
	UpperBoundPercentage        PBFloat64 `json:"upperBoundPercentage"`
	StandardDeviationPercentage PBFloat64 `json:"standardDeviationPercentage"`
}

type DailyHeartRateVariability struct {
	Date                                                       Date      `json:"date"`
	AverageHeartRateVariabilityMilliseconds                    PBFloat64 `json:"averageHeartRateVariabilityMilliseconds"`
	NonRemHeartRateBeatsPerMinute                              PBInt64   `json:"nonRemHeartRateBeatsPerMinute"`
	Entropy                                                    PBFloat64 `json:"entropy"`
	DeepSleepRootMeanSquareOfSuccessiveDifferencesMilliseconds PBFloat64 `json:"deepSleepRootMeanSquareOfSuccessiveDifferencesMilliseconds"`
}

type DailySleepTemperatureDerivations struct {
	Date                            Date      `json:"date"`
	NightlyTemperatureCelsius       PBFloat64 `json:"nightlyTemperatureCelsius"`
	BaselineTemperatureCelsius      PBFloat64 `json:"baselineTemperatureCelsius"`
	RelativeNightlyStddev30dCelsius PBFloat64 `json:"relativeNightlyStddev30dCelsius"`
}

// RespiratoryRateSleepSummary is CONFIRMED, but its shape turned out
// different from what its name/category suggested: it's sample-based
// (sampleTime), not a plain daily aggregate keyed by date.
type RespiratoryRateSleepSummary struct {
	SampleTime      ObservationSampleTime `json:"sampleTime"`
	DeepSleepStats  RespiratoryRateStats  `json:"deepSleepStats"`
	LightSleepStats RespiratoryRateStats  `json:"lightSleepStats"`
	RemSleepStats   RespiratoryRateStats  `json:"remSleepStats"`
	FullSleepStats  RespiratoryRateStats  `json:"fullSleepStats"`
}

type RespiratoryRateStats struct {
	BreathsPerMinute  PBFloat64 `json:"breathsPerMinute"`
	StandardDeviation PBFloat64 `json:"standardDeviation"`
	SignalToNoise     PBFloat64 `json:"signalToNoise"`
}

type DailyRespiratoryRate struct {
	Date             Date      `json:"date"`
	BreathsPerMinute PBFloat64 `json:"breathsPerMinute"`
}

// TotalCaloriesRollup is CONFIRMED, from dailyRollUp (list() isn't
// supported for total-calories at all).
type TotalCaloriesRollup struct {
	KcalSum PBFloat64 `json:"kcalSum"`
}

// FloorsRollup is UNCONFIRMED — floors has NoList set (list() outright
// rejected), so dailyRollUp is the only fetch path, but this account's own
// floors rollup response has always come back empty (no real sample to
// verify a shape against). This guesses by symmetry with
// TotalCaloriesRollup; a wrong key here is safe (ExtractRollupValues just
// returns no points) rather than silently decoding the wrong field.
type FloorsRollup struct {
	FloorsSum PBFloat64 `json:"floorsSum"`
}

// ---- CONFIRMED (sessions) ----

// Electrocardiogram is CONFIRMED. waveformSamples (the raw signal, an
// array of thousands of ints) is deliberately not modeled here — see the
// file-level comment.
type Electrocardiogram struct {
	Interval             SessionTimeInterval `json:"interval"`
	BeatsPerMinuteAvg    PBInt64             `json:"beatsPerMinuteAvg"`
	ResultClassification string              `json:"resultClassification"`
}

// Exercise is CONFIRMED.
type Exercise struct {
	Interval       SessionTimeInterval    `json:"interval"`
	ExerciseType   string                 `json:"exerciseType"`
	DisplayName    string                 `json:"displayName"`
	ActiveDuration PBDuration             `json:"activeDuration"`
	MetricsSummary ExerciseMetricsSummary `json:"metricsSummary"`
}

type ExerciseMetricsSummary struct {
	CaloriesKcal                   PBFloat64              `json:"caloriesKcal"`
	AverageHeartRateBeatsPerMinute PBInt64                `json:"averageHeartRateBeatsPerMinute"`
	ActiveZoneMinutes              PBInt64                `json:"activeZoneMinutes"`
	HeartRateZoneDurations         HeartRateZoneDurations `json:"heartRateZoneDurations"`
}

type HeartRateZoneDurations struct {
	LightTime    PBDuration `json:"lightTime"`
	ModerateTime PBDuration `json:"moderateTime"`
	VigorousTime PBDuration `json:"vigorousTime"`
	PeakTime     PBDuration `json:"peakTime"`
}

// Sleep is CONFIRMED.
type Sleep struct {
	Interval SessionTimeInterval `json:"interval"`
	Type     string              `json:"type"` // e.g. "STAGES"
	Stages   []SleepStage        `json:"stages"`
	Summary  SleepSummary        `json:"summary"`
}

type SleepStage struct {
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
	Type      string `json:"type"` // "AWAKE", "LIGHT", "DEEP", "REM"
}

type SleepSummary struct {
	MinutesInSleepPeriod PBInt64             `json:"minutesInSleepPeriod"`
	MinutesAfterWakeUp   PBInt64             `json:"minutesAfterWakeUp"`
	MinutesToFallAsleep  PBInt64             `json:"minutesToFallAsleep"`
	MinutesAsleep        PBInt64             `json:"minutesAsleep"`
	MinutesAwake         PBInt64             `json:"minutesAwake"`
	StagesSummary        []SleepStageSummary `json:"stagesSummary"`
}

type SleepStageSummary struct {
	Type    string  `json:"type"`
	Minutes PBInt64 `json:"minutes"`
	Count   PBInt64 `json:"count"`
}

// ---- DOCUMENTED (from Google's proto reference, not yet observed) ----

type Steps struct {
	Interval ObservationTimeInterval `json:"interval"`
	Count    PBInt64                 `json:"count"`
}

type Distance struct {
	Interval ObservationTimeInterval `json:"interval"`
	Meters   PBInt64                 `json:"meters"`
}

type Floors struct {
	Interval ObservationTimeInterval `json:"interval"`
	Floor    PBFloat64               `json:"floor"`
}

type Altitude struct {
	Interval        ObservationTimeInterval `json:"interval"`
	GainMillimeters PBInt64                 `json:"gainMillimeters"`
}

type ActiveZoneMinutes struct {
	Interval          ObservationTimeInterval `json:"interval"`
	HeartRateZone     string                  `json:"heartRateZone"`
	ActiveZoneMinutes PBInt64                 `json:"activeZoneMinutes"`
}

type ActiveMinutes struct {
	Interval                     ObservationTimeInterval `json:"interval"`
	ActiveMinutesByActivityLevel []ActiveMinutesByLevel  `json:"activeMinutesByActivityLevel"`
}

type ActiveMinutesByLevel struct {
	ActivityLevel string  `json:"activityLevel"` // "LIGHT", "MODERATE", "VIGOROUS"
	Minutes       PBInt64 `json:"minutes"`
}

type ActivityLevel struct {
	Interval          ObservationTimeInterval `json:"interval"`
	ActivityLevelType string                  `json:"activityLevelType"` // SEDENTARY, LIGHTLY_ACTIVE, MODERATELY_ACTIVE, VERY_ACTIVE
}

type ActiveEnergyBurned struct {
	Interval ObservationTimeInterval `json:"interval"`
	Kcal     PBFloat64               `json:"kcal"`
}

type TimeInHeartRateZone struct {
	Interval       ObservationTimeInterval `json:"interval"`
	HeartRateZone  string                  `json:"heartRateZone"`
	DurationMillis PBInt64                 `json:"durationMillis"`
}

// HeartRate is CONFIRMED — was DOCUMENTED-only (field guessed as "bpm")
// until real data showed the actual key is "beatsPerMinute", matching the
// verbose-naming convention Google uses everywhere else (see
// DailyRestingHeartRate, Exercise.MetricsSummary). The wrong guess didn't
// error — it silently decoded every sample's rate as 0 instead — which is
// why every DOCUMENTED/INFERRED field in this file should be treated as
// unverified until checked against a real response, not just "probably
// fine because it compiles."
type HeartRate struct {
	SampleTime     ObservationSampleTime `json:"sampleTime"`
	BeatsPerMinute PBInt64               `json:"beatsPerMinute"`
}

type Weight struct {
	SampleTime ObservationSampleTime `json:"sampleTime"`
	Kilograms  PBFloat64             `json:"kilograms"`
}

type BodyFat struct {
	SampleTime ObservationSampleTime `json:"sampleTime"`
	Percentage PBFloat64             `json:"percentage"`
}

// HeartRateVariability is CONFIRMED — was DOCUMENTED-only (field guessed
// as "millis", an int64) until real data showed the actual key is
// "rootMeanSquareOfSuccessiveDifferencesMilliseconds", a float (e.g.
// 42.1), not an integer millisecond count.
type HeartRateVariability struct {
	SampleTime                                        ObservationSampleTime `json:"sampleTime"`
	RootMeanSquareOfSuccessiveDifferencesMilliseconds PBFloat64             `json:"rootMeanSquareOfSuccessiveDifferencesMilliseconds"`
}

type BloodGlucose struct {
	SampleTime                         ObservationSampleTime `json:"sampleTime"`
	BloodGlucoseMilligramsPerDeciliter PBFloat64             `json:"bloodGlucoseMilligramsPerDeciliter"`
	MeasurementSource                  string                `json:"measurementSource,omitempty"`
	MeasurementTiming                  string                `json:"measurementTiming,omitempty"`
	MealType                           string                `json:"mealType,omitempty"`
	Specimen                           string                `json:"specimen,omitempty"`
}

type OxygenSaturation struct {
	SampleTime ObservationSampleTime `json:"sampleTime"`
	Percentage PBFloat64             `json:"percentage"`
}

type CoreBodyTemperature struct {
	SampleTime          ObservationSampleTime `json:"sampleTime"`
	TemperatureCelsius  PBFloat64             `json:"temperatureCelsius"`
	MeasurementLocation string                `json:"measurementLocation,omitempty"`
}

type Vo2Max struct {
	SampleTime                      ObservationSampleTime `json:"sampleTime"`
	Vo2MaxMillilitersPerKgPerMinute PBFloat64             `json:"vo2MaxMillilitersPerKilogramPerMinute"`
	MeasurementMethod               string                `json:"measurementMethod,omitempty"` // MANUAL, DERIVED
}

type RunVo2Max struct {
	SampleTime    ObservationSampleTime `json:"sampleTime"`
	MlPerKgPerMin PBFloat64             `json:"mlPerKgPerMin"`
}

type DailyVo2Max struct {
	Date               Date      `json:"date"`
	Estimated          bool      `json:"estimated,omitempty"`
	CardioFitnessLevel string    `json:"cardioFitnessLevel,omitempty"` // POOR..EXCELLENT
	Vo2Max             PBFloat64 `json:"vo2Max"`
	Vo2MaxCovariance   PBFloat64 `json:"vo2MaxCovariance,omitempty"`
}

// IrregularRhythmNotification is DOCUMENTED only partially — Google's
// reference names alert_window/heart_beat sub-messages without detailing
// their fields, and this account has never triggered one (list() returns
// {}), so there's nothing real to check them against yet.
type IrregularRhythmNotification struct {
	Date Date `json:"date"`
}

// ---- INFERRED (no documentation found; guessed by symmetry) ----

// Height is INFERRED from Weight's shape (sample_time + a single double
// field); Google's other length-valued types (Distance) use meters.
type Height struct {
	SampleTime ObservationSampleTime `json:"sampleTime"`
	Meters     PBFloat64             `json:"meters"`
}

// SedentaryPeriod is INFERRED as just a time span with no extra value —
// Google's reference doesn't detail a value field for it.
type SedentaryPeriod struct {
	Interval ObservationTimeInterval `json:"interval"`
}

// CaloriesInHeartRateZone is INFERRED from TimeInHeartRateZone's shape.
type CaloriesInHeartRateZone struct {
	Interval      ObservationTimeInterval `json:"interval"`
	HeartRateZone string                  `json:"heartRateZone"`
	Kcal          PBFloat64               `json:"kcal"`
}
