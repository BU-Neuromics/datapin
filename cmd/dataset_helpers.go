package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/backend/dataverse"
	"github.com/BU-Neuromics/datapin/internal/backend/figshare"
	"github.com/BU-Neuromics/datapin/internal/backend/invenio"
	"github.com/BU-Neuromics/datapin/internal/config"
	"github.com/BU-Neuromics/datapin/internal/log"
	"github.com/BU-Neuromics/datapin/internal/manifest"
	"github.com/BU-Neuromics/datapin/internal/meta"
)

// resolveArchive returns a connected backend for the dataset's archive
// remote. The remote must be configured (datapin remote add); the token
// comes from the per-remote ladder and may be empty (anonymous reads).
func resolveArchive(ds *manifest.Dataset, m *manifest.Manifest) (backend.Backend, config.Remote, error) {
	name := ds.ResolveArchive(m.Project.DefaultArchive)
	if name == "" {
		return nil, config.Remote{}, fmt.Errorf(
			"dataset %q has no archive remote — set archive = \"<name>\" on the dataset or default_archive under [project], then: datapin remote add <url> --name <name>",
			ds.Slug)
	}
	r, ok := config.GetRemote(name)
	if !ok {
		return nil, config.Remote{}, fmt.Errorf(
			"archive remote %q is not configured — add it with: datapin remote add <url> --name %s", name, name)
	}
	bk, err := newArchiveBackend(r, config.LoadRemoteToken(name))
	if err != nil {
		return nil, config.Remote{}, err
	}
	return bk, r, nil
}

// pingable is what `remote add` probes; every archive driver implements it.
type pingable interface {
	Ping(ctx context.Context) error
}

// newArchiveBackend constructs the driver for a configured remote's kind —
// the one registry both resolveArchive and `remote add` use.
func newArchiveBackend(r config.Remote, token string) (backend.Backend, error) {
	switch r.Kind {
	case "invenio":
		return invenio.New(r.URL, token)
	case "figshare":
		return figshare.New(r.URL, token)
	case "dataverse":
		return dataverse.New(r.URL, token)
	default:
		return nil, fmt.Errorf("remote %q has unsupported kind %q (supported: invenio, figshare, dataverse)", r.Name, r.Kind)
	}
}

// datasetLocalState computes each tracked file's MD5 and size. Missing
// files are returned separately — whether that is fatal depends on the
// command (publish refuses; status reports MISSING).
func datasetLocalState(repoRoot string, ds *manifest.Dataset) (md5s map[string]string, sizes map[string]int64, missing []string, err error) {
	md5s = make(map[string]string, len(ds.Files))
	sizes = make(map[string]int64, len(ds.Files))
	for _, f := range ds.Files {
		p := filepath.Join(repoRoot, f.Local)
		info, statErr := os.Stat(p)
		if statErr != nil {
			missing = append(missing, f.Local)
			continue
		}
		sum, md5Err := computeLocalMD5(p)
		if md5Err != nil {
			return nil, nil, nil, fmt.Errorf("hashing %s: %w", f.Local, md5Err)
		}
		md5s[f.Local] = sum
		sizes[f.Local] = info.Size()
	}
	return md5s, sizes, missing, nil
}

// remoteDatasetState fetches the record and the latest version's file
// set. Returns (record, key→md5, latestVersionNumber 1-based, error);
// a dataset with Record=="" short-circuits to (zero, nil, 0, nil).
func remoteDatasetState(ctx context.Context, bk backend.Backend, ds *manifest.Dataset) (backend.Record, map[string]string, int, error) {
	if ds.Record == "" {
		return backend.Record{}, nil, 0, nil
	}
	rec, err := bk.GetRecord(ctx, backend.RecordID(ds.Record))
	if err != nil {
		return backend.Record{}, nil, 0, fmt.Errorf("fetching record %s for dataset %q: %w", ds.Record, ds.Slug, err)
	}
	latestID := rec.ID
	latestNum := 0
	for _, v := range rec.Versions {
		if v.Index+1 > latestNum {
			latestNum = v.Index + 1
			latestID = v.ID
		}
	}
	files, err := bk.ListFiles(ctx, latestID)
	if err != nil {
		return rec, nil, latestNum, fmt.Errorf("listing files of %s: %w", latestID, err)
	}
	remote := make(map[string]string, len(files))
	for _, f := range files {
		remote[f.Key] = f.Checksum.Hex
	}
	return rec, remote, latestNum, nil
}

// datasetBackendMetadata renders the manifest metadata block for the
// backend, applying publish-time defaults: publisher "Zenodo" (D23/D20),
// resource type "dataset", publication date today.
func datasetBackendMetadata(ds *manifest.Dataset) backend.Metadata {
	md := ds.Metadata
	out := backend.Metadata{
		Title:           md.Title,
		Description:     md.Description,
		PublicationDate: time.Now().Format("2006-01-02"),
		Publisher:       md.Publisher,
		License:         md.License,
		Keywords:        md.Keywords,
		ResourceType:    md.ResourceType,
		ContactEmail:    md.ContactEmail,
	}
	if out.Publisher == "" {
		out.Publisher = "Zenodo"
	}
	if out.ResourceType == "" {
		out.ResourceType = "dataset"
	}
	for _, c := range md.Creators {
		family, given := splitCreatorName(c.Name)
		out.Creators = append(out.Creators, backend.Creator{
			FamilyName: family, GivenName: given,
			ORCID: c.ORCID, Affiliation: c.Affiliation,
		})
	}
	return out
}

// splitCreatorName splits the manifest's "Family, Given" display form.
// A name with no comma is all family name (mononyms, organizations).
func splitCreatorName(name string) (family, given string) {
	if f, g, ok := strings.Cut(name, ","); ok {
		return strings.TrimSpace(f), strings.TrimSpace(g)
	}
	return name, ""
}

// publishPreflight enforces metadata completeness at the publish boundary
// (plan §4.7 — only there): meta.Check errors block the publish, warnings
// are surfaced and the publish proceeds.
func publishPreflight(ds *manifest.Dataset) error {
	if len(ds.Files) == 0 {
		return fmt.Errorf("dataset %q has no files — add some under [[datasets.files]]", ds.Slug)
	}
	// A missing license is only a warning while drafting (`check`), but
	// publishing grants rights permanently: without an explicit choice the
	// backend applies its own default license on the user's behalf (D37).
	if strings.TrimSpace(ds.Metadata.License) == "" {
		return fmt.Errorf("dataset %q has no license — publishing requires an explicit license choice.\n"+
			"Set license in [datasets.metadata] (an SPDX id): CC0-1.0 dedicates the data to the public domain\n"+
			"(the common choice for research data), CC-BY-4.0 requires attribution.\n"+
			"'datapin check %s' validates the id", ds.Slug, ds.Slug)
	}
	issues := meta.Check(ds.Metadata)
	for _, i := range issues {
		if i.Severity == meta.Warning {
			log.Warnf("dataset %q metadata: %s: %s", ds.Slug, i.Field, i.Message)
		}
	}
	if meta.HasErrors(issues) {
		var msgs []string
		for _, i := range issues {
			if i.Severity == meta.Error {
				msgs = append(msgs, fmt.Sprintf("%s: %s", i.Field, i.Message))
			}
		}
		return fmt.Errorf("metadata is not publish-ready:\n  %s\nrun 'datapin check %s' for details",
			strings.Join(msgs, "\n  "), ds.Slug)
	}
	return nil
}
