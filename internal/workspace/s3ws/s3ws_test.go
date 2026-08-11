package s3ws_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/BU-Neuromics/datapin/internal/workspace"
	"github.com/BU-Neuromics/datapin/internal/workspace/s3ws"
)

// newStore runs an in-process S3 (gofakes3) and connects minio to it.
func newStore(t *testing.T, prefix string) *s3ws.Store {
	t.Helper()
	faker := gofakes3.New(s3mem.New())
	ts := httptest.NewServer(faker.Server())
	t.Cleanup(ts.Close)

	u, _ := url.Parse(ts.URL)
	client, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4("KEY", "SECRET", ""),
		Secure: false,
		Region: "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.MakeBucket(context.Background(), "testbucket", minio.MakeBucketOptions{}); err != nil {
		t.Fatal(err)
	}
	return s3ws.NewFromClient(client, "testbucket", prefix)
}

func md5hex(b []byte) string {
	s := md5.Sum(b)
	return hex.EncodeToString(s[:])
}

func TestStoreBasics(t *testing.T) {
	s := newStore(t, "lab/results")
	ctx := context.Background()

	if err := s.Put(ctx, "sub/data.csv", bytes.NewReader([]byte("hello")), 5); err != nil {
		t.Fatalf("Put: %v", err)
	}
	info, exists, err := s.Stat(ctx, "sub/data.csv")
	if err != nil || !exists {
		t.Fatalf("Stat: %v %v", exists, err)
	}
	// Simple PUTs: the ETag IS the MD5 and must surface.
	if info.MD5 != md5hex([]byte("hello")) {
		t.Errorf("Stat MD5 = %q, want the ETag-derived MD5", info.MD5)
	}
	var buf bytes.Buffer
	if err := s.Get(ctx, "sub/data.csv", &buf); err != nil || buf.String() != "hello" {
		t.Fatalf("Get = %q, %v", buf.String(), err)
	}
	// Server-side copy — the D4 archive step.
	if err := s.Copy(ctx, "sub/data.csv", ".datapin/versions/sub/data.csv/"+info.MD5); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	objs, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 {
		t.Errorf("List must hide .datapin internals: %v", objs)
	}
	keys, err := s.ListPrefix(ctx, workspace.Prefix+"/versions/")
	if err != nil || len(keys) != 1 {
		t.Errorf("ListPrefix = %v, %v", keys, err)
	}
	if _, exists, _ := s.Stat(ctx, "nope.bin"); exists {
		t.Error("Stat(nope) reported existence")
	}
}

func TestJournalOverS3(t *testing.T) {
	ws := workspace.New(newStore(t, ""))
	ctx := context.Background()

	push := func(content []byte) workspace.Event {
		t.Helper()
		ev, err := ws.Push(ctx, "results/data.csv", bytes.NewReader(content), int64(len(content)), md5hex(content), "tester")
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		return ev
	}
	push([]byte("v1"))
	push([]byte("v2 -- changed"))
	push([]byte("v3 -- more"))

	events, err := ws.Versions(ctx, "results/data.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %+v", events)
	}
	for i, want := range []string{"v1", "v2 -- changed", "v3 -- more"} {
		if !events[i].Recoverable {
			t.Errorf("version %d unrecoverable", i+1)
		}
		var buf bytes.Buffer
		if err := ws.GetVersion(ctx, "results/data.csv", events[i].Seq, &buf); err != nil {
			t.Fatalf("GetVersion(%d): %v", events[i].Seq, err)
		}
		if buf.String() != want {
			t.Errorf("version %d = %q, want %q", i+1, buf.String(), want)
		}
	}

	if _, err := ws.Revert(ctx, "results/data.csv", 1, "tester", "rollback"); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	var buf bytes.Buffer
	_ = ws.Get(ctx, "results/data.csv", &buf)
	if buf.String() != "v1" {
		t.Errorf("after revert = %q", buf.String())
	}

	freed, err := ws.GC(ctx, 1)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if freed == 0 {
		t.Error("GC freed nothing")
	}
}
