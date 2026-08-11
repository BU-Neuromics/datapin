package figshare_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/backend/figshare"
	"github.com/BU-Neuromics/datapin/internal/testutil/fakefigshare"
)

func newLicenseClient(t *testing.T) *figshare.Client {
	t.Helper()
	srv := fakefigshare.New(testToken)
	t.Cleanup(srv.Close)
	c, err := figshare.New(srv.URL(), testToken)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func licenseMeta(license string) backend.Metadata {
	return backend.Metadata{
		Title:           "license test",
		PublicationDate: "2026-08-11",
		Publisher:       "datapin tests",
		License:         license,
		ResourceType:    "dataset",
		Creators:        []backend.Creator{{FamilyName: "Tester", GivenName: "Trusty"}},
	}
}

// An SPDX id the instance's license vocabulary can't satisfy must fail
// loudly, listing what the instance offers — not be silently dropped
// (which would publish under Figshare's own default).
func TestCreateDraft_UnsupportedLicenseErrors(t *testing.T) {
	c := newLicenseClient(t)
	_, err := c.CreateDraft(context.Background(), licenseMeta("BSD-3-Clause"))
	var ve *backend.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if !strings.Contains(ve.Message, "BSD-3-Clause") || !strings.Contains(ve.Message, "CC BY 4.0") {
		t.Fatalf("message %q should name the missing license and list the offered ones", ve.Message)
	}
}

// The mapped licenses keep working; no license sends no license field.
func TestCreateDraft_KnownAndAbsentLicenses(t *testing.T) {
	c := newLicenseClient(t)
	if _, err := c.CreateDraft(context.Background(), licenseMeta("CC0-1.0")); err != nil {
		t.Fatalf("CreateDraft with CC0-1.0: %v", err)
	}
	if _, err := c.CreateDraft(context.Background(), licenseMeta("")); err != nil {
		t.Fatalf("CreateDraft without license: %v", err)
	}
}
