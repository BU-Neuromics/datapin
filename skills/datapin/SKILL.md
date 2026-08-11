---
name: datapin
description: "Use when publishing or versioning research data files: minting DOIs for datasets on FAIR archive repositories (Zenodo, any InvenioRDM instance, Dataverse, Figshare), or syncing intermediate results to a workspace remote (directory/NAS, S3/MinIO, SFTP) with journal versioning. Invoke when: the project contains a .datapin/datapin.toml manifest (or a legacy .gosf/gosf.toml from datapin's previous life as gosf); the user mentions datapin, FAIR data publication, DOI minting, research data archiving, data citation, dataset landing pages, RO-Crate or Data Package metadata, DataCite/SPDX/ORCID metadata linting, an F-UJI FAIR score, Zenodo, InvenioRDM, Dataverse, or Figshare; the task involves tracking, versioning, or transferring large data files alongside a git repository; or the user mentions the Open Science Framework (OSF, osf.io, osfclient) — datapin still syncs OSF projects, and 'datapin migrate' is the one-command exit ramp off the sunsetting platform. Covers the full datapin CLI: archive remotes (datapin remote add / ls / rm), DOI-minting dataset publication (datapin publish), metadata linting and FAIR assessment (datapin check), standard metadata export (datapin export), citations (datapin cite), documentation sites with dataset landing pages (datapin site build / preview / publish), journal-versioned workspace remotes for cluster-to-laptop sync (datapin push <slug>, pull --workspace, versions, revert, gc), the OSF exit ramp (datapin migrate), and the frozen legacy OSF surface: manifest management (datapin init / add / status / sync), file transfer (datapin pull / push / rm), storage management (datapin mkdir / mv / cp), project navigation (datapin ls / info / projects / versions / open / set), project wikis (datapin wiki ls / get / push / rm / mv / versions / open / add), and authentication (datapin auth)."
metadata:
  version: "0.2.0"
---

# datapin — pin, sync, and publish research data

`datapin` is a single-binary CLI with two jobs:

1. **Archive publication (the primary job).** Group files into a dataset and
   `datapin publish` it to a FAIR repository — Zenodo or any InvenioRDM
   instance, Dataverse, or Figshare — as an immutable, versioned,
   DOI-carrying record, with metadata linting, standard metadata exports,
   citations, and a generated landing-page site.
2. **Workspace sync.** Move the same dataset's files to a mutable remote
   (a directory/NAS, S3-compatible storage, or SFTP) with journal
   versioning and no DOI ceremony — the "run on the cluster, pull on the
   laptop" loop.

Both are driven by one committed manifest, `.datapin/datapin.toml`, which pins
every file to an exact version and checksum.

**OSF is legacy.** datapin began life as `gosf`, an Open Science Framework
client, and still speaks to OSF. OSF is sunsetting its projects service, so
that support is **frozen** (working, tested, receiving no new investment) and
will be removed in the major release after the shutdown. Steer new work to
archive/workspace remotes, and use `datapin migrate` to move existing OSF
projects off. See "Legacy: the OSF surface" at the end.

## Installation

If `datapin` is not already on `PATH` (`datapin --version` to check), install it:

```bash
# Linux / macOS — downloads the right binary, verifies the checksum
curl -fsSL https://raw.githubusercontent.com/BU-Neuromics/datapin/main/install.sh | bash

# With Go
go install github.com/BU-Neuromics/datapin@latest
```

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/BU-Neuromics/datapin/main/install.ps1 | iex
```

Or download a prebuilt archive from
<https://github.com/BU-Neuromics/datapin/releases> and put `datapin` on `PATH`.
It is a static binary with no runtime dependencies — suitable for HPC nodes.

## The manifest: `.datapin/datapin.toml`

Lives at the repository root (datapin walks up from the current directory to
find it) and is meant to be committed. Three parts matter:

- `[[datasets]]` — the publishable unit (archive) and the unit of workspace
  sync.
- `[site]` + `[[site.pages]]` — the generated documentation site.
- `[[files]]` / `[[wikis]]` — the legacy OSF surface.

```toml
[project]
default_archive   = "zenodo"       # remote name; used by datasets with no `archive`
default_workspace = "nas"          # ditto for `workspace`

