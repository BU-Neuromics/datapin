package resolver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/BU-Neuromics/datapin/internal/client"
)

// Target holds the parsed components of an OSF path argument like
// "abc12:/data/results" or "abc12/xyz34:/path".
type Target struct {
	NodeID   string // the GUID to operate on (component GUID if present)
	ParentID string // parent node GUID for component addressing (may be empty)
	Path     string // path within OSF Storage; always starts with "/"
}

// ParseTarget parses a user-supplied OSF path argument.
//
// Accepted forms:
//   - abc12                → root listing of node abc12
//   - abc12:/path/to/dir  → path within node abc12
//   - abc12/xyz34:/path   → path within component xyz34 (child of abc12)
func ParseTarget(s string) (Target, error) {
	if s == "" {
		return Target{}, fmt.Errorf("empty target")
	}

	var nodePart, path string
	if idx := strings.IndexByte(s, ':'); idx >= 0 {
		nodePart = s[:idx]
		path = s[idx+1:]
	} else {
		nodePart = s
		path = "/"
	}

	if path == "" || path == "/" {
		path = "/"
	} else if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	var t Target
	if idx := strings.IndexByte(nodePart, '/'); idx >= 0 {
		t.ParentID = nodePart[:idx]
		t.NodeID = nodePart[idx+1:]
	} else {
		t.NodeID = nodePart
	}

	if t.NodeID == "" {
		return Target{}, fmt.Errorf("missing node GUID in %q", s)
	}
	t.Path = path
	return t, nil
}

// FileLister is the subset of the OSF metadata client that the resolver needs.
// *client.OSFClient satisfies this interface; tests supply a fake.
type FileLister interface {
	ListFiles(ctx context.Context, nodeID string) ([]client.FileItem, error)
	ListFilesFromURL(ctx context.Context, url string) ([]client.FileItem, error)
}

// Resolver resolves OSF path strings to file listings using the metadata client.
type Resolver struct {
	client FileLister
}

// New creates a Resolver backed by the given file lister.
func New(c FileLister) *Resolver {
	return &Resolver{client: c}
}

// ListDir returns the file/folder entries at the given path within a node.
// Path "/" lists the storage root. Intermediate components must be folders.
// If path resolves to a single file, that file is returned as a one-element slice.
func (r *Resolver) ListDir(ctx context.Context, nodeID, path string) ([]client.FileItem, error) {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return r.client.ListFiles(ctx, nodeID)
	}

	components := strings.Split(strings.TrimLeft(path, "/"), "/")
	items, err := r.client.ListFiles(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	for i, comp := range components {
		found, ok := findItem(items, comp)
		if !ok {
			return nil, &NotFoundError{Path: "/" + strings.Join(components[:i+1], "/")}
		}

		isLast := i == len(components)-1
		if found.Attributes.Kind == "folder" {
			folderURL := found.Relationships.Files.Links.Related.Href
			if folderURL == "" {
				return nil, fmt.Errorf("folder %q has no listing URL", comp)
			}
			if isLast {
				return r.client.ListFilesFromURL(ctx, folderURL)
			}
			items, err = r.client.ListFilesFromURL(ctx, folderURL)
			if err != nil {
				return nil, err
			}
		} else {
			if !isLast {
				return nil, fmt.Errorf("not a directory: /%s", strings.Join(components[:i+1], "/"))
			}
			// Single file match
			return []client.FileItem{found}, nil
		}
	}

	return items, nil
}

// Resolve returns the FileItem at the exact path (a file or a folder),
// including its action links. Unlike ListDir, it returns the item itself
// rather than its children, which is what callers like `rm` need.
//
// The root path ("/") cannot be resolved to an item and returns an error.
// A listing failure is propagated as-is, never masked as "not found".
func (r *Resolver) Resolve(ctx context.Context, nodeID, path string) (client.FileItem, error) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return client.FileItem{}, fmt.Errorf("cannot resolve root path to an item")
	}

	idx := strings.LastIndexByte(trimmed, '/')
	parent := "/"
	name := trimmed
	if idx >= 0 {
		parent = "/" + trimmed[:idx]
		name = trimmed[idx+1:]
	}

	siblings, err := r.ListDir(ctx, nodeID, parent)
	if err != nil {
		return client.FileItem{}, err
	}
	if item, ok := findItem(siblings, name); ok {
		return item, nil
	}
	return client.FileItem{}, &NotFoundError{Path: path}
}

func findItem(items []client.FileItem, name string) (client.FileItem, bool) {
	for _, item := range items {
		if item.Attributes.Name == name {
			return item, true
		}
	}
	return client.FileItem{}, false
}

// NotFoundError reports that a path does not exist on the remote — as opposed
// to the remote being unreachable or refusing to answer.
//
// The distinction is load-bearing. A scan treats "not found" as "nothing on the
// remote to compare", which for an unpinned entry classifies NOT_PUSHED and
// makes sync upload it. If a throttled or failed request were folded into the
// same category, a transient 429 would cause datapin to re-upload files that were
// already on the remote, byte-identical (issue #86).
type NotFoundError struct {
	Path string
}

func (e *NotFoundError) Error() string { return "path not found: " + e.Path }

// IsNotFound reports whether err means "this path is not on the remote".
// Transport failures, throttling, and permission errors all report false.
func IsNotFound(err error) bool {
	var nf *NotFoundError
	return errors.As(err, &nf)
}
