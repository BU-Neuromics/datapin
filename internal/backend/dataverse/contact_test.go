package dataverse_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
)

// Live divergence (demo 6.11): Dataverse requires a Point of Contact
// e-mail in the citation block — "Validation Failed: Point of Contact
// E-mail is required." The driver must send datasetContactEmail.
func TestCreateDraft_SendsContactEmail(t *testing.T) {
	c, srv := newLicenseClient(t)
	m := licenseMeta("CC0-1.0")
	m.ContactEmail = "pi@example.edu"
	id, err := c.CreateDraft(context.Background(), m)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if got := srv.DatasetContactEmail(string(id)); got != "pi@example.edu" {
		t.Fatalf("stored contact email = %q, want pi@example.edu", got)
	}
}

// datapin has no email to invent, so a missing contact email fails in
// the driver with the manifest key the user must set — before anything
// is created on the instance.
func TestCreateDraft_MissingContactEmail(t *testing.T) {
	c, _ := newLicenseClient(t)
	m := licenseMeta("CC0-1.0")
	m.ContactEmail = ""
	_, err := c.CreateDraft(context.Background(), m)
	var ve *backend.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if !strings.Contains(ve.Message, "contact_email") {
		t.Fatalf("message %q should name the contact_email manifest key", ve.Message)
	}
}