[[datasets]]
slug        = "counts"             # local handle: publish/check/pull/cite/versions use it
archive     = "zenodo"             # optional per-dataset override of default_archive
workspace   = "nas"                # optional: the mutable, DOI-free track
record      = "1234567"            # filled in by the first publish — do not hand-edit
concept     = "1234566"
concept_doi = "10.5281/zenodo.1234566"
version     = 2
version_doi = "10.5281/zenodo.1234567"
  [datasets.metadata]
  title         = "Aligned RNA-seq count matrices"   # DataCite mandatory
  description   = "Gene-level counts for 48 cortical samples."
  license       = "CC0-1.0"        # SPDX id — REQUIRED to publish
  keywords      = ["RNA-seq", "cortex"]
  resource_type = "dataset"
  publisher     = "Boston University"
  contact_email = "data@example.edu"     # REQUIRED by dataverse remotes
  [[datasets.metadata.creators]]         # DataCite mandatory: at least one
  name        = "Labadorf, Adam"         # "Family, Given"
  orcid       = "0000-0002-1825-0097"    # checksum-validated
  affiliation = "Boston University"
  [[datasets.metadata.related]]
  identifier = "10.1000/paper"
  relation   = "IsSupplementTo"          # a DataCite relationType
  [[datasets.files]]
  local = "results/counts.h5"      # path relative to the repo root
  key   = "counts.h5"              # name on the archive; defaults to the basename
  md5   = "d41d8cd98f00b204e…"     # pinned by publish/push

[site]
title    = "Cortical RNA-seq"
base_url = "https://org.github.io/repo"   # sitemap + absolute JSON-LD URLs
repo     = "org/repo"                     # gh-pages target; default: the git origin
[[site.pages]]
local = "docs/index.md"                   # slug defaults to the basename
```

A `local` path may appear in only one of `[[datasets]]`, `[[files]]`, and
`[[wikis]]`. The manifest is rewritten atomically after every successful
publish, push, pull, or sync — never hand-edit the pins.

## Remotes and tokens

Named remotes live in `~/.config/datapin/config.toml`. **Kind implies role**:
`invenio`/`figshare`/`dataverse` are archive remotes (DOIs), `dir`/`s3`/`sftp`
are workspace remotes (no DOIs). Tokens are stored per remote, never in
`config.toml`, and resolved as: `DATAPIN_TOKEN_<NAME>` env var → OS keychain →
`~/.config/datapin/tokens/<name>`.

```bash
datapin remote add <url> --name <name> [--kind invenio|figshare|dataverse|dir|s3|sftp] \
    [--token-value <tok>] [--no-verify] [--no-keychain]
datapin remote ls [--output=json]        # name, kind, URL, and whether a token is stored
datapin remote probe <name>              # re-probe an archive remote's capabilities
datapin remote rm <name>                 # remove a remote and delete its stored token
```

`remote add` probes the URL (archive kinds: does it answer like that API;
workspace kinds: can it be listed) and refuses with a hint to pass
`--no-verify` if not. `--name` is required.

**Per-instance capabilities (InvenioRDM).** The probe also records what the
instance declares about itself under `[remotes.<name>.caps]` in
`config.toml`, so no later command re-probes and institutional instances are
not assumed to behave like zenodo.org:

- `resource_types` — the instance's resource-type vocabulary. `datapin check`
  errors when a dataset's `resource_type` is not in it, because publishing
  would be rejected.
- `multipart_upload` — inferred from the file schema the instance serves
  (pre-InvenioRDM-v13 instances have no multipart transfer). Absent when it
  could not be determined; a large upload falls back to a single PUT if the
  instance rejects the multipart transfer type.
- `max_files_per_record` / `max_file_size` — **not** exposed by the
  InvenioRDM API, so they are absent by default and datapin uses the
  documented Zenodo profile (100 files, 50 GB). Add them to the caps table by
  hand to match an institutional instance's real limits; hand-edited values
  always win.

`datapin remote probe <name>` re-probes in place — for a vocabulary that grew,
or a remote added with `--no-verify`. A failed probe leaves the stored values
untouched. Only what the API actually exposes is stored; the rest is reported
as a documented default rather than guessed at.

### Archive kinds

| Kind | Notes |
|------|-------|
| `invenio` | Zenodo and any InvenioRDM instance. Per-version DOIs under a stable concept DOI; unchanged files import server-side on a new version; files over 100 MiB upload multipart when the instance supports it (see per-instance capabilities below). |
| `figshare` | Per-version `.vN` DOIs under a stable base DOI. A new version is the mutable account draft — no server-side files import. |
| `dataverse` | **One DOI for all versions** (version picker on the landing page). Collection alias rides on the URL: `https://host/dataverse/<alias>` (default `root`). Requires `contact_email` in the dataset metadata. |

