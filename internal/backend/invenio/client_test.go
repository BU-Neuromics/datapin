package invenio_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/backend/invenio"
	"github.com/BU-Neuromics/datapin/internal/testutil/fakeinvenio"
)

const testToken = "fake-token"

func newClient(t *testing.T) (*invenio.Client, *fakeinvenio.Server) {
	t.Helper()
	srv := fakeinvenio.New(testToken)
	t.Cleanup(srv.Close)
	c, err := invenio.New(srv.URL(), testToken)
	if err != nil {
		t.Fatalf("invenio.New: %v", err)
	}
	return c, srv
}

func meta() backend.Metadata {
	return backend.Metadata{
		Title:           "test dataset",
		PublicationDate: "2026-08-10",
		Publisher:       "Zenodo",
		ResourceType:    "dataset",
		License:         "CC0-1.0",
		Keywords:        []string{"testing"},
		Creators: []backend.Creator{
			{FamilyName: "Labadorf", GivenName: "Adam", ORCID: "0000-0002-1234-5678"},
		},
	}
}

func md5of(b []byte) backend.Checksum {
	s := md5.Sum(b)
	return backend.Checksum{Algo: "md5", Hex: hex.EncodeToString(s[:])}
}

func mustUpload(t *testing.T, c *invenio.Client, id backend.DraftID, key string, content []byte) backend.FileInfo {
	t.Helper()
	fi, err := c.UploadFile(context.Background(), id, key, bytes.NewReader(content), int64(len(content)), md5of(content))
	if err != nil {
		t.Fatalf("UploadFile(%s): %v", key, err)
	}
	return fi
}

func TestPublishLifecycle(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()

	id, err := c.CreateDraft(ctx, meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	content := []byte("hello datapin\n")
	fi := mustUpload(t, c, id, "data.txt", content)
	if fi.Checksum != md5of(content) {
		t.Errorf("committed checksum = %v, want %v", fi.Checksum, md5of(content))
	}
	if fi.Pending {
		t.Error("committed file must not be pending")
	}

	res, err := c.Publish(ctx, id)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.DOI == "" || res.ConceptDOI == "" {
		t.Errorf("publish must mint DOIs, got %+v", res)
	}
	if !strings.HasPrefix(res.DOI, "10.5072/zenodo.") {
		t.Errorf("DOI = %q, want the sandbox test prefix", res.DOI)
	}

	rec, err := c.GetRecord(ctx, res.RecordID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if !rec.Published || rec.DOI != res.DOI || rec.ConceptDOI != res.ConceptDOI {
		t.Errorf("record = %+v, want published with matching DOIs", rec)
	}
	if len(rec.Versions) != 1 || !rec.Versions[0].IsLatest {
		t.Errorf("versions = %+v, want a single latest entry", rec.Versions)
	}

	files, err := c.ListFiles(ctx, res.RecordID)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0].Key != "data.txt" || files[0].Checksum != md5of(content) {
		t.Errorf("files = %+v", files)
	}

	var buf bytes.Buffer
	if err := c.DownloadFile(ctx, res.RecordID, "data.txt", &buf); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), content) {
		t.Errorf("downloaded %q, want %q", buf.Bytes(), content)
	}
}

func TestPublish_MissingPublisherIsValidationError(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()
	m := meta()
	m.Publisher = ""
	id, err := c.CreateDraft(ctx, m)
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	mustUpload(t, c, id, "a.txt", []byte("x"))

	_, err = c.Publish(ctx, id)
	var verr *backend.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("Publish error = %v, want *backend.ValidationError", err)
	}
	if _, ok := verr.Fields["metadata.publisher"]; !ok {
		t.Errorf("validation fields = %v, want metadata.publisher", verr.Fields)
	}
}

