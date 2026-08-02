package cronometer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sdhungan/Personal-Health-Data/internal/crypto"
)

const dateLayout = "2006-01-02"

// DBSyncer fetches one day of Cronometer data and upserts it into
// cronometer_* tables (see internal/db/schema.sql). It implements the same
// SyncDay(ctx, time.Time) (bool, error) shape internal/syncengine.DaySyncer
// expects, without importing that package — same convention
// internal/googlehealth.DBSyncer uses.
//
// Exercise/Biometric diary entries whose Source is "Fitbit" are skipped —
// confirmed 2026-07-31 that this account has Fitbit auto-synced into
// Cronometer, so those entries duplicate (at lower fidelity — a single
// daily number vs. watch_heart_rate_intraday's thousands of samples, a
// coarse sleep-hours summary vs. watch_sleep_stage's full timeline) what
// internal/googlehealth already captures directly. Only entries actually
// logged in Cronometer itself are synced here (user decision, 2026-07-31).
// cronometer_serving/cronometer_daily_nutrition have no such overlap —
// nutrition/food data is Cronometer's own domain, never pulled from Google
// Health at all.
type DBSyncer struct {
	Client          *Client
	DB              *sql.DB
	UserID          int64
	Key             crypto.Key
	CredentialsPath string
	SessionPath     string

	credentials *Credentials
	session     *Session
	metrics     []Metric // cached biometric metric catalog, fetched lazily
}

// NewDBSyncer loads the encrypted Cronometer credentials from
// credentialsPath (see "healthd auth cronometer") and any cached session
// from sessionPath, returning a DBSyncer ready for SyncDay. A missing or
// unreadable cached session is not an error — SyncDay logs in fresh on
// first use. userID scopes every row this syncer writes — one DBSyncer is
// constructed per user per sync pass, never shared across users.
func NewDBSyncer(db *sql.DB, userID int64, key crypto.Key, credentialsPath, sessionPath string) (*DBSyncer, error) {
	creds, err := LoadCredentials(credentialsPath, key)
	if err != nil {
		return nil, fmt.Errorf("loading Cronometer credentials (run \"healthd auth cronometer\" first?): %w", err)
	}

	s := &DBSyncer{
		Client:          NewClient(),
		DB:              db,
		UserID:          userID,
		Key:             key,
		CredentialsPath: credentialsPath,
		SessionPath:     sessionPath,
		credentials:     creds,
	}
	if sess, err := LoadSession(sessionPath, key); err == nil {
		s.session = sess
	}
	return s, nil
}

