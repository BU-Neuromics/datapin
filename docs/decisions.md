# Implementation decisions (post-handoff)

Continues the numbering from `docs/datapin-handoff.md` §2 (D1–D10). These
were made autonomously during the feature-complete build-out (2026-08-10,
maintainer instruction: "work autonomously… make opinionated decisions if
needed, recording them for review later"). Flag any of these for revision in
review; none are load-bearing beyond the code that cites them.

| # | Decision | Rationale |
|---|---|---|
| D11 | **Feature-complete scope = plan §8 Phases 1–3** (core+Zenodo, metadata/FAIR floor, site generator). Figshare/Dataverse (Phase 4) and Phase 5 breadth deferred; `check --fair` (F-UJI) kept in scope as a Phase 2 item. | Maintainer tabled OSF migration and said "focus on Zenodo"; a second archive backend adds breadth, not depth. The cross-adapter contract suite still lands (parameterized over fakeinvenio + sandbox) so Phase 4 has rails. |
| D12 | **OSF is NOT refactored behind the Backend interface yet.** Existing OSF sync paths (`push/pull/sync/status` on `[[files]]`/`[[wikis]]`) stay as-is; the `Backend` interface serves archive backends (invenio) now. | Maintainer: "table any OSF migration and just focus on zenodo." The refactor is mechanical but wide; doing it while introducing the interface doubles the blast radius. Plan §4.7's workspace-role architecture is unchanged — this only re-orders work. |
| D13 | **Manifest schema 2 = schema 1 + `[[datasets]]`.** `[[files]]`/`[[wikis]]` remain valid at schema 2 and keep their OSF semantics; datasets are additive. A schema-1 file (no `schema` key) loads identically. | Keeps every existing workflow running through the pivot with no `migrate` step (which is tabled with OSF work). Cross-section duplicate-`local` validation extends to datasets. |
| D14 | **Generalized retry lives in new `internal/httpx`; OSF's `internal/client/retry.go` is left untouched.** The invenio driver uses httpx (Retry-After first, then `X-RateLimit-Reset`, injectable sleep/clock, decline-don't-truncate). | Sharing one implementation would mean touching stable OSF code mid-pivot. httpx is the carry-over target when OSF is eventually adapterized (D12). |
| D15 | **`Retry-After` is only honored on retryable statuses.** Sandbox sends `retry-after` on 200s too (see `docs/zenodo-notes.md` §3) — the retry layer keys on status code, never header presence. | Spike finding; encoding it as a rule prevents a subtle stall bug. |
| D16 | **`backend.Metadata` is a struct now, superseded by `internal/meta` in Phase 2.** Phase 1 publishes with the DataCite floor (title, publisher, publication date, creators, resource type, license, description, keywords); Phase 2's model serializes into it. | Avoids blocking the publish transaction on the full metadata subsystem. |
| D17 | **fakeinvenio defaults mirror observed sandbox behavior** (empty files accepted, hybrid legacy/RDM response shapes, idempotent `POST /versions`, atomic 100-file cap at registration, `retry-after` on 200s). Strictness/divergence knobs are opt-in per test. | The fake encodes our assumptions; the assumptions are the fixtures. |
| D18 | **Publish reconciles inside the driver.** On a 5xx/timeout from the publish action, `invenio.Publish` re-GETs the record and returns success iff `status == published` (spike: publish-twice → 404, publish can 504 while succeeding). Callers see one clean result. | Centralizes the trickiest recovery rule so every command gets it right. |
| D19 | **Zenodo legacy/RDM hybrid responses are read defensively**: DOIs read from `pids.doi.identifier` first, then top-level `doi`/`conceptdoi`. | Spike finding: sandbox publish/draft responses are legacy-shaped. |
| D20 | **Default license suggestion: CC0-1.0** (the open item in handoff §2), presented as a suggestion with CC-BY-4.0 as the named alternative; `check` warns on NC/ND. | Plan §2.2 already leans CC0 for data (Dryad precedent, attribution stacking); wording-only decision, easily reversed. |

## Phase 3 additions

