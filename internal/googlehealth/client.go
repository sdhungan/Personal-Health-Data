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
	"net/http"
	"net/url"
)

// BaseURL is the Google Health API's REST base.
const BaseURL = "https://health.googleapis.com/v4"

// Client is a minimal wrapper around an already-authenticated *http.Client
// (see internal/googleauth.HTTPClient).
type Client struct {
	HTTP *http.Client
}

func NewClient(hc *http.Client) *Client { return &Client{HTTP: hc} }

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

func (c *Client) do(ctx context.Context, method, u string, body io.Reader) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s %s: %w", method, u, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body from %s %s: %w", method, u, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{StatusCode: resp.StatusCode, Body: respBody}
	}
	return respBody, nil
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