// SyncDay fetches and upserts every Cronometer data type this package maps
// for the calendar day day represents, and reports whether any data was
// found at all.
func (s *DBSyncer) SyncDay(ctx context.Context, day time.Time) (bool, error) {
	dayStr := day.Format(dateLayout)

	diary, err := withRetry(ctx, s, func(sess *Session) (*DiaryResponse, error) {
		return s.Client.GetDiary(ctx, sess, dayStr)
	})
	if err != nil {
		return false, fmt.Errorf("fetching diary for %s: %w", dayStr, err)
	}

	var servings, exercises, biometrics []DiaryEntry
	for _, e := range diary.Diary {
		switch e.Type {
		case "Serving":
			servings = append(servings, e)
		case "Exercise":
			if e.Source != "Fitbit" {
				exercises = append(exercises, e)
			}
		case "Biometric":
			if e.Source != "Fitbit" {
				biometrics = append(biometrics, e)
			}
		}
	}

	if len(servings) > 0 {
		foods, err := withRetry(ctx, s, func(sess *Session) ([]Food, error) {
			return s.Client.GetFoods(ctx, sess, uniqueFoodIDs(servings))
		})
		if err != nil {
			return false, fmt.Errorf("fetching foods for %s: %w", dayStr, err)
		}
		if err := upsertServings(ctx, s.DB, s.UserID, dayStr, servings, indexFoods(foods)); err != nil {
			return false, fmt.Errorf("upserting servings for %s: %w", dayStr, err)
		}
	} else if err := deleteServingsForDay(ctx, s.DB, s.UserID, dayStr); err != nil {
		return false, fmt.Errorf("clearing servings for %s: %w", dayStr, err)
	}

	if len(exercises) > 0 {
		if err := upsertExercises(ctx, s.DB, s.UserID, dayStr, exercises); err != nil {
			return false, fmt.Errorf("upserting exercises for %s: %w", dayStr, err)
		}
	} else if err := deleteExercisesForDay(ctx, s.DB, s.UserID, dayStr); err != nil {
		return false, fmt.Errorf("clearing exercises for %s: %w", dayStr, err)
	}

	if len(biometrics) > 0 {
		metrics, err := s.metricCatalog(ctx)
		if err != nil {
			return false, fmt.Errorf("fetching metric catalog: %w", err)
		}
		if err := upsertBiometrics(ctx, s.DB, s.UserID, dayStr, biometrics, metrics); err != nil {
			return false, fmt.Errorf("upserting biometrics for %s: %w", dayStr, err)
		}
	} else if err := deleteBiometricsForDay(ctx, s.DB, s.UserID, dayStr); err != nil {
		return false, fmt.Errorf("clearing biometrics for %s: %w", dayStr, err)
	}

	scores, err := withRetry(ctx, s, func(sess *Session) (*NutritionScoresResponse, error) {
		return s.Client.GetNutritionScores(ctx, sess, dayStr, servingIDsOf(servings))
	})
	if err != nil {
		return false, fmt.Errorf("fetching nutrition scores for %s: %w", dayStr, err)
	}
	components := scores.AllTargetsComponents()
	kcalBurned := diary.Summary.Burned.Total
	if err := upsertDailyNutrition(ctx, s.DB, s.UserID, dayStr, diary.Info.Complete, kcalBurned, nutritionAmountsFromScores(components)); err != nil {
		return false, fmt.Errorf("upserting daily nutrition for %s: %w", dayStr, err)
	}

	hasData := len(diary.Diary) > 0 || kcalBurned != 0 || len(components) > 0
	return hasData, nil
}

// metricCatalog fetches and caches the account's biometric metric catalog
// (stable per account, so one fetch per DBSyncer lifetime is enough — same
// reasoning as the reference client's get_nutrient_definitions caching).
func (s *DBSyncer) metricCatalog(ctx context.Context) ([]Metric, error) {
	if s.metrics != nil {
		return s.metrics, nil
	}
	metrics, err := withRetry(ctx, s, func(sess *Session) ([]Metric, error) {
		return s.Client.GetMetrics(ctx, sess)
	})
	if err != nil {
		return nil, err
	}
	s.metrics = metrics
	return metrics, nil
}

// withRetry calls fn with s's current session, logging in fresh (and
// persisting the new session) and retrying once if fn reports the session
// as expired — same one-retry-after-relogin behavior the reference client
// uses, since Cronometer sessions expire without warning.
func withRetry[T any](ctx context.Context, s *DBSyncer, fn func(sess *Session) (T, error)) (T, error) {
	var zero T
	if s.session == nil {
		if err := s.login(ctx); err != nil {
			return zero, err
		}
	}

	out, err := fn(s.session)
	if errors.Is(err, ErrSessionExpired) {
		if err := s.login(ctx); err != nil {
			return zero, err
		}
		return fn(s.session)
	}
	return out, err
}

func (s *DBSyncer) login(ctx context.Context) error {
	sess, err := s.Client.Login(ctx, s.credentials.Username, s.credentials.Password)
	if err != nil {
		return fmt.Errorf("logging in to Cronometer: %w", err)
	}
	s.session = sess
	if err := SaveSession(s.SessionPath, s.Key, sess); err != nil {
		return fmt.Errorf("caching Cronometer session: %w", err)
	}
	return nil
}

func uniqueFoodIDs(entries []DiaryEntry) []int64 {
	seen := make(map[int64]bool, len(entries))
	var ids []int64
	for _, e := range entries {
		if !seen[e.FoodID] {
			seen[e.FoodID] = true
			ids = append(ids, e.FoodID)
		}
	}
	return ids
}

func indexFoods(foods []Food) map[int64]Food {
	m := make(map[int64]Food, len(foods))
	for _, f := range foods {
		m[f.ID] = f
	}
	return m
}

func servingIDsOf(entries []DiaryEntry) []int64 {
	ids := make([]int64, len(entries))
	for i, e := range entries {
		ids[i] = e.ServingID
	}
	return ids
}
