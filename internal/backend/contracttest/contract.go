// Package contracttest is the cross-adapter contract suite (plan §6): one
// set of assertions about backend.Backend semantics, run against every
// adapter (parameterized over its hermetic fake, and — where credentials
// exist — its live sandbox), so adapters cannot drift apart.
package contracttest

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
)

// Factory builds a fresh, isolated Backend per test.
type Factory func(t *testing.T) backend.Backend

func md5of(b []byte) backend.Checksum {
	s := md5.Sum(b)
	return backend.Checksum{Algo: "md5", Hex: hex.EncodeToString(s[:])}
}

func testMeta(title string) backend.Metadata {
	return backend.Metadata{
		Title:           title,
		Description:     "contract-suite dataset",
		PublicationDate: "2026-08-11",
		Publisher:       "Zenodo",
		ResourceType:    "dataset",
		License:         "CC0-1.0",
		ContactEmail:    "contract@example.edu",
		Keywords:        []string{"contract"},
		Creators: []backend.Creator{
			{FamilyName: "Suite", GivenName: "Contract"},
		},
	}
}

func upload(t *testing.T, bk backend.Backend, id backend.DraftID, key string, content []byte) backend.FileInfo {
	t.Helper()
	fi, err := bk.UploadFile(context.Background(), id, key, bytes.NewReader(content), int64(len(content)), md5of(content))
	if err != nil {
		t.Fatalf("UploadFile(%s): %v", key, err)
	}
	return fi
}

// Run executes the whole contract against the adapter.
func Run(t *testing.T, factory Factory) {
	t.Run("PublishLifecycle", func(t *testing.T) { publishLifecycle(t, factory) })
	t.Run("ChecksumRoundTrip", func(t *testing.T) { checksumRoundTrip(t, factory) })
	t.Run("UploadChecksumMismatchFails", func(t *testing.T) { checksumMismatch(t, factory) })
	t.Run("DiscardLeavesNoTrace", func(t *testing.T) { discardLeavesNoTrace(t, factory) })
	t.Run("NewVersionChain", func(t *testing.T) { newVersionChain(t, factory) })
	t.Run("NewVersionIsReentrant", func(t *testing.T) { newVersionReentrant(t, factory) })
	t.Run("PublishedVersionIsImmutable", func(t *testing.T) { publishedImmutable(t, factory) })
	t.Run("ReserveDOI", func(t *testing.T) { reserveDOI(t, factory) })
}

