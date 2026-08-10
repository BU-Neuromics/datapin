package client

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/BU-Neuromics/datapin/internal/log"
)

// OSF throttles: roughly 100 requests/hour unauthenticated and 10,000/day
// authenticated. It signals a throttled request with 429 and a Retry-After
// header, which datapin used to ignore outright — the request simply failed and
// the run died mid-scan (issue #86).
//
// A bounded retry absorbs the brief throttles that a large scan provokes
// without turning a genuinely exhausted quota into an unbounded hang: after
// maxRetries the 429 is returned, and the command turns it into an actionable
// message.
const (
	maxRetries     = 3
	baseRetryDelay = 2 * time.Second
	// maxRetryDelay bounds how long a single wait may be. It caps the
	// exponential backoff, and is the threshold past which the server's own
	// Retry-After is declined rather than truncated: OSF answers "come back in
	// 47 minutes" when an hourly quota is spent, and silently sitting on that
	// is worse than failing with the number. Truncating it would be worse
	// still — we would retry early, earn another 429, and burn the budget.
	maxRetryDelay = 30 * time.Second
)

// isRetryableStatus reports whether a status is worth retrying: throttling and
// transient gateway failures. 500 is excluded deliberately — a genuine server
// error is not usually cured by waiting, and retrying it hides bugs.
func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// parseRetryAfter interprets a Retry-After header, which RFC 9110 allows to be
// either a delay in seconds or an HTTP date. A date already in the past yields
// zero rather than a negative wait. Reports false when the header is absent or
// unparseable, leaving the caller to fall back to its own backoff.
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

// retryDelay picks how long to wait before attempt+1. The server's Retry-After
// wins when it sent one — it knows when the quota resets — and is honoured
// exactly, never truncated. Without a header the delay doubles per attempt,
// capped at maxRetryDelay.
func retryDelay(attempt int, retryAfter time.Duration, hasRetryAfter bool) time.Duration {
	if hasRetryAfter {
		return retryAfter
	}
	d := baseRetryDelay << attempt
	if d > maxRetryDelay || d <= 0 { // <=0 guards the shift overflowing
		return maxRetryDelay
	}
	return d
}

// shouldRetry decides whether a response is worth another attempt. A wait
// longer than maxRetryDelay is declined: the quota is genuinely spent, and the
// caller should surface how long it will be rather than block the run.
func shouldRetry(status, attempt int, delay time.Duration) bool {
	return isRetryableStatus(status) && attempt < maxRetries && delay <= maxRetryDelay
}

// sleepContext waits for d, or returns early if the context is cancelled. This
// is what keeps Ctrl-C responsive during a rate-limit wait.
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

// logRetry reports a throttle to the user. A scan that pauses for 30s with no
// output looks like a hang, so this is INFO rather than debug.
func logRetry(status int, attempt int, d time.Duration) {
	if status == http.StatusTooManyRequests {
		log.Infof("rate limited by OSF — waiting %s before retrying (attempt %d/%d)",
			d.Round(time.Second), attempt+1, maxRetries)
		return
	}
	log.Infof("OSF returned %d — waiting %s before retrying (attempt %d/%d)",
		status, d.Round(time.Second), attempt+1, maxRetries)
}
