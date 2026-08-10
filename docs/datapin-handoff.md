# datapin — implementation handoff

**Date:** 2026-08-10 · **From:** research/planning session on
`BU-Neuromics/gosf` (branch `claude/gosf-fair-data-research-6ic6l9`) ·
**For:** the implementation agent session (with permissions to create
repositories and configure org/repo settings).

This document is the operational companion to
[`docs/reboot-plan.md`](./reboot-plan.md) — the full architecture,
research findings, and rationale live there. **Read the plan first**; this
handoff adds the decision register, the bootstrap checklist, the
code-carryover map, and the working agreements the new repo inherits.

---

## 1. One-paragraph context

gosf (this repo) is a Go single-binary CLI that syncs a project's data
files against OSF with a manifest + git-like safety gates. It is being
rebooted as **datapin**: same manifest-driven workflow, but files publish
to archival repositories (Zenodo first) as DOI-carrying FAIR datasets, the
OSF wiki is replaced by a generated static site on GitHub Pages, and
storage backends sit behind an adapter interface. The founding use case —
push intermediate results from a compute cluster, `git clone` + pull them
anywhere, publish only when ready — remains first-class via
workspace-vs-archive remote roles.

## 2. Decision register (all settled — do not relitigate)

| # | Decision | Detail |
|---|---|---|
| D1 | **Name: `datapin`** | Collision-checked 2026-08-10; binary `datapin`, module `github.com/BU-Neuromics/datapin` |
| D2 | **Fresh repository, seeded history** | New repo `BU-Neuromics/datapin`; push gosf's `dev` branch **without tags** so blame/regression context survives while releases/issues start clean at v0.1.0. gosf stays live (NOT archived) until datapin reaches workflow parity |
| D3 | **Workspace vs archive remote roles** | `push/pull/sync/status` target mutable, DOI-free **workspace** remotes with today's gosf semantics; `publish` promotes a pinned dataset to an **archive** backend and mints the DOI. Metadata is enforced only at the publish boundary. Plan §4.7 |
| D4 | **Versioning is an interface invariant** | Every workspace remote supports list/download/revert of versions. Scheme per remote, probed or configured: `native` (OSF, versioned S3) or `datapin` (plain keys + server-side copy to md5-addressed archives + append-only journal; crash-safe by idempotence; revert = new journaled event; retention default-on with `gc`). Invariant stated precisely: *every version datapin wrote is revertible*. Bespoke layout — OCFL consciously diverged from. Plan §4.7 |
| D5 | **Zenodo via the new InvenioRDM API** | Not the legacy deposit API. Implemented as an `invenio` driver + Zenodo profile so institutional InvenioRDM instances come free. Plan §2.4 |
| D6 | **Unit of publication = dataset (record)** | Manifest v2 groups files into `[[datasets]]`; publish is a transaction (new-version draft → files-import → mutate → verify → publish → re-pin). Plan §4.1, §4.3 |
| D7 | **Gate matrix and TDD carry over unchanged** | `ClassifyFile` L/B/R states, pure `*Decision` functions, red/green/refactor, regression-test-per-bug, three test tiers. Plan §4.1, §6 |
| D8 | **Site generator: pure-Go goldmark pipeline** | No Hugo/hugolib, no external SSG binary; embedded theme via `go:embed`; deploy = orphan single-commit force-push to `gh-pages` + Pages REST enablement. Plan §2.6, §4.5 |
| D9 | **Metadata: one internal model, standard serializers** | DataCite-4.7 floor; emit datapackage.json / RO-Crate / schema.org JSON-LD; read CITATION.cff/.zenodo.json; never invent a schema. Plan §4.4 |
| D10 | **First backends** | Workspace: OSF (carried over) → S3-compatible → SFTP. Archive: Zenodo/InvenioRDM → Figshare → Dataverse. Dryad deprioritized. Plan §2.3, §8 |

**Still open** (decide with the maintainer when they become blocking):
license default suggestion (CC0-1.0 recommended vs CC-BY-4.0 — wording
only), and wiki parity scope (plan assumes one-way markdown → site).

## 3. Bootstrap checklist (do these in order)

Phase B ("bootstrap") precedes the plan's Phase 0. Each step should end
committed/green before the next.

1. **Create `BU-Neuromics/datapin`** — public, description "Pin, sync, and
   publish research data — single-binary FAIR data publication CLI".
   Do NOT initialize with a README (history gets seeded).
2. **Seed history**: clone `BU-Neuromics/gosf`, push its `dev` branch to
   the new repo (`git push <new-remote> dev:dev` — plain push, **no
   `--tags`**). Set `dev` as the default branch; create `main` from `dev`.
   Copy `docs/reboot-plan.md` and this handoff from the
   `claude/gosf-fair-data-research-6ic6l9` branch into the new repo's
   `docs/` (that branch is not part of `dev` history).
3. **Repo settings**: branch protection on `dev` and `main` mirroring
   gosf's (PRs target `dev`; CI required); Actions enabled.
   Secrets/variables: `ZENODO_SANDBOX_TOKEN` (see §5 — human must supply),
   drop `OSF_TEST_TOKEN`/`OSF_TEST_*` usage as the live tier migrates.
