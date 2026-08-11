package dataverse_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/backend/dataverse"
	"github.com/BU-Neuromics/datapin/internal/testutil/fakedataverse"
)

func newLicenseClient(t *testing.T) (*dataverse.Client, *fakedataverse.Server) {
	t.Helper()
	srv := fakedataverse.New(testToken)
	t.Cleanup(srv.Close)
	c, err := dataverse.New(srv.URL(), testToken)
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func licenseMeta(license string) backend.Metadata {
	return backend.Metadata{
		Title:           "license test",
		PublicationDate: "2026-08-11",
		Publisher:       "datapin tests",
		License:         license,
		ResourceType:    "dataset",
		ContactEmail:    "pi@example.edu",
		Creators:        []backend.Creator{{FamilyName: "Tester", GivenName: "Trusty"}},
	}
}

// Real Dataverse validates license.name/uri against the instance's
// /api/licenses registry — the first live run (demo 6.11) rejected a raw
// SPDX id with "Error parsing Json: Invalid or unsupported license:
// CC0-1.0". The driver must resolve the manifest's SPDX id to the
// registered entry, here via the entry's SPDX rightsIdentifier crosswalk.
func TestCreateDraft_TranslatesSPDXLicense(t *testing.T) {
	c, srv := newLicenseClient(t)
	id, err := c.CreateDraft(context.Background(), licenseMeta("CC0-1.0"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	lic := srv.DatasetLicense(string(id))
	if lic == nil || lic["name"] != "CC0 1.0" || lic["uri"] != "http://creativecommons.org/publicdomain/zero/1.0" {
		t.Fatalf("stored license = %v, want the registered CC0 1.0 entry", lic)
	}
}

// Not every registry entry carries the SPDX crosswalk (demo's "CC BY 4.0"
// doesn't) — those resolve by normalized name.
func TestCreateDraft_LicenseResolvedByName(t *testing.T) {
	c, srv := newLicenseClient(t)
	id, err := c.CreateDraft(context.Background(), licenseMeta("CC-BY-4.0"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if lic := srv.DatasetLicense(string(id)); lic == nil || lic["name"] != "CC BY 4.0" {
		t.Fatalf("stored license = %v, want CC BY 4.0", lic)
	}
}

// A license the instance does not offer fails loudly, listing what it
// does offer — never a silent substitution.
func TestCreateDraft_UnsupportedLicense(t *testing.T) {
	c, _ := newLicenseClient(t)
	_, err := c.CreateDraft(context.Background(), licenseMeta("MIT"))
	var ve *backend.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if !strings.Contains(ve.Message, "MIT") || !strings.Contains(ve.Message, "CC0 1.0") {
		t.Fatalf("message %q should name the missing license and list the offered ones", ve.Message)
	}
}

// No license in the metadata sends no license field. The publish gate
// upstream forbids that for datapin publishes; the driver must not
// invent one (the instance would apply its own default silently).
func TestCreateDraft_NoLicenseOmitsField(t *testing.T) {
	c, srv := newLicenseClient(t)
	id, err := c.CreateDraft(context.Background(), licenseMeta(""))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if lic := srv.DatasetLicense(string(id)); lic != nil {
		t.Fatalf("stored license = %v, want none", lic)
	}
}
