package dataverse_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/backend/dataverse"
	"github.com/BU-Neuromics/datapin/internal/testutil/fakedataverse"
)

func sumOf(b []byte) backend.Checksum {
	s := md5.Sum(b)
	return backend.Checksum{Algo: "md5", Hex: hex.EncodeToString(s[:])}
}

// Live divergence (demo 6.11): uploading a tabular file (CSV/TSV/…)
// starts asynchronous ingest, which locks the dataset (publish 403s:
// "This dataset is locked. Reason: Ingest.") — and rewrites the file
// (data.csv → data.tab, new checksums), breaking datapin's byte-fidelity
// contract. The driver must upload with "tabIngest": false; the fake's
// ingest lock never clears, so waiting it out cannot pass this test.
func TestUploadCSV_DisablesIngestSoPublishSucceeds(t *testing.T) {
	c, _ := newLicenseClient(t)
	ctx := context.Background()
	id, err := c.CreateDraft(ctx, licenseMeta("CC0-1.0"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	content := []byte("x,y\n1,2\n")
	if _, err := c.UploadFile(ctx, id, "tables/data.csv", bytes.NewReader(content), int64(len(content)), sumOf(content)); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if _, err := c.Publish(ctx, id); err != nil {
		t.Fatalf("Publish after CSV upload: %v", err)
	}
}

// Locks other than ingest (e.g. finalizePublication after a previous
// release) clear on their own — Publish waits for them instead of
// failing on the first 403.
func TestPublish_WaitsForClearingLock(t *testing.T) {
	srv := fakedataverse.New(testToken)
	t.Cleanup(srv.Close)
	c, err := dataverse.New(srv.URL(), testToken, dataverse.WithSleep(func(context.Context) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := c.CreateDraft(ctx, licenseMeta("CC0-1.0"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	content := []byte("plain bytes")
	if _, err := c.UploadFile(ctx, id, "data.txt", bytes.NewReader(content), int64(len(content)), sumOf(content)); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	srv.SetLock(string(id), "finalizePublication", 3)
	if _, err := c.Publish(ctx, id); err != nil {
		t.Fatalf("Publish should have waited out the lock: %v", err)
	}
}

// A lock that never clears is a bounded, explained failure — not an
// infinite wait.
func TestPublish_LockNeverClears(t *testing.T) {
	srv := fakedataverse.New(testToken)
	t.Cleanup(srv.Close)
	c, err := dataverse.New(srv.URL(), testToken,
		dataverse.WithSleep(func(context.Context) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, err := c.CreateDraft(ctx, licenseMeta("CC0-1.0"))
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	content := []byte("plain bytes")
	if _, err := c.UploadFile(ctx, id, "data.txt", bytes.NewReader(content), int64(len(content)), sumOf(content)); err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	srv.SetLock(string(id), "Ingest", -1)
	_, err = c.Publish(ctx, id)
	if err == nil || !strings.Contains(err.Error(), "locked") || !strings.Contains(err.Error(), "Ingest") {
		t.Fatalf("err = %v, want a bounded locked-dataset error naming the lock", err)
	}
}
