package dataverse_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

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
