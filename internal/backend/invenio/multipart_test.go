package invenio_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/backend/invenio"
	"github.com/BU-Neuromics/datapin/internal/testutil/fakeinvenio"
)

// newMultipartClient returns a client whose multipart threshold and part
// size are small enough for hermetic tests to cross.
func newMultipartClient(t *testing.T, threshold, partSize int64) (*invenio.Client, *fakeinvenio.Server) {
	t.Helper()
	srv := fakeinvenio.New(testToken)
	t.Cleanup(srv.Close)
	c, err := invenio.New(srv.URL(), testToken,
		invenio.WithMultipartThreshold(threshold), invenio.WithPartSize(partSize))
	if err != nil {
		t.Fatalf("invenio.New: %v", err)
	}
	return c, srv
}

// countRequests counts served requests whose "METHOD path" ends in suffix.
func countRequests(srv *fakeinvenio.Server, method, suffix string) int {
	n := 0
	for _, req := range srv.ListRequests() {
		if strings.HasPrefix(req, method+" ") && strings.HasSuffix(req, suffix) {
			n++
		}
	}
	return n
}

// A file at or under the threshold must keep the existing single-PUT path
// byte-for-byte: one PUT to …/content, never a part URL.
func TestUploadFile_SmallFileUsesSinglePut(t *testing.T) {
	c, srv := newMultipartClient(t, 64, 16)
	ctx := context.Background()
	id, err := c.CreateDraft(ctx, meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	content := []byte("small enough for one PUT")
	mustUpload(t, c, id, "small.txt", content)

	if n := countRequests(srv, "PUT", "/small.txt/content"); n != 1 {
		t.Errorf("single-PUT uploads to …/content = %d, want 1", n)
	}
	if n := countRequests(srv, "PUT", "/small.txt/content/1"); n != 0 {
		t.Errorf("part uploads for a small file = %d, want 0", n)
	}
}

// A file over the threshold must register with transfer type M and PUT each
// part to its part URL; the bytes must reassemble exactly.
func TestUploadFile_LargeFileUsesMultipartAndRoundTrips(t *testing.T) {
	c, srv := newMultipartClient(t, 16, 10)
	ctx := context.Background()
	id, err := c.CreateDraft(ctx, meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	content := []byte("0123456789abcdefghij01234") // 25 bytes → parts of 10, 10, 5
	fi := mustUpload(t, c, id, "big.bin", content)
	if fi.Checksum != md5of(content) {
		t.Errorf("committed checksum = %v, want %v", fi.Checksum, md5of(content))
	}
	if fi.Size != int64(len(content)) {
		t.Errorf("committed size = %d, want %d", fi.Size, len(content))
	}
	if fi.Pending {
		t.Error("a committed multipart upload must not report Pending")
	}

	for part := 1; part <= 3; part++ {
		if n := countRequests(srv, "PUT", fmt.Sprintf("/big.bin/content/%d", part)); n != 1 {
			t.Errorf("PUTs to part %d = %d, want 1", part, n)
		}
	}
	if n := countRequests(srv, "PUT", "/big.bin/content"); n != 0 {
		t.Errorf("single-PUT uploads for a multipart file = %d, want 0", n)
	}

	res, err := c.Publish(ctx, id)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	var buf bytes.Buffer
	if err := c.DownloadFile(ctx, res.RecordID, "big.bin", &buf); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), content) {
		t.Errorf("multipart round trip corrupted: got %q, want %q", buf.Bytes(), content)
	}
}