Zenodo sandbox (`https://sandbox.zenodo.org`) and production
(`https://zenodo.org`) are **separate services with separate accounts and
tokens** — add both as remotes and rehearse on the sandbox. Sandbox DOIs
(prefix `10.5072`) never resolve.

### Workspace kinds

| Kind | URL form | Credentials |
|------|----------|-------------|
| `dir` | a plain filesystem path (`/mnt/lab-share`) | none |
| `s3` | `s3://<endpoint>/<bucket>[/<prefix>]` (`?insecure=true` for plain-HTTP MinIO, `?region=…`) | per-remote token as `ACCESSKEY:SECRETKEY`, else `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` |
| `sftp` | `sftp://user@host[:port]/base/path` | `SSH_AUTH_SOCK` agent → `~/.ssh/id_ed25519`/`id_rsa` → `DATAPIN_SFTP_PASSWORD`; host keys must verify against `~/.ssh/known_hosts` |

```bash
datapin remote add /mnt/lab-share --name nas --kind dir
datapin remote add s3://minio.lab:9000/bucket/prefix --name obj --kind s3 --token-value "ACCESS:SECRET"
datapin remote add sftp://user@cluster/scratch/proj --name hpc --kind sftp
```

## Archive publication (DOIs)

### Publish

```bash
datapin publish [<slug>] [--dry-run] [--yes] [--force] [--reserve] [--output=json]
datapin versions <slug>                  # archive version chain: version, record, DOI, pin
datapin pull <slug> [--latest]           # fetch published bytes (pinned; --latest re-pins)
datapin open <slug>                      # the record's landing page (browser)
```

`datapin publish` promotes a dataset's current files to a published,
immutable, DOI-carrying version. **Publishing is PERMANENT and PUBLIC** — a
published version cannot be edited or deleted and its DOI resolves forever.
Rehearse on a sandbox remote first. With no slug, every dataset with
publishable changes is processed. Every publish prints the DOI and a
paste-ready citation.

The transaction: open a draft (create, or an idempotent new version) → import
the previous version's files server-side where supported → plan per file key
(upload/replace/keep/remove; only changed bytes transfer) → refresh metadata →
publish → atomically re-pin the manifest. A failure before the publish step
discards the draft, so a crashed run leaves no half-record.

Two hard requirements at the publish boundary:

- **A license is required.** `datapin check` only *warns* about a missing
  license (drafting without one is fine); `publish` refuses. datapin never
  fills a license in and drivers never substitute one — the SPDX id is
  resolved against the target instance's own license registry, and an id it
  does not offer is a loud error listing what it does.
- **`contact_email` is required for `dataverse` remotes** (their citation block
  mandates a Point of Contact). Other backends ignore the field.

Flag semantics:

- `--reserve` uploads everything and reserves the DOI but does NOT publish —
  the DOI can go into a manuscript first; the draft survives, and publishing
  again without `--reserve` makes it live.
- `--force` publishes on top of a remote version the manifest pin has not seen
  (otherwise refused — the dataset analogue of `REMOTE_NEWER`).
- `--yes` skips the confirmation prompt, and **`--output=json` refuses to
  publish without it** (`--force`, `--reserve`, and `--dry-run` also satisfy the
  gate, since each states an explicit intent).
- `--dry-run` prints the full plan and uploads nothing.

A pinned `datapin pull <slug>` verifies every file against the pin and **fails
hard if the archive's checksum contradicts it** rather than delivering
different bytes; `--latest` is the deliberate way to move the pin.

### Metadata quality and standard exports

```bash
datapin check [<slug>] [--fair]          # lint metadata; exit 1 on errors
datapin export <slug> [--datapackage] [--ro-crate] [--dir <path>]
datapin cite <slug> [--bibtex]           # paste-ready citation via DOI content negotiation
```

`datapin check` errors are exactly what `publish` refuses: no title, no
creators, a blank creator name, a malformed ORCID (ISO 7064 checksum), a
`license` that is not an SPDX id (with a did-you-mean), a `resource_type` the
target instance's probed vocabulary does not offer, a blank related identifier
or a non-DataCite `relation`, a file set over the backend's record cap.
Warnings are FAIR nudges: no description, no keywords, no ORCID, **no
license**, an NC/ND license, a zero-byte file. Exit 0 when there are no errors
(warnings allowed), 1 otherwise — CI-friendly.

`check --fair` additionally runs an F-UJI FAIR assessment of each published
DOI. F-UJI probes the public record, so it needs a resolving, non-sandbox DOI;
configure `DATAPIN_FUJI_URL`, `DATAPIN_FUJI_USER`, `DATAPIN_FUJI_PASS`.

