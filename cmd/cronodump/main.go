// Command cronodump is a throwaway diagnostic tool (ad hoc dev use only,
// same status as cmd/tmpinspect — not part of the healthd product). It logs
// in to Cronometer's unofficial mobile REST API and dumps raw JSON
// responses for a given day to a directory, so internal/cronometer/values.go
// can eventually be written against real field shapes instead of guessed
// ones (see cronometer-integration.md's "Ordered task list", step 2-3).
//
// Credentials are entered interactively at this program's own stdin/stdout
// — never passed as a flag, env var, or logged — and only the resulting
// session (user ID + session token + timezone) is persisted, to
// Creds/cronometer_session.json (gitignored). The password itself is never
// written anywhere. Re-running this tool reuses that cached session until
// Cronometer rejects it, at which point it prompts to log in again.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"
)

const baseURL = "https://mobile.cronometer.com"

type session struct {
	Email    string `json:"email"`
	UserID   int    `json:"user_id"`
	Token    string `json:"token"`
	Timezone string `json:"timezone"`
}

func main() {
	date := flag.String("date", time.Now().Format("2006-01-02"), "day to dump, YYYY-MM-DD")
	outDir := flag.String("out", filepath.Join("bin", "cronometer-dump"), "output directory for raw JSON responses")
	sessionFile := flag.String("session-file", filepath.Join("Creds", "cronometer_session.json"), "where the (non-password) session cache lives")
	flag.Parse()

	if err := run(*date, *outDir, *sessionFile); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(date, outDir, sessionFile string) error {
	client := &http.Client{Timeout: 30 * time.Second}

	sess, err := loadSession(sessionFile)
	if err != nil {
		return err
	}
	if sess == nil {
		sess, err = login(client)
		if err != nil {
			return fmt.Errorf("login: %w", err)
		}
		if err := saveSession(sessionFile, sess); err != nil {
			return fmt.Errorf("saving session: %w", err)
		}
		fmt.Println("logged in, session cached at", sessionFile)
	} else {
		fmt.Println("reusing cached session from", sessionFile, "(user_id", sess.UserID, ")")
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output directory %s: %w", outDir, err)
	}

	// call wraps post + the one-retry-after-relogin behavior every real
	// endpoint below needs.
	call := func(endpoint string, payload map[string]any) (json.RawMessage, error) {
		data, err := authedPost(client, sess, endpoint, payload)
		if err != nil {
			return nil, err
		}
		if isSessionFailure(data) {
			fmt.Println("cached session rejected, logging in again")
			sess, err = login(client)
			if err != nil {
				return nil, fmt.Errorf("re-login: %w", err)
			}
			if err := saveSession(sessionFile, sess); err != nil {
				return nil, fmt.Errorf("saving refreshed session: %w", err)
			}
			return authedPost(client, sess, endpoint, payload)
		}
		return data, nil
	}

	diary, err := call("/api/v2/get_diary", map[string]any{
		"day":    date,
		"config": map[string]any{"call_version": 1},
	})
	writeJSON(outDir, "get_diary.json", diary, err)

	foodIDs, servingIDs := extractDiaryIDs(diary)

	if len(foodIDs) > 0 {
		foods, err := call("/api/v2/get_foods", map[string]any{
			"ids":    foodIDs,
			"config": map[string]any{"call_version": 1},
		})
		writeJSON(outDir, "get_foods.json", foods, err)
	} else {
		fmt.Println("skipping get_foods: no Serving entries in diary for", date)
	}

	scores, err := call("/api/v2/get_nutrition_scores", map[string]any{
		"startDay":    "1900-1-1",
		"endDay":      "1900-1-1",
		"servingIds":  servingIDs,
		"supplements": "true",
		"config":      map[string]any{"call_version": 1},
	})
	writeJSON(outDir, "get_nutrition_scores.json", scores, err)

	nutrients, err := call("/api/v2/get_nutrients", map[string]any{
		"day":    date,
		"config": map[string]any{"call_version": 1},
	})
	writeJSON(outDir, "get_nutrients.json", nutrients, err)

	metrics, err := call("/api/v2/get_metrics", map[string]any{
		"config": map[string]any{"call_version": 1},
	})
	writeJSON(outDir, "get_metrics.json", metrics, err)

	fmt.Println("done — inspect", outDir)
	return nil
}

// extractDiaryIDs pulls foodId (from Serving entries, deduped) and
// servingId (from every entry that has one) out of a raw get_diary
// response, tolerating whatever shape it turns out to have.
func extractDiaryIDs(raw json.RawMessage) (foodIDs []int, servingIDs []any) {
	var parsed struct {
		Diary []map[string]any `json:"diary"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, nil
	}
	seen := map[int]bool{}
	for _, e := range parsed.Diary {
		if e["type"] == "Serving" {
			if id, ok := e["foodId"].(float64); ok && !seen[int(id)] {
				seen[int(id)] = true
				foodIDs = append(foodIDs, int(id))
			}
		}
		if sid, ok := e["servingId"]; ok {
			servingIDs = append(servingIDs, sid)
		}
	}
	return foodIDs, servingIDs
}

func isSessionFailure(raw json.RawMessage) bool {
	var probe struct {
		Result string `json:"result"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.Result == "FAIL" || probe.Result == "FAILURE"
}

func authedPost(client *http.Client, sess *session, endpoint string, payload map[string]any) (json.RawMessage, error) {
	payload["auth"] = map[string]any{
		"userId":  sess.UserID,
		"token":   sess.Token,
		"api":     3,
		"os":      "Android",
		"build":   "2807",
		"flavour": "free",
	}
	payload["lastSeen"] = 0
	return post(client, endpoint, payload)
}

func login(client *http.Client) (*session, error) {
	fmt.Print("Cronometer email: ")
	var email string
	if _, err := fmt.Scanln(&email); err != nil {
		return nil, fmt.Errorf("reading email: %w", err)
	}

	fmt.Print("Cronometer password (hidden): ")
	pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return nil, fmt.Errorf("reading password: %w", err)
	}
	password := string(pwBytes)

	payload := map[string]any{
		"email":         email,
		"password":      password,
		"timezone":      "UTC",
		"userCode":      nil,
		"build":         "4.48.2 b2807-a",
		"device":        "Android 14 (SDK 34), Google Pixel 6 Pro",
		"firebaseToken": "",
		"auth": map[string]any{
			"userId":  nil,
			"token":   nil,
			"api":     3,
			"os":      "Android",
			"build":   "2807",
			"flavour": "free",
		},
		"lastSeen": 0,
		"config":   map[string]any{"call_version": 2},
	}
	// password is only ever referenced above, to build this one request
	// body — it is not logged, stored, or returned from this function.

	data, err := post(client, "/api/v2/login", payload)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Result     string `json:"result"`
		ID         int    `json:"id"`
		SessionKey string `json:"sessionKey"`
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
	return &session{Email: email, UserID: resp.ID, Token: resp.SessionKey, Timezone: tz}, nil
}

func post(client *http.Client, path string, payload map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling request to %s: %w", path, err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request to %s: %w", path, err)
	}
	req.Header.Set("user-agent", "Dart/3.9 (dart:io)")
	req.Header.Set("content-type", "text/plain; charset=utf-8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

func loadSession(path string) (*session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var sess session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &sess, nil
}

func saveSession(path string, sess *session) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling session: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

func writeJSON(outDir, filename string, data json.RawMessage, callErr error) {
	path := filepath.Join(outDir, filename)

	var payload []byte
	if callErr != nil {
		payload, _ = json.MarshalIndent(map[string]string{"error": callErr.Error()}, "", "  ")
	} else {
		var pretty bytes.Buffer
		if json.Indent(&pretty, data, "", "  ") == nil {
			payload = pretty.Bytes()
		} else {
			payload = data
		}
	}

	if err := os.WriteFile(path, payload, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "warning: writing", path, "failed:", err)
		return
	}
	fmt.Println("wrote", path)
}
