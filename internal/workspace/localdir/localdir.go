// Package localdir is the simplest workspace Store: a directory path —
// a mounted NAS share, a scratch filesystem, a USB drive. It doubles as
// the hermetic test vehicle for the journal scheme.
package localdir

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BU-Neuromics/datapin/internal/workspace"
)

// Store implements workspace.Store over a root directory.
type Store struct {
	root string
}

// New opens (creating if needed) the root directory.
func New(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return nil, fmt.Errorf("creating workspace directory: %w", err)
	}
	return &Store{root: abs}, nil
}

// path maps a flat key onto the filesystem, refusing escapes.
func (s *Store) path(key string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(key))
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("key %q escapes the workspace root", key)
	}
	return filepath.Join(s.root, clean), nil
}

// List implements workspace.Store.
func (s *Store) List(ctx context.Context) (map[string]workspace.ObjectInfo, error) {
	out := map[string]workspace.ObjectInfo{}
	err := filepath.WalkDir(s.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == workspace.Prefix {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		sum, err := hashFile(p)
		if err != nil {
			return err
		}
		out[key] = workspace.ObjectInfo{Size: info.Size(), MD5: sum}
		return nil
	})
	return out, err
}

// ListPrefix implements workspace.PrefixLister (internal keys included).
func (s *Store) ListPrefix(ctx context.Context, prefix string) ([]string, error) {
	dir, err := s.path(prefix)
	if err != nil {
		return nil, err
	}
	var keys []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		keys = append(keys, filepath.ToSlash(rel))
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	sort.Strings(keys)
	return keys, err
}

// Stat implements workspace.Store, reporting a computed MD5 (local reads
// are cheap; lab-scale files hash fast enough, and the journal layer
// depends on knowing content addresses).
func (s *Store) Stat(ctx context.Context, key string) (workspace.ObjectInfo, bool, error) {
	p, err := s.path(key)
	if err != nil {
		return workspace.ObjectInfo{}, false, err
	}
	info, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return workspace.ObjectInfo{}, false, nil
		}
		return workspace.ObjectInfo{}, false, err
	}
	if info.IsDir() {
		return workspace.ObjectInfo{}, false, nil
	}
	sum, err := hashFile(p)
	if err != nil {
		return workspace.ObjectInfo{}, false, err
	}
	return workspace.ObjectInfo{Size: info.Size(), MD5: sum}, true, nil
}

// Get implements workspace.Store.
func (s *Store) Get(ctx context.Context, key string, w io.Writer) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

// Put implements workspace.Store atomically (temp + rename).
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".datapin.put.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Rename(name, p)
}

// Copy implements workspace.Store.
func (s *Store) Copy(ctx context.Context, src, dst string) error {
	sp, err := s.path(src)
	if err != nil {
		return err
	}
	f, err := os.Open(sp)
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
	p, err := s.path(key)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
