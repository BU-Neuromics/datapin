package output

import "testing"

func TestMigrateResultJSON(t *testing.T) {
	r := NewMigrateResult("guid", "abc12", true)
	r.Manifest = ".datapin/datapin.toml"
	r.Downloaded = append(r.Downloaded, TransferItem{Path: "data/a.csv", Size: 3})
	r.Skipped = append(r.Skipped, TransferItem{Path: "data/b.csv", Size: 4})
	r.WikiPages = append(r.WikiPages, MigrateWikiPage{Page: "home", Local: "docs/home.md"})
	r.Datasets = append(r.Datasets, MigrateDataset{Slug: "data", Files: 2})
	r.ComponentsSkipped = append(r.ComponentsSkipped, "xyz34")
	r.TODOs = append(r.TODOs, "license")

	m := roundTrip(t, r)
	if m["mode"] != "guid" || m["source"] != "abc12" || m["dry_run"] != true {
		t.Errorf("header fields = %+v", m)
	}
	if m["manifest"] != ".datapin/datapin.toml" {
		t.Errorf("manifest = %v", m["manifest"])
	}
	dl := m["downloaded"].([]any)
	if len(dl) != 1 || dl[0].(map[string]any)["path"] != "data/a.csv" {
		t.Errorf("downloaded = %v", m["downloaded"])
	}
	sk := m["skipped"].([]any)
	if len(sk) != 1 || sk[0].(map[string]any)["path"] != "data/b.csv" {
		t.Errorf("skipped = %v", m["skipped"])
	}
	wp := m["wiki_pages"].([]any)[0].(map[string]any)
	if wp["page"] != "home" || wp["local"] != "docs/home.md" {
		t.Errorf("wiki_pages = %v", m["wiki_pages"])
	}
	ds := m["datasets"].([]any)[0].(map[string]any)
	if ds["slug"] != "data" || ds["files"].(float64) != 2 {
		t.Errorf("datasets = %v", m["datasets"])
	}
	if m["components_skipped"].([]any)[0] != "xyz34" {
		t.Errorf("components_skipped = %v", m["components_skipped"])
	}
	if m["todos"].([]any)[0] != "license" {
		t.Errorf("todos = %v", m["todos"])
	}
}

// Empty slices must serialise as [] rather than null, like every other result.
func TestMigrateResultJSON_EmptySlices(t *testing.T) {
	m := roundTrip(t, NewMigrateResult("manifest", "abc12", false))
	for _, key := range []string{"downloaded", "skipped", "wiki_pages", "datasets", "todos"} {
		if _, ok := m[key].([]any); !ok {
			t.Errorf("%s = %v (%T), want []", key, m[key], m[key])
		}
	}
	if _, present := m["components_skipped"]; present {
		t.Errorf("components_skipped should be omitted when empty, got %v", m["components_skipped"])
	}
}
