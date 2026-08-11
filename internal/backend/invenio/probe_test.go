package invenio_test

import (
	"context"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/backend/invenio"
	"github.com/BU-Neuromics/datapin/internal/testutil/fakeinvenio"
)

// A remote's stored caps are handed to the driver, not re-derived: the
// caps the client reports are the ones it was constructed with (issue #20).
func TestWithCaps_OverridesTheZenodoProfile(t *testing.T) {
	srv := fakeinvenio.New(testToken)
	t.Cleanup(srv.Close)
	want := backend.Caps{
		MintsDOI: true, PerVersionDOI: true, ReserveDOI: true, PIDKind: "doi",
		SyncPublish: true, ImportsPrevious: true,
		MultipartUpload: false, MaxFilesPerRecord: 250, MaxFileSize: 1 << 30,
		ChecksumAlgo: "md5",
	}
	c, err := invenio.New(srv.URL(), testToken, invenio.WithCaps(want))
	if err != nil {
		t.Fatalf("invenio.New: %v", err)
	}
	if got := c.Capabilities(); got != want {
		t.Errorf("Capabilities() = %+v, want %+v", got, want)
	}
}

// An instance whose caps say it has no multipart transfer must never
// register an `M` transfer, however large the file: Zenodo gates part PUTs
// server-side (D52), and an instance that never declared multipart at all
// would fail the same way.
func TestUploadFile_MultipartDisabledByCaps(t *testing.T) {
	srv := fakeinvenio.New(testToken)
	t.Cleanup(srv.Close)
	caps := invenio.DefaultCaps("example.org")
	caps.MultipartUpload = false
	c, err := invenio.New(srv.URL(), testToken,
		invenio.WithCaps(caps), invenio.WithMultipartThreshold(16), invenio.WithPartSize(10))
	if err != nil {
		t.Fatalf("invenio.New: %v", err)
	}
	ctx := context.Background()
	id, err := c.CreateDraft(ctx, meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	content := []byte("0123456789abcdefghij01234") // 25 bytes, over the threshold
	mustUpload(t, c, id, "big.bin", content)
	if n := countRequests(srv, "PUT", "/big.bin/content"); n != 1 {
		t.Errorf("single-PUT uploads = %d, want 1 (multipart is disabled)", n)
	}
	if n := countRequests(srv, "PUT", "/big.bin/content/1"); n != 0 {
		t.Errorf("part uploads = %d, want 0", n)
	}
}

// Probe reads the instance's own resource-type vocabulary, paginating it.
func TestProbe_ReadsResourceTypeVocabulary(t *testing.T) {
	srv := fakeinvenio.New(testToken)
	t.Cleanup(srv.Close)
	srv.ResourceTypes = []string{"dataset", "software", "image-photo", "publication-article"}
	c, err := invenio.New(srv.URL(), testToken)
	if err != nil {
		t.Fatalf("invenio.New: %v", err)
	}
	p, err := c.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if strings.Join(p.ResourceTypes, ",") != "dataset,image-photo,publication-article,software" {
		t.Errorf("ResourceTypes = %v, want the instance vocabulary sorted", p.ResourceTypes)
	}
}

// Nothing in the InvenioRDM API states per-record file-count or size
// limits, so Probe must leave them unset (the caller keeps the documented
// Zenodo default) and say so in a note rather than invent a number (D54).
func TestProbe_LimitsAreNotInvented(t *testing.T) {
	c, _ := newClient(t)
	p, err := c.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if p.MaxFilesPerRecord != 0 || p.MaxFileSize != 0 {
		t.Errorf("Probe reported limits %d/%d — the API exposes none", p.MaxFilesPerRecord, p.MaxFileSize)
	}
	if len(p.Notes) == 0 {
		t.Error("Probe must note which caps fell back to defaults")
	}
	joined := strings.Join(p.Notes, "\n")
	if !strings.Contains(joined, "file") {
		t.Errorf("notes should explain the file-limit default, got:\n%s", joined)
	}
}

// publishRecordWithFile seeds the fake with one published record carrying
// one file — what the multipart probe reads.
func publishRecordWithFile(t *testing.T, c *invenio.Client) {
	t.Helper()
	ctx := context.Background()
	id, err := c.CreateDraft(ctx, meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	mustUpload(t, c, id, "seed.txt", []byte("seed"))
	if _, err := c.Publish(ctx, id); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

// An instance serving the pre-v13 file schema (no `transfer` object) cannot
// have the multipart transfer at all, so the probe reports it as absent.
func TestProbe_MultipartAbsentOnLegacyFileSchema(t *testing.T) {
	c, srv := newClient(t)
	srv.LegacyFileSchema = true
	publishRecordWithFile(t, c)

	p, err := c.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if p.MultipartUpload == nil || *p.MultipartUpload {
		t.Fatalf("MultipartUpload = %v, want a definite false", p.MultipartUpload)
	}
}

// An instance whose file listing carries `transfer` runs the pluggable
// transfer model, so multipart may exist: the probe reports the floor.
func TestProbe_MultipartFloorFromTransferKey(t *testing.T) {
	c, _ := newClient(t)
	publishRecordWithFile(t, c)

	p, err := c.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if p.MultipartUpload == nil || !*p.MultipartUpload {
		t.Fatalf("MultipartUpload = %v, want true", p.MultipartUpload)
	}
}

// With no public record to read, the transfer model is unknowable — the
// probe must say nothing rather than guess either way.
func TestProbe_MultipartInconclusiveWithoutPublicRecords(t *testing.T) {
	c, _ := newClient(t)
	p, err := c.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if p.MultipartUpload != nil {
		t.Errorf("MultipartUpload = %v, want nil (inconclusive)", *p.MultipartUpload)
	}
}

// Nothing in the API declares which transfer types an instance registered,
// so a rejected multipart registration is not fatal: no bytes have been
// read yet, and the single-PUT path still works (D55).
func TestUploadFile_FallsBackWhenMultipartUnsupported(t *testing.T) {
	srv := fakeinvenio.New(testToken)
	t.Cleanup(srv.Close)
	srv.RejectMultipart = true
	c, err := invenio.New(srv.URL(), testToken,
		invenio.WithMultipartThreshold(16), invenio.WithPartSize(10))
	if err != nil {
		t.Fatalf("invenio.New: %v", err)
	}
	ctx := context.Background()
	id, err := c.CreateDraft(ctx, meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	content := []byte("0123456789abcdefghij01234")
	fi := mustUpload(t, c, id, "big.bin", content)
	if fi.Checksum != md5of(content) {
		t.Errorf("checksum = %v, want %v", fi.Checksum, md5of(content))
	}
	if n := countRequests(srv, "PUT", "/big.bin/content"); n != 1 {
		t.Errorf("single-PUT uploads = %d, want 1 after the fallback", n)
	}
}

// A URL that does not answer like InvenioRDM fails the probe (the Ping
// contract `remote add` relies on).
func TestProbe_RejectsNonInvenioInstance(t *testing.T) {
	c, srv := newClient(t)
	srv.Close()
	if _, err := c.Probe(context.Background()); err == nil {
		t.Fatal("Probe against a dead instance must fail")
	}
}
