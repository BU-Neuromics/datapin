package sftpws_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net"
	"testing"

	"github.com/pkg/sftp"

	"github.com/BU-Neuromics/datapin/internal/workspace"
	"github.com/BU-Neuromics/datapin/internal/workspace/sftpws"
)

// newStore runs a real sftp server in-process over a pipe — the whole
// protocol without ssh — rooted at a temp dir.
func newStore(t *testing.T) *sftpws.Store {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	server, err := sftp.NewServer(serverConn)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve() }()
	t.Cleanup(func() { _ = server.Close() })

	client, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return sftpws.NewFromClient(client, t.TempDir())
}

func md5hex(b []byte) string {
	s := md5.Sum(b)
	return hex.EncodeToString(s[:])
}

// The journal scheme over a real SFTP round trip: push, versions,
// revert — the same semantics the localdir tests pin down.
func TestJournalOverSFTP(t *testing.T) {
	ws := workspace.New(newStore(t))
	ctx := context.Background()

	pushBytes := func(content []byte) workspace.Event {
		t.Helper()
		ev, err := ws.Push(ctx, "results/data.csv", bytes.NewReader(content), int64(len(content)), md5hex(content), "tester")
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		return ev
	}
	pushBytes([]byte("v1"))
	pushBytes([]byte("v2 changed"))

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

	if _, err := ws.Revert(ctx, "results/data.csv", 1, "tester", "rollback"); err != nil {
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

func TestStoreBasics(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	if err := s.Put(ctx, "sub/dir/file.txt", bytes.NewReader([]byte("hello")), 5); err != nil {
		t.Fatalf("Put: %v", err)
	}
	info, exists, err := s.Stat(ctx, "sub/dir/file.txt")
	if err != nil || !exists || info.Size != 5 {
		t.Fatalf("Stat = %+v/%v/%v", info, exists, err)
	}
	var buf bytes.Buffer
	if err := s.Get(ctx, "sub/dir/file.txt", &buf); err != nil || buf.String() != "hello" {
		t.Fatalf("Get = %q, %v", buf.String(), err)
	}
	if err := s.Copy(ctx, "sub/dir/file.txt", "copied.txt"); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	objs, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 {
		t.Errorf("List = %v", objs)
	}
	if err := s.Delete(ctx, "copied.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, exists, _ := s.Stat(ctx, "copied.txt"); exists {
		t.Error("copied.txt survived Delete")
	}
	// Overwrite through the tmp+rename path.
	if err := s.Put(ctx, "sub/dir/file.txt", bytes.NewReader([]byte("rewritten")), 9); err != nil {
		t.Fatalf("overwrite Put: %v", err)
	}
	buf.Reset()
	_ = s.Get(ctx, "sub/dir/file.txt", &buf)
	if buf.String() != "rewritten" {
		t.Errorf("overwrite = %q", buf.String())
	}
}

var _ io.Writer = (*bytes.Buffer)(nil)