func TestPendingFileBlocksPublish_AndPreflightClears(t *testing.T) {
	c, srv := newClient(t)
	ctx := context.Background()
	id, err := c.CreateDraft(ctx, meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	mustUpload(t, c, id, "good.txt", []byte("ok"))

	// Simulate a crashed upload: register an entry directly, never commit.
	req, _ := http.NewRequest("POST", srv.URL()+"/api/records/"+string(id)+"/draft/files",
		strings.NewReader(`[{"key":"crashed.bin"}]`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 201 {
		t.Fatalf("registering pending entry: %v %v", err, resp)
	}

	_, err = c.Publish(ctx, id)
	var verr *backend.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("Publish with pending file = %v, want ValidationError", err)
	}

	// Preflight: list draft files, remove the pending one, publish clean.
	files, err := c.ListDraftFiles(ctx, id)
	if err != nil {
		t.Fatalf("ListDraftFiles: %v", err)
	}
	var pending []string
	for _, f := range files {
		if f.Pending {
			pending = append(pending, f.Key)
		}
	}
	if len(pending) != 1 || pending[0] != "crashed.bin" {
		t.Fatalf("pending = %v, want [crashed.bin]", pending)
	}
	if err := c.DeleteDraftFile(ctx, id, "crashed.bin"); err != nil {
		t.Fatalf("DeleteDraftFile: %v", err)
	}
	if _, err := c.Publish(ctx, id); err != nil {
		t.Fatalf("Publish after clearing pending: %v", err)
	}
}

func TestUploadFile_ChecksumMismatchFails(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()
	id, err := c.CreateDraft(ctx, meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	wrong := backend.Checksum{Algo: "md5", Hex: strings.Repeat("0", 32)}
	_, err = c.UploadFile(ctx, id, "a.txt", bytes.NewReader([]byte("content")), 7, wrong)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("UploadFile with wrong checksum = %v, want checksum mismatch error", err)
	}
}

func TestNewVersionFlow(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()

	id, _ := c.CreateDraft(ctx, meta())
	v1content := []byte("v1 bytes")
	mustUpload(t, c, id, "data.txt", v1content)
	res1, err := c.Publish(ctx, id)
	if err != nil {
		t.Fatalf("publish v1: %v", err)
	}

	d2, err := c.NewVersion(ctx, res1.RecordID)
	if err != nil {
		t.Fatalf("NewVersion: %v", err)
	}
	// Idempotent while the draft exists (zenodo-notes §2).
	d2again, err := c.NewVersion(ctx, res1.RecordID)
	if err != nil || d2again != d2 {
		t.Fatalf("NewVersion twice = (%v, %v), want same draft %v", d2again, err, d2)
	}

	// New-version drafts start with no files; import copies the previous set.
	if err := c.ImportPreviousFiles(ctx, d2); err != nil {
		t.Fatalf("ImportPreviousFiles: %v", err)
	}
	files, err := c.ListDraftFiles(ctx, d2)
	if err != nil || len(files) != 1 || files[0].Checksum != md5of(v1content) {
		t.Fatalf("after import: files=%+v err=%v", files, err)
	}

	// Changed-file flow: delete, re-upload, publish v2.
	if err := c.DeleteDraftFile(ctx, d2, "data.txt"); err != nil {
		t.Fatalf("DeleteDraftFile: %v", err)
	}
	v2content := []byte("v2 bytes -- changed")
	mustUpload(t, c, d2, "data.txt", v2content)
	res2, err := c.Publish(ctx, d2)
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	if res2.DOI == res1.DOI {
		t.Error("v2 must mint a new version DOI")
	}
	if res2.ConceptDOI != res1.ConceptDOI {
		t.Error("concept DOI must be stable across versions")
	}

	rec, err := c.GetRecord(ctx, res2.RecordID)
	if err != nil {
		t.Fatalf("GetRecord v2: %v", err)
	}
	if len(rec.Versions) != 2 {
		t.Fatalf("versions = %+v, want chain of 2", rec.Versions)
	}
	if !rec.Versions[1].IsLatest || rec.Versions[0].IsLatest {
		t.Errorf("latest flag misplaced: %+v", rec.Versions)
	}
}

func TestPublish_ReconcilesAfter5xx(t *testing.T) {
	c, srv := newClient(t)
	ctx := context.Background()
	id, _ := c.CreateDraft(ctx, meta())
	mustUpload(t, c, id, "a.txt", []byte("x"))

	// zenodo#2131: publish 504s while succeeding server-side. The driver
	// must reconcile by re-GET and report success (D18).
	srv.FailPublish = map[string]int{string(id): 504}
	res, err := c.Publish(ctx, id)
	if err != nil {
		t.Fatalf("Publish after 504-but-published: %v", err)
	}
	if res.DOI == "" {
		t.Errorf("reconciled publish must carry the DOI, got %+v", res)
	}

	// Regression: the publish POST must never be blind-retried on a 5xx —
	// reconcile-by-GET is the only recovery (plan §2.4). A retried publish
	// also honored the response's ever-present Retry-After (9s) and made
	// this test take 9 wall-clock seconds.
	publishPosts := 0
	for _, req := range srv.ListRequests() {
		if strings.HasSuffix(req, "/actions/publish") && strings.HasPrefix(req, "POST") {
			publishPosts++
		}
	}
	if publishPosts != 1 {
		t.Errorf("publish POSTs = %d, want exactly 1 (no blind retry)", publishPosts)
	}
}

func TestDiscardLeavesNoTrace(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()
	id, _ := c.CreateDraft(ctx, meta())
	if err := c.Discard(ctx, id); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	_, err := c.GetRecord(ctx, backend.RecordID(id))
	if !backend.IsNotFound(err) {
		t.Errorf("GetRecord after discard = %v, want NotFoundError", err)
	}
}

func TestReserveDOI(t *testing.T) {
	c, _ := newClient(t)
	ctx := context.Background()
	id, _ := c.CreateDraft(ctx, meta())
	doi, err := c.ReserveDOI(ctx, id)
	if err != nil {
		t.Fatalf("ReserveDOI: %v", err)
	}
	if !strings.HasPrefix(doi, "10.5072/zenodo.") {
		t.Errorf("reserved DOI = %q", doi)
	}
}

func TestRetryWiring_Absorbs429(t *testing.T) {
	c, srv := newClient(t)
	srv.Throttle429 = 2
	id, err := c.CreateDraft(context.Background(), meta())
	if err != nil {
		t.Fatalf("CreateDraft through 429s: %v", err)
	}
	if id == "" {
		t.Error("empty draft id")
	}
}

func TestCapabilities_ZenodoProfile(t *testing.T) {
	c, _ := newClient(t)
	caps := c.Capabilities()
	if !caps.MintsDOI || !caps.ReserveDOI || !caps.ImportsPrevious {
		t.Errorf("caps = %+v, want DOI+reserve+files-import", caps)
	}
	if caps.MaxFilesPerRecord != 100 || caps.ChecksumAlgo != "md5" {
		t.Errorf("caps = %+v, want 100-file cap and md5", caps)
	}
}

func TestPing(t *testing.T) {
	c, srv := newClient(t)
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping against a live instance: %v", err)
	}
	srv.Close()
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("Ping against a dead instance must fail")
	}
}
