package cmd

import (
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/site"
)

func TestPagesSourceWarning(t *testing.T) {
	tests := []struct {
		name       string
		status     site.PagesStatus
		wantEmpty  bool
		wantSubstr []string
	}{
		{
			name:      "serving gh-pages root — nothing to warn about",
			status:    site.PagesStatus{Created: true, Branch: "gh-pages", Path: "/"},
			wantEmpty: true,
		},
		{
			name:       "already enabled on another branch",
			status:     site.PagesStatus{Branch: "main", Path: "/"},
			wantSubstr: []string{"serving main", "gh-pages branch (root)", "org/repo"},
		},
		{
			name:       "another branch and a subdirectory",
			status:     site.PagesStatus{Branch: "main", Path: "/docs"},
			wantSubstr: []string{"serving main /docs"},
		},
		{
			name:       "configuration unreadable — unverified, not broken",
			status:     site.PagesStatus{},
			wantSubstr: []string{"could not be read", "confirm"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pagesSourceWarning(tt.status, "org/repo")
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("want no warning, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("want a warning, got none")
			}
			for _, want := range tt.wantSubstr {
				if !strings.Contains(got, want) {
					t.Errorf("warning %q missing %q", got, want)
				}
			}
		})
	}
}