`datapin export` writes `datapackage.json` (Data Package v2) and/or
`ro-crate-metadata.json` (RO-Crate 1.2) — both when neither flag is given —
into `--dir` (default: the repository root). datapin emits existing standards;
it never invents a metadata schema.

`datapin cite` asks DataCite to format the citation for the minted DOI; for
sandbox DOIs (which never resolve) it renders locally from the manifest
metadata. `DATAPIN_DOI_BASE` overrides the resolver (tests).

### Documentation site

`datapin site` renders a static site from the manifest — markdown pages under
`[[site.pages]]` plus a generated, citation-ready landing page per dataset
(DOI links, file checksums, schema.org JSON-LD for Google Dataset Search) —
and deploys it as a single orphan commit force-pushed to the `gh-pages` branch
(no workflow file, no site history). `site build` is fully offline and
deterministic.

```bash
datapin site build [--out <dir>]                               # render into <repo>/public
datapin site preview [--addr localhost:8383] [--out <dir>]     # build + serve locally
datapin site publish [--github-token <tok>] [--out <dir>]      # build + push to gh-pages
```

`site publish` finds a GitHub token via `--github-token` → `GITHUB_TOKEN` →
`GH_TOKEN` → `gh auth token`; without one the push uses the git credential
helper and the first-run Pages toggle is skipped with a note.

## Workspace remotes (cluster→laptop intermediate results)

A dataset can also sync to a mutable **workspace** remote — no DOIs, no
metadata ceremony. Every workspace kind gets identical journal versioning:
before an overwrite the superseded bytes are archived server-side under
`.datapin/versions/<key>/<md5>`, and an append-only journal under
`.datapin/journal/` narrates every push and revert. **Every version datapin
wrote is revertible** until `gc` reclaims it.

```bash
datapin push <slug>                       # dataset files → workspace (journaled)
datapin pull <slug> --workspace           # fetch the current workspace bytes
datapin versions <slug>/<key>             # one file's journal: pushes, reverts, recoverability
datapin revert <slug>/<key> --to <n> [--reason <why>]   # journaled restore
datapin gc [--keep N]                     # reclaim archived versions (default: keep 3 per file)
```

- Set `workspace = "<name>"` on the dataset, or `default_workspace` under
  `[project]`. An unpublished dataset with a workspace pulls from it by
  default; a published one needs `--workspace` to prefer workspace bytes over
  archive bytes.
- `<slug>/<key>` splits on the **first** `/`, so keys may themselves contain
  slashes (`datapin versions counts/results/counts.h5`).
- `revert` is a **new** journal event — history never rewrites, and the
  regretted version stays retrievable. `--to` takes the journal sequence
  number shown by `versions`; `--reason` is recorded in the journal. Run
  `datapin pull <slug> --workspace` afterwards to update the local copy.
- `gc --keep N` runs across every workspace remote the manifest's datasets
  use; the current version never counts against the budget. Reclaimed versions
  stay listed in the journal and report themselves unrecoverable.
- Out-of-band overwrites (someone `scp`s over a tracked file) are detected,
  the foreign bytes archived under their own content address, and the fact
  recorded in the journal.

## Dataset status

`datapin status` shows one row per dataset alongside the legacy file/wiki rows:

| State | Meaning |
|-------|---------|
| `NOT_PUBLISHED` | The dataset has never been published |
| `IN_SYNC` | Local files match the published version |
| `MISSING` | Local files are absent (`datapin pull <slug>`) |
| `AHEAD` | Local changes since the published version (`datapin publish <slug>`) |
| `REMOTE_NEWER` | The archive has a newer version (`datapin pull <slug> --latest`) |
| `DIVERGED` | Local changed AND the archive moved |

In `--output=json` each row carries `"kind": "dataset"|"file"|"wiki"`.

## Guided setup

```bash
datapin onboard [--no-keychain] [--osf] [--project <guid>] [--remote-base <path>]
```

`datapin onboard` is a resumable, TTY-only wizard for the publish workflow:
choose an archive remote (Zenodo sandbox is option 1 and the default — the
rehearsal path) → group git-untracked files into a `[[datasets]]` entry via a
tree checkbox UI → fill in the DataCite floor (title, creators with validated
ORCIDs, an explicit license choice — CC0-1.0 suggested, CC-BY-4.0 the named
alternative, nothing ever prefilled; contact e-mail when the target is
Dataverse) → optionally add a workspace remote for mutable intermediate
results. It writes the manifest, lints it, and stops, pointing at
`datapin check <slug>` + `datapin publish <slug>`.

