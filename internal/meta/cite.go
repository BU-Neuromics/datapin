package meta

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BU-Neuromics/datapin/internal/manifest"
)

// CitationFormat selects the DOI content-negotiation representation.
type CitationFormat string

const (
	FormatBibTeX CitationFormat = "bibtex"
	FormatAPA    CitationFormat = "apa"
)

// Citation fetches a formatted citation for a DOI via content negotiation
// against the resolver (https://doi.org unless baseURL overrides it —
// DataCite serves BibTeX and styled bibliography entries directly).
func Citation(ctx context.Context, baseURL, doi string, format CitationFormat) (string, error) {
	if baseURL == "" {
		baseURL = "https://doi.org"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(baseURL, "/")+"/"+doi, nil)
	if err != nil {
		return "", err
	}
	switch format {
	case FormatBibTeX:
		req.Header.Set("Accept", "application/x-bibtex")
	default:
		req.Header.Set("Accept", "text/x-bibliography; style=apa")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", doi, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("resolving %s: HTTP %d (sandbox DOIs with prefix 10.5072 never resolve)", doi, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// LocalCitation renders a citation from manifest metadata alone — the
// fallback when the DOI does not resolve (sandbox) or the network is off.
func LocalCitation(ds *manifest.Dataset, year string) string {
	var authors []string
	for _, c := range ds.Metadata.Creators {
		authors = append(authors, c.Name)
	}
	return fmt.Sprintf("%s (%s). %s (Version %d) [Data set]. https://doi.org/%s",
		strings.Join(authors, "; "), year, ds.Metadata.Title, ds.Version, ds.VersionDOI)
}
