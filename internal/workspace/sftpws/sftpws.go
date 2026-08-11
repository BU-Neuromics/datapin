// Package sftpws is the SFTP workspace Store (plan §4.7 priority 3:
// every cluster has SFTP; results can live on lab storage with no
// third-party service). SFTP reports no checksums, so the journal layer
// hashes objects by reading them when it needs a content address.
package sftpws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/BU-Neuromics/datapin/internal/workspace"
)

// Store implements workspace.Store over an *sftp.Client and a base path.
type Store struct {
	client *sftp.Client
	base   string
	closer io.Closer
}

// NewFromClient wraps an existing SFTP client (tests use an in-process
// server over a pipe).
func NewFromClient(client *sftp.Client, base string) *Store {
	return &Store{client: client, base: base}
}

// Dial connects per the URL sftp://user@host[:port]/base/path using the
// ssh-agent, then default key files, then DATAPIN_SFTP_PASSWORD. Host
// keys verify against ~/.ssh/known_hosts — an unknown host is an error,
// never a silent accept.
func Dial(rawURL string) (*Store, error) {
	user, host, base, err := parseURL(rawURL)
	if err != nil {
		return nil, err
	}
	var methods []ssh.AuthMethod
	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}
	home, _ := os.UserHomeDir()
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		keyPath := filepath.Join(home, ".ssh", name)
		data, err := os.ReadFile(keyPath)
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			continue // passphrase-protected keys go through the agent
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if pw := os.Getenv("DATAPIN_SFTP_PASSWORD"); pw != "" {
		methods = append(methods, ssh.Password(pw))
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("no SSH auth available for %s — run an ssh-agent, provide ~/.ssh/id_ed25519, or set DATAPIN_SFTP_PASSWORD", host)
	}
	hostKeys, err := knownhosts.New(filepath.Join(home, ".ssh", "known_hosts"))
	if err != nil {
		return nil, fmt.Errorf("reading known_hosts (SFTP host keys must be verifiable): %w", err)
	}
	conn, err := ssh.Dial("tcp", host, &ssh.ClientConfig{
		User:            user,
		Auth:            methods,
		HostKeyCallback: hostKeys,
		Timeout:         30 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", host, err)
	}
	client, err := sftp.NewClient(conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &Store{client: client, base: base, closer: conn}, nil
}

// Close releases the connection (no-op for test stores).
func (s *Store) Close() error {
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}

// parseURL splits sftp://user@host[:port]/base/path.
func parseURL(raw string) (user, hostport, base string, err error) {
	rest, ok := strings.CutPrefix(raw, "sftp://")
	if !ok {
		return "", "", "", fmt.Errorf("not an sftp:// URL: %q", raw)
	}
	if at := strings.Index(rest, "@"); at >= 0 {
		user = rest[:at]
		rest = rest[at+1:]
	} else {
		user = os.Getenv("USER")
	}
	slash := strings.Index(rest, "/")
	if slash < 0 {
		return "", "", "", fmt.Errorf("sftp URL needs a base path: sftp://user@host/path")
	}
	hostport = rest[:slash]
	base = rest[slash:]
	if !strings.Contains(hostport, ":") {
		hostport += ":22"
	}
	return user, hostport, base, nil
}

func (s *Store) path(key string) string { return path.Join(s.base, key) }

// List implements workspace.Store.
func (s *Store) List(ctx context.Context) (map[string]workspace.ObjectInfo, error) {
	out := map[string]workspace.ObjectInfo{}
	walker := s.client.Walk(s.base)
	for walker.Step() {
		if err := walker.Err(); err != nil {
			return nil, err
		}
		info := walker.Stat()
		if info == nil || info.IsDir() {
			if info != nil && info.IsDir() && path.Base(walker.Path()) == workspace.Prefix {
				walker.SkipDir()
			}
			continue
		}
		rel, err := filepath.Rel(s.base, walker.Path())
		if err != nil {
			continue
		}
		key := filepath.ToSlash(rel)
		if strings.HasPrefix(key, workspace.Prefix+"/") {
			continue
		}
		// SFTP reports no checksum; the journal layer hashes on demand.
		out[key] = workspace.ObjectInfo{Size: info.Size()}
	}
	return out, nil
}

// ListPrefix implements workspace.PrefixLister.
func (s *Store) ListPrefix(ctx context.Context, prefix string) ([]string, error) {
	root := s.path(prefix)
	var keys []string
	walker := s.client.Walk(root)
	for walker.Step() {
		if err := walker.Err(); err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, err
		}
		info := walker.Stat()
		if info == nil || info.IsDir() {
			continue
		}
		rel, err := filepath.Rel(s.base, walker.Path())
		if err != nil {
			continue
		}
		keys = append(keys, filepath.ToSlash(rel))
	}
	sort.Strings(keys)
	return keys, nil
}

// Stat implements workspace.Store (MD5 always empty — SFTP has none).
func (s *Store) Stat(ctx context.Context, key string) (workspace.ObjectInfo, bool, error) {
	info, err := s.client.Stat(s.path(key))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
			return workspace.ObjectInfo{}, false, nil
		}
		return workspace.ObjectInfo{}, false, err
	}
	if info.IsDir() {
		return workspace.ObjectInfo{}, false, nil
	}
	return workspace.ObjectInfo{Size: info.Size()}, true, nil
}

// Get implements workspace.Store.
func (s *Store) Get(ctx context.Context, key string, w io.Writer) error {
	f, err := s.client.Open(s.path(key))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

// Put implements workspace.Store with write-then-rename (readers never
// see a torn object).
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	p := s.path(key)
	if err := s.client.MkdirAll(path.Dir(p)); err != nil {
		return err
	}
	tmp := p + ".datapin-tmp"
	f, err := s.client.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = s.client.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = s.client.Remove(tmp)
		return err
	}
	// PosixRename overwrites atomically where the server supports it.
	if err := s.client.PosixRename(tmp, p); err != nil {
		_ = s.client.Remove(p)
		if err := s.client.Rename(tmp, p); err != nil {
			_ = s.client.Remove(tmp)
			return err
		}
	}
	return nil
}

// Copy implements workspace.Store (read+write — SFTP has no server-side
// copy in the base protocol).
func (s *Store) Copy(ctx context.Context, src, dst string) error {
	f, err := s.client.Open(s.path(src))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	return s.Put(ctx, dst, f, info.Size())
}

// Delete implements workspace.Store.
func (s *Store) Delete(ctx context.Context, key string) error {
	return s.client.Remove(s.path(key))
}
