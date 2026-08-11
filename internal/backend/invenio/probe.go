package invenio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/BU-Neuromics/datapin/internal/backend"
	"github.com/BU-Neuromics/datapin/internal/log"
)

// vocabPageSize is the page size used when reading a vocabulary. Invenio's
// search layer defaults to 25 and instances cap it (Zenodo: 100+ accepted);
// the pagination loop below does not depend on the value being honored.
const vocabPageSize = 100

// maxVocabPages bounds the pagination loop so a misbehaving instance cannot
// spin `remote add` forever.
const maxVocabPages = 40

// Probe derives what this InvenioRDM instance declares about itself
// (issue #20, D53/D54). It is called once, by `datapin remote add` /
// `datapin remote probe`, and its result is persisted in the remote's config
// entry — runtime never re-probes.
//
// What is genuinely probeable is small, and Probe deliberately reports
// nothing else:
//
//   - the resource-type vocabulary (GET /api/vocabularies/resourcetypes),
//     which institutional instances curate and which `datapin check` uses to
//     catch a resource_type publish would reject;
//   - that the instance answers like InvenioRDM at all (the Ping contract).
//
// Per-record file counts and size quotas are Flask configuration
// (`RDM_RECORDS_MAX_FILES_PER_INSTANCE`-style settings and per-owner quotas
// resolved server-side), exposed nowhere in the REST API; transfer-type
// support is likewise undeclared — the multipart provider is registered
// server-side and Zenodo additionally gates part PUTs (D52). Those keep the
// documented Zenodo defaults, and Probe says so in Notes instead of
// guessing. Screen-scraping the instance's HTML for its "50 GB per record"
// prose is explicitly out of scope: a limit datapin invents is worse than
// one it documents.
func (c *Client) Probe(ctx context.Context) (backend.ProbeResult, error) {
	var out backend.ProbeResult
	types, err := c.resourceTypeVocabulary(ctx)
	if err != nil {
		return out, fmt.Errorf("%s does not answer like an InvenioRDM instance: %w", c.base, err)
	}
	out.ResourceTypes = types
	log.Debugf("probe: %s serves %d resource types", c.base, len(types))

	out.Notes = append(out.Notes, fmt.Sprintf(
		"resource-type vocabulary: %d ids read from the instance", len(types)))
	out.Notes = append(out.Notes,
		"per-record file count and size limits are not exposed by the InvenioRDM API "+
			"(RDM_RECORDS_MAX_FILES_COUNT and the quota tables are server-side) — keeping the "+
			"documented defaults; override under [remotes.<name>.caps] in config.toml")

	mp, note := c.probeTransferModel(ctx)
	out.MultipartUpload = mp
	out.Notes = append(out.Notes, note)
	return out, nil
}

// probeTransferModel infers whether the multipart (`M`) transfer can exist
// on this instance. No endpoint declares an instance's registered transfer
// types, but a public record's file listing does reveal which file schema
// the instance serves: InvenioRDM ≥13 (invenio-records-resources ≥8, the
// release that added pluggable transfers) serializes a `transfer` object,
// while older instances carry only the legacy `storage_class`. So:
//
//   - no `transfer` key on a real entry → multipart definitely absent (false);
//   - `transfer` present → the transfer model exists, multipart may be
//     registered (true — a floor, not a permission: Zenodo registers `M` but
//     gates part PUTs by identity, D52, and the upload path falls back on a
//     rejected registration, D55);
//   - no public record with files to read → nothing is claimed (nil).
//
// Failures are never fatal: a closed instance that requires auth to search
// simply leaves the capability at its default.
func (c *Client) probeTransferModel(ctx context.Context) (*bool, string) {
	var search hitsJSON
	// No sort parameter: sort vocabularies differ between instances and an
	// unknown value is a 400, which would cost the signal for nothing.
	if err := c.doJSON(ctx, "GET", "/api/records?size=5", nil, &search); err != nil {
		return nil, "transfer model: the instance's record search is not readable — " +
			"leaving multipart support at the default"
	}
	for _, rec := range search.Hits.Hits {
		id := rec.ID.String()
		if id == "" {
			continue
		}
		var list struct {
			Entries []struct {
				Key      string          `json:"key"`
				Transfer json.RawMessage `json:"transfer"`
			} `json:"entries"`
		}
		if err := c.doJSON(ctx, "GET", "/api/records/"+id+"/files", nil, &list); err != nil {
			continue
		}
		for _, e := range list.Entries {
			if e.Key == "" {
				continue
			}
			if len(e.Transfer) > 0 && string(e.Transfer) != "null" {
				yes := true
				return &yes, "transfer model: the instance serves the pluggable-transfer file schema " +
					"(InvenioRDM ≥13), so multipart uploads are attempted"
			}
			no := false
			return &no, "transfer model: the instance serves the pre-v13 file schema " +
				"(no transfer object), so it has no multipart transfer — large files upload in one PUT"
		}
	}
	return nil, "transfer model: no public record with files to read — " +
		"leaving multipart support at the default"
}

// resourceTypeVocabulary reads every id from the instance's resource-type
// vocabulary, sorted and deduplicated. An instance that does not serve the
// endpoint is not an InvenioRDM instance as far as datapin is concerned.
func (c *Client) resourceTypeVocabulary(ctx context.Context) ([]string, error) {
	type vocabPage struct {
		Hits struct {
			Hits []struct {
				ID string `json:"id"`
			} `json:"hits"`
			Total int `json:"total"`
		} `json:"hits"`
		Links struct {
			Next string `json:"next"`
		} `json:"links"`
	}

	seen := map[string]bool{}
	var ids []string
	next := fmt.Sprintf("/api/vocabularies/resourcetypes?size=%d", vocabPageSize)
	for page := 0; page < maxVocabPages && next != ""; page++ {
		var vp vocabPage
		if err := c.doJSON(ctx, "GET", next, nil, &vp); err != nil {
			return nil, err
		}
		before := len(ids)
		for _, h := range vp.Hits.Hits {
			if h.ID != "" && !seen[h.ID] {
				seen[h.ID] = true
				ids = append(ids, h.ID)
			}
		}
		// Stop when a page adds nothing (an instance that ignores `page`
		// would otherwise loop until the bound) or the chain ends.
		if len(ids) == before || len(ids) >= vp.Hits.Total {
			break
		}
		next = sameOriginNext(vp.Links.Next, c.base)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("the resource-type vocabulary is empty")
	}
	sort.Strings(ids)
	return ids, nil
}

// sameOriginNext accepts a links.next URL only when it points at the same
// instance — a pagination link is not an invitation to follow a redirect
// off-host with the bearer token attached.
func sameOriginNext(next, base string) string {
	if next == "" {
		return ""
	}
	if strings.HasPrefix(next, "/") {
		return next
	}
	if !sameHost(next, base) {
		return ""
	}
	if _, err := url.Parse(next); err != nil {
		return ""
	}
	return next
}
