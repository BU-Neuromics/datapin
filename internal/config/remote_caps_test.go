package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/config"
)

// Probed caps belong to the remote's config entry so runtime never
// re-probes (issue #20): AddRemote persists them and GetRemote reads them
// back.
func TestAddRemote_PersistsProbedCaps(t *testing.T) {
	setupRemotesEnv(t)
	yes := true
	r := config.Remote{
		Name: "tuwien", Kind: "invenio", URL: "https://researchdata.tuwien.ac.at",
		Caps: &config.RemoteCaps{
			ProbedAt:          "2026-08-11T00:00:00Z",
			MaxFilesPerRecord: 250,
			MaxFileSize:       25 << 30,
			MultipartUpload:   &yes,
			ResourceTypes:     []string{"dataset", "software"},
		},
	}
	if err := config.AddRemote(r); err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	got, ok := config.GetRemote("tuwien")
	if !ok {
		t.Fatal("GetRemote(tuwien) reports absence")
	}
	if got.Caps == nil {
		t.Fatal("probed caps were not persisted")
	}
	if got.Caps.MaxFilesPerRecord != 250 || got.Caps.MaxFileSize != 25<<30 {
		t.Errorf("caps = %+v, want the probed limits", got.Caps)
	}
	if got.Caps.MultipartUpload == nil || !*got.Caps.MultipartUpload {
		t.Errorf("multipart_upload = %v, want true", got.Caps.MultipartUpload)
	}
	if strings.Join(got.Caps.ResourceTypes, ",") != "dataset,software" {
		t.Errorf("resource_types = %v", got.Caps.ResourceTypes)
	}
	if got.Caps.ProbedAt != "2026-08-11T00:00:00Z" {
		t.Errorf("probed_at = %q", got.Caps.ProbedAt)
	}
}

// A remote added with --no-verify stores no caps at all: absent is how
// "use the driver default" is spelled.
func TestAddRemote_NoCapsWhenUnprobed(t *testing.T) {
	setupRemotesEnv(t)
	if err := config.AddRemote(config.Remote{Name: "z", Kind: "invenio", URL: "https://zenodo.org"}); err != nil {
		t.Fatalf("AddRemote: %v", err)
	}
	got, _ := config.GetRemote("z")
	if got.Caps != nil {
		t.Errorf("caps = %+v, want nil for an unprobed remote", got.Caps)
	}
	// The caps table must not appear in the file either — a hand-editing
	// user should not see keys datapin never learned.
	dir, err := config.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "caps") {
		t.Errorf("config.toml carries an empty caps table:\n%s", data)
	}
}

// SetRemoteCaps re-probes in place: the remote keeps its name/kind/url and
// token, only the caps table is rewritten.
func TestSetRemoteCaps_ReplacesInPlace(t *testing.T) {
	setupRemotesEnv(t)
	if err := config.AddRemote(config.Remote{Name: "z", Kind: "invenio", URL: "https://zenodo.org"}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetRemoteCaps("z", &config.RemoteCaps{MaxFilesPerRecord: 42, ResourceTypes: []string{"dataset"}}); err != nil {
		t.Fatalf("SetRemoteCaps: %v", err)
	}
	got, _ := config.GetRemote("z")
	if got.URL != "https://zenodo.org" || got.Kind != "invenio" {
		t.Errorf("remote identity changed: %+v", got)
	}
	if got.Caps == nil || got.Caps.MaxFilesPerRecord != 42 {
		t.Fatalf("caps = %+v", got.Caps)
	}
	// Re-probing replaces rather than merges.
	if err := config.SetRemoteCaps("z", &config.RemoteCaps{MaxFilesPerRecord: 7}); err != nil {
		t.Fatalf("second SetRemoteCaps: %v", err)
	}
	got, _ = config.GetRemote("z")
	if got.Caps.MaxFilesPerRecord != 7 || len(got.Caps.ResourceTypes) != 0 {
		t.Errorf("caps after re-probe = %+v, want replacement", got.Caps)
	}
	if err := config.SetRemoteCaps("nope", &config.RemoteCaps{}); err == nil {
		t.Error("SetRemoteCaps on a missing remote must error")
	}
}

func TestRemoteCaps_Apply(t *testing.T) {
	base := backend.Caps{
		MintsDOI: true, MultipartUpload: true,
		MaxFileSize: 50 << 30, MaxFilesPerRecord: 100,
	}
	no := false
	tests := []struct {
		name string
		caps *config.RemoteCaps
		want backend.Caps
	}{
		{"nil keeps the driver profile", nil, base},
		{"zero fields keep the driver profile", &config.RemoteCaps{}, base},
		{
			"probed limits override",
			&config.RemoteCaps{MaxFilesPerRecord: 250, MaxFileSize: 1 << 30},
			backend.Caps{MintsDOI: true, MultipartUpload: true, MaxFileSize: 1 << 30, MaxFilesPerRecord: 250},
		},
		{
			"multipart can be turned off",
			&config.RemoteCaps{MultipartUpload: &no},
			backend.Caps{MintsDOI: true, MultipartUpload: false, MaxFileSize: 50 << 30, MaxFilesPerRecord: 100},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.caps.Apply(base); got != tt.want {
				t.Errorf("Apply() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
