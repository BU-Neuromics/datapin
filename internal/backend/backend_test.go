package backend_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
)

func TestParseChecksum(t *testing.T) {
	tests := []struct {
		in      string
		want    backend.Checksum
		wantErr bool
	}{
		{"md5:9695a27bc7b9b0dc3c12b4dd4af31c82", backend.Checksum{Algo: "md5", Hex: "9695a27bc7b9b0dc3c12b4dd4af31c82"}, false},
		{"MD5:ABCDEF", backend.Checksum{Algo: "md5", Hex: "abcdef"}, false}, // downloads send "MD5:<hex>" (oc-checksum)
		{"sha256:deadbeef", backend.Checksum{Algo: "sha256", Hex: "deadbeef"}, false},
		{"", backend.Checksum{}, true},
		{"md5", backend.Checksum{}, true},
		{"md5:", backend.Checksum{}, true},
		{":abc", backend.Checksum{}, true},
	}
	for _, tt := range tests {
		got, err := backend.ParseChecksum(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseChecksum(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseChecksum(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestChecksum_String(t *testing.T) {
	c := backend.Checksum{Algo: "md5", Hex: "abc"}
	if got := c.String(); got != "md5:abc" {
		t.Errorf("String() = %q, want %q", got, "md5:abc")
	}
}

func TestValidationError_Error(t *testing.T) {
	e := &backend.ValidationError{
		Message: "A validation error occurred.",
		Fields:  map[string][]string{"metadata.publisher": {"Missing publisher field required for DOI registration."}},
	}
	msg := e.Error()
	if !strings.Contains(msg, "metadata.publisher") || !strings.Contains(msg, "Missing publisher") {
		t.Errorf("Error() must carry field and message, got %q", msg)
	}
	bare := &backend.ValidationError{Message: "nope"}
	if bare.Error() != "nope" {
		t.Errorf("field-less Error() = %q", bare.Error())
	}
}

func TestIsNotFound(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &backend.NotFoundError{What: "record 585305"})
	if !backend.IsNotFound(err) {
		t.Error("IsNotFound must see through wrapping")
	}
	if backend.IsNotFound(errors.New("plain")) {
		t.Error("IsNotFound(plain error) = true")
	}
}
