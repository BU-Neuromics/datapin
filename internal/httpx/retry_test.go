package httpx_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/BU-Neuromics/datapin/internal/httpx"
)

// scriptedDoer returns canned responses in order and records requests.
type scriptedDoer struct {
	responses []*http.Response
	requests  []*http.Request
	bodies    []string
}

func (s *scriptedDoer) Do(req *http.Request) (*http.Response, error) {
	s.requests = append(s.requests, req)
	body := ""
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	s.bodies = append(s.bodies, body)
	if len(s.responses) == 0 {
		return resp(200, nil), nil
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	return r, nil
}

func resp(status int, headers map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(bytes.NewReader(nil))}
}

// newClient builds a RetryClient with a recording sleep and a fixed clock.
func newClient(d httpx.Doer, slept *[]time.Duration, now time.Time) *httpx.RetryClient {
	return httpx.New(d,
		httpx.WithSleep(func(ctx context.Context, dur time.Duration) error {
			*slept = append(*slept, dur)
			return ctx.Err()
		}),
		httpx.WithNow(func() time.Time { return now }),
	)
}

func get(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), "GET", "http://x/api", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestDo_RetriesOn429HonoringRetryAfter(t *testing.T) {
	d := &scriptedDoer{responses: []*http.Response{
		resp(429, map[string]string{"Retry-After": "7"}),
		resp(200, nil),
	}}
	var slept []time.Duration
	c := newClient(d, &slept, time.Now())

	r, err := c.Do(get(t))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if r.StatusCode != 200 {
		t.Errorf("status = %d, want 200", r.StatusCode)
	}
	if len(d.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(d.requests))
	}
	if len(slept) != 1 || slept[0] != 7*time.Second {
		t.Errorf("slept = %v, want [7s] (Retry-After honored exactly)", slept)
	}
}

func TestDo_FallsBackToRateLimitReset(t *testing.T) {
	now := time.Now()
	reset := now.Add(5 * time.Second).Unix()
	d := &scriptedDoer{responses: []*http.Response{
		resp(429, map[string]string{"X-RateLimit-Reset": strconv.FormatInt(reset, 10)}),
		resp(200, nil),
	}}
	var slept []time.Duration
	c := newClient(d, &slept, now)

	if _, err := c.Do(get(t)); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(slept) != 1 || slept[0] < 4*time.Second || slept[0] > 6*time.Second {
		t.Errorf("slept = %v, want ~5s from X-RateLimit-Reset", slept)
	}
}

func TestDo_200WithRetryAfterIsNotRetried(t *testing.T) {
	// Zenodo sends retry-after on successful responses (zenodo-notes §3);
	// header presence must never trigger a retry on its own.
	d := &scriptedDoer{responses: []*http.Response{
		resp(200, map[string]string{"Retry-After": "9"}),
	}}
	var slept []time.Duration
	c := newClient(d, &slept, time.Now())

	r, err := c.Do(get(t))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if r.StatusCode != 200 || len(d.requests) != 1 || len(slept) != 0 {
		t.Errorf("200+Retry-After: requests=%d slept=%v, want single request, no sleep", len(d.requests), slept)
	}
}

func TestDo_500IsNotRetried(t *testing.T) {
	d := &scriptedDoer{responses: []*http.Response{resp(500, nil), resp(200, nil)}}
	var slept []time.Duration
	c := newClient(d, &slept, time.Now())

	r, err := c.Do(get(t))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if r.StatusCode != 500 || len(d.requests) != 1 {
		t.Errorf("got status %d after %d requests, want 500 after 1 (500 is never retried)", r.StatusCode, len(d.requests))
	}
}

func TestDo_DeclinesWaitsPastMax(t *testing.T) {
	d := &scriptedDoer{responses: []*http.Response{
		resp(429, map[string]string{"Retry-After": "3600"}),
		resp(200, nil),
	}}
	var slept []time.Duration
	c := newClient(d, &slept, time.Now())

	r, err := c.Do(get(t))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if r.StatusCode != 429 || len(slept) != 0 {
		t.Errorf("hour-long Retry-After must be declined: status=%d slept=%v", r.StatusCode, slept)
	}
}

