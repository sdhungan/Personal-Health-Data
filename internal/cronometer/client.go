// Package cronometer is a client for Cronometer's unofficial mobile REST
// API (mobile.cronometer.com/api/v2/*) — reverse-engineered by
// github.com/rwestergren/cronometer-api-mcp via static analysis of the
// Android app, confirmed against this account's real responses on
// 2026-07-31 (see cmd/cronodump and bin/cronometer-dump). Cronometer has no
// OAuth: Login exchanges a username/password for a short-lived Session that
// every other call embeds in its request body (see session.go for how that
// Session — and the credentials themselves — are cached encrypted at rest).
package cronometer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BaseURL is the Cronometer mobile API's REST base.
const BaseURL = "https://mobile.cronometer.com"

// ErrSessionExpired is returned by call (and every method built on it) when
// Cronometer reports the session as invalid — an HTTP 200 body of
// {"result":"FAIL"} (confirmed real shape) or {"result":"FAILURE"}
// (defensive; never observed, kept in case some endpoint uses it), or an
// HTTP 401/403. Callers (DBSyncer) re-login and retry once on this error.
var ErrSessionExpired = errors.New("cronometer: session expired")

// Client is a minimal wrapper around net/http — Cronometer's mobile API has
// no bearer-token header scheme (auth rides in the JSON body of every
// request), so unlike internal/googlehealth.Client it takes a *Session
// explicitly per call rather than baking auth into the *http.Client.
type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// APIError carries a non-2xx response body verbatim.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("cronometer: HTTP %d: %s", e.StatusCode, e.Body)
}

// appAuth mirrors the Android app's static auth template exactly (api/os/
// build/flavour never change between requests).
var appAuth = map[string]any{
	"api":     3,
	"os":      "Android",
	"build":   "2807",
	"flavour": "free",
}

// Login authenticates and returns the Session every other call needs.
func (c *Client) Login(ctx context.Context, username, password string) (*Session, error) {
	auth := map[string]any{"userId": nil, "token": nil}
	for k, v := range appAuth {
		auth[k] = v
	}
	payload := map[string]any{
		"email":         username,
		"password":      password,
		"timezone":      "UTC",
		"userCode":      nil,
		"build":         "4.48.2 b2807-a",
		"device":        "Android 14 (SDK 34), Google Pixel 6 Pro",
		"firebaseToken": "",
		"auth":          auth,
		"lastSeen":      0,
		"config":        map[string]any{"call_version": 2},
	}

	data, err := c.post(ctx, "/api/v2/login", payload)
	if err != nil {
		return nil, err
	}

	var resp struct {
		SessionKey string `json:"sessionKey"`
		ID         int    `json:"id"`
		Timezone   string `json:"timezone"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decoding login response: %w", err)
	}
	if resp.SessionKey == "" {
		msg := resp.Error
		if msg == "" {
			msg = string(data)
		}
		return nil, fmt.Errorf("login failed: %s", msg)
	}

	tz := resp.Timezone
	if tz == "" {
		tz = "UTC"
	}
	return &Session{UserID: resp.ID, Token: resp.SessionKey, Timezone: tz}, nil
}

// call sends an authenticated v2 POST request and returns the raw JSON
// response, or ErrSessionExpired if Cronometer reports the session invalid.
func (c *Client) call(ctx context.Context, sess *Session, endpoint string, payload map[string]any) (json.RawMessage, error) {
	auth := map[string]any{"userId": sess.UserID, "token": sess.Token}
	for k, v := range appAuth {
		auth[k] = v
	}
	payload["auth"] = auth
	if _, ok := payload["lastSeen"]; !ok {
		payload["lastSeen"] = 0
	}

	data, err := c.post(ctx, endpoint, payload)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
			return nil, ErrSessionExpired
		}
		return nil, err
	}

	var probe struct {
		Result string `json:"result"`
	}
	_ = json.Unmarshal(data, &probe)
	if probe.Result == "FAIL" || probe.Result == "FAILURE" {
		return nil, ErrSessionExpired
	}
	return data, nil
}

func (c *Client) post(ctx context.Context, path string, payload map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling request to %s: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request to %s: %w", path, err)
	}
	req.Header.Set("user-agent", "Dart/3.9 (dart:io)")
	req.Header.Set("content-type", "text/plain; charset=utf-8")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}
	return respBody, nil
}

// ------------------------------------------------------------------
// Typed endpoint methods
// ------------------------------------------------------------------

// GetDiary fetches every diary entry (Serving/Exercise/Biometric) logged
// for day (YYYY-MM-DD), plus Cronometer's own computed daily summary.
func (c *Client) GetDiary(ctx context.Context, sess *Session, day string) (*DiaryResponse, error) {
	data, err := c.call(ctx, sess, "/api/v2/get_diary", map[string]any{
		"day":    day,
		"config": map[string]any{"call_version": 1},
	})
	if err != nil {
		return nil, err
	}
	var out DiaryResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding get_diary response: %w", err)
	}
	return &out, nil
}

// GetFoods batch-resolves food name + per-100g nutrient profile for a set
// of food IDs (from Serving diary entries). Returns an empty slice for an
// empty ids.
func (c *Client) GetFoods(ctx context.Context, sess *Session, ids []int64) ([]Food, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	data, err := c.call(ctx, sess, "/api/v2/get_foods", map[string]any{
		"ids":    ids,
		"config": map[string]any{"call_version": 1},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Foods []Food `json:"foods"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding get_foods response: %w", err)
	}
	return out.Foods, nil
}

