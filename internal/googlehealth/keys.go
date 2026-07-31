package googlehealth

// ValueKey returns the camelCase JSON key a data type's value appears under
// in list()/dailyRollUp() responses (e.g. "daily-resting-heart-rate" ->
// "dailyRestingHeartRate"). This does NOT always match a simple kebab-to-
// camel conversion of the URL path segment in every API — for this one it
// has, consistently, across every type checked against a real response.
// Returns "" for an unrecognized name.
func ValueKey(dataTypeName string) string {
	switch dataTypeName {
	case "steps":
		return "steps"
	case "distance":
		return "distance"
	case "floors":
		return "floors"
	case "altitude":
		return "altitude"
	case "active-minutes":
		return "activeMinutes"
	case "active-zone-minutes":
		return "activeZoneMinutes"
	case "activity-level":
		return "activityLevel"
	case "sedentary-period":
		return "sedentaryPeriod"
	case "time-in-heart-rate-zone":
		return "timeInHeartRateZone"
	case "calories-in-heart-rate-zone":
		return "caloriesInHeartRateZone"
	case "total-calories":
		return "totalCalories"
	case "active-energy-burned":
		return "activeEnergyBurned"
	case "vo2-max":
		return "vo2Max"
	case "run-vo2-max":
		return "runVo2Max"
	case "daily-vo2-max":
		return "dailyVo2Max"
	case "exercise":
		return "exercise"
	case "body-fat":
		return "bodyFat"
	case "height":
		return "height"
	case "weight":
		return "weight"
	case "sleep":
		return "sleep"
	case "daily-sleep-temperature-derivations":
		return "dailySleepTemperatureDerivations"
	case "heart-rate":
		return "heartRate"
	case "heart-rate-variability":
		return "heartRateVariability"
	case "daily-heart-rate-variability":
		return "dailyHeartRateVariability"
	case "daily-heart-rate-zones":
		return "dailyHeartRateZones"
	case "daily-resting-heart-rate":
		return "dailyRestingHeartRate"
	case "electrocardiogram":
		return "electrocardiogram"
	case "irregular-rhythm-notification":
		return "irregularRhythmNotification"
	case "blood-glucose":
		return "bloodGlucose"
	case "core-body-temperature":
		return "coreBodyTemperature"
	case "daily-oxygen-saturation":
		return "dailyOxygenSaturation"
	case "oxygen-saturation":
		return "oxygenSaturation"
	case "daily-respiratory-rate":
		return "dailyRespiratoryRate"
	case "respiratory-rate-sleep-summary":
		return "respiratoryRateSleepSummary"
	default:
		return ""
	}
}
