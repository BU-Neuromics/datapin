// Package httpx provides a retrying HTTP client shared by archive backend
// drivers. It generalizes internal/client/retry.go (D14): the server's
// Retry-After wins when present, X-RateLimit-Reset is the fallback, waits
// past maxRetryDelay are declined rather than truncated, and sleeps are
// context-aware and injectable. Retry headers are consulted only on
// retryable statuses — Zenodo sends retry-after on 200s too (D15).
package httpx

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/BU-Neuromics/datapin/internal/log"
)

const (
	maxRetries     = 3
	baseRetryDelay = 2 * time.Second
	// maxRetryDelay bounds a single wait. A server answering "come back in
	// an hour" has a genuinely spent quota; declining surfaces the number
	// instead of hanging, and truncating would just earn another 429.
	maxRetryDelay = 30 * time.Second
)

// Doer is the transport surface RetryClient wraps.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// RetryClient retries throttled/transient responses with bounded waits.
type RetryClient struct {
	doer  Doer
	sleep func(ctx context.Context, d time.Duration) error
	now   func() time.Time
}

// Option customizes a RetryClient (test injection).
type Option func(*RetryClient)

// WithSleep replaces the context-aware sleep.
func WithSleep(f func(ctx context.Context, d time.Duration) error) Option {
	return func(c *RetryClient) { c.sleep = f }
}

// WithNow replaces the clock used to interpret X-RateLimit-Reset.
func WithNow(f func() time.Time) Option {
	return func(c *RetryClient) { c.now = f }
}

// New wraps doer with the retry policy.
func New(doer Doer, opts ...Option) *RetryClient {
	c := &RetryClient{doer: doer, sleep: sleepContext, now: time.Now}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Do executes req, retrying retryable statuses (429/502/503/504) up to
// maxRetries times. Requests with a body are retried only when req.GetBody
// is set (http.NewRequest sets it for common in-memory body types); a
// streaming body cannot be replayed, so its response is returned as-is.
func (c *RetryClient) Do(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := c.doer.Do(req)
		if err != nil {
			return nil, err
		}
		if !isRetryableStatus(resp.StatusCode) || attempt >= maxRetries {
			return resp, nil
		}
		if req.Body != nil && req.GetBody == nil {
			return resp, nil
		}
		delay := c.delayFor(resp, attempt)
		if delay > maxRetryDelay {
			return resp, nil
		}
		_ = resp.Body.Close()
		logRetry(resp.StatusCode, attempt, delay)
		if err := c.sleep(req.Context(), delay); err != nil {
			return nil, err
		}
		if req.GetBody != nil {
			body, gerr := req.GetBody()
			if gerr != nil {
				return nil, gerr
			}
			req.Body = body
		}
	}
}

// delayFor picks the wait before the next attempt: Retry-After exactly when
// present, else X-RateLimit-Reset (an epoch timestamp), else exponential
// backoff capped at maxRetryDelay.
func (c *RetryClient) delayFor(resp *http.Response, attempt int) time.Duration {
	if d, ok := parseRetryAfter(resp.Header.Get("Retry-After"), c.now()); ok {
		return d
	}
	if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
		if epoch, err := strconv.ParseInt(reset, 10, 64); err == nil {
			if d := time.Unix(epoch, 0).Sub(c.now()); d > 0 {
				return d
			}
			return 0
		}
	}
	d := baseRetryDelay << attempt
	if d > maxRetryDelay || d <= 0 {
		return maxRetryDelay
	}
	return d
}

// isRetryableStatus mirrors internal/client: throttling and transient
// gateway failures only. 500 is excluded — retrying genuine server errors
// hides bugs (and a publish 5xx has its own reconcile path, D18).
func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// parseRetryAfter interprets Retry-After as seconds or an HTTP date.
func parseRetryAfter(header string, now time.Time) (time.Duration, bool) {
	if header == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0, true
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(header); err == nil {
		if d := t.Sub(now); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// logRetry keeps throttle waits visible — a silent 30s pause reads as a hang.
func logRetry(status, attempt int, d time.Duration) {
	log.Infof("server returned %d — waiting %s before retrying (attempt %d/%d)",
		status, d.Round(time.Second), attempt+1, maxRetries)
}
