//go:build live

package livezenodo

import (
	"context"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend/invenio"
)

// TestLiveZenodo_Probe runs the per-instance capability probe (issue #20,
// D54/D55) against the real sandbox. Everything it reads is anonymous, so
// it needs no token: the resource-type vocabulary and the file-schema
// signal that says whether the instance serves the pluggable-transfer
// model. fakeinvenio encodes both shapes — this is the run that keeps them
// honest (the Zenodo tier already caught one such divergence).
func TestLiveZenodo_Probe(t *testing.T) {
	c, err := invenio.New("https://sandbox.zenodo.org", "")
	if err != nil {
		t.Fatalf("invenio.New: %v", err)
	}
	p, err := c.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	// Zenodo's vocabulary is dozens of ids and certainly includes "dataset".
	if len(p.ResourceTypes) < 5 {
		t.Errorf("probed %d resource types, expected the full vocabulary: %v", len(p.ResourceTypes), p.ResourceTypes)
	}
	found := false
	for _, id := range p.ResourceTypes {
		if id == "dataset" {
			found = true
		}
	}
	if !found {
		t.Errorf("resource-type vocabulary lacks \"dataset\": %v", p.ResourceTypes)
	}

	// Sandbox runs a v13+ InvenioRDM, so its file listings carry `transfer`
	// and the probe must report multipart as available. A nil here means the
	// search or files endpoint changed shape — the assumption to re-check.
	if p.MultipartUpload == nil {
		t.Errorf("multipart support inconclusive against sandbox; notes: %v", p.Notes)
	} else if !*p.MultipartUpload {
		t.Errorf("probe reports no multipart on sandbox, which runs the pluggable-transfer model; notes: %v", p.Notes)
	}

	// The API states no per-record limits anywhere; if that ever changes,
	// this is the test that should be taught the new source.
	if p.MaxFilesPerRecord != 0 || p.MaxFileSize != 0 {
		t.Errorf("probe reported limits %d/%d — the API is not supposed to expose any", p.MaxFilesPerRecord, p.MaxFileSize)
	}
}