| # | Decision | Rationale |
|---|---|---|
| D25 | **Site pipeline ships goldmark + GFM + frontmatter only**; chroma highlighting, mermaid, and KaTeX (named in plan §2.6) are deferred as theme enhancements. Raw HTML in markdown is escaped by default. | Keeps the dependency and vendoring surface small for the first cut; the pipeline structure (pure stages, embedded theme) is what D8 fixes, and extensions bolt on without redesign. |
| D26 | **`site build` is fully offline** — landing-page citations render locally from manifest metadata; DOI content negotiation stays in `datapin cite` (live fetch). The plan's cached content-negotiation for pages is an enhancement. | Deterministic, network-free builds (CI-friendly); sandbox DOIs never resolve anyway, so the fetch path would be dead in every rehearsal. |
| D27 | **`site publish` works without a token**: push relies on the user's git credential helper, and first-run Pages enablement is skipped with a note when no token is found (--github-token → GITHUB_TOKEN → GH_TOKEN → `gh auth token`). | Never block a deploy on the optional REST call; the ladder matches plan §2.6. |

## Phase 4 additions (second and third archive backends)

| # | Decision | Rationale |
|---|---|---|
| D28 | **fakefigshare and fakedataverse encode DOCUMENTED behavior, not live-verified behavior** — no credentials were available. Both fakes and drivers carry an "unverified against live" marker; live tiers exist (`integration/livefigshare` pattern to follow) and skip without credentials. The first live run against each service should be treated as a Phase-0-style spike: expect divergences (the Zenodo oc-checksum lesson) and fix fake+driver together. | Maintainer: "implement the backends, though I can't provide live services for testing yet." |
| D29 | **A Dataverse remote's collection alias rides on the URL path** (`https://host/dataverse/<alias>`), defaulting to `root`. | Keeps config.Remote at kind+url without a per-kind options bag; matches how Dataverse users see collections. |
| D30 | **`Caps.PerVersionDOI` added**: Zenodo/Figshare mint per-version DOIs; Dataverse has one DOI for all versions (version picker on the landing page). The contract suite and pins handle both; `versions <slug>` shows the same DOI per row on Dataverse remotes. | The interface must not assume Zenodo's DOI model — this is exactly the drift the contract suite exists to catch. |
| D31 | **Figshare/Dataverse model "new version" as the mutable account draft** (ImportsPrevious=false): the draft retains published files, so the publish transaction skips files-import and the per-key plan converges on whatever state the draft is in (upload replaces same-name entries idempotently). | Matches both platforms' actual lifecycle; keeps the transaction crash-re-runnable everywhere. |

## Phase 5 additions (workspace backends)

| # | Decision | Rationale |
|---|---|---|
| D32 | **Workspace sync operates at the dataset level**: `datapin push <slug>` / `pull <slug> --workspace` move a dataset's files to/from its `workspace` remote (dataset field or `default_workspace`). The schema-1 `[[files]]` OSF flow is untouched. | Retrofitting arbitrary workspace remotes into the OSF-shaped `[[files]]` entries would have forked their semantics; datasets already group "the outputs that travel together", which is what the founding cluster→laptop use case moves. Revisit if per-file workspace tracking is wanted. |
| D33 | **Remote kind implies role**: `dir`/`s3`/`sftp` are workspace kinds, `invenio`/`figshare`/`dataverse` are archive kinds. No separate role field in config. | One word says both; a kind that could serve both roles doesn't exist in the current set. |
| D34 | **S3 remote URL form is `s3://<endpoint>/<bucket>[/<prefix>]`** (`?insecure=true` for plain-HTTP MinIO, `?region=`); credentials are the per-remote token as `ACCESSKEY:SECRETKEY`, falling back to `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`. A `dir` remote is a plain path (mounted NAS). | Endpoint-in-URL serves the actual audience (institutional MinIO/R2, not AWS-default); reusing the token ladder avoids a second credential mechanism. |
| D35 | **The journal layer is generic** (`internal/workspace` over a 6-method Store); localdir/S3/SFTP get identical versioning semantics, tested once against the real protocols (in-process SFTP server, gofakes3). Native-scheme remotes (versioned S3 buckets) remain future work — every current remote uses the `datapin` scheme. | One implementation of the D4 invariant instead of three; the Store surface is small enough that a new workspace backend is ~200 lines. |
| D36 | **Out-of-band overwrites are archived, not just detected**: when the observed object disagrees with the journal head, its bytes are archived under their own content address before the push proceeds, and the event records the fact. | Strictly better than the plan's minimum (detect + report unrecoverable): bytes someone scp'd over a tracked file are usually bytes someone cared about. |
