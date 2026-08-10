package pathutil_test

import (
	"testing"

	"github.com/BU-Neuromics/datapin/internal/pathutil"
)

// ---- FileRemotePath (push/add single file) ----

func TestFileRemotePath(t *testing.T) {
	tests := []struct {
		name       string
		localSrc   string
		remoteDest string
		want       string
	}{
		// No dest: mirror full local path as remote path (add leading /).
		{"no dest, flat file", "file.txt", "", "/file.txt"},
		{"no dest, nested file", "data/raw/file.txt", "", "/data/raw/file.txt"},
		// Dest ends with /: destination is a directory, preserve filename.
		{"dest dir, flat src", "file.txt", "data/", "/data/file.txt"},
		{"dest dir, nested src", "data/raw/file.txt", "results/", "/results/file.txt"},
		{"dest dir with leading slash", "file.txt", "/data/", "/data/file.txt"},
		// Dest does not end with /: explicit destination path.
		{"explicit dest", "file.txt", "data/renamed.txt", "/data/renamed.txt"},
		{"explicit dest with leading slash", "file.txt", "/data/out.txt", "/data/out.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathutil.FileRemotePath(tt.localSrc, tt.remoteDest)
			if got != tt.want {
				t.Errorf("FileRemotePath(%q, %q) = %q, want %q", tt.localSrc, tt.remoteDest, got, tt.want)
			}
		})
	}
}

// ---- FileLocalPath (pull single file) ----

func TestFileLocalPath(t *testing.T) {
	tests := []struct {
		name      string
		remoteSrc string
		localDest string
		want      string
	}{
		// No dest / ".": strip leading slash to mirror remote path locally.
		{"no dest", "/data/file.txt", "", "data/file.txt"},
		{"dot dest", "/data/file.txt", ".", "data/file.txt"},
		// Dest ends with /: destination is a directory, preserve filename.
		{"dest dir", "/data/file.txt", "local/", "local/file.txt"},
		{"dest dir, nested remote", "/data/raw/counts.h5", "out/", "out/counts.h5"},
		// Explicit destination path.
		{"explicit dest", "/data/file.txt", "local/renamed.txt", "local/renamed.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathutil.FileLocalPath(tt.remoteSrc, tt.localDest)
			if got != tt.want {
				t.Errorf("FileLocalPath(%q, %q) = %q, want %q", tt.remoteSrc, tt.localDest, got, tt.want)
			}
		})
	}
}

// ---- PushDirBases (push/add directory) ----

func TestPushDirBases(t *testing.T) {
	tests := []struct {
		name             string
		localSrc         string
		remoteDest       string
		srcTrailingSlash bool
		wantLocalBase    string
		wantRemoteBase   string
	}{
		// Trailing slash on src: copy CONTENTS; dir name is stripped.
		{
			name:             "trailing slash, dest dir",
			localSrc:         "local/dir",
			remoteDest:       "data/",
			srcTrailingSlash: true,
			wantLocalBase:    "local/dir/",
			wantRemoteBase:   "/data/",
		},
		// No trailing slash: copy dir itself; dir name preserved in dest.
		{
			name:             "no trailing slash, dest dir",
			localSrc:         "local/dir",
			remoteDest:       "data/",
			srcTrailingSlash: false,
			wantLocalBase:    "local/",
			wantRemoteBase:   "/data/",
		},
		// No dest (mirror): full local path becomes remote path.
		{
			name:             "trailing slash, no dest",
			localSrc:         "local/dir",
			remoteDest:       "",
			srcTrailingSlash: true,
			wantLocalBase:    "",
			wantRemoteBase:   "/",
		},
		{
			name:             "no trailing slash, no dest",
			localSrc:         "local/dir",
			remoteDest:       "",
			srcTrailingSlash: false,
			wantLocalBase:    "",
			wantRemoteBase:   "/",
		},
		// Top-level dir (no parent).
		{
			name:             "top-level dir, no trailing slash",
			localSrc:         "dir",
			remoteDest:       "data/",
			srcTrailingSlash: false,
			wantLocalBase:    "",
			wantRemoteBase:   "/data/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			localBase, remoteBase := pathutil.PushDirBases(tt.localSrc, tt.remoteDest, tt.srcTrailingSlash)
			if localBase != tt.wantLocalBase {
				t.Errorf("localBase = %q, want %q", localBase, tt.wantLocalBase)
			}
			if remoteBase != tt.wantRemoteBase {
				t.Errorf("remoteBase = %q, want %q", remoteBase, tt.wantRemoteBase)
			}
		})
	}
}

