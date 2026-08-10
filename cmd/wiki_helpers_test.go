package cmd

import (
	"testing"

	"github.com/BU-Neuromics/datapin/internal/client"
)

func TestParseWikiTarget(t *testing.T) {
	cases := []struct {
		in          string
		defaultPage string
		wantNode    string
		wantPage    string
		wantErr     bool
	}{
		{"abc12:home", "", "abc12", "home", false},
		{"abc12:Analysis Notes", "", "abc12", "Analysis Notes", false},
		{"abc12", "home", "abc12", "home", false},
		{"abc12:", "home", "abc12", "home", false},
		{"abc12/xyz34:protocol", "", "xyz34", "protocol", false},
		{"abc12", "", "", "", true},     // page required, no default
		{"abc12:a/b", "", "", "", true}, // wiki names cannot contain "/"
		{"", "home", "", "", true},      // empty target
		{":home", "home", "", "", true}, // missing node
	}
	for _, c := range cases {
		node, page, err := parseWikiTarget(c.in, c.defaultPage)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseWikiTarget(%q, %q): expected error, got %q %q", c.in, c.defaultPage, node, page)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseWikiTarget(%q, %q): %v", c.in, c.defaultPage, err)
			continue
		}
		if node != c.wantNode || page != c.wantPage {
			t.Errorf("parseWikiTarget(%q, %q) = (%q, %q), want (%q, %q)",
				c.in, c.defaultPage, node, page, c.wantNode, c.wantPage)
		}
	}
}

func TestPageNameFromFile(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"docs/home.md", "home"},
		{"docs/Analysis Notes.md", "Analysis Notes"},
		{"README.markdown", "README"},
		{"notes.MD", "notes"},
		{"plain", "plain"},
		{"docs/nested/deep.md", "deep"},
		{"weird.txt", "weird.txt"}, // only markdown extensions are stripped
	}
	for _, c := range cases {
		if got := pageNameFromFile(c.in); got != c.want {
			t.Errorf("pageNameFromFile(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWikiWebURL(t *testing.T) {
	cases := []struct {
		node, page string
		want       string
	}{
		{"abc12", "home", "https://osf.io/abc12/wiki/home/"},
		{"abc12", "Analysis Notes", "https://osf.io/abc12/wiki/Analysis%20Notes/"},
	}
	for _, c := range cases {
		if got := wikiWebURL(c.node, c.page); got != c.want {
			t.Errorf("wikiWebURL(%q, %q) = %q, want %q", c.node, c.page, got, c.want)
		}
	}
}

func TestFindWikiPage(t *testing.T) {
	wikis := []client.Wiki{
		{ID: "w1", Attributes: client.WikiAttributes{Name: "home"}},
		{ID: "w2", Attributes: client.WikiAttributes{Name: "Analysis Notes"}},
		{ID: "w3", Attributes: client.WikiAttributes{Name: "notes"}},
		{ID: "w4", Attributes: client.WikiAttributes{Name: "Notes"}},
	}

	// Exact match wins.
	if w, ok := findWikiPage(wikis, "home"); !ok || w.ID != "w1" {
		t.Errorf("findWikiPage(home) = %v, %v", w, ok)
	}
	// Exact match beats case-insensitive candidates.
	if w, ok := findWikiPage(wikis, "notes"); !ok || w.ID != "w3" {
		t.Errorf("findWikiPage(notes) = %v, %v", w, ok)
	}
	// Unique case-insensitive fallback.
	if w, ok := findWikiPage(wikis, "analysis notes"); !ok || w.ID != "w2" {
		t.Errorf("findWikiPage(analysis notes) = %v, %v", w, ok)
	}
	// Ambiguous case-insensitive match (notes vs Notes) without exact hit: no match.
	if _, ok := findWikiPage(wikis, "nOtEs"); ok {
		t.Error("findWikiPage(nOtEs) should be ambiguous → not found")
	}
	// Absent.
	if _, ok := findWikiPage(wikis, "missing"); ok {
		t.Error("findWikiPage(missing) should not match")
	}
}

func TestIsHomeWiki(t *testing.T) {
	if !isHomeWiki("home") || !isHomeWiki("Home") || !isHomeWiki("HOME") {
		t.Error("isHomeWiki should match 'home' case-insensitively")
	}
	if isHomeWiki("homepage") {
		t.Error("isHomeWiki(homepage) should be false")
	}
}
