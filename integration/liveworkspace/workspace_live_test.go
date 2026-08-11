//go:build live

// Package liveworkspace drives the full workspace CLI surface — remote
// add (with its connectivity probe), push, idempotent re-push, pull
// --workspace, versions, revert, gc — end to end against REAL protocol
// servers: a real S3 (MinIO in CI) and a real OpenSSH sshd. The driver
// live tests (internal/workspace/{s3ws,sftpws}) cover the Store layer;
// this tier proves the journey a user actually takes, through the built
// binary, the config/token ladder, and the manifest.
//
// Each test skips unless its endpoint env vars are set (same variables
// as the driver live tier — see ci.yml's workspace-protocols job):
//
//	DATAPIN_TEST_S3_ENDPOINT / DATAPIN_TEST_S3_ACCESS / DATAPIN_TEST_S3_SECRET
//	DATAPIN_TEST_SFTP_URL / DATAPIN_TEST_SFTP_PASSWORD
//
// Run locally:
//
//	docker run -d -p 9000:9000 -e MINIO_ROOT_USER=datapin \
//	  -e MINIO_ROOT_PASSWORD=datapin-secret quay.io/minio/minio server /data
//	docker run -d -p 2222:22 atmoz/sftp demo:demopass:::upload
//	DATAPIN_TEST_S3_ENDPOINT=localhost:9000 DATAPIN_TEST_S3_ACCESS=datapin \
//	  DATAPIN_TEST_S3_SECRET=datapin-secret \
//	  DATAPIN_TEST_SFTP_URL=sftp://demo@localhost:2222/upload \
//	  DATAPIN_TEST_SFTP_PASSWORD=demopass \
//	  go test -tags live -count=1 -v ./integration/liveworkspace/
package liveworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

var binaryPath string

func TestMain(m *testing.M) {
	wd, _ := os.Getwd()
	repoRoot, err := filepath.Abs(filepath.Join(wd, "..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tmp, err := os.MkdirTemp("", "datapin-livews-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin := filepath.Join(tmp, "datapin")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "liveworkspace: build: %s\n", out)
		os.Exit(1)
	}
	binaryPath = bin
	os.Exit(m.Run())
}

type liveEnv struct {
	t     *testing.T
	dir   string
	extra []string // additional env for the binary (passwords etc.)
}

func newLiveEnv(t *testing.T) *liveEnv {
	return &liveEnv{t: t, dir: t.TempDir()}
}

func (e *liveEnv) run(args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = e.dir
	cmd.Env = append(os.Environ(),
		"HOME="+e.dir,
		"XDG_CONFIG_HOME="+filepath.Join(e.dir, ".config"),
		// A locked desktop keyring must never park the binary on a
		// Secret Service prompt (same hermeticity rule as integration/).
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/datapin-hermetic",
	)
	cmd.Env = append(cmd.Env, e.extra...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
	}
	return outBuf.String(), errBuf.String(), code
}

func (e *liveEnv) write(rel, content string) {
	e.t.Helper()
	p := filepath.Join(e.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		e.t.Fatal(err)
	}
}

