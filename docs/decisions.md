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