func TestDo_ExhaustsRetries(t *testing.T) {
	d := &scriptedDoer{responses: []*http.Response{
		resp(429, map[string]string{"Retry-After": "1"}),
		resp(429, map[string]string{"Retry-After": "1"}),
		resp(429, map[string]string{"Retry-After": "1"}),
		resp(429, map[string]string{"Retry-After": "1"}),
		resp(429, map[string]string{"Retry-After": "1"}),
	}}
	var slept []time.Duration
	c := newClient(d, &slept, time.Now())

	r, err := c.Do(get(t))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if r.StatusCode != 429 {
		t.Errorf("status = %d, want the final 429 surfaced", r.StatusCode)
	}
	if len(d.requests) != 4 { // initial + maxRetries
		t.Errorf("requests = %d, want 4 (initial + 3 retries)", len(d.requests))
	}
}

func TestDo_CancelledContextAbortsSleep(t *testing.T) {
	d := &scriptedDoer{responses: []*http.Response{
		resp(429, map[string]string{"Retry-After": "5"}),
		resp(200, nil),
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://x/api", nil)

	var slept []time.Duration
	c := newClient(d, &slept, time.Now())
	if _, err := c.Do(req); err == nil {
		t.Fatal("Do with cancelled context must return the context error")
	}
}

func TestDo_RewindsBodyOnRetry(t *testing.T) {
	d := &scriptedDoer{responses: []*http.Response{
		resp(429, map[string]string{"Retry-After": "1"}),
		resp(200, nil),
	}}
	var slept []time.Duration
	c := newClient(d, &slept, time.Now())

	req, err := http.NewRequestWithContext(context.Background(), "POST", "http://x/api", bytes.NewReader([]byte(`{"key":"v"}`)))
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if r.StatusCode != 200 {
		t.Errorf("status = %d, want 200", r.StatusCode)
	}
	if len(d.bodies) != 2 || d.bodies[0] != `{"key":"v"}` || d.bodies[1] != `{"key":"v"}` {
		t.Errorf("bodies = %q, want the same body sent twice", d.bodies)
	}
}

func TestDo_UnrewindableBodyIsNotRetried(t *testing.T) {
	d := &scriptedDoer{responses: []*http.Response{
		resp(429, map[string]string{"Retry-After": "1"}),
		resp(200, nil),
	}}
	var slept []time.Duration
	c := newClient(d, &slept, time.Now())

	req, err := http.NewRequestWithContext(context.Background(), "PUT", "http://x/api",
		io.NopCloser(bytes.NewReader([]byte("streamed")))) // NopCloser defeats GetBody
	if err != nil {
		t.Fatal(err)
	}
	if req.GetBody != nil {
		t.Fatal("precondition: GetBody must be nil for this test")
	}
	r, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if r.StatusCode != 429 || len(d.requests) != 1 {
		t.Errorf("unrewindable body: status=%d requests=%d, want 429 after 1", r.StatusCode, len(d.requests))
	}
}

func TestDo_OnlyRetry429SkipsGatewayRetries(t *testing.T) {
	// Non-idempotent actions (publish) recover from 5xx by reconciling,
	// never by blind retry — but throttling is still absorbed.
	d := &scriptedDoer{responses: []*http.Response{
		resp(504, map[string]string{"Retry-After": "1"}),
		resp(200, nil),
	}}
	var slept []time.Duration
	c := newClient(d, &slept, time.Now())

	r, err := c.Do(httpx.OnlyRetry429(get(t)))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if r.StatusCode != 504 || len(d.requests) != 1 {
		t.Errorf("504 under OnlyRetry429: status=%d requests=%d, want 504 after 1 request", r.StatusCode, len(d.requests))
	}

	d2 := &scriptedDoer{responses: []*http.Response{
		resp(429, map[string]string{"Retry-After": "1"}),
		resp(200, nil),
	}}
	c2 := newClient(d2, &slept, time.Now())
	r2, err := c2.Do(httpx.OnlyRetry429(get(t)))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if r2.StatusCode != 200 || len(d2.requests) != 2 {
		t.Errorf("429 under OnlyRetry429: status=%d requests=%d, want retried to 200", r2.StatusCode, len(d2.requests))
	}
}

var _ = fmt.Sprintf // keep fmt imported if assertions change