`--osf` runs the legacy OSF workspace wizard instead (deprecated — OSF is
sunsetting; `--project` and `--remote-base` apply only there and are refused
without it).

**For agents: do not run `onboard`** — it requires an interactive terminal.
Use `datapin remote add` and write `.datapin/datapin.toml` directly.

## Migrate off OSF

```bash
# GUID mode: export any OSF project (anonymous works for public projects)
datapin migrate abc12 [dest]        # files + wikis (docs/<page>.md) + fresh manifest
datapin migrate abc12 --components  # also export each component as its own dataset
                                    # (default: root only, with a notice)

# Manifest mode: convert a repo whose manifest tracks OSF, in place
datapin migrate                     # fetch MISSING/BEHIND first (DIVERGED fails hard),
                                    # group [[files]] into [[datasets]] (one per
                                    # top-level directory), [[wikis]] → site pages,
                                    # rewrite the manifest with OSF sections removed
datapin migrate --dataset raw=data/raw/**   # override grouping (repeatable)

# Both modes
datapin migrate ... --dry-run       # print the full plan, write nothing

# Manifest mode only: skip the TTY confirmation before the manifest rewrite
datapin migrate --yes               # (GUID mode never prompts)
```

`migrate` turns an OSF project into a datapin project: files downloaded and
MD5-verified, wiki pages exported as markdown site pages, node metadata mapped
to a dataset metadata skeleton, `[[datasets]]` scaffolded. A `MIGRATED.md`
provenance breadcrumb (source GUID, timestamp, TODO checklist) is written next
to the data, and each dataset carries an `IsDerivedFrom` related identifier
pointing at the osf.io origin.

What OSF cannot supply (license, ORCIDs, contact email) is written as a literal
`TODO` marker, which is deliberately invalid: `TODO` is not an SPDX id, so
`datapin check` errors and `publish` refuses — an unreviewed migration can never
publish by accident. Re-running is idempotent: MD5-identical files are skipped
and metadata you have edited is preserved per dataset slug. Nothing is uploaded
and no DOI is minted. Under `--output=json` there are no prompts and exit code
1 signals that TODOs remain (the export itself succeeded).

## Legacy: the OSF surface

**Frozen.** Everything below still works and is still tested, but OSF is
sunsetting its projects service and this surface will be removed in the major
release after the shutdown. Prefer archive/workspace remotes for new work, and
`datapin migrate` for existing OSF projects.

### Path syntax

```
abc12                         # project/component GUID (5 chars)
abc12:/data/results.csv       # file inside OSF Storage
abc12:/data/                  # folder inside OSF Storage
abc12/xyz34:/path             # path inside component xyz34 of project abc12
```

GUIDs appear in OSF URLs: `https://osf.io/abc12/` → `abc12`. A bare argument
with no `:` is interpreted as a **dataset slug**, not a GUID, by `pull`,
`push`, `versions`, and `open`.

### Authentication

Public OSF projects are readable without auth; private data and any write need
a token from <https://osf.io/settings/tokens/>. Resolved in priority order:

1. `--token` flag
2. `OSF_TOKEN` environment variable
3. `~/.config/datapin/token` (written by `datapin auth login --no-keychain`)
4. OS keychain (written by `datapin auth login`)

```bash
datapin auth login [--no-keychain]   # prompt for and store a token
datapin auth status                  # who you are and where the token came from
datapin auth logout                  # remove the token file, best-effort keychain
```

On HPC/headless systems use `OSF_TOKEN` or `--no-keychain`. `datapin auth
logout` always removes the token file and only warns if the keychain is
unavailable. This token is OSF-only — archive and workspace remotes use their
own per-remote tokens.

### `[[files]]` / `[[wikis]]` entries

```toml
[project]
id = "abc12"          # default project GUID for all entries

[[files]]
local   = "data/counts.h5"       # path relative to repo root
remote  = "/data/counts.h5"      # path in OSF Storage
version = 3                      # pinned OSF version; 0 = not yet pushed
md5     = "d41d8cd98f00b204..."  # MD5 of pinned version; "" if version=0
project = "xyz89"                # optional: override [project].id for this entry

[[wikis]]
local   = "docs/home.md"   # markdown file, relative to repo root
page    = "home"           # wiki page name on OSF (flat namespace, may contain spaces)
version = 3                # pinned wiki version; 0 = not yet pushed
md5     = "…"              # MD5 of the pinned version's content, computed by datapin
project = "xyz89"          # optional: override [project].id for this entry
```

