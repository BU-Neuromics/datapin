package env_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/env"
	"github.com/BU-Neuromics/datapin/internal/log"
)

// captureWarnings routes log output to a buffer for the duration of the test.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetWriter(&buf, 0, false)
	t.Cleanup(func() { log.Init(0, true) })
	return &buf
}

func TestGet_ReadsPrimaryVar(t *testing.T) {
	t.Setenv("DATAPIN_API_BASE", "https://primary.example")
	t.Setenv("GOSF_API_BASE", "")
	buf := captureWarnings(t)

	if got := env.Get("API_BASE"); got != "https://primary.example" {
		t.Errorf("Get = %q, want %q", got, "https://primary.example")
	}
	if buf.Len() != 0 {
		t.Errorf("no warning expected for the primary var, got: %s", buf.String())
	}
}

func TestGet_FallsBackToLegacyVarWithWarning(t *testing.T) {
	t.Setenv("DATAPIN_API_BASE", "")
	t.Setenv("GOSF_API_BASE", "https://legacy.example")
	buf := captureWarnings(t)

	if got := env.Get("API_BASE"); got != "https://legacy.example" {
		t.Errorf("Get = %q, want %q", got, "https://legacy.example")
	}
	warning := buf.String()
	if !strings.Contains(warning, "GOSF_API_BASE") || !strings.Contains(warning, "DATAPIN_API_BASE") {
		t.Errorf("warning must name both the legacy and replacement vars, got: %q", warning)
	}
	if !strings.Contains(strings.ToLower(warning), "deprecated") {
		t.Errorf("warning must say the legacy var is deprecated, got: %q", warning)
	}
}

func TestGet_PrimaryWinsOverLegacy(t *testing.T) {
	t.Setenv("DATAPIN_API_BASE", "https://primary.example")
	t.Setenv("GOSF_API_BASE", "https://legacy.example")
	captureWarnings(t)

	if got := env.Get("API_BASE"); got != "https://primary.example" {
		t.Errorf("Get = %q, want the primary value to win", got)
	}
}

func TestGet_EmptyWhenNeitherSet(t *testing.T) {
	t.Setenv("DATAPIN_API_BASE", "")
	t.Setenv("GOSF_API_BASE", "")
	buf := captureWarnings(t)

	if got := env.Get("API_BASE"); got != "" {
		t.Errorf("Get = %q, want empty", got)
	}
	if buf.Len() != 0 {
		t.Errorf("no warning expected when neither var is set, got: %s", buf.String())
	}
}