func publishLifecycle(t *testing.T, factory Factory) {
	bk := factory(t)
	ctx := context.Background()
	caps := bk.Capabilities()

	id, err := bk.CreateDraft(ctx, testMeta("contract lifecycle"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	content := []byte("contract lifecycle bytes\n")
	fi := upload(t, bk, id, "data.txt", content)
	if fi.Pending {
		t.Error("a completed upload must not report Pending")
	}

	res, err := bk.Publish(ctx, id)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.RecordID == "" {
		t.Fatal("publish must yield a record id")
	}
	if caps.MintsDOI && res.DOI == "" {
		t.Error("a DOI-minting backend must return the DOI from publish")
	}

	rec, err := bk.GetRecord(ctx, res.RecordID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if !rec.Published {
		t.Error("record must report published")
	}
	if len(rec.Versions) != 1 {
		t.Errorf("version chain = %+v, want exactly one entry", rec.Versions)
	} else if !rec.Versions[0].IsLatest {
		t.Error("the only version must be latest")
	}

	files, err := bk.ListFiles(ctx, res.RecordID)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0].Key != "data.txt" {
		t.Fatalf("files = %+v", files)
	}
	if files[0].Checksum != md5of(content) {
		t.Errorf("published checksum = %v, want %v", files[0].Checksum, md5of(content))
	}
}

func checksumRoundTrip(t *testing.T, factory Factory) {
	bk := factory(t)
	ctx := context.Background()

	id, err := bk.CreateDraft(ctx, testMeta("contract checksum"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	// Content whose MD5 begins with 0 — the class of bug the live Zenodo
	// tier caught (zero-stripped checksum headers).
	content := []byte("leading-zero-probe-34")
	fi := upload(t, bk, id, "zero.bin", content)
	if fi.Checksum != md5of(content) {
		t.Errorf("commit checksum = %v, want %v", fi.Checksum, md5of(content))
	}
	res, err := bk.Publish(ctx, id)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	var buf bytes.Buffer
	if err := bk.DownloadFile(ctx, res.RecordID, "zero.bin", &buf); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), content) {
		t.Errorf("download round trip corrupted: %q", buf.Bytes())
	}
}

func checksumMismatch(t *testing.T, factory Factory) {
	bk := factory(t)
	ctx := context.Background()
	id, err := bk.CreateDraft(ctx, testMeta("contract mismatch"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	wrong := backend.Checksum{Algo: "md5", Hex: strings.Repeat("0", 32)}
	if _, err := bk.UploadFile(ctx, id, "bad.bin", bytes.NewReader([]byte("payload")), 7, wrong); err == nil {
		t.Fatal("upload with a wrong expected checksum must fail")
	}
}

func discardLeavesNoTrace(t *testing.T, factory Factory) {
	bk := factory(t)
	ctx := context.Background()
	id, err := bk.CreateDraft(ctx, testMeta("contract discard"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	upload(t, bk, id, "gone.txt", []byte("ephemeral"))
	if err := bk.Discard(ctx, id); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if _, err := bk.GetRecord(ctx, backend.RecordID(id)); !backend.IsNotFound(err) {
		t.Errorf("GetRecord after discard = %v, want NotFoundError", err)
	}
}

func newVersionChain(t *testing.T, factory Factory) {
	bk := factory(t)
	ctx := context.Background()
	caps := bk.Capabilities()

	id, err := bk.CreateDraft(ctx, testMeta("contract versions"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	v1content := []byte("version one")
	upload(t, bk, id, "data.txt", v1content)
	upload(t, bk, id, "stable.txt", []byte("never changes"))
	res1, err := bk.Publish(ctx, id)
	if err != nil {
		t.Fatalf("publish v1: %v", err)
	}

	draft, err := bk.NewVersion(ctx, res1.RecordID)
	if err != nil {
		t.Fatalf("NewVersion: %v", err)
	}
	if caps.ImportsPrevious {
		if err := bk.ImportPreviousFiles(ctx, draft); err != nil {
			t.Fatalf("ImportPreviousFiles: %v", err)
		}
	}
	// Either way, the draft must now carry the previous version's files.
	files, err := bk.ListDraftFiles(ctx, draft)
	if err != nil {
		t.Fatalf("ListDraftFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("new-version draft files = %+v, want the previous 2", files)
	}

	if err := bk.DeleteDraftFile(ctx, draft, "data.txt"); err != nil {
		t.Fatalf("DeleteDraftFile: %v", err)
	}
	v2content := []byte("version two -- changed")
	upload(t, bk, draft, "data.txt", v2content)
	res2, err := bk.Publish(ctx, draft)
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	if caps.MintsDOI {
		if caps.PerVersionDOI && res2.DOI == res1.DOI {
			t.Error("v2 must mint a distinct version DOI")
		}
		if !caps.PerVersionDOI && (res2.DOI == "" || res2.DOI != res1.DOI) {
			t.Errorf("single-DOI backend: v2 DOI = %q, want the stable %q", res2.DOI, res1.DOI)
		}
		if res1.ConceptDOI != "" && res2.ConceptDOI != res1.ConceptDOI {
			t.Error("the concept DOI must be stable across versions")
		}
	}

	rec, err := bk.GetRecord(ctx, res2.RecordID)
	if err != nil {
		t.Fatalf("GetRecord v2: %v", err)
	}
	if len(rec.Versions) != 2 {
		t.Fatalf("version chain = %+v, want 2", rec.Versions)
	}
	if !rec.Versions[1].IsLatest || rec.Versions[0].IsLatest {
		t.Errorf("latest flags wrong: %+v", rec.Versions)
	}

	files, err = bk.ListFiles(ctx, res2.RecordID)
	if err != nil {
		t.Fatalf("ListFiles v2: %v", err)
	}
	byKey := map[string]backend.Checksum{}
	for _, f := range files {
		byKey[f.Key] = f.Checksum
	}
	if byKey["data.txt"] != md5of(v2content) {
		t.Errorf("v2 data.txt checksum = %v", byKey["data.txt"])
	}
	if byKey["stable.txt"] != md5of([]byte("never changes")) {
		t.Errorf("carried-over file corrupted: %v", byKey["stable.txt"])
	}
}

func newVersionReentrant(t *testing.T, factory Factory) {
	bk := factory(t)
	ctx := context.Background()
	id, err := bk.CreateDraft(ctx, testMeta("contract reentrant"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	upload(t, bk, id, "a.txt", []byte("x"))
	res, err := bk.Publish(ctx, id)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	d1, err := bk.NewVersion(ctx, res.RecordID)
	if err != nil {
		t.Fatalf("NewVersion: %v", err)
	}
	d2, err := bk.NewVersion(ctx, res.RecordID)
	if err != nil {
		t.Fatalf("NewVersion twice: %v", err)
	}
	if d1 != d2 {
		t.Errorf("NewVersion twice = %q then %q — must be re-entrant (same pending draft)", d1, d2)
	}
}

func publishedImmutable(t *testing.T, factory Factory) {
	bk := factory(t)
	ctx := context.Background()
	id, err := bk.CreateDraft(ctx, testMeta("contract immutable"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	v1 := []byte("immutable v1")
	upload(t, bk, id, "data.txt", v1)
	res1, err := bk.Publish(ctx, id)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	rec1, err := bk.GetRecord(ctx, res1.RecordID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	v1id := rec1.Versions[0].ID

	// Publish a v2 with different bytes...
	draft, err := bk.NewVersion(ctx, res1.RecordID)
	if err != nil {
		t.Fatalf("NewVersion: %v", err)
	}
	if bk.Capabilities().ImportsPrevious {
		if err := bk.ImportPreviousFiles(ctx, draft); err != nil {
			t.Fatalf("ImportPreviousFiles: %v", err)
		}
	}
	if err := bk.DeleteDraftFile(ctx, draft, "data.txt"); err != nil {
		t.Fatalf("DeleteDraftFile: %v", err)
	}
	upload(t, bk, draft, "data.txt", []byte("mutated in v2"))
	if _, err := bk.Publish(ctx, draft); err != nil {
		t.Fatalf("publish v2: %v", err)
	}

	// ...and v1 must still serve its original bytes.
	files, err := bk.ListFiles(ctx, v1id)
	if err != nil {
		t.Fatalf("ListFiles(v1): %v", err)
	}
	if len(files) != 1 || files[0].Checksum != md5of(v1) {
		t.Errorf("v1 files after v2 publish = %+v, want original checksum", files)
	}
	var buf bytes.Buffer
	if err := bk.DownloadFile(ctx, v1id, "data.txt", &buf); err != nil {
		t.Fatalf("DownloadFile(v1): %v", err)
	}
	if !bytes.Equal(buf.Bytes(), v1) {
		t.Errorf("v1 bytes changed after v2 publish: %q", buf.Bytes())
	}
}

func reserveDOI(t *testing.T, factory Factory) {
	bk := factory(t)
	if !bk.Capabilities().ReserveDOI {
		t.Skip("backend does not reserve DOIs")
	}
	ctx := context.Background()
	id, err := bk.CreateDraft(ctx, testMeta("contract reserve"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	doi, err := bk.ReserveDOI(ctx, id)
	if err != nil {
		t.Fatalf("ReserveDOI: %v", err)
	}
	if !strings.HasPrefix(doi, "10.") {
		t.Errorf("reserved DOI = %q", doi)
	}
}

// RunPathKeyRoundTrip asserts that keys containing "/" survive the
// publish → list → download round trip (hierarchical backends map them
// onto real paths or path hints; flat backends keep them as names).
func RunPathKeyRoundTrip(t *testing.T, bk backend.Backend) {
	ctx := context.Background()
	id, err := bk.CreateDraft(ctx, testMeta("contract path keys"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	content := []byte("nested bytes")
	key := "sub/dir/data.csv"
	upload(t, bk, id, key, content)

	files, err := bk.ListDraftFiles(ctx, id)
	if err != nil {
		t.Fatalf("ListDraftFiles: %v", err)
	}
	if len(files) != 1 || files[0].Key != key {
		t.Fatalf("draft files = %+v, want key %q", files, key)
	}

	res, err := bk.Publish(ctx, id)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	published, err := bk.ListFiles(ctx, res.RecordID)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(published) != 1 || published[0].Key != key {
		t.Fatalf("published files = %+v, want key %q", published, key)
	}
	var buf bytes.Buffer
	if err := bk.DownloadFile(ctx, res.RecordID, key, &buf); err != nil {
		t.Fatalf("DownloadFile(%q): %v", key, err)
	}
	if !bytes.Equal(buf.Bytes(), content) {
		t.Errorf("round trip corrupted: %q", buf.Bytes())
	}
}