Wiki entries flow through the same states, gates, and `--force`/`--resolve`
handling as files.

**There is no per-entry direction.** What a transfer should do is decided at
the moment of the transfer, by comparing local content, the pinned baseline,
and the remote. Manifests written by datapin ≤ 1.9 still carry a `direction`
key: it is ignored with a warning on load and dropped the next time datapin
writes the file. No migration is needed.

### File states

From `datapin status`, comparing Local / pinned Baseline / Remote:

| State | Meaning |
|-------|---------|
| `IN_SYNC` | Local matches the pinned version and the remote |
| `PIN_ONLY` | Local already matches the remote, but the pin is stale/absent — record it, no transfer |
| `MISSING` | Local file does not exist |
| `BEHIND` | Local matches an older remote version |
| `AHEAD_OF_MANIFEST` | Only local changed since the baseline |
| `REMOTE_NEWER` | Only the remote changed since the baseline |
| `DIVERGED` | Both local and remote changed — unsafe to transfer automatically |
| `NOT_PUSHED` | version = 0 and nothing on the remote to compare |

`datapin status` is read-only and exits 0 only if all entries are `IN_SYNC`, 1
otherwise — safe to use in CI. It content-compares unpinned (`version=0`)
entries against the remote instead of blindly reporting "never pushed".

### Manifest commands

```bash
datapin init <project-id>                              # create/update .datapin/datapin.toml
datapin add <local-path> [<project>:]<remote-path>     # track file(s) (dir = recursive)
datapin status [--no-check-remote] [--jobs=N] [--output=json]
datapin sync [--force] [--resolve=ours|theirs] [--dry-run] [--no-check-remote] [--jobs=N] [--output=json]
```

`datapin add` registers a local file; `datapin pull` registers what it
downloads. Both are just ways to get an entry into the manifest — neither fixes
which way the file will move later. If the remote path is omitted it mirrors the
local path.

`datapin sync` reconciles every entry that has one unambiguous answer, and
reports the ones that do not:

| State | Action |
|-------|--------|
| `IN_SYNC` | Skip |
| `PIN_ONLY` | Record the pin (version+MD5), no transfer |
| `MISSING` | Download it, pin — no flag needed; nothing local is at risk |
| `BEHIND` | Fast-forward to the remote's latest, re-pin |
| `REMOTE_NEWER` | Fast-forward to the remote's latest, re-pin |
| `NOT_PUSHED` (file exists) | Upload, set manifest version+MD5 |
| `NOT_PUSHED` (file missing) | Skip — there is nothing anywhere |
| `AHEAD_OF_MANIFEST` | **Report only**, no transfer; `sync` exits non-zero |
| `DIVERGED` | **Fail hard** before any transfer — requires `--resolve=ours\|theirs` |

`AHEAD_OF_MANIFEST` is the one state `sync` will not guess at: the same
difference means "publish this" for a generated output and "throw this away" for
an edited input, and no hash comparison tells them apart. Say which you meant
with the verb — `datapin push` publishes it, `datapin pull --force` (or
`datapin sync --force`) discards it.

- `--force` on `sync`/`pull` discards local modifications, restoring the tracked
  version from OSF. It does **not** cover divergence.
- `--resolve=ours` keeps local; `--resolve=theirs` keeps remote. Whichever you
  pass is honored as given — nothing on the entry overrides it.

### File transfer

```bash
datapin pull <project>[:<path>] [dest] [--version=N] [--force] [--resolve=theirs] [--no-track] [--dry-run]
datapin pull <project>:<path> <dest> --track-only      # register entries, transfer nothing
datapin pull [--force] [--resolve=theirs] [--jobs=N]   # no args: pull tracked entries
datapin push <src> <project>:<path> [--conflict=skip|overwrite|rename] [--no-track] [--dry-run]
datapin push [--yes|--force] [--resolve=ours] [--no-check-remote] [--jobs=N]   # no args: push manifest entries
datapin rm <project>:<path> [--yes] [--dry-run]
```

- Both `pull` and `push` are **idempotent**: a transfer whose content already
  matches is skipped (no redundant version, no needless download).
- Explicit `push <src> <dest>` uses `--conflict` (default `skip`; `overwrite`
  creates a new version; `rename` → `name_1.ext`).
- Bare `datapin push` (manifest-driven) prints a per-file plan and prompts before
  writing remote data. `--yes` skips the prompt for safe pushes; `--force` also
  authorizes a rollback. **In `--output=json`, `--force` is required** (no prompt).
