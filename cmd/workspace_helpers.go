package cmd

import (
	"fmt"
	"strings"

	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/workspace"
	"github.com/BU-Neuromics/datapin/internal/workspace/localdir"
	"github.com/BU-Neuromics/datapin/internal/workspace/s3ws"
	"github.com/BU-Neuromics/datapin/internal/workspace/sftpws"
)

// workspaceKinds are the remote kinds carrying the workspace role
// (mutable, DOI-free); kind implies role (D33).
func isWorkspaceKind(kind string) bool {
	switch kind {
	case "dir", "s3", "sftp":
		return true
	}
	return false
}

// newWorkspace constructs the journal-layered workspace for a configured
// remote. Callers with a Store that needs closing (sftp) get it via the
// returned closer (nil otherwise).
func newWorkspace(r config.Remote, token string) (*workspace.Workspace, func() error, error) {
	switch r.Kind {
	case "dir":
		store, err := localdir.New(strings.TrimPrefix(r.URL, "file://"))
		if err != nil {
			return nil, nil, err
		}
		return workspace.New(store), nil, nil
	case "s3":
		store, err := s3ws.New(r.URL, token)
		if err != nil {
			return nil, nil, err
		}
		return workspace.New(store), nil, nil
	case "sftp":
		store, err := sftpws.Dial(r.URL)
		if err != nil {
			return nil, nil, err
		}
		return workspace.New(store), store.Close, nil
	default:
		return nil, nil, fmt.Errorf("remote %q has kind %q, which is not a workspace kind (dir, s3, sftp)", r.Name, r.Kind)
	}
}

// resolveWorkspace finds and connects the dataset's workspace remote.
func resolveWorkspace(ds *manifest.Dataset, m *manifest.Manifest) (*workspace.Workspace, config.Remote, func() error, error) {
	name := ds.ResolveWorkspace(m.Project.DefaultWorkspace)
	if name == "" {
		return nil, config.Remote{}, nil, fmt.Errorf(
			"dataset %q has no workspace remote — set workspace = \"<name>\" on the dataset or default_workspace under [project], then: datapin remote add <url> --name <name> --kind dir|s3|sftp",
			ds.Slug)
	}
	r, ok := config.GetRemote(name)
	if !ok {
		return nil, config.Remote{}, nil, fmt.Errorf(
			"workspace remote %q is not configured — add it with: datapin remote add <url> --name %s --kind dir|s3|sftp", name, name)
	}
	ws, closer, err := newWorkspace(r, config.LoadRemoteToken(name))
	if err != nil {
		return nil, config.Remote{}, nil, err
	}
	if closer == nil {
		closer = func() error { return nil }
	}
	return ws, r, closer, nil
}

// splitSlugKey parses "<slug>/<key...>" against the manifest's datasets.
// Returns (nil, "") when the first segment names no dataset.
func splitSlugKey(m *manifest.Manifest, arg string) (*manifest.Dataset, string) {
	slug, key, ok := strings.Cut(arg, "/")
	if !ok {
		return m.FindDataset(arg), ""
	}
	return m.FindDataset(slug), key
}
