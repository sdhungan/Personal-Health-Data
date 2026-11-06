package googlehealth

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// sequenceTransport returns each response in order on successive
// RoundTrips (looping the last one if there are more calls than
// responses), and counts how many requests it actually saw.
type sequenceTransport struct {
	statuses []int
	calls    int
}

func (t *sequenceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	i := t.calls
	if i >= len(t.statuses) {
		i = len(t.statuses) - 1
	}
	t.calls++
	status := t.statuses[i]
	body := `{}`
	if status == http.StatusTooManyRequests {
		body = `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func withFastRetries(t *testing.T) {
	t.Helper()
	orig := rateLimitBaseDelay
	rateLimitBaseDelay = time.Millisecond
	t.Cleanup(func() { rateLimitBaseDelay = orig })
}

func TestClientRetriesOn429ThenSucceeds(t *testing.T) {
	withFastRetries(t)
	transport := &sequenceTransport{statuses: []int{http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusOK}}
	client := NewClient(&http.Client{Transport: transport})

	_, err := client.ListDataPoints(context.Background(), "steps", "", 10)
	if err != nil {
		t.Fatalf("ListDataPoints: expected eventual success, got: %v", err)
	}
	if transport.calls != 3 {
		t.Errorf("calls = %d, want 3 (a 429 and a 503, then a success)", transport.calls)
	}
}

func TestClientGivesUpAfterMaxRateLimitRetries(t *testing.T) {
	withFastRetries(t)
	transport := &sequenceTransport{statuses: []int{http.StatusTooManyRequests}}
	client := NewClient(&http.Client{Transport: transport})

	_, err := client.ListDataPoints(context.Background(), "steps", "", 10)
	if err == nil {
		t.Fatal("ListDataPoints: expected an error after exhausting retries, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("APIError.StatusCode = %d, want %d", apiErr.StatusCode, http.StatusTooManyRequests)
	}
	if want := maxRateLimitRetries + 1; transport.calls != want {
		t.Errorf("calls = %d, want %d (the initial attempt plus every retry)", transport.calls, want)
	}
}

func TestClientDoesNotRetryOnOtherErrors(t *testing.T) {
	withFastRetries(t)
	transport := &sequenceTransport{statuses: []int{http.StatusInternalServerError}}
	client := NewClient(&http.Client{Transport: transport})

	_, err := client.ListDataPoints(context.Background(), "steps", "", 10)
	if err == nil {
		t.Fatal("ListDataPoints: expected an error, got nil")
	}
	if transport.calls != 1 {
		t.Errorf("calls = %d, want 1 (only 429/503 trigger the retry loop)", transport.calls)
	}
}

// TestMain lifts the request pacing: the sync tests make dozens of calls
// against a fake transport and shouldn't wait out a real quota.
func TestMain(m *testing.M) {
	requestRate = rate.Inf
	os.Exit(m.Run())
}
