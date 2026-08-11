//go:build live

package livezenodo

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/backend/invenio"
)

// TestLiveZenodo_MultipartUpload exercises the multipart (`M` transfer)
// upload path against the real sandbox. The threshold and part size are
// injectable exactly so this stays cheap: a ~6 MiB file with 5 MiB parts
// (the S3 part-size floor, which sandbox storage may enforce) crosses into
// two real parts. fakeinvenio's part-PUT/commit model is DOCUMENTED-only
// (D28/D43) — this is the run that keeps it honest.
func TestLiveZenodo_MultipartUpload(t *testing.T) {
	token := os.Getenv("ZENODO_SANDBOX_TOKEN")
	if token == "" {
		t.Skip("ZENODO_SANDBOX_TOKEN unset")
	}
	c, err := invenio.New("https://sandbox.zenodo.org", token,
		invenio.WithMultipartThreshold(1<<20), // 1 MiB — force multipart for a small file
		invenio.WithPartSize(5<<20))           // 5 MiB — the documented S3 minimum part size
	if err != nil {
		t.Fatalf("invenio.New: %v", err)
	}
	ctx := context.Background()

	id, err := c.CreateDraft(ctx, backend.Metadata{
		Title:           "datapin-ci multipart upload probe",
		Description:     "Created by datapin's live test suite; safe to delete.",
		PublicationDate: "2026-08-11",
		Publisher:       "Zenodo",
		ResourceType:    "dataset",
		License:         "CC0-1.0",
		Creators:        []backend.Creator{{FamilyName: "CI", GivenName: "Datapin"}},
	})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	t.Cleanup(func() {
		if derr := c.Discard(context.Background(), id); derr != nil {
			t.Logf("discarding probe draft %s: %v", id, derr)
		}
	})

	// ~6 MiB, non-round so the final part differs from part_size.
	content := bytes.Repeat([]byte("datapin multipart probe payload "), (6<<20)/32)
	content = append(content, []byte("tail-bytes")...)
	sum := md5.Sum(content)
	want := backend.Checksum{Algo: "md5", Hex: hex.EncodeToString(sum[:])}

	fi, err := c.UploadFile(ctx, id, "multipart.bin", bytes.NewReader(content), int64(len(content)), want)
	if err != nil {
		t.Fatalf("multipart UploadFile: %v", err)
	}
	if fi.Pending {
		t.Error("committed multipart upload reports Pending")
	}
	if fi.Size != int64(len(content)) {
		t.Errorf("committed size = %d, want %d", fi.Size, len(content))
	}
	// The commit may report the real md5 or defer it to an async recompute
	// (in which case the driver surfaces no checksum — D44). Only a present
	// md5 is asserted.
	if fi.Checksum.Algo == "md5" && fi.Checksum != want {
		t.Errorf("committed checksum = %v, want %v", fi.Checksum, want)
	}

	files, err := c.ListDraftFiles(ctx, id)
	if err != nil {
		t.Fatalf("ListDraftFiles: %v", err)
	}
	if len(files) != 1 || files[0].Key != "multipart.bin" || files[0].Pending {
		t.Fatalf("draft files = %+v, want one completed multipart.bin", files)
	}
}
