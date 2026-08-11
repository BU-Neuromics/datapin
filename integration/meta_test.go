//go:build integration

package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheck_CleanAndDirty(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)

	// The fixture dataset has title+creator+license but no description or
	// keywords or ORCID: warnings only → exit 0.
	stdout, stderr, code := e.run("check", "--output=json")
	if code != 0 {
		t.Fatalf("check on clean dataset: code=%d\n%s%s", code, stdout, stderr)
	}
	var res []struct {
		Slug   string `json:"slug"`
		Issues []struct {
			Severity string `json:"severity"`
			Field    string `json:"field"`
		} `json:"issues"`
	}
	if err := json.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("check JSON: %v\n%s", err, stdout)
	}
	for _, i := range res[0].Issues {
		if i.Severity == "error" {
			t.Errorf("unexpected error: %+v", i)
		}
	}

	// Break the license and drop the creator → errors, exit 1.
	p := filepath.Join(e.dir, ".datapin", "datapin.toml")
	data, _ := os.ReadFile(p)
	bad := strings.ReplaceAll(string(data), `license = "CC0-1.0"`, `license = "CC0"`)
	bad = strings.ReplaceAll(bad, `name = "Tester, Trusty"`, `name = ""`)
	if err := os.WriteFile(p, []byte(bad), 0644); err != nil {
		t.Fatal(err)
	}
	stdout, _, code = e.run("check", "--output=json")
	if code != 1 {
		t.Fatalf("check on broken metadata must exit 1, got %d\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "CC0-1.0") {
		t.Errorf("license error should suggest CC0-1.0:\n%s", stdout)
	}

	// And publish must refuse with the same findings.
	_, stderr, code = e.run("publish", "counts", "--yes")
	if code == 0 || !strings.Contains(stderr, "publish-ready") {
		t.Fatalf("publish with broken metadata: code=%d stderr=%s", code, stderr)
	}
}

func TestExport_DatapackageAndROCrate(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)
	mustPublish(t, e, "counts")

	stdout, stderr, code := e.run("export", "counts", "--output=json")
	if code != 0 {
		t.Fatalf("export: code=%d %s", code, stderr)
	}
	if !strings.Contains(stdout, "datapackage.json") || !strings.Contains(stdout, "ro-crate-metadata.json") {
		t.Fatalf("export result: %s", stdout)
	}

	dp, err := os.ReadFile(filepath.Join(e.dir, "datapackage.json"))
	if err != nil {
		t.Fatalf("datapackage.json: %v", err)
	}
	var pkg struct {
		ID        string `json:"id"`
		Resources []struct {
			Hash string `json:"hash"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(dp, &pkg); err != nil {
		t.Fatalf("datapackage.json parse: %v", err)
	}
	if !strings.Contains(pkg.ID, "10.5072/zenodo.") {
		t.Errorf("datapackage id = %q, want the minted DOI", pkg.ID)
	}
	if len(pkg.Resources) != 2 || !strings.HasPrefix(pkg.Resources[0].Hash, "md5:") {
		t.Errorf("resources = %+v", pkg.Resources)
	}

	if _, err := os.Stat(filepath.Join(e.dir, "ro-crate-metadata.json")); err != nil {
		t.Errorf("ro-crate-metadata.json: %v", err)
	}
}

func TestCite_LocalFallbackForSandboxDOI(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)
	mustPublish(t, e, "counts")

	stdout, stderr, code := e.run("cite", "counts")
	if code != 0 {
		t.Fatalf("cite: code=%d %s", code, stderr)
	}
	if !strings.Contains(stdout, "Tester, Trusty") || !strings.Contains(stdout, "10.5072/zenodo.") {
		t.Errorf("citation = %q", stdout)
	}
	if !strings.Contains(stderr, "sandbox") {
		t.Errorf("cite should explain the local fallback for sandbox DOIs, stderr=%s", stderr)
	}
}

func TestCite_UnpublishedRefused(t *testing.T) {
	e := newInvenioEnv(t)
	e.setupDataset(t)
	_, stderr, code := e.run("cite", "counts")
	if code == 0 || !strings.Contains(stderr, "publish") {
		t.Fatalf("cite before publish: code=%d stderr=%s", code, stderr)
	}
}
