// Package googlehealth is a thin client for the Google Health API
// (https://health.googleapis.com/v4 — the successor to the Fitbit Web API
// that Pixel Watch 3 data flows through). It currently exists to support an
// exploratory "dump everything for today" pass (see Dump in dump.go); the
// day-completeness-aware sync engine described in ARCHITECTURE.md §3 is
// built on top of this once we know exactly what each data type's response
// looks like for this account.
package googlehealth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/time/rate"
)

// BaseURL is the Google Health API's REST base.
const BaseURL = "https://health.googleapis.com/v4"

// Client is a minimal wrapper around an already-authenticated *http.Client
// (see internal/googleauth.HTTPClient).
type Client struct {
	HTTP    *http.Client
	limiter *rate.Limiter
}

// requestRate paces every request a Client makes. Google's quota is 300
// requests per minute per user (see maxRateLimitRetries); a backfill fires
// ~45 per day, many concurrently, day after day, and outran it: retries
// alone just piled back in. 4/s = 240/min leaves headroom for another
// process (scheduler, CLI) syncing the same user. A var so tests can lift it.
var requestRate = rate.Limit(4)

func NewClient(hc *http.Client) *Client {
	return &Client{HTTP: hc, limiter: rate.NewLimiter(requestRate, 4)}
}

// APIError carries a non-2xx response body verbatim — for the exploratory
// dump, a documented error from Google (wrong filter syntax, missing scope,
// etc.) is just as informative as a successful response, so callers should
// generally record it rather than discard it.
type APIError struct {
	StatusCode int
	Body       json.RawMessage
}

func (e *APIError) Error() string {
	return fmt.Sprintf("googlehealth: HTTP %d: %s", e.StatusCode, e.Body)
}

// ListDataPoints calls GET .../dataTypes/{dataType}/dataPoints with an
// optional AIP-160 filter expression and page size.
func (c *Client) ListDataPoints(ctx context.Context, dataType, filter string, pageSize int) (json.RawMessage, error) {
	u := fmt.Sprintf("%s/users/me/dataTypes/%s/dataPoints", BaseURL, dataType)

	q := url.Values{}
	if filter != "" {
		q.Set("filter", filter)
	}
	if pageSize > 0 {
		q.Set("pageSize", fmt.Sprintf("%d", pageSize))
	}
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}

	return c.do(ctx, http.MethodGet, u, nil)
}

// RollUp calls POST .../dataTypes/{dataType}/dataPoints:rollUp: values
// summed into fixed windows (e.g. "3600s") over [start, end), deduplicated
// across sources the same way dailyRollUp is (CONFIRMED 2026-11-06: 24
// hourly windows summed to exactly the dailyRollUp value for all 8 types
// synced hourly). Windows with no data are omitted.
func (c *Client) RollUp(ctx context.Context, dataType string, start, end time.Time, window string) (json.RawMessage, error) {
	u := fmt.Sprintf("%s/users/me/dataTypes/%s/dataPoints:rollUp", BaseURL, dataType)
	body, err := json.Marshal(map[string]any{
		"range":      map[string]string{"startTime": start.UTC().Format(time.RFC3339), "endTime": end.UTC().Format(time.RFC3339)},
		"windowSize": window,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling rollUp request body: %w", err)
	}
	return c.do(ctx, http.MethodPost, u, bytes.NewReader(body))
}

// DailyRollUp calls POST .../dataTypes/{dataType}/dataPoints:dailyRollUp
// for the given CivilDateTime range.
func (c *Client) DailyRollUp(ctx context.Context, dataType string, start, end CivilDateTime) (json.RawMessage, error) {
	u := fmt.Sprintf("%s/users/me/dataTypes/%s/dataPoints:dailyRollUp", BaseURL, dataType)

	body, err := json.Marshal(map[string]any{
		"range": map[string]any{
			"start": start,
			"end":   end,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling dailyRollUp request body: %w", err)
	}

	return c.do(ctx, http.MethodPost, u, bytes.NewReader(body))
}

// maxRateLimitRetries/rateLimitBaseDelay bound how long do() waits out a
// 429 (RESOURCE_EXHAUSTED) or 503 (UNAVAILABLE) before giving up and returning the error to the
// caller. Google Health's quota here is "requests per minute per user"
// (300/min at the time of writing — see a real 429 body's
// metadata.quota_limit_value), so a handful of exponentially-backed-off
// retries comfortably clears within one such window, rather than aborting
// the whole sync — and the day it's in the middle of — on the very first
// burst. That burst is easy to hit now that a full-history catch-up bulk
// sync (see internal/web/settings.go's runGoogleCatchUpSync) can walk many
// days back-to-back, each one dozens of requests, on top of whatever the
// scheduler or a manual "Sync" click is doing at the same time.
// rateLimitBaseDelay is a var, not a const, purely so tests can shrink it.
const maxRateLimitRetries = 5

var rateLimitBaseDelay = 2 * time.Second

func (c *Client) do(ctx context.Context, method, u string, body io.Reader) (json.RawMessage, error) {
	// Buffered upfront so each retry attempt can replay the same body — an
	// io.Reader can only be consumed once, and DailyRollUp's POST body
	// needs to survive a retry same as any GET.
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("reading request body: %w", err)
		}
	}

	for attempt := 0; ; attempt++ {
		if c.limiter != nil {
			if err := c.limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}
		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
		if err != nil {
			return nil, fmt.Errorf("building request: %w", err)
		}
		if reqBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("calling %s %s: %w", method, u, err)
		}
		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reading response body from %s %s: %w", method, u, err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			// 503 UNAVAILABLE is transient too (seen mid-backfill twice on
			// 2026-11-06); failing on it aborted the whole sync pass.
			retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable
			if retryable && attempt < maxRateLimitRetries {
				base := rateLimitBaseDelay * time.Duration(1<<attempt)
				delay := base + time.Duration(rand.Int63n(int64(base)+1)) // jitter up to +100%, avoids a thundering herd on the shared per-minute quota
				select {
				case <-time.After(delay):
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, &APIError{StatusCode: resp.StatusCode, Body: respBody}
		}
		return respBody, nil
	}
}

// CivilDateTime mirrors the API's date/time message shape:
// {"date":{"year":Y,"month":M,"day":D},"time":{"hours":H,"minutes":M}}.
// Date reuses the same Date type as everywhere else in this package
// (see wire.go) rather than a redundant lookalike.
type CivilDateTime struct {
	Date Date      `json:"date"`
	Time CivilTime `json:"time"`
}

type CivilTime struct {
	Hours   int `json:"hours"`
	Minutes int `json:"minutes"`
}