// lifecycle drives the full user journey against the already-configured
// workspace remote "ws": push v1 → idempotent re-push → push v2 →
// pull --workspace → versions → revert --to 1 → pull → gc.
func lifecycle(t *testing.T, e *liveEnv) {
	t.Helper()
	local := "results/counts.csv"
	key := "results/counts.csv"
	v1, v2 := "gene,count\nA,1\n", "gene,count\nA,2\n"

	e.write(local, v1)
	e.write(".datapin/datapin.toml", `
schema = 2

[project]
default_workspace = "ws"

[[datasets]]
slug = "counts"
  [[datasets.files]]
  local = "results/counts.csv"
  key   = "results/counts.csv"
`)

	if stdout, stderr, code := e.run("push", "counts", "--output=json"); code != 0 || !strings.Contains(stdout, `"pushed"`) {
		t.Fatalf("push v1 (%d): %s\n%s", code, stdout, stderr)
	}
	if stdout, stderr, code := e.run("push", "counts", "--output=json"); code != 0 || !strings.Contains(stdout, `"unchanged"`) {
		t.Fatalf("re-push not idempotent (%d): %s\n%s", code, stdout, stderr)
	}

	e.write(local, v2)
	if _, stderr, code := e.run("push", "counts"); code != 0 {
		t.Fatalf("push v2: %s", stderr)
	}

	if err := os.Remove(filepath.Join(e.dir, local)); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := e.run("pull", "counts", "--workspace"); code != 0 {
		t.Fatalf("pull --workspace: %s", stderr)
	}
	if got, _ := os.ReadFile(filepath.Join(e.dir, local)); string(got) != v2 {
		t.Fatalf("pulled = %q, want v2", got)
	}

	stdout, stderr, code := e.run("versions", "counts/"+key, "--output=json")
	if code != 0 {
		t.Fatalf("versions (%d): %s", code, stderr)
	}
	var vres struct {
		Events []struct {
			Seq         int    `json:"seq"`
			Action      string `json:"action"`
			Recoverable bool   `json:"recoverable"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(stdout), &vres); err != nil {
		t.Fatalf("versions JSON: %v\n%s", err, stdout)
	}
	if len(vres.Events) != 2 || vres.Events[0].Seq != 1 || vres.Events[1].Seq != 2 ||
		!vres.Events[0].Recoverable || !vres.Events[1].Recoverable {
		t.Fatalf("events = %+v, want two recoverable pushes", vres.Events)
	}

	if _, stderr, code := e.run("revert", "counts/"+key, "--to", "1"); code != 0 {
		t.Fatalf("revert --to 1: %s", stderr)
	}
	if err := os.Remove(filepath.Join(e.dir, local)); err != nil {
		t.Fatal(err)
	}
	if _, stderr, code := e.run("pull", "counts", "--workspace"); code != 0 {
		t.Fatalf("pull after revert: %s", stderr)
	}
	if got, _ := os.ReadFile(filepath.Join(e.dir, local)); string(got) != v1 {
		t.Fatalf("after revert pulled = %q, want v1", got)
	}

	if _, stderr, code := e.run("gc", "--keep", "1"); code != 0 {
		t.Fatalf("gc: %s", stderr)
	}
}

func TestLiveWorkspaceCLI_S3(t *testing.T) {
	endpoint := os.Getenv("DATAPIN_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("DATAPIN_TEST_S3_ENDPOINT unset — skipping real-S3 CLI tier")
	}
	access, secret := os.Getenv("DATAPIN_TEST_S3_ACCESS"), os.Getenv("DATAPIN_TEST_S3_SECRET")
	if access == "" || secret == "" {
		t.Fatal("DATAPIN_TEST_S3_ACCESS / DATAPIN_TEST_S3_SECRET must be set alongside the endpoint")
	}
	ctx := context.Background()

	bucket := fmt.Sprintf("datapin-cli-%d-%d", time.Now().UnixNano(), os.Getpid())
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

	e := newLiveEnv(t)
	wsURL := fmt.Sprintf("s3://%s/%s/lab?insecure=true", endpoint, bucket)
	if _, stderr, code := e.run("remote", "add", wsURL, "--name", "ws", "--kind", "s3",
		"--token-value", access+":"+secret, "--no-keychain"); code != 0 {
		t.Fatalf("remote add --kind s3 (probe included): %s", stderr)
	}
	lifecycle(t, e)
}

func TestLiveWorkspaceCLI_SFTP(t *testing.T) {
	rawURL := os.Getenv("DATAPIN_TEST_SFTP_URL")
	if rawURL == "" {
		t.Skip("DATAPIN_TEST_SFTP_URL unset — skipping real-SSH CLI tier")
	}
	password := os.Getenv("DATAPIN_TEST_SFTP_PASSWORD")
	if password == "" {
		t.Fatal("DATAPIN_TEST_SFTP_PASSWORD must be set alongside the URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing %s: %v", rawURL, err)
	}
	port := u.Port()
	if port == "" {
		port = "22"
	}

	e := newLiveEnv(t)

	// The binary verifies host keys against $HOME/.ssh/known_hosts —
	// seed the scratch HOME with the server's real key, exactly what
	// ssh-keyscan hands a first-time user.
	sshDir := filepath.Join(e.dir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	keys, err := exec.Command("ssh-keyscan", "-p", port, u.Hostname()).Output()
	if err != nil || len(keys) == 0 {
		t.Fatalf("ssh-keyscan -p %s %s: %v", port, u.Hostname(), err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), keys, 0600); err != nil {
		t.Fatal(err)
	}
	e.extra = []string{
		"DATAPIN_SFTP_PASSWORD=" + password,
		"SSH_AUTH_SOCK=", // force the password rung of the auth ladder
	}

	// A unique base path per run keeps reruns independent.
	u.Path = fmt.Sprintf("%s/datapin-cli-%d-%d", strings.TrimSuffix(u.Path, "/"), time.Now().UnixNano(), os.Getpid())
	if _, stderr, code := e.run("remote", "add", u.String(), "--name", "ws", "--kind", "sftp", "--no-keychain"); code != 0 {
		t.Fatalf("remote add --kind sftp (probe included): %s", stderr)
	}
	lifecycle(t, e)
}
