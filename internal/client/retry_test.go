package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// OSF returns Retry-After on a 429. datapin ignored it entirely: no retry, no
// backoff, no wait (issue #86).
func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		header string
		want   time.Duration
		ok     bool
	}{
		{"delta seconds", "120", 120 * time.Second, true},
		{"zero seconds", "0", 0, true},
		{"http date in the future", "Thu, 30 Jul 2026 12:01:30 GMT", 90 * time.Second, true},
		{"http date in the past clamps to zero", "Thu, 30 Jul 2026 11:59:00 GMT", 0, true},
		{"absent", "", 0, false},
		{"garbage", "soon please", 0, false},
		{"negative seconds clamps to zero", "-5", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRetryAfter(tt.header, now)
			if ok != tt.ok || got != tt.want {
				t.Errorf("parseRetryAfter(%q) = (%v, %v), want (%v, %v)", tt.header, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// The server's Retry-After wins when present and is reported exactly; deciding
// whether it is too long to wait for is shouldRetry's job, not this one's.
// Without a header the delay backs off exponentially, capped.
func TestRetryDelay(t *testing.T) {
	tests := []struct {
		name       string
		attempt    int
		retryAfter time.Duration
		hasHeader  bool
		want       time.Duration
	}{
		{"honors the server's Retry-After exactly", 0, 45 * time.Second, true, 45 * time.Second},
		{"reports a long Retry-After rather than truncating it", 0, 48 * time.Hour, true, 48 * time.Hour},
		{"exponential backoff without a header", 0, 0, false, baseRetryDelay},
		{"backoff doubles", 1, 0, false, 2 * baseRetryDelay},
		{"backoff doubles again", 2, 0, false, 4 * baseRetryDelay},
		{"backoff is capped", 20, 0, false, maxRetryDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryDelay(tt.attempt, tt.retryAfter, tt.hasHeader); got != tt.want {
				t.Errorf("retryDelay(%d, %v, %v) = %v, want %v", tt.attempt, tt.retryAfter, tt.hasHeader, got, tt.want)
			}
		})
	}
}

// A wait longer than we are willing to block for is declined outright: the
// quota is genuinely spent, and truncating the wait would only earn another 429.
func TestShouldRetry(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		attempt int
		delay   time.Duration
		want    bool
	}{
		{"throttled, short wait, attempts left", 429, 0, 5 * time.Second, true},
		{"throttled but out of attempts", 429, maxRetries, 5 * time.Second, false},
		{"throttled with an hour-long wait is declined", 429, 0, time.Hour, false},
		{"wait exactly at the cap is still retried", 429, 0, maxRetryDelay, true},
		{"non-retryable status", 404, 0, time.Second, false},
		{"gateway timeout is retried", 504, 0, time.Second, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRetry(tt.status, tt.attempt, tt.delay); got != tt.want {
				t.Errorf("shouldRetry(%d, %d, %v) = %v, want %v", tt.status, tt.attempt, tt.delay, got, tt.want)
			}
		})
	}
}

// An exhausted hourly quota surfaces immediately with the server's number,
// instead of blocking the run or retrying early.
func TestDoGet_DeclinesAnHourLongRetryAfter(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Retry-After", "2820") // 47 minutes
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"errors":[{"detail":"Request was throttled."}]}`))
	}))
	defer srv.Close()

	c := New("tok")
	c.baseURL = srv.URL
	c.sleep = func(ctx context.Context, d time.Duration) error {
		t.Errorf("must not wait %v for an exhausted quota", d)
		return nil
	}
	_, err := c.GetNode(context.Background(), "abc12")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 {
		t.Fatalf("want a 429 APIError, got %v", err)
	}
	if hits != 1 {
		t.Errorf("made %d requests, want 1 (no pointless retries)", hits)
	}
}

func TestIsRetryableStatus(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   bool
	}{
		{429, true}, {500, false}, {502, true}, {503, true}, {504, true},
		{200, false}, {404, false}, {401, false}, {403, false}, {409, false},
	} {
		if got := isRetryableStatus(tc.status); got != tc.want {
			t.Errorf("isRetryableStatus(%d) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

// A 429 carrying Retry-After is waited out and the request retried.
func TestDoGet_RetriesOn429(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&hits, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errors":[{"detail":"Request was throttled."}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"abc12","attributes":{"title":"T"}}}`))
	}))
	defer srv.Close()

	c := New("tok")
	c.baseURL = srv.URL
	var slept []time.Duration
	c.sleep = func(ctx context.Context, d time.Duration) error { slept = append(slept, d); return nil }

	node, err := c.GetNode(context.Background(), "abc12")
	if err != nil {
		t.Fatalf("GetNode after a 429 should succeed on retry: %v", err)
	}
	if node.Attributes.Title != "T" {
		t.Errorf("title = %q", node.Attributes.Title)
	}
	if hits != 2 {
		t.Errorf("made %d requests, want 2 (one throttled, one retried)", hits)
	}
	if len(slept) != 1 || slept[0] != time.Second {
		t.Errorf("waits = %v, want one 1s wait from Retry-After", slept)
	}
}

// Retries are bounded: a server that throttles forever must surface a 429
// rather than spinning.
func TestDoGet_GivesUpAfterMaxRetries(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"errors":[{"detail":"Request was throttled."}]}`))
	}))
	defer srv.Close()

	c := New("tok")
	c.baseURL = srv.URL
	c.sleep = func(ctx context.Context, d time.Duration) error { return nil }

	_, err := c.GetNode(context.Background(), "abc12")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 429 {
		t.Fatalf("want a 429 APIError after exhausting retries, got %v", err)
	}
	if want := int64(maxRetries + 1); hits != want {
		t.Errorf("made %d requests, want %d (initial + %d retries)", hits, want, maxRetries)
	}
}

// Ctrl-C during a rate-limit wait must abort the run, not sit out the delay.
func TestDoGet_RetryWaitRespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := New("tok")
	c.baseURL = srv.URL
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the wait must return immediately

	done := make(chan error, 1)
	go func() { _, err := c.GetNode(ctx, "abc12"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled context must produce an error")
		}
		if !errors.Is(err, context.Canceled) && !strings.Contains(err.Error(), "context canceled") {
			t.Errorf("error should reflect cancellation, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the retry wait ignored context cancellation")
	}
}

// A non-retryable failure is returned immediately, without burning retries.
func TestDoGet_DoesNotRetryClientErrors(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"detail":"Not found."}]}`))
	}))
	defer srv.Close()

	c := New("tok")
	c.baseURL = srv.URL
	c.sleep = func(ctx context.Context, d time.Duration) error {
		t.Error("a 404 must not be retried")
		return nil
	}
	if _, err := c.GetNode(context.Background(), "abc12"); err == nil {
		t.Fatal("expected a 404 error")
	}
	if hits != 1 {
		t.Errorf("made %d requests, want 1", hits)
	}
}
