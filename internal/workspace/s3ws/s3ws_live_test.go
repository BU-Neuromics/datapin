//go:build live

// Real-protocol tier: the same journal semantics the gofakes3 unit tests
// pin down, run against a real S3 server (MinIO in CI — the
// workspace-protocols job in ci.yml). gofakes3 is a Go reimplementation;
// this tier catches where a real server diverges from it. Skips unless
// DATAPIN_TEST_S3_ENDPOINT is set.
//
// Run locally:
//
//	docker run -d -p 9000:9000 -e MINIO_ROOT_USER=datapin \
//	  -e MINIO_ROOT_PASSWORD=datapin-secret quay.io/minio/minio server /data
//	DATAPIN_TEST_S3_ENDPOINT=localhost:9000 DATAPIN_TEST_S3_ACCESS=datapin \
//	  DATAPIN_TEST_S3_SECRET=datapin-secret \
//	  go test -tags live -count=1 -v ./internal/workspace/s3ws/
package s3ws_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/BU-Neuromics/datapin/internal/workspace"
	"github.com/BU-Neuromics/datapin/internal/workspace/s3ws"
)

func TestLive_JournalOverRealS3(t *testing.T) {
	endpoint := os.Getenv("DATAPIN_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("DATAPIN_TEST_S3_ENDPOINT unset — skipping real-S3 tier")
	}
	access, secret := os.Getenv("DATAPIN_TEST_S3_ACCESS"), os.Getenv("DATAPIN_TEST_S3_SECRET")
	if access == "" || secret == "" {
		t.Fatal("DATAPIN_TEST_S3_ACCESS / DATAPIN_TEST_S3_SECRET must be set alongside the endpoint")
	}
	ctx := context.Background()

	// A unique bucket per run keeps reruns independent; best-effort
	// cleanup so a healthy run leaves no residue.
	bucket := fmt.Sprintf("datapin-ci-%d-%d", time.Now().UnixNano(), os.Getpid())
	admin, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(access, secret, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("MakeBucket: %v", err)
	}
	t.Cleanup(func() {
		for obj := range admin.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true}) {
			if obj.Err == nil {
				_ = admin.RemoveObject(ctx, bucket, obj.Key, minio.RemoveObjectOptions{})
			}
		}
		_ = admin.RemoveBucket(ctx, bucket)
	})

	// The URL+token constructor is the exact path real users hit (D34):
	// endpoint-in-URL, insecure query param, "ACCESS:SECRET" token, prefix.
	store, err := s3ws.New(
		fmt.Sprintf("s3://%s/%s/lab/results?insecure=true", endpoint, bucket),
		access+":"+secret,
	)
	if err != nil {
		t.Fatalf("s3ws.New: %v", err)
	}
	journalRoundTrip(t, workspace.New(store))
}

// journalRoundTrip asserts the D4 invariant — every version datapin wrote
// is revertible — over a real remote: push twice, read back the first
// version, revert to it, and see it as current.
func journalRoundTrip(t *testing.T, ws *workspace.Workspace) {
	t.Helper()
	ctx := context.Background()

	push := func(content []byte) {
		t.Helper()
		if _, err := ws.Push(ctx, "results/data.csv", bytes.NewReader(content), int64(len(content)), md5hex(content), "live-ci"); err != nil {
			t.Fatalf("Push: %v", err)
		}
	}
	push([]byte("v1"))
	push([]byte("v2 changed"))

	events, err := ws.Versions(ctx, "results/data.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || !events[0].Recoverable || !events[1].Recoverable {
		t.Fatalf("events = %+v", events)
	}

	var buf bytes.Buffer
	if err := ws.GetVersion(ctx, "results/data.csv", 1, &buf); err != nil {
		t.Fatalf("GetVersion(1): %v", err)
	}
	if buf.String() != "v1" {
		t.Errorf("v1 bytes = %q", buf.String())
	}

	if _, err := ws.Revert(ctx, "results/data.csv", 1, "live-ci", "rollback"); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	buf.Reset()
	if err := ws.Get(ctx, "results/data.csv", &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "v1" {
		t.Errorf("after revert current = %q", buf.String())
	}
}