- `pull --version=N` fetches a historical version (single-file targets only).
- `pull --track-only` registers a remote subtree in the manifest without moving
  any bytes, so a large project can be adopted and reviewed before it is
  downloaded. The entries land as `MISSING`; a plain `datapin sync` then fetches
  them. `sync` only ever visits entries in the manifest, so this is how remote
  files that nothing tracks become visible to it.

### Storage management and navigation

```bash
datapin mkdir <project>:<path> [--dry-run]   # create a folder (parent must exist)
datapin mv <src> <dest> [--conflict=warn|replace|keep] [--dry-run]
datapin cp <src> <dest> [--conflict=keep|replace|warn] [--dry-run]
datapin ls <project>[:<path>] [--output=json]     # list files/folders
datapin info <project> [--output=json]            # project metadata
datapin projects [--output=json]                  # accessible projects (needs auth)
datapin versions <project>:<path> [--output=json] # file versions (files only, not folders)
datapin open <project>[:<path>] [--output=json]   # open in browser (or print URL)
datapin set <project> [--title ...] [--description ...] [--category ...] [--tags ...]
```

`datapin set` sends only the flags you pass; at least one is required.
New files and folders upload into the parent folder resolved from OSF
(osfstorage addresses folders by ID, not name), so the parent must exist —
`datapin mkdir` first if needed.

### Wiki pages

OSF projects have a wiki: versioned markdown pages, addressed as
`<project>:<page>`. The part after the colon is a **page name**, not a path — a
flat namespace that may contain spaces — and defaults to `home` where optional.
Component addressing (`abc12/xyz34:page`) works as for files.

```bash
datapin wiki ls   <project> [--output=json]                  # list pages
datapin wiki get  <project>[:<page>] [dest] [--version=N] [--force]
datapin wiki push <src.md> <project>[:<page>] [--dry-run]    # create page or add a version
datapin wiki rm   <project>:<page> [--yes] [--dry-run]
datapin wiki mv   <project>:<page> <new-name> [--dry-run]    # rename
datapin wiki versions <project>:<page> [--output=json]
datapin wiki open <project>[:<page>]
datapin wiki add  <local.md> [<project>:]<page>              # track as a [[wikis]] entry
```

- `wiki get` prints to stdout by default; pass a `dest` to write a file
  (`--force` overwrites an existing one).
- `wiki push` creates the page if absent, otherwise mints a new version; an
  identical re-push is skipped rather than minting a redundant version.
- The `home` page cannot be renamed or deleted — datapin refuses client-side.
- `wiki add` tracks a markdown file so `datapin status` / `datapin sync` reconcile the
  page alongside files. `status`/`sync` items carry `"kind": "file"|"wiki"`.
- **Content is canonicalized, not byte-exact.** OSF normalizes wiki content on
  save (CRLF → LF, surrounding whitespace trimmed), so datapin compares a canonical
  form. A local file differing from the page only in line endings or a trailing
  newline still counts as in sync, and re-pushing it is a no-op. Do not diff raw
  bytes against what you pushed.
- A project can have its wiki addon disabled, which produces an actionable error
  rather than a bare 404. Registrations are read-only.

Migrating a wiki off OSF: `datapin migrate` exports every page as a markdown
site page, and `datapin site` renders them — that is the replacement.

### gosf back-compat

A legacy `.gosf/gosf.toml` manifest is found and loaded read-only (rename it to
`.datapin/datapin.toml` to write), `~/.config/gosf` config/tokens are read when
the datapin ones are absent, and `GOSF_*` environment variables are accepted
with a deprecation warning.

## Common workflows

### Publish a dataset with a DOI (the main path)

```bash
datapin remote add https://sandbox.zenodo.org --name sandbox --token-value "$TOK"
# write .datapin/datapin.toml: default_archive = "sandbox", one [[datasets]] block
datapin check counts               # fix every error it reports (license included)
datapin publish counts --dry-run   # read the plan
datapin publish counts --yes       # mint the DOI
datapin cite counts                # paste-ready citation
```

Rehearse on the sandbox, then re-add production Zenodo as a second remote,
point `default_archive` at it, clear the `record`/`concept`/`version` pins, and
publish for real.

### Publish a second version

```bash
# edit results/counts.h5, then:
datapin status                     # the dataset row reads AHEAD
datapin publish counts --yes       # only changed bytes upload; a new version DOI is minted
datapin versions counts            # the chain, with the concept DOI stable across it
```