// A failed part must abort cleanly: the registered entry is deleted so no
// pending entry is left to block publish — the draft stays publishable.
func TestUploadFile_MultipartFailedPartAbortsCleanly(t *testing.T) {
	c, srv := newMultipartClient(t, 16, 10)
	srv.FailPart = map[string]int{"big.bin": 2}
	ctx := context.Background()
	id, err := c.CreateDraft(ctx, meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	content := []byte("0123456789abcdefghij01234")
	_, err = c.UploadFile(ctx, id, "big.bin", bytes.NewReader(content), int64(len(content)), md5of(content))
	if err == nil {
		t.Fatal("UploadFile with a failing part must error")
	}

	files, err := c.ListDraftFiles(ctx, id)
	if err != nil {
		t.Fatalf("ListDraftFiles: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("draft files after failed multipart upload = %+v, want none (clean abort)", files)
	}

	// The draft must still be publishable after the abort.
	mustUpload(t, c, id, "good.txt", []byte("ok"))
	if _, err := c.Publish(ctx, id); err != nil {
		t.Fatalf("Publish after aborted multipart upload: %v", err)
	}
}

// blockingReader signals its first Read and then blocks until the context
// is canceled — a deterministic mid-part hang.
type blockingReader struct {
	ctx     context.Context
	started chan struct{}
	once    sync.Once
}

func (r *blockingReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

// Cancelling the context mid-part must abort the upload promptly and still
// clean up the registered entry (cleanup must survive the cancellation).
func TestUploadFile_MultipartCancellationAbortsMidPart(t *testing.T) {
	c, _ := newMultipartClient(t, 16, 32)
	bg := context.Background()
	id, err := c.CreateDraft(bg, meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	r := &blockingReader{ctx: ctx, started: make(chan struct{})}
	go func() {
		<-r.started // first part PUT is streaming
		cancel()
	}()
	_, err = c.UploadFile(ctx, id, "big.bin", r, 64, backend.Checksum{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("UploadFile after cancel = %v, want context.Canceled", err)
	}

	files, err := c.ListDraftFiles(bg, id)
	if err != nil {
		t.Fatalf("ListDraftFiles: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("draft files after canceled multipart upload = %+v, want none", files)
	}
}

// When the multipart commit does not yield an md5 (InvenioRDM stores a
// "multipart:{etag}-{part_size}" placeholder and recomputes asynchronously),
// the driver must surface NO checksum rather than a wrong one — the s3ws
// precedent — and must not fail the upload.
func TestUploadFile_MultipartAsyncChecksumSurfacesNone(t *testing.T) {
	c, srv := newMultipartClient(t, 16, 10)
	srv.AsyncMultipartChecksum = true
	ctx := context.Background()
	id, err := c.CreateDraft(ctx, meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	content := []byte("0123456789abcdefghij01234")
	fi := mustUpload(t, c, id, "big.bin", content)
	if fi.Checksum != (backend.Checksum{}) {
		t.Errorf("checksum with async recompute pending = %v, want none", fi.Checksum)
	}
	if fi.Pending {
		t.Error("committed file must not be pending")
	}
}

// rawDo sends an authenticated raw request to the fake (for exercising the
// documented protocol shapes the driver deliberately never emits).
func rawDo(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// Committing a multipart file before every part has been uploaded must be
// rejected (the storage backend cannot complete the upload with parts
// missing) — DOCUMENTED-only modeling, see the fakeinvenio doc comment.
func TestMultipart_CommitWithMissingPartsRejected(t *testing.T) {
	c, srv := newMultipartClient(t, 16, 10)
	id, err := c.CreateDraft(context.Background(), meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	base := srv.URL() + "/api/records/" + string(id) + "/draft/files"
	if resp := rawDo(t, "POST", base, `[{"key":"parts.bin","size":20,"transfer":{"type":"M","parts":2,"part_size":10}}]`); resp.StatusCode != 201 {
		t.Fatalf("multipart register = %d, want 201", resp.StatusCode)
	}
	if resp := rawDo(t, "PUT", base+"/parts.bin/content/1", "0123456789"); resp.StatusCode != 200 {
		t.Fatalf("part 1 PUT = %d, want 200", resp.StatusCode)
	}
	if resp := rawDo(t, "POST", base+"/parts.bin/commit", ""); resp.StatusCode != 400 {
		t.Fatalf("commit with a missing part = %d, want 400", resp.StatusCode)
	}
}

// The documented provider rejects direct content PUTs on a multipart file
// ("Can not set content for multipart file, use the parts instead.").
func TestMultipart_SinglePutOnMultipartFileRejected(t *testing.T) {
	c, srv := newMultipartClient(t, 16, 10)
	id, err := c.CreateDraft(context.Background(), meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	base := srv.URL() + "/api/records/" + string(id) + "/draft/files"
	if resp := rawDo(t, "POST", base, `[{"key":"parts.bin","size":20,"transfer":{"type":"M","parts":2,"part_size":10}}]`); resp.StatusCode != 201 {
		t.Fatalf("multipart register = %d, want 201", resp.StatusCode)
	}
	if resp := rawDo(t, "PUT", base+"/parts.bin/content", "01234567890123456789"); resp.StatusCode != 400 {
		t.Fatalf("direct content PUT on a multipart file = %d, want 400", resp.StatusCode)
	}
}

// A non-final part whose length differs from the declared part_size is
// rejected (the local-storage provider validates this exactly).
func TestMultipart_WrongSizedPartRejected(t *testing.T) {
	c, srv := newMultipartClient(t, 16, 10)
	id, err := c.CreateDraft(context.Background(), meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	base := srv.URL() + "/api/records/" + string(id) + "/draft/files"
	if resp := rawDo(t, "POST", base, `[{"key":"parts.bin","size":20,"transfer":{"type":"M","parts":2,"part_size":10}}]`); resp.StatusCode != 201 {
		t.Fatalf("multipart register = %d, want 201", resp.StatusCode)
	}
	if resp := rawDo(t, "PUT", base+"/parts.bin/content/1", "short"); resp.StatusCode != 400 {
		t.Fatalf("wrong-sized non-final part = %d, want 400", resp.StatusCode)
	}
}

// Multipart registration without parts/size/part_size is rejected (the
// provider raises TransferException for each).
func TestMultipart_RegistrationRequiresPartsSizeAndPartSize(t *testing.T) {
	c, srv := newMultipartClient(t, 16, 10)
	id, err := c.CreateDraft(context.Background(), meta())
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	base := srv.URL() + "/api/records/" + string(id) + "/draft/files"
	for name, body := range map[string]string{
		"no parts":     `[{"key":"a.bin","size":20,"transfer":{"type":"M","part_size":10}}]`,
		"no size":      `[{"key":"b.bin","transfer":{"type":"M","parts":2,"part_size":10}}]`,
		"no part_size": `[{"key":"c.bin","size":20,"transfer":{"type":"M","parts":2}}]`,
	} {
		if resp := rawDo(t, "POST", base, body); resp.StatusCode != 400 {
			t.Errorf("%s: register = %d, want 400", name, resp.StatusCode)
		}
	}
}
