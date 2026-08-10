# Rebooting gosf as a general-purpose FAIR data publication tool

**Status:** proposal · **Date:** 2026-08-10 · **Author:** research sweep + plan by Claude, commissioned by @labadorf

> **Decision log (2026-08-10):** the reboot is named **`datapin`**, developed
> in a **fresh repository** seeded with gosf's commit history (branch pushed
> without tags; gosf archived with a pointer once datapin reaches parity).
> Remotes carry a **workspace vs archive role** (§4.7) so the founding
> "portable intermediate results" workflow stays first-class and DOI-free
> until the user explicitly publishes. Command examples below are written as
> `gosf`; read them as `datapin`.

This document is the outcome of a research sweep across FAIR/open-data best
practices, repository platforms and their APIs, the existing tool landscape,
and static-site strategies. It proposes a concrete architecture, CLI surface,
manifest schema, testing strategy, migration path, and phased roadmap for
pivoting gosf from an OSF client into a **multi-backend research-data
publication tool**: files on archival repositories (Zenodo first), docs on
static HTML (GitHub Pages first), the same manifest-driven, state-gated
workflow gosf has today.

---

## 1. Executive summary

- **Why pivot.** OSF is not collapsing, but it is stressed: the Center for
  Open Science publicly acknowledged 2025 funding uncertainty from U.S.
  federal cuts, suspended OSF Preprints indefinitely in August 2025, and is
  pivoting to a community-sustained model (NSF POSE). The single-nonprofit,
  grant-funded model is exactly the risk a multi-backend adapter hedges.
  Meanwhile Zenodo (CERN-backed, InvenioRDM-based) mints DataCite DOIs,
  which OSF-style mutable projects never gave us — and DOIs are the currency
  of FAIR.
- **The niche is genuinely open.** No single-binary, zero-runtime client
  exists for archival repositories. Every credible tool (frictionless-py
  portals, HERMES, pyDataverse, zenodo_client, ropensci/deposits) needs
  Python/R/Node, and *all* of them are one-shot snapshot pushers — none can
  answer "is what's on Zenodo still what I published, and which side moved?"
  gosf's L/B/R state machine + gate matrix, transplanted onto the
  draft→publish→new-version lifecycle, is novel. Three zero-star Go
  `zenodo-cli` attempts started in 2026 — the niche is being probed but
  nobody has landed it.
- **The second niche is also open.** "pkgdown for data" — manifest + DOI
  metadata → static dataset landing pages with citations, checksums, version
  history, and schema.org JSON-LD — has zero active competition (Livemark is
  dormant, PortalJS/Quilt are heavyweight server frameworks).
- **What carries over.** Most of gosf's ~22k lines of value is
  backend-agnostic: the manifest package, the `ClassifyFile` state machine,
  the gate matrix (`syncDecision`/`pushDecision`/`pullDecision`), the scan
  concurrency, retry/rate-limit machinery, the logging/output/JSON
  contracts, the three-tier test strategy, and the shipped agent skill. The
  OSF specifics are already isolated in `internal/client/` and
  `internal/resolver/`.
- **The two real conceptual shifts.** (1) Publication repositories have
  **flat file namespaces and immutable published versions** — the unit of
  sync becomes a *dataset (record)*, not a file path in a mutable tree, and
  push becomes a transaction (open draft → import → mutate → publish), not a
  per-file PUT. (2) Metadata becomes a first-class local artifact — the
  DataCite-mandatory six fields plus license/creators/ORCIDs live in the
  manifest and are linted before anything publishes.

---

## 2. Research findings (condensed)

The full agent reports informing this section covered: FAIR principles and
metadata standards; the Zenodo/InvenioRDM API; a platform comparison
(Figshare, Dataverse, Dryad, OSF, InvenioRDM, plus non-DOI backends); the
CLI-tool landscape; and static-site strategy. Key facts, with sources inline.

### 2.1 FAIR, distilled to tool requirements