### Cluster → laptop, no DOI

```bash
# on the cluster
datapin remote add sftp://me@cluster/scratch/proj --name hpc --kind sftp
datapin push counts
# on the laptop, after git pull
datapin pull counts --workspace
# something went wrong in v3:
datapin versions counts/counts.h5
datapin revert counts/counts.h5 --to 2 --reason "batch 3 mislabeled"
datapin pull counts --workspace
```

### Move an OSF project to datapin

```bash
datapin migrate abc12 ./my-project --dry-run   # read the plan first
datapin migrate abc12 ./my-project --yes
cd my-project
$EDITOR .datapin/datapin.toml                  # resolve every TODO (license, ORCIDs)
datapin check                                  # must be clean before publish
```

### Check whether everything is in sync (CI)

```bash
datapin status --no-check-remote   # fast: no remote API calls
datapin status                     # full: checks the remote/archive too
# exits 0 if everything is IN_SYNC, 1 otherwise
datapin check                      # exits 1 while any metadata error remains
```

## Global flags

| Flag | Description |
|------|-------------|
| `--token <tok>` | OSF token (overrides env/file/keychain). Archive/workspace remotes use per-remote tokens instead. |
| `--output json` | JSON to stdout; activity logging/color suppressed |
| `--color auto\|always\|never` | Colorize output (default `auto`) |
| `--verbose` / `-v` | Increase log verbosity (repeatable: `-v`/`-vv`/`-vvv`) |
| `--progress-bar` / `-p` | Live progress bars for transfers (default: log lines) |
| `--quiet` / `-q` | Errors only (conflicts with `-v`) |
| `--version` | Print datapin version |

`--jobs` / `-j` is **not** global: it is accepted by the manifest-scanning
commands (`sync`, `status`, and bare `push`/`pull`) and bounds how many entries
are checked against the remote concurrently (default 8).

## Output streams and logging

`datapin` prints **results to stdout and activity to stderr**. stdout carries only
the machine/result surface (`ls`/`status`/`versions`/`projects` tables,
`info`/`set` fields, and all `--output=json` payloads); everything else — the
remote-scan phase, per-file transfers, skips, and `add`/`init`/`cp`/`mv`/`mkdir`/
`rm` confirmations — is a leveled activity log on stderr. Default level shows
high-level activity; `-v`/`-vv`/`-vvv` add detail, `--quiet` drops to errors.
Transfers log a one-line summary by default; pass `-p` for a live progress bar.

**For agents:** parse stdout only (redirect `2>/dev/null` if activity noise is
unwanted, or `2>run.log` to keep it). Prefer `--output=json`, which keeps stdout
pure JSON and silences stderr logging unless `-v` is given.

## JSON output

Every command supports `--output=json` (stdout; errors/activity on stderr). JSON
is never colorized. Because JSON mode never prompts, commands that write remote
data need their intent stated explicitly: `datapin publish` requires `--yes`,
bare `datapin push` requires `--force`, and `datapin rm` / `datapin wiki rm`
require `--yes`.

## Constraints to respect

- **`datapin publish` is irreversible.** A published version cannot be edited or
  deleted and its DOI resolves forever. Never publish to a production remote
  without the user's explicit go-ahead; rehearse on a sandbox remote.
- **Never invent a license or a contact e-mail.** Both are user decisions;
  datapin deliberately refuses to publish without a license rather than
  defaulting to one, and a `TODO` left by `datapin migrate` must be resolved by
  the user, not guessed at.
- Do not hand-edit a dataset's `record`/`concept`/`version`/`md5` pins — they are
  datapin's contract with the archive, written atomically after each publish.
- `datapin onboard` needs a TTY; use `datapin remote add` plus manifest edits.
- A `DIVERGED` file (changed both locally and remotely) blocks `sync`/`push`/`pull`
  until you pass `--resolve=ours|theirs`; plain `--force` will not override it.
- `--force` means different things by command, and each is a deliberate,
  one-sided discard: on `sync`/`pull` it discards local modifications and
  restores the tracked version; on `push` it authorizes a rollback (burying a
  newer remote version) and bypasses the confirmation prompt; on `publish` it
  publishes over an unseen remote version.
- `datapin versions <project>:<path>` works on files, not folders.
- New OSF files/folders upload into the parent folder resolved from OSF
  (osfstorage addresses folders by ID); the parent must exist
  (`datapin mkdir` first if needed).
- The manifest is updated atomically after every successful push, pull, sync, or
  publish.
