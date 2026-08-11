package dataverse_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/backend/dataverse"
	"github.com/BU-Neuromics/datapin/internal/testutil/fakedataverse"
)

// Live divergence (demo 6.11): the publish POST is accepted but the
// version is not RELEASED yet — Dataverse finalizes publication
// asynchronously (DOI registration). Publish must poll until the record
// actually shows published instead of checking once and failing.
func TestPublish_WaitsForAsyncFinalization(t *testing.T) {
	srv := fakedataverse.New(testToken)
	t.Cleanup(srv.Close)
	srv.SetPublishFinalizePolls(4)
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
	res, err := c.Publish(ctx, id)
	if err != nil {
		t.Fatalf("Publish should have waited for finalization: %v", err)
	}
	if res.DOI == "" {
		t.Fatalf("result carries no DOI: %+v", res)
	}
}

// A publication that never finalizes is a bounded, explained failure —
// not a nil-wrapped "%!w(<nil>)".
func TestPublish_FinalizationNeverCompletes(t *testing.T) {
	srv := fakedataverse.New(testToken)
	t.Cleanup(srv.Close)
	srv.SetPublishFinalizePolls(-1)
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
	_, err = c.Publish(ctx, id)
	if err == nil || !strings.Contains(err.Error(), "not finished publishing") {
		t.Fatalf("err = %v, want a bounded not-finished-publishing error", err)
	}
	if strings.Contains(err.Error(), "%!w") {
		t.Fatalf("err %v wraps a nil error", err)
	}
}

// Re-uploading an existing key must replace it in place — never trigger
// Dataverse's silent duplicate rename (data.csv → data-1.csv), and never
// leave two entries. Exercises the implicit-draft fallback (the draft is
// not readable until a mutation opens it) plus the pre-delete.
func TestUploadFile_ReplacesSameKeyInImplicitDraft(t *testing.T) {
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
	v1 := []byte("x,y\n1,2\n")
	if _, err := c.UploadFile(ctx, id, "tables/data.csv", bytes.NewReader(v1), int64(len(v1)), sumOf(v1)); err != nil {
		t.Fatalf("upload v1: %v", err)
	}
	if _, err := c.Publish(ctx, id); err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	draft, err := c.NewVersion(ctx, backend.RecordID(id))
	if err != nil {
		t.Fatalf("NewVersion: %v", err)
	}
	v2 := []byte("x,y\n1,3\n")
	fi, err := c.UploadFile(ctx, draft, "tables/data.csv", bytes.NewReader(v2), int64(len(v2)), sumOf(v2))
	if err != nil {
		t.Fatalf("upload v2 over v1's key: %v", err)
	}
	if fi.Key != "tables/data.csv" || fi.Checksum != sumOf(v2) {
		t.Fatalf("v2 upload = %+v, want the same key with v2's checksum", fi)
	}
	files, err := c.ListDraftFiles(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Key != "tables/data.csv" || files[0].Checksum != sumOf(v2) {
		t.Fatalf("draft files = %+v, want exactly one replaced entry", files)
	}
}
