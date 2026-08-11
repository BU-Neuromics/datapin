package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/client"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/resolver"
)

func mkfile(name string) client.FileItem {
	fi := client.FileItem{}
	fi.Attributes.Name = name
	fi.Attributes.Kind = "file"
	return fi
}

func TestFindFreeName(t *testing.T) {
	cases := []struct {
		name     string
		siblings []string
		want     string
	}{
		{"data.csv", nil, "data_1.csv"},
		{"data.csv", []string{"data.csv"}, "data_1.csv"},
		{"data.csv", []string{"data.csv", "data_1.csv"}, "data_2.csv"},
		{"data.csv", []string{"data.csv", "data_1.csv", "data_2.csv"}, "data_3.csv"},
		{"noext", []string{"noext"}, "noext_1"},
		{"archive.tar.gz", []string{"archive.tar.gz"}, "archive.tar_1.gz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sibs []client.FileItem
			for _, s := range tc.siblings {
				sibs = append(sibs, mkfile(s))
			}
			if got := findFreeName(tc.name, sibs); got != tc.want {
				t.Errorf("findFreeName(%q, %v) = %q, want %q", tc.name, tc.siblings, got, tc.want)
			}
		})
	}
}

func TestPlanUpload(t *testing.T) {
	existing := mkfile("data.csv")
	existing.Links.Upload = "https://files.osf.io/upload/existing"
	siblings := []client.FileItem{existing}
	// The parent folder's ID-based upload base (as the metadata API hands it over).
	base := "https://files.osf.io/v1/resources/abc12/providers/osfstorage/5fd0e/"

	t.Run("new file", func(t *testing.T) {
		p, err := planUpload("skip", nil, base, "new.csv", nil)
		if err != nil {
			t.Fatal(err)
		}
		if p.action != "upload" || p.name != "new.csv" {
			t.Errorf("got %+v", p)
		}
		if p.url != client.AppendUploadName(base, "new.csv") {
			t.Errorf("url = %q", p.url)
		}
	})

	t.Run("skip existing", func(t *testing.T) {
		p, err := planUpload("skip", &existing, base, "data.csv", siblings)
		if err != nil {
			t.Fatal(err)
		}
		if p.action != "skip" || p.url != "" {
			t.Errorf("got %+v, want skip with empty url", p)
		}
	})

	t.Run("overwrite existing", func(t *testing.T) {
		p, err := planUpload("overwrite", &existing, base, "data.csv", siblings)
		if err != nil {
			t.Fatal(err)
		}
		if p.action != "overwrite" || p.url != existing.Links.Upload {
			t.Errorf("got %+v, want overwrite using existing upload link", p)
		}
	})

	t.Run("rename existing", func(t *testing.T) {
		p, err := planUpload("rename", &existing, base, "data.csv", siblings)
		if err != nil {
			t.Fatal(err)
		}
		if p.action != "rename" || p.name != "data_1.csv" {
			t.Errorf("got %+v, want rename to data_1.csv", p)
		}
		if p.url != client.AppendUploadName(base, "data_1.csv") {
			t.Errorf("url = %q", p.url)
		}
	})

	t.Run("unknown conflict mode", func(t *testing.T) {
		if _, err := planUpload("bogus", &existing, base, "data.csv", siblings); err == nil {
			t.Error("expected error for unknown conflict mode")
		}
	})
}

func TestDeriveUploadTarget(t *testing.T) {
	cases := []struct {
		dest       string
		src        string
		wantParent string
		wantName   string
	}{
		// Explicit filename in dest.
		{"/data/out.csv", "in.csv", "/data", "out.csv"},
		{"/out.csv", "in.csv", "/", "out.csv"},
		{"/data/sub/out.csv", "in.csv", "/data/sub", "out.csv"},
		// Trailing slash => destination is a directory, keep source filename.
		{"/data/", "in.csv", "/data", "in.csv"},
		{"/data/sub/", "report.pdf", "/data/sub", "report.pdf"},
		{"/", "in.csv", "/", "in.csv"},
		// Bare/empty dest behaves like root directory.
		{"", "in.csv", "/", "in.csv"},
	}
	for _, tc := range cases {
		t.Run(tc.dest, func(t *testing.T) {
			parent, name := deriveUploadTarget(tc.dest, tc.src)
			if parent != tc.wantParent || name != tc.wantName {
				t.Errorf("deriveUploadTarget(%q, %q) = (%q, %q), want (%q, %q)",
					tc.dest, tc.src, parent, name, tc.wantParent, tc.wantName)
			}
		})
	}
}