// GetNutritionScores fetches consumed nutrient totals for day, scored
// against the account's tracked targets. servingIDs must be every Serving
// entry's ServingID from that day's diary (the endpoint scores exactly
// those servings, not "whatever's logged for day").
func (c *Client) GetNutritionScores(ctx context.Context, sess *Session, day string, servingIDs []int64) (*NutritionScoresResponse, error) {
	data, err := c.call(ctx, sess, "/api/v2/get_nutrition_scores", map[string]any{
		"startDay":    "1900-1-1",
		"endDay":      "1900-1-1",
		"servingIds":  servingIDs,
		"supplements": "true",
		"config":      map[string]any{"call_version": 1},
	})
	if err != nil {
		return nil, err
	}
	var out NutritionScoresResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding get_nutrition_scores response: %w", err)
	}
	return &out, nil
}

// GetMetrics fetches the account's biometric metric catalog (Weight, Body
// Fat, Heart Rate, ...) — used to label a Biometric diary entry's numeric
// metricId/unitId with human-readable names.
func (c *Client) GetMetrics(ctx context.Context, sess *Session) ([]Metric, error) {
	data, err := c.call(ctx, sess, "/api/v2/get_metrics", map[string]any{
		"config": map[string]any{"call_version": 1},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Metrics []Metric `json:"metrics"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding get_metrics response: %w", err)
	}
	return out.Metrics, nil
}

// ------------------------------------------------------------------
// Write endpoints (DOCUMENTED, not CONFIRMED — see values.go's "Write
// endpoints" section for what that distinction means here).
// ------------------------------------------------------------------

// FindFood searches Cronometer's own food database by name — the grounding
// step for an LLM-identified ingredient (see internal/web's photo-log flow):
// resolve a candidate match here, then GetFoods for its full per-100g
// nutrient profile.
func (c *Client) FindFood(ctx context.Context, sess *Session, query string) ([]FoodSearchResult, error) {
	data, err := c.call(ctx, sess, "/api/v2/find_food", map[string]any{
		"query":   query,
		"tab":     "ALL",
		"sources": []string{"All"},
		"config":  map[string]any{"newSearch": true, "newSpellcheck": true, "call_version": 1},
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Foods []FoodSearchResult `json:"foods"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding find_food response: %w", err)
	}
	return out.Foods, nil
}

// CreateCustomFood defines a new food in the account's own food database
// with an explicit nutrient profile, and returns its new food ID. nutrients
// follows the same per-100g convention internal/cronometer.Food.Nutrients
// already uses for reads (see nutritionAmountsFromFood in nutrients.go) —
// reusing FoodNutrient rather than a new type. measureName/measureGrams
// define the food's one default measure (e.g. ("g", 1.0) for a plain
// gram-based measure) — AddServing's own "grams" field controls the actual
// logged quantity regardless of which measure is selected, so this measure
// only needs to be valid, not meaningful.
func (c *Client) CreateCustomFood(ctx context.Context, sess *Session, name string, measureName string, measureGrams float64, nutrients []FoodNutrient) (int64, error) {
	nutrientPayload := make([]map[string]any, len(nutrients))
	for i, n := range nutrients {
		nutrientPayload[i] = map[string]any{"id": n.ID, "amount": n.Amount}
	}

	data, err := c.call(ctx, sess, "/api/v2/add_food", map[string]any{
		"data": map[string]any{
			"id":               0,
			"name":             name,
			"category":         0,
			"owner":            nil,
			"retired":          nil,
			"source":           nil,
			"defaultMeasureId": 0,
			"comments":         nil,
			"alternateId":      nil,
			"measures": []map[string]any{
				{"id": 0, "name": measureName, "value": measureGrams, "amount": 1.0, "type": "Atomic"},
			},
			"labelType":  "AMERICAN_2016",
			"nutrients":  nutrientPayload,
			"properties": map[string]any{},
			"foodTags":   []string{},
		},
		"config": map[string]any{"call_version": 1},
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return 0, fmt.Errorf("decoding add_food response: %w", err)
	}
	return out.ID, nil
}

// ServingEntry is the input to AddServing — one Serving-type diary entry to
// log. Day is "YYYY-MM-DD" (zero-padded, the same dateLayout GetDiary
// already uses — CONFIRMED 2026-08-05 via cmd/cronoverify; the original
// DOCUMENTED claim of non-zero-padded "YYYY-M-D" was never actually
// exercised and turned out unnecessary, add_serving accepts the standard
// zero-padded form fine); Time is "HH:MM:SS".
type ServingEntry struct {
	Day       string
	Time      string
	FoodID    int64
	MeasureID int64
	Grams     float64
}

// AddServing logs a Serving-type diary entry — the actual "push to
// Cronometer" step for the photo-log flow. Returns the new entry's serving
// ID (needed if the caller wants to remove it later via DeleteEntries) —
// CONFIRMED int64 against a real account on 2026-08-05 (via cmd/cronoverify;
// the original DOCUMENTED guess of a string-typed id was wrong: the real
// add_serving response's "id" is a JSON number, matching
// DiaryEntry.ServingID's type on the read side exactly — there is no
// read/write type asymmetry here after all).
func (c *Client) AddServing(ctx context.Context, sess *Session, e ServingEntry) (int64, error) {
	data, err := c.call(ctx, sess, "/api/v2/add_serving", map[string]any{
		"serving": map[string]any{
			"order":         0,
			"day":           e.Day,
			"time":          e.Time,
			"offset":        nil,
			"source":        nil,
			"userId":        sess.UserID,
			"servingId":     nil,
			"type":          "Serving",
			"foodId":        e.FoodID,
			"measureId":     e.MeasureID,
			"grams":         e.Grams,
			"translationId": 0,
		},
		"config": map[string]any{"call_version": 2},
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return 0, fmt.Errorf("decoding add_serving response: %w", err)
	}
	return out.ID, nil
}

// DiaryEntryRef identifies one diary entry to remove via DeleteEntries.
// ServingID is int64 — CONFIRMED 2026-08-05, see AddServing's doc comment.
type DiaryEntryRef struct {
	ServingID int64
	FoodID    int64
	MeasureID int64
	Grams     float64
}

// DeleteEntries removes one or more diary entries via the v3 API — the only
// endpoint observed using header-based auth (x-crono-session) rather than
// this package's usual JSON-body auth (see call), so it builds its own
// request instead of going through call/post. Used to clean up a throwaway
// entry during write-endpoint verification, and potentially a future "undo"
// affordance on the dashboard.
func (c *Client) DeleteEntries(ctx context.Context, sess *Session, entries []DiaryEntryRef) error {
	type diaryEntry struct {
		ServingID int64   `json:"servingId"`
		FoodID    int64   `json:"foodId"`
		MeasureID int64   `json:"measureId"`
		Grams     float64 `json:"grams"`
	}
	payload := struct {
		DiaryEntries []diaryEntry `json:"diaryEntries"`
	}{DiaryEntries: make([]diaryEntry, len(entries))}
	for i, e := range entries {
		payload.DiaryEntries[i] = diaryEntry{ServingID: e.ServingID, FoodID: e.FoodID, MeasureID: e.MeasureID, Grams: e.Grams}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling delete_entries request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v3/user/%d/diary-entries", BaseURL, sess.UserID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building delete_entries request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-crono-session", sess.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("calling delete_entries: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}
	return nil
}
