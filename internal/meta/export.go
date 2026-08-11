package meta

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// doiURL renders a DOI as its resolver URL, "" for none.
func doiURL(doi string) string {
	if doi == "" {
		return ""
	}
	return "https://doi.org/" + doi
}

// Datapackage renders a dataset as a Data Package v2 datapackage.json
// (https://datapackage.org). sizes maps local path → bytes (0 when
// unknown). Standard serializer — never an invented schema (D9).
func Datapackage(ds *manifest.Dataset, sizes map[string]int64) ([]byte, error) {
	md := ds.Metadata
	dp := map[string]any{
		"$schema": "https://datapackage.org/profiles/2.0/datapackage.json",
		"name":    strings.ToLower(ds.Slug),
		"title":   md.Title,
	}
	if id := doiURL(ds.VersionDOI); id != "" {
		dp["id"] = id
	}
	if md.Description != "" {
		dp["description"] = md.Description
	}
	if ds.Version > 0 {
		dp["version"] = fmt.Sprintf("%d", ds.Version)
	}
	if len(md.Keywords) > 0 {
		dp["keywords"] = md.Keywords
	}
	if md.License != "" {
		dp["licenses"] = []any{map[string]any{
			"name": md.License,
			"path": "https://spdx.org/licenses/" + md.License + ".html",
		}}
	}
	if len(md.Creators) > 0 {
		var contributors []any
		for _, c := range md.Creators {
			entry := map[string]any{"title": c.Name, "roles": []string{"creator"}}
			if orcid := NormalizeORCID(c.ORCID); orcid != "" {
				entry["path"] = "https://orcid.org/" + orcid
			}
			if c.Affiliation != "" {
				entry["organization"] = c.Affiliation
			}
			contributors = append(contributors, entry)
		}
		dp["contributors"] = contributors
	}

	var resources []any
	for _, f := range ds.Files {
		r := map[string]any{
			"name": resourceName(f.Key),
			"path": f.Key,
		}
		if sz, ok := sizes[f.Local]; ok && sz > 0 {
			r["bytes"] = sz
		}
		if f.MD5 != "" {
			r["hash"] = "md5:" + f.MD5
		}
		resources = append(resources, r)
	}
	dp["resources"] = resources

	return json.MarshalIndent(dp, "", "  ")
}

// resourceName sanitizes a file key into a Data Package resource name
// (lowercase alphanumerics plus ._- only).
func resourceName(key string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(key) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

// ROCrate renders a dataset directory's ro-crate-metadata.json
// (RO-Crate 1.2): a metadata descriptor, the root Dataset entity, one
// File entity per key, and Person entities for ORCID-carrying creators.
func ROCrate(ds *manifest.Dataset, sizes map[string]int64) ([]byte, error) {
	md := ds.Metadata

	descriptor := map[string]any{
		"@id":        "ro-crate-metadata.json",
		"@type":      "CreativeWork",
		"conformsTo": map[string]any{"@id": "https://w3id.org/ro/crate/1.2"},
		"about":      map[string]any{"@id": "./"},
	}

	root := map[string]any{
		"@id":   "./",
		"@type": "Dataset",
		"name":  md.Title,
	}
	if md.Description != "" {
		root["description"] = md.Description
	}
	if id := doiURL(ds.VersionDOI); id != "" {
		root["identifier"] = id
	}
	if md.License != "" {
		root["license"] = map[string]any{"@id": "https://spdx.org/licenses/" + md.License + ".html"}
	}
	if len(md.Keywords) > 0 {
		root["keywords"] = strings.Join(md.Keywords, ", ")
	}

	graph := []any{descriptor}
	var hasPart, authors []any
	var persons []any
	for _, c := range md.Creators {
		if orcid := NormalizeORCID(c.ORCID); orcid != "" {
			id := "https://orcid.org/" + orcid
			person := map[string]any{"@id": id, "@type": "Person", "name": c.Name}
			if c.Affiliation != "" {
				person["affiliation"] = c.Affiliation
			}
			persons = append(persons, person)
			authors = append(authors, map[string]any{"@id": id})
		} else {
			authors = append(authors, map[string]any{"name": c.Name})
		}
	}
	if len(authors) > 0 {
		root["author"] = authors
	}

	var files []any
	for _, f := range ds.Files {
		hasPart = append(hasPart, map[string]any{"@id": f.Key})
		fe := map[string]any{"@id": f.Key, "@type": "File", "name": f.Key}
		if sz, ok := sizes[f.Local]; ok && sz > 0 {
			fe["contentSize"] = fmt.Sprintf("%d", sz)
		}
		files = append(files, fe)
	}
	if len(hasPart) > 0 {
		root["hasPart"] = hasPart
	}

	graph = append(graph, root)
	graph = append(graph, files...)
	graph = append(graph, persons...)

	crate := map[string]any{
		"@context": "https://w3id.org/ro/crate/1.2/context",
		"@graph":   graph,
	}
	return json.MarshalIndent(crate, "", "  ")
}