func TestValidateVersion(t *testing.T) {
	versions := []client.FileVersion{
		{ID: "3", Attributes: client.FileVersionAttributes{Version: 3}},
		{ID: "2", Attributes: client.FileVersionAttributes{Version: 2}},
		{ID: "1", Attributes: client.FileVersionAttributes{Version: 1}},
	}

	t.Run("valid version", func(t *testing.T) {
		if err := validateVersion(versions, 2); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	t.Run("missing version", func(t *testing.T) {
		err := validateVersion(versions, 99)
		if err == nil {
			t.Fatal("expected error for missing version")
		}
		// Error should mention the requested version and how to list them.
		msg := err.Error()
		if !contains(msg, "99") || !contains(msg, "versions") {
			t.Errorf("unhelpful error: %q", msg)
		}
	})
	t.Run("no versions at all", func(t *testing.T) {
		if err := validateVersion(nil, 1); err == nil {
			t.Fatal("expected error when no versions exist")
		}
	})
}

// contains is a tiny helper so we don't import strings just for tests.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func TestComputeLocalMD5_KnownContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.txt")
	// MD5("hello") = 5d41402abc4b2a76b9719d911017c592
	if err := os.WriteFile(p, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := computeLocalMD5(p)
	if err != nil {
		t.Fatalf("computeLocalMD5: %v", err)
	}
	const want = "5d41402abc4b2a76b9719d911017c592"
	if got != want {
		t.Errorf("MD5 = %q, want %q", got, want)
	}
}

func TestComputeLocalMD5_MissingFile(t *testing.T) {
	got, err := computeLocalMD5("/no/such/file.txt")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for missing file, got %q", got)
	}
}

func TestFileVersionsToRemote(t *testing.T) {
	versions := []client.FileVersion{
		{Attributes: client.FileVersionAttributes{Version: 3}},
		{Attributes: client.FileVersionAttributes{Version: 2}},
	}
	versions[0].Attributes.Extra.Hashes.MD5 = "aaa"
	versions[1].Attributes.Extra.Hashes.MD5 = "bbb"

	remote := fileVersionsToRemote(versions)
	if len(remote) != 2 {
		t.Fatalf("len = %d, want 2", len(remote))
	}
	if remote[0].Version != 3 || remote[0].MD5 != "aaa" {
		t.Errorf("remote[0] = %+v", remote[0])
	}
	if remote[1].Version != 2 || remote[1].MD5 != "bbb" {
		t.Errorf("remote[1] = %+v", remote[1])
	}
}

func TestFileVersionsToRemote_EmptyInput(t *testing.T) {
	remote := fileVersionsToRemote(nil)
	if remote != nil {
		t.Errorf("expected nil for nil input, got %v", remote)
	}
}

func TestLatestRemoteVersion(t *testing.T) {
	versions := []manifest.RemoteVersion{
		{Version: 3, MD5: "aaa"},
		{Version: 1, MD5: "ccc"},
		{Version: 2, MD5: "bbb"},
	}
	if got := latestRemoteVersion(versions); got != 3 {
		t.Errorf("latestRemoteVersion = %d, want 3", got)
	}
	if got := latestRemoteVersion(nil); got != 0 {
		t.Errorf("latestRemoteVersion(nil) = %d, want 0", got)
	}
}

// An explicit `datapin push <src> <project>:<path>` is no longer refused because
// of anything recorded on the tracked entry: the verb is the intent, and the
// state gates decide the rest (issue #81).
func TestPushNotRefusedForTrackedEntry(t *testing.T) {
	m := &manifest.Manifest{
		Files: []manifest.Entry{
			{Local: "data/counts.h5", Remote: "/data/counts.h5", Version: 2},
		},
	}
	idx := findEntryByLocal(m, "data/counts.h5")
	if idx < 0 {
		t.Fatal("expected entry to be found")
	}
	// Whatever its history, an AHEAD entry is publishable.
	if got := pushDecision(manifest.StateAheadOfManifest, true, false, ""); got != actionPush {
		t.Errorf("pushDecision(AHEAD) = %v, want actionPush", got)
	}
}

func TestFindEntryByLocal(t *testing.T) {
	m := &manifest.Manifest{
		Files: []manifest.Entry{
			{Local: "data/a.csv"},
			{Local: "data/b.csv"},
		},
	}
	if i := findEntryByLocal(m, "data/a.csv"); i != 0 {
		t.Errorf("findEntryByLocal(a.csv) = %d, want 0", i)
	}
	if i := findEntryByLocal(m, "data/b.csv"); i != 1 {
		t.Errorf("findEntryByLocal(b.csv) = %d, want 1", i)
	}
	if i := findEntryByLocal(m, "data/c.csv"); i != -1 {
		t.Errorf("findEntryByLocal(c.csv) = %d, want -1", i)
	}
}

func TestTrackedRemoteConflict(t *testing.T) {
	m := &manifest.Manifest{
		Project: manifest.ProjectConfig{ID: "abc12"},
		Files: []manifest.Entry{
			{Local: "ml_prediction/ccc/lca_class.csv", Remote: "/ccc/lca_class.csv", Version: 1},
		},
	}

	cases := []struct {
		name       string
		nodeID     string
		remotePath string
		destPath   string
		want       string
	}{
		{
			name:       "explicit alternate destination is a plain download, not a re-track",
			nodeID:     "abc12",
			remotePath: "/ccc/lca_class.csv",
			destPath:   "/tmp/scratch/lca_class.csv",
			want:       "ml_prediction/ccc/lca_class.csv",
		},
		{
			name:       "same destination as tracked local is not a conflict",
			nodeID:     "abc12",
			remotePath: "/ccc/lca_class.csv",
			destPath:   "ml_prediction/ccc/lca_class.csv",
			want:       "",
		},
		{
			name:       "untracked remote path is not a conflict",
			nodeID:     "abc12",
			remotePath: "/ccc/other.csv",
			destPath:   "/tmp/scratch/other.csv",
			want:       "",
		},
		{
			name:       "same remote path in a different project is not a conflict",
			nodeID:     "zzz99",
			remotePath: "/ccc/lca_class.csv",
			destPath:   "/tmp/scratch/lca_class.csv",
			want:       "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := trackedRemoteConflict(m, tc.nodeID, tc.remotePath, tc.destPath)
			if got != tc.want {
				t.Errorf("trackedRemoteConflict(%q,%q,%q) = %q, want %q",
					tc.nodeID, tc.remotePath, tc.destPath, got, tc.want)
			}
		})
	}

	// A nil manifest never reports a conflict.
	if got := trackedRemoteConflict(nil, "abc12", "/ccc/lca_class.csv", "/tmp/x"); got != "" {
		t.Errorf("trackedRemoteConflict(nil, ...) = %q, want empty", got)
	}
}

func TestBuildOSFWebURL(t *testing.T) {
	cases := []struct {
		target resolver.Target
		want   string
	}{
		{resolver.Target{NodeID: "abc12", Path: "/"}, "https://osf.io/abc12/"},
		{resolver.Target{NodeID: "abc12", Path: ""}, "https://osf.io/abc12/"},
		{resolver.Target{NodeID: "abc12", Path: "/data"}, "https://osf.io/abc12/files/osfstorage/data"},
		{resolver.Target{NodeID: "abc12", Path: "/data/file.csv"}, "https://osf.io/abc12/files/osfstorage/data/file.csv"},
	}
	for _, tc := range cases {
		got := buildOSFWebURL(tc.target)
		if got != tc.want {
			t.Errorf("buildOSFWebURL(%+v) = %q, want %q", tc.target, got, tc.want)
		}
	}
}