4. **The great rename** (one PR, mostly mechanical, keep tests green
   throughout): module path `github.com/BU-Neuromics/datapin` in go.mod +
   all imports; binary/command name in `cmd/root.go`, `main.go`,
   Makefile, `.goreleaser.yaml`, `install.sh`/`install.ps1`,
   `internal/update` (release-check repo), CI workflows; config dir
   `~/.config/datapin/`; manifest dir/file `.datapin/datapin.toml`
   (accept `.gosf/gosf.toml` read-only for migration); env vars
   `DATAPIN_*` (accept `GOSF_*` with a deprecation warning);
   `skills/gosf` → `skills/datapin` + `skills.sh.json`; README rewritten
   for the datapin pitch (workspace sync now, publish later); CLAUDE.md
   rewritten: keep the development-strategy/TDD/testing sections nearly
   verbatim, replace the OSF API sections with pointers to the plan until
   real adapter docs exist. Release version resets: next tag will be
   `v0.1.0`, not a continuation of gosf's `v2.x`.
5. **Phase 0 — Zenodo sandbox spike** (plan §8): against
   `sandbox.zenodo.org` with the human-supplied token, verify and record
   in `docs/zenodo-notes.md`: publish-twice status code; `POST /versions`
   while a version draft exists; whether 429s carry `Retry-After` (vs
   `X-RateLimit-Reset` only); multipart (`M` transfer) availability;
   draft listing via `GET /api/user/records`; empty-file and
   101st-file rejections; pending-file-blocks-publish behavior;
   `files-import` semantics. Capture real request/response fixtures —
   they seed `fakeinvenio`.
6. **Phase 1 onward** per plan §8. Checkpoint with the maintainer after
   Phase 0 findings and after the Phase 1 MVP is green on sandbox.

## 4. Code carryover map

| gosf code | Fate in datapin |
|---|---|
| `internal/manifest/` (Load/Save/validate, `ClassifyFile`, states) | **Keep**; extend to manifest v2 (`[[datasets]]` grouping, metadata block, workspace/archive fields). Schema-1 read support for migration |
| `cmd/gate.go` (`syncDecision`/`pushDecision`/`pullDecision`, `entryPlan`) | **Keep**; add dataset-level aggregation above the per-file decisions |
| `cmd/scan.go`, `internal/resolver/cache.go` (concurrency, memoization, singleflight) | **Keep** pattern; re-target at adapter `ListFiles`/`GetRecord` |
| `internal/client/retry.go` (bounded retry, Retry-After, injectable sleep) | **Keep**; generalize header handling (`X-RateLimit-Reset`) |
| `internal/output/` (result types, tables, color), `internal/log/`, `internal/config/`, `internal/update/`, `internal/gitutil/`, `internal/picker/` | **Keep** as-is (config gains per-remote tokens) |
| `internal/client/osf.go`, `waterbutler.go`, `internal/resolver/path.go` | **Keep**, refactored *behind* the Backend interface as the OSF workspace adapter — tree-walk becomes an adapter-internal detail |
| `internal/client/wiki.go`, `cmd/wiki_*.go` | **Retire** after `migrate` can pull wiki pages into `docs/`; wiki syncing is replaced by the site generator |
| `internal/testutil/fakeosf/` | **Keep** (serves the OSF adapter's hermetic tier); build `fakeinvenio` alongside it |
| `cmd/skill_doc_test.go` (agent-skill parity test) | **Keep** — it must pass against the renamed skill from the first PR |
| New packages | `internal/backend` (interface + Caps), `internal/backend/invenio`, `internal/journal` (datapin versioning scheme), `internal/meta`, `internal/site`, `internal/testutil/fakeinvenio` |

## 5. Human-in-the-loop items (ask the maintainer, don't fake)

1. **Zenodo sandbox account + PAT** (`deposit:write` + `deposit:actions`)
   for Phase 0 and the live CI tier → repo secret `ZENODO_SANDBOX_TOKEN`.
   Sandbox tokens are separate from production; sandbox can be wiped at
   any time, so nothing may assume persistence.
2. **A production Zenodo token** much later — only when end-to-end
   verification against real Zenodo is wanted; never required for CI.
3. **Org-level settings** if branch protection/secrets APIs are
   restricted.
4. The two open decisions in §2 when they become blocking.

## 6. Working agreements (inherited from gosf — non-negotiable)

- **TDD**: failing test first, watch it fail, then implement. Bug fixes
  start with a regression test. No production logic without a test.
- **Quality gates per PR**: `gofmt -l .` prints nothing; `go vet ./...`;
  `go test -race ./...`; integration tag builds; `golangci-lint run ./...`
  → `0 issues`.
- **Branch model**: PRs target `dev`; `main` is the release branch;
  feature branches `claude/<slug>`; stubs never land.
- **Contracts**: `--output=json` on every command with typed results;
  result data on stdout, everything else stderr logs; non-zero exit on
  all error paths; context-aware cancellation; no `os.Exit` outside
  `Execute`.
- **Three test tiers**: unit / hermetic fake server / live (sandbox),
  plus the cross-adapter contract suite (plan §6).
- **Skill parity**: adding a command or flag without updating the skill
  fails CI — keep it that way through the rename.

## 7. Pointers

- Architecture, research, API details, roadmap: `docs/reboot-plan.md`
  (this directory). Sections most load-bearing for implementation:
  §2.4 (Zenodo API + gotchas), §4.1–4.7 (architecture), §6 (testing),
  §8 (phases).
- The gosf codebase itself is the reference implementation for every
  carried-over pattern; its CLAUDE.md documents the conventions in depth.
- Zenodo/InvenioRDM primary docs: developers.zenodo.org,
  inveniordm.docs.cern.ch/reference/rest_api_drafts_records/, and the
  zenodo/zenodo-rdm source repo (the running truth).