// ---- PullDirBases (pull directory) ----

func TestPullDirBases(t *testing.T) {
	tests := []struct {
		name             string
		remoteSrc        string
		localDest        string
		srcTrailingSlash bool
		wantRemoteBase   string
		wantLocalBase    string
	}{
		// Trailing slash on remote src: copy CONTENTS.
		{
			name:             "trailing slash, local dest dir",
			remoteSrc:        "/data/dir",
			localDest:        "local/",
			srcTrailingSlash: true,
			wantRemoteBase:   "/data/dir/",
			wantLocalBase:    "local/",
		},
		// No trailing slash: copy dir itself.
		{
			name:             "no trailing slash, local dest dir",
			remoteSrc:        "/data/dir",
			localDest:        "local/",
			srcTrailingSlash: false,
			wantRemoteBase:   "/data/",
			wantLocalBase:    "local/",
		},
		// No local dest (default: current dir ".").
		{
			name:             "trailing slash, no local dest",
			remoteSrc:        "/data/dir",
			localDest:        "",
			srcTrailingSlash: true,
			wantRemoteBase:   "/data/dir/",
			wantLocalBase:    "./",
		},
		{
			name:             "no trailing slash, no local dest",
			remoteSrc:        "/data/dir",
			localDest:        ".",
			srcTrailingSlash: false,
			wantRemoteBase:   "/data/",
			wantLocalBase:    "./",
		},
		// Remote src at root level.
		{
			name:             "top-level remote dir, no trailing slash",
			remoteSrc:        "/dir",
			localDest:        "local/",
			srcTrailingSlash: false,
			wantRemoteBase:   "/",
			wantLocalBase:    "local/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remoteBase, localBase := pathutil.PullDirBases(tt.remoteSrc, tt.localDest, tt.srcTrailingSlash)
			if remoteBase != tt.wantRemoteBase {
				t.Errorf("remoteBase = %q, want %q", remoteBase, tt.wantRemoteBase)
			}
			if localBase != tt.wantLocalBase {
				t.Errorf("localBase = %q, want %q", localBase, tt.wantLocalBase)
			}
		})
	}
}

// ---- MapFilePath ----

func TestMapFilePath(t *testing.T) {
	tests := []struct {
		name     string
		srcBase  string
		destBase string
		filePath string
		want     string
	}{
		// Push: local→remote
		{
			name:     "push dir contents",
			srcBase:  "local/dir/",
			destBase: "/data/",
			filePath: "local/dir/sub/file.txt",
			want:     "/data/sub/file.txt",
		},
		{
			name:     "push dir itself",
			srcBase:  "local/",
			destBase: "/data/",
			filePath: "local/dir/sub/file.txt",
			want:     "/data/dir/sub/file.txt",
		},
		{
			name:     "push mirror (no dest)",
			srcBase:  "",
			destBase: "/",
			filePath: "local/dir/file.txt",
			want:     "/local/dir/file.txt",
		},
		// Pull: remote→local
		{
			name:     "pull dir contents",
			srcBase:  "/data/dir/",
			destBase: "local/",
			filePath: "/data/dir/sub/file.txt",
			want:     "local/sub/file.txt",
		},
		{
			name:     "pull dir itself",
			srcBase:  "/data/",
			destBase: "local/",
			filePath: "/data/dir/sub/file.txt",
			want:     "local/dir/sub/file.txt",
		},
		{
			name:     "pull to current dir",
			srcBase:  "/data/",
			destBase: "./",
			filePath: "/data/dir/file.txt",
			want:     "dir/file.txt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathutil.MapFilePath(tt.srcBase, tt.destBase, tt.filePath)
			if got != tt.want {
				t.Errorf("MapFilePath(%q, %q, %q) = %q, want %q",
					tt.srcBase, tt.destBase, tt.filePath, got, tt.want)
			}
		})
	}
}
