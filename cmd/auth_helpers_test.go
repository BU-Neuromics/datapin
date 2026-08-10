package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/client"
)

func TestFriendlyAuthError(t *testing.T) {
	t.Run("nil passes through", func(t *testing.T) {
		if friendlyAuthError(nil) != nil {
			t.Error("nil should stay nil")
		}
	})

	for _, code := range []int{401, 403} {
		t.Run("wraps auth failure", func(t *testing.T) {
			err := friendlyAuthError(&client.APIError{StatusCode: code, Message: "Forbidden"})
			if err == nil {
				t.Fatal("expected wrapped error")
			}
			msg := err.Error()
			if !strings.Contains(msg, "datapin auth login") || !strings.Contains(msg, "OSF_TOKEN") {
				t.Errorf("message should suggest authentication: %q", msg)
			}
		})
	}

	t.Run("other API errors pass through unchanged", func(t *testing.T) {
		orig := &client.APIError{StatusCode: 404, Message: "Not Found"}
		if got := friendlyAuthError(orig); !errors.Is(got, orig) && got != error(orig) {
			t.Errorf("404 should pass through, got %v", got)
		}
	})

	t.Run("non-API errors pass through", func(t *testing.T) {
		orig := errors.New("network down")
		if got := friendlyAuthError(orig); got != orig {
			t.Errorf("non-API error should pass through unchanged, got %v", got)
		}
	})
}