The 15 FAIR sub-principles ([GO FAIR](https://www.go-fair.org/fair-principles/),
[Wilkinson et al. 2016](https://www.nature.com/articles/sdata201618)) reduce,
for a publishing CLI, to:

1. **Every published dataset ends in a persistent identifier** — in practice
   a DataCite DOI minted by the repository (F1), included *inside* the
   metadata record (F3), indexed by DataCite/Google Dataset Search (F4).
2. **Rich, machine-readable metadata**: title, creators (with ORCID URLs),
   description, keywords, dates, license as SPDX ID + URI (F2, R1.1, I1).
3. **Typed cross-links**, not bare URLs: `IsSupplementTo` the paper's DOI,
   `IsDerivedFrom` source datasets, SWHID for the exact code state (I3) —
   Crossref↔DataCite propagate these bidirectionally for free.
4. **Provenance**: who/how/from-what — the manifest's per-file version+MD5
   pins are raw R1.2 material already.
5. **Anonymous read stays sacred** (A1): public data fetchable with no
   token — gosf's unauthenticated code path is itself a FAIR feature.

Automated FAIR assessors ([F-UJI](https://www.f-uji.net/), FAIR-Checker) can
only see machine-readable metadata reachable from the PID: a resolvable DOI,
schema.org JSON-LD on the landing page, an SPDX-matched license, checksums,
typed related identifiers. Optimizing for F-UJI is therefore concrete and
cheap, and a `check --fair` command can literally call the F-UJI API.

### 2.2 Metadata standards worth emitting (not inventing)

| Standard | Current | Role for us |
|---|---|---|
| [DataCite schema 4.7](https://datacite-metadata-schema.readthedocs.io/en/4.7/) (Mar 2026) | 6 mandatory fields: Identifier, Creator, Title, Publisher, PublicationYear, ResourceType | What the DOI infrastructure requires; the floor for our metadata model |
| [schema.org/Dataset](https://developers.google.com/search/docs/appearance/structured-data/dataset) JSON-LD | Science-on-Schema.org v1.3 profile | What Google Dataset Search reads; embedded in our generated pages |
| [CITATION.cff 1.2.0](https://citation-file-format.github.io/) | GitHub-native "cite this repo" | Read if present; optionally generate |
| [CodeMeta 3.0](https://codemeta.github.io/) | software metadata JSON-LD | Read-only interop (HERMES's lane) |
| [RO-Crate 1.2](https://www.researchobject.org/ro-crate/specification) (Jun 2025) | `ro-crate-metadata.json` packaging | Optional emit — one JSON-LD file makes any dataset directory self-describing |
| [Data Package v2](https://datapackage.org/blog/2024-06-26-v2-release/) (2024) | `datapackage.json` + Table Schema | Optional emit; OSF and CKAN already consume it |

One internal metadata model, multiple serializers. Never invent a schema.

**Licensing:** machine-readable SPDX ID everywhere (`CC0-1.0`, `CC-BY-4.0`);
default suggestion **CC0-1.0 for data** (attribution stacking is real —
[Dryad requires CC0](https://blog.datadryad.org/2023/05/30/good-data-practices-removing-barriers-to-data-reuse-with-cc0-licensing/));
warn hard on NC/ND variants. Ship a `LICENSE` file inside the dataset too.

**DOI mechanics:** Zenodo's model — one **concept DOI** for all versions
(resolves to latest) + one **version DOI** per published version — is now the
norm and maps beautifully onto the manifest: the project pins the concept,
each entry pins a version. Citations default to the version DOI
(reproducibility), landing pages show both.

### 2.3 Platform comparison (backend candidates)

| | Zenodo/InvenioRDM | Figshare | Dataverse | Dryad | OSF (today) |
|---|---|---|---|---|---|
| Draft→publish lifecycle | yes | yes | yes (major/minor) | yes + **human curation** | no (registrations only) |
| Published files immutable | yes | yes | yes | yes | **no** (per-file versions) |
| File namespace | **flat** | **flat** | flat + `directoryLabel` | flat | **hierarchical tree** |
| DOI | concept + version | base + `.vN`, reservable | reserved at draft | at create, registered post-curation | optional, weak semantics |
| Checksums via API | `md5:` per file | MD5, server-verified | MD5 (configurable) | optional digest | MD5+SHA256 |
| Upload | single PUT (50 GB) | chunked parts | multipart via direct-S3 | single PUT | single PUT |
| Limits | 50 GB/record, 100 files (self-service to ~200 GB) | 20 GB free → 5 TB inst. | per-install (2.5–4 GB typical) | 2 TB, **fees >10 GB** | 5/50 GB caps |
| Sandbox | sandbox.zenodo.org | api.figsh.com | demo.dataverse.org | sandbox.datadryad.org | test.osf.io |
| Cost | free | free tier | free (Harvard) | **$150+/dataset** | free |

Full details and sources in the agent report; canonical docs:
[InvenioRDM REST reference](https://inveniordm.docs.cern.ch/reference/rest_api_drafts_records/),
[docs.figshare.com](https://docs.figshare.com/),
[guides.dataverse.org](https://guides.dataverse.org/en/latest/api/native-api.html),
[Dryad submission API](https://github.com/CDL-Dryad/dryad-app/blob/main/documentation/apis/submission.md).

Non-DOI backends (S3, Globus, Hugging Face without DOI, Software Heritage)
fit later as `PIDKind: none` transports; they are explicitly out of scope for
v1.

**Backend priority:** Zenodo first — and implemented as an **InvenioRDM
driver with a Zenodo profile**, because Zenodo has run on InvenioRDM since
Oct 2023 and the same API serves CaltechDATA, TU Wien, and dozens of
institutional repositories. One driver, many backends. Figshare second
(real sandbox, token auth, chunked upload). Dataverse third (huge academic
footprint, no Go client exists at all — a confirmed gap). Dryad last or
never (gated OAuth credentials, asynchronous human curation, paywall).

### 2.4 Zenodo API essentials (the first backend)

Two APIs coexist; **build on the new InvenioRDM API** (`/api/records`), not
the legacy deposit API — the legacy layer is a lossy compatibility shim,
Zenodo has said new integrations shouldn't rely on it, and the new-version
file flow actively broke legacy clients in Nov 2023
([zenodo#2506](https://github.com/zenodo/zenodo/issues/2506)).

Lifecycle (all under `Authorization: Bearer <token>`, PAT scopes
`deposit:write` + `deposit:actions`):

```
POST /api/records                                  → create draft (id, parent.id, links)
POST /api/records/{id}/draft/files   [{"key":…}]   → register file entries
PUT  …/draft/files/{key}/content                   → stream bytes
POST …/draft/files/{key}/commit                    → returns checksum "md5:<hex>"
POST /api/records/{id}/draft/pids/doi              → reserve DOI pre-publish
POST /api/records/{id}/draft/actions/publish       → publish (202)
POST /api/records/{id}/versions                    → new-version draft (files NOT carried)
POST /api/records/{id}/draft/actions/files-import  → link previous version's files (no re-upload)
```

Facts that shape the design:

- **MD5 everywhere**: commit/list/metadata responses carry `md5:<hex>`;
  downloads carry `Content-MD5`. The existing MD5-based classification
  survives intact.
- **Files immutable after publish** (a 30-day UI correction window exists
  but is not API-verified — do not design around it). "Push changed files"
  = new version = new version DOI, every time.
- **Drive everything off the `links` map** in responses, never hand-built
  URLs (mirrors the Waterbutler lesson).
- **Rate limits**: 100 req/min & 5,000/hr authenticated; **search is 30
  req/min** — a manifest scan must prefer record-scoped GETs and per-run
  caching (exactly `resolver.NewCachingLister`'s pattern). Honor
  `X-RateLimit-Reset`; a `Retry-After` header is not guaranteed.
- **Publish can 504 while succeeding server-side**
  ([zenodo#2131](https://github.com/zenodo/zenodo/issues/2131)) — after any
  5xx on publish, reconcile by re-GET, never blind-retry. Same principle as
  gosf's "throttling is never mistaken for absence."
- **Pending (init'd-but-uncommitted) file entries block publish** — a
  crashed upload must be detected and cleaned in preflight.
- **Empty files are rejected**; max 100 files/record, 50 GB/record default.
- **Sandbox** (sandbox.zenodo.org): separate account + token, wipeable at
  any time, test DOI prefix `10.5072` — the live-tier target.
- **Communities**: submitting a draft to a community pre-publish hands
  publish control to a curator. v1 publishes community-less; community
  inclusion is post-publish and out of scope.
- **`resource_type` is instance-defined** — fetch
  `GET /api/vocabularies/resourcetypes`, don't hardcode.

### 2.5 What already exists (gap analysis)

Nothing occupies the target square. The closest neighbors:

- [ropensci/deposits](https://docs.ropensci.org/deposits/) (R) proved the
  two-backend abstraction (Zenodo+Figshare, frictionless metadata as neutral
  layer) but is an R library, no state machine, no CLI.
- [frictionless-py portals](https://framework.frictionlessdata.io/docs/portals/zenodo.html)
  publish a datapackage to Zenodo — one-shot, no pins, no diff, Python.
- [zenodo_client](https://github.com/cthoyt/zenodo-client) has
  "create-or-update by concept" ergonomics worth studying; Python, per-record.
- [HERMES](https://github.com/softwarepub/hermes) is the serious
  CI-publication pipeline — for *software releases*, not data files.
- GitHub↔Zenodo integration archives whole-repo zips per release — can't
  select files, can't attach to existing records.

**Deliberately do not rebuild:** working-data versioning (DVC/DataLad/lakeFS
own it — read `dvc.lock` hashes as courtesy interop at most), tabular
validation (frictionless), CITATION.cff/codemeta generation (cffinit,
codemetapy), code archival (GitHub↔Zenodo, Software Heritage), portals/servers
(static HTML only), FAIR metric implementations (link to F-UJI).

### 2.6 Wiki → static site

- **Generator: hand-rolled, pure Go.** Hugo-as-a-library is explicitly
  unsupported and would balloon the binary; MkDocs/Quarto break the
  single-binary story. A goldmark pipeline is small and fully testable:
  [`yuin/goldmark`](https://github.com/yuin/goldmark) + `extension.GFM` +
  [`goldmark-highlighting/v2`](https://github.com/yuin/goldmark-highlighting)
  (chroma v2) + [`go.abhg.dev/goldmark/{frontmatter,toc,anchor,mermaid}`](https://github.com/abhinav/goldmark-frontmatter)
  + hugo-goldmark-extensions/passthrough for math → `html/template` theme
  embedded via `go:embed` (CSS, KaTeX auto-render, mermaid.min.js vendored —
  generated sites are CDN-free and work offline).
- **Deploy: orphan single-commit force-push to `gh-pages`** with `.nojekyll`
  (+ optional `CNAME`), then first-run
  `POST /repos/{o}/{r}/pages` (`build_type: legacy`) to enable Pages so the
  user never opens the settings UI. No workflow file. Prefer shelling to
  system `git` (inherits credential helpers); fall back to go-git v5 with a
  token from a `--github-token` → `GITHUB_TOKEN`/`GH_TOKEN` → `gh auth token`
  → keychain ladder. GitHub Pages limits (1 GB site, 100 GB/mo soft) are fine
  because **bytes live on Zenodo, pages only link to them**. Cloudflare Pages
  Direct Upload is the designated second deploy target if limits ever bite.
- **FAIR landing pages**: per-dataset page with schema.org/Dataset JSON-LD,
  citation box (version DOI default, concept DOI as "latest"), DOI badge,
  file manifest with checksums (falls straight out of the manifest), versions
  table, license, ORCID-linked creators; site-wide `sitemap.xml` +
  `robots.txt` + `DataCatalog` JSON-LD on the index.
- **Build-time trick**: DataCite serves ready-made schema.org JSON-LD,
  BibTeX, and formatted APA via
  [DOI content negotiation](https://support.datacite.org/docs/datacite-content-resolver)
  — fetch once at build time and cache (the `internal/update` cached-check
  pattern), so pages are static and rate-limit-free.
- Reference pattern: [DataLad Catalog / SFB1451 portal](https://github.com/sfb1451/metadata-catalog)
  — metadata files → static catalog on GitHub Pages.

---

## 3. Product vision

**One sentence:** a single-binary CLI that publishes a research project's
data files to archival repositories (Zenodo first) as versioned,
DOI-carrying, FAIR-metadata-complete datasets, keeps them verifiably in sync
via a manifest with git-like safety gates, and generates a static
documentation site with citation-ready landing pages.

**The workflow stays what it is today** — that's the explicit requirement.
`init → add → status → push/publish → sync`, manifest in the repo, state
gates refusing to clobber, `--output=json`, agent skill. What changes is
underneath: the remote is a dataset record with versions and a DOI instead
of a mutable file tree, and the wiki becomes a generated site.

**Positioning:** "use DVC/DataLad for your pipeline; use this to *publish*
the outputs." Publication, citation, and documentation of final artifacts —
not working-data versioning.

**Naming — decided: `datapin`.** With no install base, the rename is free.
`datapin` names the tool's actual differentiator — nothing else in the
landscape *pins* published data versions (version + MD5) and verifies
against the pin; everything else is one-shot push. It reads well as a
command (`datapin push`, `datapin sync`, `datapin publish`), has a friendly
conceptual precedent in Posit's `pins` R package while staying distinct,
and a collision sweep found no existing software squatting the name
(rejected alternatives: `datapub` — taken by datopian's CKAN framework;
`fairpub` — archived R package; `datamint`, `stele` — crowded/taken).
Command examples in this doc remain written as `gosf` for continuity with
the codebase being described; read them as `datapin`.

---

## 4. Architecture

### 4.1 The conceptual model shift

OSF gave us a mutable hierarchical file tree with per-file version history.
Publication repositories give us:

- a **record** (dataset) with a flat set of named files,
- an explicit **draft → publish** lifecycle,
- **immutable files once published**,
- record-level **versions** chained under a concept identifier.

Consequences, adopted as design decisions:

1. **The unit of sync is the dataset, not the file.** The manifest groups
   files into `[[datasets]]`. Classification still runs per file (MD5s
   compare exactly as today), but *actions* aggregate per dataset: one push
   = one new version transaction covering every changed file in that
   dataset.
2. **Push is a transaction**: open new-version draft → `files-import`
   previous files → delete changed/removed entries → upload changed files →
   verify checksums → set metadata → publish → re-pin. Any failure before
   publish is discarded cleanly (`DELETE …/draft`); a 5xx on publish
   reconciles by read. This replaces `executeEntry`'s per-file PUT for
   publication backends; OSF (if kept as an adapter) retains the per-file
   path.
3. **The gate matrix survives — and gets *stronger*.** Because published
   versions are immutable, R≠B can only mean "a newer version exists,"
   never "someone edited the file under us." The L/B/R states map:

   | State | Meaning post-pivot |
   |---|---|
   | `IN_SYNC` | local files match pinned version's checksums; pinned version is latest |
   | `AHEAD` | local files differ from pin; remote latest = pin → push opens a new version |
   | `REMOTE_NEWER` | a newer published version exists; local = pin → fast-forward pull + re-pin |
   | `DIVERGED` | local differs from pin AND newer version exists → `--resolve=ours|theirs` |
   | `MISSING` | local file absent → download from pinned (or latest) version |
   | `NOT_PUBLISHED` | no record yet (`record = ""`) → first publish creates it |
   | `PIN_ONLY` | local content equals remote latest but pin is stale → re-pin, no transfer |

   `syncDecision`/`pushDecision`/`pullDecision` stay pure and table-tested;
   they gain a dataset-level aggregation layer above them.
4. **Flat namespace is canonical.** The adapter's file address is a flat
   `key` that may contain `/`. Hierarchical backends map keys onto real
   paths; flat backends use them as filenames (Zenodo permits `/` in keys;
   Dataverse maps the directory part to `directoryLabel`). The
   `internal/resolver` tree-walk becomes an OSF-adapter implementation
   detail, not part of any interface. `ListDir` does not exist in the
   adapter; only `ListFiles(record) → []FileInfo`.

### 4.2 Backend adapter interface

```go
package backend

// Backend is the least-common-denominator surface every publication
// repository implements. File addresses are flat keys (may contain "/").
type Backend interface {
    Capabilities() Caps

    // Draft lifecycle
    CreateDraft(ctx context.Context, meta Metadata) (DraftID, error)
    UpdateMetadata(ctx context.Context, id DraftID, meta Metadata) error
    UploadFile(ctx context.Context, id DraftID, key string, r io.Reader, size int64, sum Checksum) (FileInfo, error)
    DeleteDraftFile(ctx context.Context, id DraftID, key string) error
    ImportPreviousFiles(ctx context.Context, id DraftID) error // no-op where unsupported
    Publish(ctx context.Context, id DraftID) (PublishResult, error)
    Discard(ctx context.Context, id DraftID) error

    // Published records
    NewVersion(ctx context.Context, rec RecordID) (DraftID, error)
    GetRecord(ctx context.Context, rec RecordID) (Record, error)     // incl. version chain, PIDs
    ListFiles(ctx context.Context, rec RecordID) ([]FileInfo, error) // Key, Size, Checksum
    DownloadFile(ctx context.Context, rec RecordID, key string, w io.Writer) error
}

type Checksum struct{ Algo, Hex string } // "md5" | "sha256"

type PublishResult struct {
    PID     string // DOI ("10.5281/zenodo.x"), SWHID, or "" if none
    Pending bool   // Dryad-style curation: submitted, not yet public
}

// Caps are declared per configured remote (probed/configured at
// `gosf remote add`), not hardcoded per backend type — Dataverse and
// InvenioRDM instances vary per installation.
type Caps struct {
    MintsDOI, ReserveDOI       bool
    PIDKind                    string // "doi" | "swhid" | "none"
    SyncPublish                bool   // false: Dryad curation, SWH async
    MutablePublished           bool   // true only for OSF/S3-style backends
    ImportsPreviousFiles       bool   // Zenodo files-import
    PathHint                   bool   // Dataverse directoryLabel
    MultipartUpload            bool
    MaxFileSize, MaxRecordSize int64
    MaxFilesPerRecord          int
    VersionScheme              string // "native" | "datapin" — versioning itself is invariant (§4.7)
    ChecksumAlgo               string
    ServerVerifiesChecksum     bool
    Embargo                    bool
    Sandbox                    string // test-instance base URL, "" if none
}
```

Design rules:

- **Zenodo ships as `invenio` driver + Zenodo profile** (base URL, quirks,
  rate-limit numbers). `gosf remote add https://sandbox.zenodo.org --name
  sandbox` probes vocabularies/limits and records Caps in config.
- **Caps gate features, never crash them**: `--embargo` on a backend without
  embargo is a clean preflight error; a `Pending` publish result surfaces as
  "submitted for curation — poll with `gosf status`".
- **Checksum algo is declared, not assumed** — MD5 is the lingua franca
  (Zenodo, Figshare, Dataverse-default, OSF all speak it), so
  `computeLocalMD5` and the classification machinery carry over; `Caps`
  leaves the door open for SHA-256-only backends.
- The retry/backoff layer (`internal/client/retry.go`) generalizes: honor
  `Retry-After` when present, else `X-RateLimit-Reset`, decline waits past
  `maxRetryDelay`, context-aware sleeps. Same code, injected per driver.

### 4.3 Manifest v2 (`.gosf/gosf.toml`)

```toml
schema = 2

[project]
name              = "cortical-rnaseq-2026"
default_workspace = "osf"          # mutable sync target (see §4.7); "" = none
default_archive   = "zenodo"       # DOI-minting publish target

# ── one dataset = one publishable record with a DOI ──────────────
[[datasets]]
slug    = "counts"                 # local handle; also the site page slug
archive = "zenodo"                 # optional override of default_archive
record  = "4d0ns-ntd89"            # concept/parent record id; "" until first publish
concept_doi = "10.5281/zenodo.1234567"
version     = 3                    # pinned version index; 0 = not yet published
version_doi = "10.5281/zenodo.1234570"

  [datasets.metadata]              # DataCite floor + FAIR extras, linted by `gosf check`
  title       = "Aligned RNA-seq count matrices, cortex, batch 1–4"
  description = "…"
  license     = "CC0-1.0"          # SPDX, validated
  keywords    = ["RNA-seq", "cortex"]
  resource_type = "dataset"
  [[datasets.metadata.creators]]
  name        = "Labadorf, Adam"
  orcid       = "0000-0000-0000-0000"
  affiliation_ror = "https://ror.org/05qwgg493"
  [[datasets.metadata.related]]
  identifier = "10.1101/2026.01.01.123456"
  relation   = "IsSupplementTo"    # DataCite relationType, validated

  [[datasets.files]]
  local = "results/counts.h5"
  key   = "counts.h5"              # flat key on the record
  md5   = "d41d8cd98f00b204e9800998ecf8427e"
  [[datasets.files]]
  local = "results/coldata.csv"
  key   = "coldata.csv"
  md5   = "…"

# ── site pages replace [[wikis]] ─────────────────────────────────
[site]
title  = "Cortical RNA-seq"
deploy = "gh-pages"                # gh-pages | dir | (later: cloudflare)
repo   = "BU-Neuromics/cortical-rnaseq"

[[site.pages]]
local = "docs/index.md"
slug  = "index"
[[site.pages]]
local = "docs/methods.md"
slug  = "methods"
```

Validation on load mirrors today's rules: unique `local` paths across
datasets and pages, unique `(remote, record)` and `(dataset, key)` pairs,
SPDX license validated against the embedded SPDX list, ORCID/ROR format
checks, every dataset resolves a remote. Version+MD5 pins per file preserve
the whole classification machinery; dataset-level `version` pins the record
version the file set belongs to.

Migration: `Load` recognizes schema 1 (OSF `[[files]]`/`[[wikis]]`) and
either (a) keeps serving it through the OSF adapter, or (b) `gosf migrate`
rewrites it (see §7).

### 4.4 Metadata subsystem (`internal/meta`)

One internal model (superset of DataCite 4.7 mandatory + recommended),
sourced from the manifest, with serializers:

- → backend payloads (InvenioRDM `metadata`, Figshare article fields,
  Dataverse citation block),
- → `datapackage.json` (Data Package v2) and `ro-crate-metadata.json`
  (RO-Crate 1.2) emitted next to the data on request (`gosf export`),
- → schema.org/Dataset JSON-LD for the site generator,
- ← readers for `CITATION.cff` and `.zenodo.json` if present (interop,
  never generate what cffinit/codemetapy do better).

`gosf check` is the linter: fails fast pre-publish on missing
title/creators/license, non-SPDX license, malformed ORCID/ROR, NC/ND
warning, zero-byte files (Zenodo rejects them), >100 files/record,
oversize files vs Caps. `gosf check --fair` optionally calls the F-UJI API
against an already-published DOI.

### 4.5 Site generator (`internal/site`)

Pure-Go pipeline, per §2.6: front-matter parse → goldmark → `html/template`
theme from `go:embed` → `public/`. Pages come from `[[site.pages]]`;
dataset landing pages are generated from the manifest + cached DOI
content-negotiation responses (BibTeX, APA, schema.org JSON-LD). Every
stage is a pure function (table-testable); the DOI fetcher sits behind an
injectable HTTP client with an on-disk cache.

`gosf site build` → `public/`; `gosf site preview` → localhost server;
`gosf site publish` → orphan commit to `gh-pages` + first-run Pages
enablement via the GitHub REST API. Deploy backends are their own small
adapter (`gh-pages`, `dir`, later Cloudflare Direct Upload).

### 4.6 Config and auth

`~/.config/gosf/config.toml` grows a `[remotes]` table: named remotes with
kind (`invenio`, `figshare`, …), base URL, and probed Caps. Tokens are
**per-remote** (Zenodo sandbox and production tokens are different!) via
the existing ladder: flag > env (`GOSF_TOKEN_<REMOTE>` / backend-native
names like `ZENODO_TOKEN`) > config > OS keychain. GitHub token for site
publish has its own ladder (§2.6). Never echo tokens; anonymous reads work
on every backend that allows them.

### 4.7 Remote roles: workspace vs archive (portable intermediate results)

The founding gosf use case — run the analysis on the compute cluster, push
the results, `git clone` the repo on a laptop, pull the results back
alongside the code — must survive the pivot, and must not require DOIs,
metadata, or a publication decision. Publication repositories are the wrong
tool for it: published versions are immutable, every push would mint a DOI,
and drafts/quotas/rate limits are designed around publishing, not iterating.
(Zenodo drafts are explicitly not working storage.)

So a configured remote carries a **role**, and the verbs split along it:

- **workspace** — mutable, cheap, no PID. `push`/`pull`/`sync`/`status`
  operate here with *exactly today's gosf semantics*: the manifest pins
  version + MD5 per file, `ClassifyFile` and the gate matrix arbitrate,
  transfers are idempotent, `--dry-run`/`--force`/`--resolve` all behave as
  they do now. Because the manifest is committed to git, cloning the repo
  anywhere and running `datapin pull` reproduces the pinned results — **the
  manifest is the portability mechanism**. No git hooks, no smudge filters,
  no pointer files, no content-addressed cache directory: files stay plain
  files in the working tree, state stays in one readable TOML, and the tool
  stays one static binary. That is the deliberate contrast with
  git-lfs/git-annex/DVC; the traded-away features (dedup cache, pipeline
  graphs) are exactly the ones this plan already delegates to DVC.
- **archive** — immutable, DOI-minting. `datapin publish <slug>` promotes a
  dataset's pinned file set through the record transaction of §4.1 and
  writes the concept/version DOIs back to the manifest. Metadata
  completeness (`gosf check`) is enforced only at this boundary — a dataset
  with no `[datasets.metadata]` block is perfectly valid while it only
  syncs to a workspace.

Workspace backends, in priority order:

1. **OSF** — already written; it is today's gosf (mutable tree, per-file
   versions, MD5s). Carrying it over as the first workspace backend means
   existing workflows keep working from day one of the reboot.
2. **S3-compatible** (institutional object storage, MinIO, Cloudflare R2) —
   flat keys, ubiquitous near clusters. Multipart ETags are not MD5s, so
   the local manifest pin is the checksum source of truth (optionally
   mirrored to `x-amz-meta-md5`); bucket versioning, where enabled, provides
   the `native` scheme, otherwise the `datapin` scheme applies (below).
3. **SFTP/SSH** — every cluster has it; results can live on lab storage
   with no third-party service at all.

**Versioning is an interface invariant.** Every workspace remote presents
the same versioning surface — `Versions(key)`, download-of-version, and
explicit reversion — *regardless of native support*. There is no
`FileVersions: false`; adapters differ only in **scheme**, selected per
remote at `remote add` (auto-probed — S3 `GetBucketVersioning`, OSF always
native — with manual override in config):

- **`native`** — the backend's own history (OSF file versions, versioned S3
  buckets) mapped onto the common surface. Captures out-of-band writes too.
- **`datapin`** — a deliberately simple, unoptimized scheme the adapter
  implements itself under a reserved `.datapin/` prefix:
  - current bytes live at the plain key (the remote stays a browsable
    plain-file layout); before overwrite, the superseded object is archived
    to `.datapin/versions/<key>/<md5>` via **server-side copy** (S3
    CopyObject — no bytes transit; SFTP copy/link);
  - one **append-only journal event** per write —
    `.datapin/journal/<key>/<seq>-<md5>.json` (key, MD5, size, timestamp,
    pusher, action) — never a mutable index, so concurrent pushes cannot
    corrupt history (conditional PUT / rename-into-place covers the
    residual naming race);
  - **crash-safe by idempotence, not transactions**: archives are
    content-addressed (re-copy is a no-op) and the journal entry is written
    last, so recovery from any crash is "re-run the push";
  - **revert is a new version, git-style**: `revert --to <n>` copies the
    archived blob back and appends `{action: "revert", to: n, reason}` —
    history never rewrites, and the journal reads as a narrative of what
    happened (the explainability goal);
  - per-key dedup falls out of MD5-addressed archives; cross-key dedup is
    deliberately out of scope (that's DVC's content-addressed cache).

Rules that keep it sound: **the journal is evidence, never authority** — if
the observed object MD5 disagrees with the journal head, observed state
wins, history is treated as incomplete, and classification degrades
conservatively (`DIVERGED`, explicit `--resolve`/`--force`). And the
invariant is stated precisely: **every version datapin wrote is
revertible.** On `datapin`-scheme remotes an out-of-band overwrite (`scp`
over a result) loses bytes nothing archived; datapin detects it, says so,
and reports that version as unrecoverable with the reason — it never
pretends otherwise.

Retention is default-on (the invariant requires bytes), so cost is managed
explicitly: `datapin gc --keep N` reclaims archived versions and `status`
reports retained bytes per remote.

**Build vs buy**: [OCFL](https://ocfl.io/) (the archival-community standard
for versioned object layouts; Go: [srerickson/ocfl-go](https://github.com/srerickson/ocfl-go))
is the one serious candidate for buying this — but its mutable per-object
`inventory.json` reintroduces the read-modify-write concurrency problem the
append-only journal avoids, and it models curated preservation objects, not
high-churn sync targets. restic/git-based stores are optimized-and-
complicated. Conclusion: bespoke dumb layout as above; OCFL noted as a
consciously-diverged-from standard and a possible future export format.

---

## 5. CLI surface

Familiar verbs preserved; the OSF-specific addressing (`abc12:/path`)
gives way to dataset slugs.

| Command | Role |
|---|---|
| `gosf init` | create manifest; prompt for project name + default remote |
| `gosf remote add/ls/rm` | configure backends (probe Caps, store token) |
| `gosf add <local>... --dataset <slug>` | add files to a dataset (creates it, prompting/deriving metadata) |
| `gosf status` | classify all datasets/files (L/B/R), read-only, CI exit codes — as today |
| `gosf push [<slug>]` | sync local → **workspace** remote, today's semantics (plan, confirm, idempotent, gates) |
| `gosf pull [<slug>]` | download from workspace (or `--archive` for published bytes); `--track-only` mirrors today |
| `gosf sync` | reconcile local ↔ workspace per the gate matrix, non-interactive |
| `gosf publish [<slug>]` | **archive** promotion: preview plan (files, sizes, MD5s, DOI to be minted, **PUBLIC warning**) → confirm → draft/upload/publish transaction → print DOI + citation |
| `gosf versions <slug>[/<key>]` | version chain — record versions with DOIs on archives; per-file journal/native history on workspaces (works on **every** remote, §4.7) |
| `gosf revert <slug>/<key> --to <n>` | explicit, journaled restore of an earlier version; appends a revert event with reason, never rewrites history |
| `gosf gc [--keep N]` | reclaim archived workspace versions; `status` reports retained bytes |
| `gosf check [--fair]` | metadata/preflight linter; F-UJI on published DOIs |
| `gosf export [--datapackage] [--ro-crate]` | emit standard metadata files |
| `gosf open <slug>` | record landing page (or `--site` page) in browser |
| `gosf site build/preview/publish` | the wiki replacement |
| `gosf migrate` | OSF manifest/wiki → v2 manifest + site pages (§7) |
| `gosf auth login/status/logout` | per-remote token management |
| `gosf onboard` | guided setup, now ending at "publish your first dataset" |

Contracts that do not change: `--output=json` on everything (result types
in `internal/output/result.go`, unit-tested), `--dry-run`, `--yes`/`--force`
semantics (`--force` in JSON mode mandatory for destructive ops), stderr
logging ladder (`-v/-vv/-vvv`, `--quiet`), colorized ANSI-safe tables,
non-TTY refusal rules, cancellation via context, exit-code discipline, the
skill-parity test (`cmd/skill_doc_test.go`) forcing docs to keep up.

New UX obligations that come with DOIs:

- **Publishing is louder than pushing was.** A DOI is forever and public;
  the confirmation plan shows the minted-DOI consequence explicitly, and
  `--sandbox` (or a sandbox-kind remote) is the loudly-documented rehearsal
  path.
- **Every publish ends by printing the DOI + a paste-ready citation**
  (and updates the manifest pins atomically).
- Reserve-DOI-before-publish (`gosf push --reserve`) so the DOI can go into
  a paper before the data is final.

---

## 6. Testing strategy

The three-tier model transplants directly and is a competitive moat — none
of the existing tools have anything like it:

1. **Unit** — pure functions (gate matrix, classification, metadata
   serializers, site render stages, Caps gating) + HTTP clients against
   `httptest`.
2. **Integration** (`-tags integration`) — the built binary against
   **`fakeinvenio`**, a hermetic in-process fake of the InvenioRDM API
   (successor to `fakeosf`): drafts, three-step upload with real MD5
   verification, publish, versions with files-import, pagination, rate-limit
   headers, the pending-file-blocks-publish behavior, empty-file rejection.
   The fake encodes our assumptions; per-backend fakes (`fakefigshare`, …)
   follow as adapters land. Site generation integrates against a temp dir +
   a local git remote; Pages enablement against `httptest`.
3. **Live** (`-tags live`) — the binary against **sandbox.zenodo.org**
   (unique per-run prefixes; sandbox is wipeable, so tests create their own
   records and never assume persistence). The live tier exists precisely to
   catch where real Zenodo diverges from the InvenioRDM reference — the
   fakeosf lesson (the folder-upload 404 the live tier caught) applies
   verbatim.

Additionally: **a single contract-test suite run against every backend
adapter** (parameterized over the fake + sandbox), asserting the Backend
interface semantics (checksum round-trip, draft discard leaves no trace,
new-version chains, publish immutability) so adapters cannot drift apart —
the same philosophy as the shared `executeEntry` today.

---

## 7. Migration path (from gosf 2.x / OSF)

1. **OSF survives as an adapter** (`Caps{MutablePublished: true,
   Hierarchical…}`) at least through the transition — existing users' data
   stays reachable with the same binary, and the OSF client/resolver code
   already exists. It may be demoted or dropped once migration completes.
2. **`gosf migrate`** (interactive):
   - reads the v1 manifest, groups `[[files]]` into proposed `[[datasets]]`
     (by top-level directory, user-editable), derives flat keys from remote
     paths;
   - pulls OSF project title/description → dataset metadata skeleton;
     prompts for the missing DataCite floor (creators/ORCIDs, license);
   - fetches wiki pages (already canonicalized by `client.GetWikiContent`)
     into `docs/` as `[[site.pages]]`;
   - optionally drives the initial `pull` from OSF so local files are
     complete, then the first `push` to Zenodo — data moves OSF→local→Zenodo
     with checksum verification at both hops;
   - leaves a `MIGRATED.md` breadcrumb recording the OSF GUID ↔ DOI mapping,
     and suggests posting the DOI on the OSF project page.
3. **Docs/skill**: the shipped skills.sh skill is regenerated for the new
   surface; the skill-parity test keeps it honest from day one.

---

## 8. Phased roadmap

**Phase 0 — sandbox spike (1 short cycle).** Verify the unverified against
sandbox.zenodo.org before committing to design details: publish-twice
status code, `files-import` behavior when a version draft already exists,
whether 429s carry `Retry-After`, multipart (`M` transfer) availability,
draft-listing behavior, empty-file and >100-file rejections. Deliverable: a
`docs/zenodo-notes.md` of confirmed behaviors + the first `fakeinvenio`
test fixtures derived from real responses.

**Phase 1 — core + Zenodo (MVP).** `internal/backend` interface with
**workspace/archive roles** (§4.7), the OSF adapter carried over as the
first workspace backend (existing workflows keep working day one),
`invenio` driver + Zenodo profile as the first archive backend, manifest v2
(Load/Save/validation), gate-matrix aggregation to dataset transactions,
`init/remote/add/status/push/pull/sync/publish/versions/auth`,
`fakeinvenio` + sandbox live tier, JSON contracts, skill update. Exit
criteria: cluster→laptop workspace round-trip identical to gosf today, and
publish → new version → sync green on sandbox with idempotent re-runs and
the DOI printed.

**Phase 2 — metadata & FAIR floor.** `internal/meta` model + linter
(`gosf check`), SPDX/ORCID/ROR validation, related identifiers,
reserve-DOI, `gosf export` (datapackage.json first, RO-Crate second),
citation output via DOI content negotiation.

**Phase 3 — site generator.** `internal/site` goldmark pipeline, embedded
theme, dataset landing pages with JSON-LD/citations/checksums, sitemap,
`site build/preview/publish` with gh-pages deploy + Pages API enablement,
`gosf migrate` wiki import.

**Phase 4 — second backend + polish.** Figshare adapter (proves Caps is
real: chunked upload, reserve-DOI, `.vN` DOIs, no files-import), contract
suite over both, `check --fair` (F-UJI), onboard flow rewrite.

**Phase 5 — breadth (as demanded).** **S3-compatible and SFTP workspace
backends** (§4.7 — frees the workspace tier from OSF's fate), Dataverse
(no Go client exists — publishable as its own library), generic InvenioRDM
remotes (institutional repos), embargo/restricted access, Cloudflare Pages
deploy, DVC interop (read `dvc.lock` hashes). Dryad only if users
materialize.

Each phase lands via the existing TDD discipline (red/green/refactor,
regression tests for every bug) and the existing CI matrix; goreleaser and
install scripts carry over unchanged.

---

## 9. Decisions

1. **Name** — ✅ **decided: `datapin`** (2026-08-10). Fresh brand; no
   collision found; re-registers on skills.sh under the new repo.
2. **Repo strategy** — ✅ **decided: fresh repository**, seeded with gosf's
   commit history (push the `dev` branch without tags, so blame and
   regression-test context survive while releases/issues start clean at
   v0.1.0). gosf is archived with a pointer README once datapin reaches
   parity; settings to recreate: branch protection, `dev` default branch,
   and a `ZENODO_SANDBOX_TOKEN` secret replacing `OSF_TEST_TOKEN`.
3. **OSF adapter lifespan** — reframed by §4.7: OSF ships as the first
   *workspace* backend (not a publication target) and remains until
   S3/SFTP workspace backends land and users migrate; its lifespan is
   ultimately tied to COS's.
4. **Default license suggestion** (open): CC0-1.0 (recommended for data) vs
   CC-BY-4.0 — affects `init`/`onboard` wording only.
5. **Wiki parity scope** (open): is one-way (local markdown → site) enough,
   or do OSF-wiki-style server-side edits need a path back? Plan assumes
   one-way; the site is generated, git is the editor.

---

## Appendix: primary sources

FAIR/metadata: [GO FAIR](https://www.go-fair.org/fair-principles/) ·
[DataCite 4.7](https://datacite-metadata-schema.readthedocs.io/en/4.7/) ·
[Science-on-Schema.org](https://github.com/esipfed/science-on-schema.org) ·
[RO-Crate 1.2](https://www.researchobject.org/ro-crate/specification) ·
[Data Package v2](https://datapackage.org/blog/2024-06-26-v2-release/) ·
[CFF](https://citation-file-format.github.io/) ·
[SPDX](https://spdx.org/licenses/) ·
[F-UJI](https://www.f-uji.net/) ·
[RDA data versioning](https://www.rd-alliance.org/wp-content/uploads/2020/01/Report20of20the20RDA20Data20Versioning20Working20Group_V1.1.pdf) ·
[SWHID ISO 18670](https://www.softwareheritage.org/2025/05/14/iso-standard-swhid/)

Zenodo/InvenioRDM: [developers.zenodo.org](https://developers.zenodo.org/) ·
[InvenioRDM REST reference](https://inveniordm.docs.cern.ch/reference/rest_api_drafts_records/) ·
[zenodo-rdm source](https://github.com/zenodo/zenodo-rdm) ·
[Zenodo on InvenioRDM](https://blog.zenodo.org/2022/12/07/2022-12-07-zenodo-on-inveniordm/) ·
issues [#2506](https://github.com/zenodo/zenodo/issues/2506),
[#2131](https://github.com/zenodo/zenodo/issues/2131),
[#2517](https://github.com/zenodo/zenodo/issues/2517)

Platforms: [Figshare API](https://docs.figshare.com/) ·
[Dataverse native API](https://guides.dataverse.org/en/latest/api/native-api.html) ·
[Dryad API](https://github.com/CDL-Dryad/dryad-app/blob/main/documentation/apis/submission.md) ·
[COS on OSF Preprints](https://www.cos.io/blog/update-on-future-of-osf-preprints) ·
[COS ecosystem/POSE](https://www.cos.io/blog/building-the-open-science-ecosystem-a-recap-and-future-vision)

Landscape: [ropensci/deposits](https://docs.ropensci.org/deposits/) ·
[frictionless Zenodo portal](https://framework.frictionlessdata.io/docs/portals/zenodo.html) ·
[HERMES](https://github.com/softwarepub/hermes) ·
[zenodo_client](https://github.com/cthoyt/zenodo-client) ·
[zenodo_get](https://pypi.org/project/zenodo-get/) ·
[DataLad Catalog](https://docs.datalad.org/projects/catalog/en/stable/)

Site: [goldmark](https://github.com/yuin/goldmark) ·
[GitHub Pages limits](https://docs.github.com/en/enterprise-server@3.17/pages/getting-started-with-github-pages/github-pages-limits) ·
[Pages REST API](https://docs.github.com/en/enterprise-cloud@latest/rest/pages) ·
[Google Dataset structured data](https://developers.google.com/search/docs/appearance/structured-data/dataset) ·
[DataCite content negotiation](https://support.datacite.org/docs/datacite-content-resolver) ·
[Cloudflare Pages Direct Upload](https://developers.cloudflare.com/pages/get-started/direct-upload/)
