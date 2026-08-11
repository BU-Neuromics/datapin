package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/client"
)

// A bare "OSF API 429: Request was throttled." tells the user nothing they can
// act on. The message must say what happened, why the ceiling was where it was,
// and what to do — and the single most useful fact is whether the run was
// authenticated at all, because the anonymous ceiling is ~100/hour against
// 10,000/day (issue #86).
func TestFriendlyAPIError_RateLimited(t *testing.T) {
	throttled := &client.APIError{StatusCode: 429, Message: "Request was throttled."}

	t.Run("unauthenticated names the real cause", func(t *testing.T) {
		msg := friendlyAPIError(throttled, false).Error()
		for _, want := range []string{"rate limit", "unauthenticated", "datapin auth login"} {
			if !strings.Contains(strings.ToLower(msg), strings.ToLower(want)) {
				t.Errorf("message should mention %q:\n%s", want, msg)
			}
		}
	})

	t.Run("authenticated does not suggest logging in again", func(t *testing.T) {
		msg := friendlyAPIError(throttled, true).Error()
		if !strings.Contains(strings.ToLower(msg), "rate limit") {
			t.Errorf("message should name the rate limit:\n%s", msg)
		}
		if strings.Contains(msg, "datapin auth login") {
			t.Errorf("an already-authenticated user must not be told to log in:\n%s", msg)
		}
		// Still needs an actionable suggestion.
		if !strings.Contains(msg, "--jobs") && !strings.Contains(strings.ToLower(msg), "wait") {
			t.Errorf("message should suggest something actionable:\n%s", msg)
		}
	})
}

// The existing 401/403 handling must keep working through the same entry point.
func TestFriendlyAPIError_StillHandlesAuthErrors(t *testing.T) {
	for _, code := range []int{401, 403} {
		err := friendlyAPIError(&client.APIError{StatusCode: code, Message: "no"}, false)
		if !strings.Contains(err.Error(), "datapin auth login") {
			t.Errorf("HTTP %d should still produce the auth hint, got %v", code, err)
		}
	}
}

// Unrelated errors pass through untouched.
func TestFriendlyAPIError_PassesThroughOthers(t *testing.T) {
	orig := errors.New("some transport failure")
	if got := friendlyAPIError(orig, true); !errors.Is(got, orig) {
		t.Errorf("unrelated errors must pass through unchanged, got %v", got)
	}
	if friendlyAPIError(nil, true) != nil {
		t.Error("nil must stay nil")
	}
}

// A 404 is not a rate limit and must not be dressed up as one.
func TestFriendlyAPIError_LeavesNotFoundAlone(t *testing.T) {
	err := friendlyAPIError(&client.APIError{StatusCode: 404, Message: "Not found."}, true)
	if strings.Contains(strings.ToLower(err.Error()), "rate limit") {
		t.Errorf("a 404 must not be reported as a rate limit: %v", err)
	}
}

// The silent-anonymous trap: LoadToken returns "" on a locked keychain or a
// mistyped env var, and nothing said so. A scanning command must warn, because
// the anonymous ceiling is what users are actually hitting.
func TestUnauthenticatedScanWarning(t *testing.T) {
	tests := []struct {
		name    string
		token   string
		entries int
		want    bool
	}{
		{"anonymous with tracked files warns", "", 12, true},
		{"authenticated does not warn", "tok", 12, false},
		{"anonymous with an empty manifest has nothing to warn about", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldWarnUnauthenticated(tt.token, tt.entries); got != tt.want {
				t.Errorf("shouldWarnUnauthenticated(%q, %d) = %v, want %v",
					tt.token, tt.entries, got, tt.want)
			}
		})
	}
}
