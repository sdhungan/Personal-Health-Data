package googlehealth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// upsertDailySummary writes sum into watch_daily_summary. Every column
// binds a nullable value: COALESCE(excluded.col, watch_daily_summary.col)
// means a field this fetch didn't find (nil) leaves whatever was already
// stored untouched, while a freshly fetched value always overwrites —
// sync is the sole writer of these columns (see ARCHITECTURE.md §3), so
// there's no manual edit to protect against clobbering.
func (s *DBSyncer) upsertDailySummary(ctx context.Context, dayStr string, sum dailySummary) error {
	var rawPayload any
	if len(sum.RawPayload) > 0 {
		b, err := json.Marshal(sum.RawPayload)
		if err != nil {
			return fmt.Errorf("marshaling raw_payload: %w", err)
		}
		rawPayload = string(b)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO watch_daily_summary (
			day, steps_total, distance_m, floors_climbed, altitude_gain_m, sedentary_minutes,
			active_minutes, light_active_minutes, moderate_active_minutes, vigorous_active_minutes,
			active_zone_minutes, kcal_burned_google, active_energy_burned_kcal,
			resting_heart_rate_bpm, heart_rate_min_bpm,
			heart_rate_max_bpm, heart_rate_avg_bpm, hrv_avg_ms, vo2_max, vo2_max_sample, vo2_max_run_sample,
			spo2_avg_pct, spo2_min_pct, respiratory_rate_avg_bpm,
			sleep_duration_minutes, sleep_temperature_c, sleep_temperature_baseline_c, sleep_temperature_deviation_30d_c,
			source_synced_at, raw_payload, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(day) DO UPDATE SET
			steps_total              = COALESCE(excluded.steps_total, watch_daily_summary.steps_total),
			distance_m               = COALESCE(excluded.distance_m, watch_daily_summary.distance_m),
			floors_climbed           = COALESCE(excluded.floors_climbed, watch_daily_summary.floors_climbed),
			altitude_gain_m          = COALESCE(excluded.altitude_gain_m, watch_daily_summary.altitude_gain_m),
			sedentary_minutes        = COALESCE(excluded.sedentary_minutes, watch_daily_summary.sedentary_minutes),
			active_minutes           = COALESCE(excluded.active_minutes, watch_daily_summary.active_minutes),
			light_active_minutes     = COALESCE(excluded.light_active_minutes, watch_daily_summary.light_active_minutes),
			moderate_active_minutes  = COALESCE(excluded.moderate_active_minutes, watch_daily_summary.moderate_active_minutes),
			vigorous_active_minutes  = COALESCE(excluded.vigorous_active_minutes, watch_daily_summary.vigorous_active_minutes),
			active_zone_minutes      = COALESCE(excluded.active_zone_minutes, watch_daily_summary.active_zone_minutes),
			kcal_burned_google       = COALESCE(excluded.kcal_burned_google, watch_daily_summary.kcal_burned_google),
			active_energy_burned_kcal = COALESCE(excluded.active_energy_burned_kcal, watch_daily_summary.active_energy_burned_kcal),
			resting_heart_rate_bpm   = COALESCE(excluded.resting_heart_rate_bpm, watch_daily_summary.resting_heart_rate_bpm),
			heart_rate_min_bpm       = COALESCE(excluded.heart_rate_min_bpm, watch_daily_summary.heart_rate_min_bpm),
			heart_rate_max_bpm       = COALESCE(excluded.heart_rate_max_bpm, watch_daily_summary.heart_rate_max_bpm),
			heart_rate_avg_bpm       = COALESCE(excluded.heart_rate_avg_bpm, watch_daily_summary.heart_rate_avg_bpm),
			hrv_avg_ms               = COALESCE(excluded.hrv_avg_ms, watch_daily_summary.hrv_avg_ms),
			vo2_max                  = COALESCE(excluded.vo2_max, watch_daily_summary.vo2_max),
			vo2_max_sample           = COALESCE(excluded.vo2_max_sample, watch_daily_summary.vo2_max_sample),
			vo2_max_run_sample       = COALESCE(excluded.vo2_max_run_sample, watch_daily_summary.vo2_max_run_sample),
			spo2_avg_pct             = COALESCE(excluded.spo2_avg_pct, watch_daily_summary.spo2_avg_pct),
			spo2_min_pct             = COALESCE(excluded.spo2_min_pct, watch_daily_summary.spo2_min_pct),
			respiratory_rate_avg_bpm = COALESCE(excluded.respiratory_rate_avg_bpm, watch_daily_summary.respiratory_rate_avg_bpm),
			sleep_duration_minutes   = COALESCE(excluded.sleep_duration_minutes, watch_daily_summary.sleep_duration_minutes),
			sleep_temperature_c               = COALESCE(excluded.sleep_temperature_c, watch_daily_summary.sleep_temperature_c),
			sleep_temperature_baseline_c       = COALESCE(excluded.sleep_temperature_baseline_c, watch_daily_summary.sleep_temperature_baseline_c),
			sleep_temperature_deviation_30d_c  = COALESCE(excluded.sleep_temperature_deviation_30d_c, watch_daily_summary.sleep_temperature_deviation_30d_c),
			source_synced_at         = excluded.source_synced_at,
			raw_payload              = COALESCE(excluded.raw_payload, watch_daily_summary.raw_payload),
			updated_at               = excluded.updated_at
	`,
		dayStr, sum.StepsTotal, sum.DistanceM, sum.FloorsClimbed, sum.AltitudeGainM, sum.SedentaryMinutes,
		sum.ActiveMinutes, sum.LightActiveMinutes, sum.ModerateActiveMinutes, sum.VigorousActiveMinutes,
		sum.ActiveZoneMinutes, sum.KcalBurnedGoogle, sum.ActiveEnergyBurnedKcal,
		sum.RestingHeartRateBpm, sum.HeartRateMinBpm,
		sum.HeartRateMaxBpm, sum.HeartRateAvgBpm, sum.HrvAvgMs, sum.Vo2Max, sum.Vo2MaxSample, sum.Vo2MaxRunSample,
		sum.Spo2AvgPct, sum.Spo2MinPct, sum.RespiratoryRateAvgBpm,
		sum.SleepDurationMinutes, sum.SleepTemperatureC, sum.SleepTemperatureBaselineC, sum.SleepTemperatureDeviation30dC,
		now, rawPayload, now,
	)
	if err != nil {
		return fmt.Errorf("upserting watch_daily_summary: %w", err)
	}
	return nil
}
