# datapin

[![CI](https://github.com/BU-Neuromics/datapin/actions/workflows/ci.yml/badge.svg)](https://github.com/BU-Neuromics/datapin/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/BU-Neuromics/datapin)](https://github.com/BU-Neuromics/datapin/releases)

**Pin, sync, and publish research data.** `datapin` is a fast, single-binary
CLI that publishes your data to a FAIR archive with a DOI — and, before it is
ready for that, keeps it moving between the cluster and your laptop. Both
tracks are driven by one committed manifest (`.datapin/datapin.toml`) that pins
every file to an exact version and MD5, so a `git clone` is enough to get the
bytes back.

```console
$ datapin publish counts      # dataset → Zenodo/Dataverse/Figshare record + DOI + citation
$ datapin check --fair        # DataCite/SPDX/ORCID lint + F-UJI FAIR score
$ datapin cite counts         # paste-ready citation for the minted DOI
$ datapin site publish        # dataset landing pages → GitHub Pages
$ datapin push counts         # same files → workspace remote (versioned, no DOI)
```

Two kinds of remote, one manifest:

| | **Archive remote** | **Workspace remote** |
|---|---|---|
| For | finished, citable data | intermediate results in flight |
| Kinds | `invenio` (Zenodo + any InvenioRDM), `dataverse`, `figshare` | `dir` (mounted NAS), `s3` (MinIO/R2/institutional), `sftp` (any cluster) |
| Identity | a DOI, resolving forever | a name; no DOI, no metadata ceremony |
| Mutability | published versions are immutable | overwrite freely; superseded bytes are archived |
| Verb | `datapin publish <slug>` | `datapin push <slug>` |
| History | version chain + concept DOI | append-only journal, `revert` to any version |

Everything is a single static binary — no Python, no virtualenv, drop it on an
HPC node and go.

## Publish a dataset in five minutes

```console
$ datapin remote add https://sandbox.zenodo.org --name sandbox   # rehearse on sandbox first!
$ datapin onboard                  # guided: pick the remote, the files, the metadata
$ datapin check                    # metadata lint: exactly what publish would refuse
$ datapin publish counts           # plan → confirm → transaction → DOI + citation
$ datapin versions counts          # the version chain, DOIs, and your pin
```

`datapin onboard` writes the manifest for you. Written out by hand, a dataset
looks like this:

```toml
[project]
default_archive = "sandbox"        # remote name from `datapin remote add`

[[datasets]]
slug = "counts"                    # the local handle: publish/check/pull/cite use it
  [datasets.metadata]
  title       = "Aligned RNA-seq count matrices"
  description = "Gene-level counts for 48 cortical samples."
  license     = "CC0-1.0"          # required to publish; always your explicit choice
  keywords    = ["RNA-seq", "cortex"]
  [[datasets.metadata.creators]]
  name  = "Labadorf, Adam"
  orcid = "0000-0002-1825-0097"
  [[datasets.files]]
  local = "results/counts.h5"      # the archive key defaults to the basename
```

Publishing again after editing files opens a new version: unchanged files are
carried over server-side (no re-upload), only changed content transfers, and a
fresh version DOI is minted under the stable concept DOI. `datapin pull counts`
restores the pinned bytes anywhere the repo is cloned.

**Rehearse on a sandbox.** Publishing to production Zenodo is permanent and
public, and its DOIs resolve forever.
[sandbox.zenodo.org](https://sandbox.zenodo.org) is a separate service with its
own account and token; its DOIs (prefix `10.5072`) never resolve, which is
exactly what you want for a dry run.

## Installation

### Script (recommended)

**Linux / macOS**

```bash
curl -fsSL https://raw.githubusercontent.com/BU-Neuromics/datapin/main/install.sh | bash
```

Detects your OS and architecture, downloads the right binary, verifies the
SHA-256 checksum, and installs to `/usr/local/bin` (or `~/.local/bin` if
`/usr/local/bin` is not writable). Override the destination with
`DATAPIN_INSTALL_DIR`:

```bash
DATAPIN_INSTALL_DIR=~/.local/bin curl -fsSL https://raw.githubusercontent.com/BU-Neuromics/datapin/main/install.sh | bash
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/BU-Neuromics/datapin/main/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\Programs\datapin` and adds it to your user `PATH`.
Override with `$env:DATAPIN_INSTALL_DIR`.

### Pre-built binary (manual)

Download the archive for your platform from the
[releases page](https://github.com/BU-Neuromics/datapin/releases), verify the
checksum against `checksums.txt`, extract, and put `datapin` on your `PATH`:

```console
# Linux x86_64 example
tar -xzf datapin_*_linux_amd64.tar.gz
sudo mv datapin /usr/local/bin/
```

### With Go

```console
go install github.com/BU-Neuromics/datapin@latest
```

### From source

```console
git clone https://github.com/BU-Neuromics/datapin
cd datapin
go build -o datapin .
```

## The manifest (`.datapin/datapin.toml`)

The manifest lives at `.datapin/datapin.toml` in your repository root (datapin
walks up from the current directory to find it) and is meant to be committed.
It has three parts:

- **`[[datasets]]`** — the publishable unit. A slug, an optional `archive` /
  `workspace` remote name, the publish pins (`record`, `concept`,
  `concept_doi`, `version`, `version_doi`), a `[datasets.metadata]` block, and
  the files that travel together.
- **`[site]` + `[[site.pages]]`** — the generated documentation site.
- **`[[files]]` / `[[wikis]]`** — the legacy OSF workspace surface
  ([see below](#legacy-the-osf-surface)).

```toml
[project]
default_archive   = "zenodo"       # used by datasets with no `archive` of their own
default_workspace = "nas"          # ditto for `workspace`

[[datasets]]
slug      = "counts"
archive   = "zenodo"               # optional per-dataset override
workspace = "nas"                  # optional: the mutable, DOI-free track
record    = "1234567"              # filled in by the first publish
concept   = "1234566"
concept_doi = "10.5281/zenodo.1234566"
version     = 2
version_doi = "10.5281/zenodo.1234567"
  [datasets.metadata]
  title         = "Aligned RNA-seq count matrices"
  description   = "Gene-level counts for 48 cortical samples."
  license       = "CC0-1.0"        # SPDX id — required at publish
  keywords      = ["RNA-seq", "cortex"]
  resource_type = "dataset"
  publisher     = "Boston University"
  contact_email = "data@example.edu"   # required by Dataverse remotes
  [[datasets.metadata.creators]]
  name        = "Labadorf, Adam"
  orcid       = "0000-0002-1825-0097"
  affiliation = "Boston University"
  [[datasets.metadata.related]]
  identifier = "10.1000/paper"
  relation   = "IsSupplementTo"    # a DataCite relationType
  [[datasets.files]]
  local = "results/counts.h5"
  key   = "counts.h5"              # the name on the archive; defaults to the basename
  md5   = "d41d8cd98f00b204e…"     # pinned by publish/push

[site]
title    = "Cortical RNA-seq"
base_url = "https://org.github.io/repo"   # for the sitemap + absolute JSON-LD URLs
repo     = "org/repo"                     # gh-pages target; default: the git origin
[[site.pages]]
local = "docs/index.md"                   # slug defaults to the basename
```

The manifest is rewritten atomically after every successful publish, push,
pull, or sync — you rarely need to edit the pins by hand.

## Archive publication

### `datapin remote add <url> --name <name>`

Register a backend instance as a named remote in `~/.config/datapin/config.toml`.
Tokens are stored **separately** per remote — in the OS keychain, or a token
file on headless systems — never in `config.toml`.

```console
$ datapin remote add https://sandbox.zenodo.org --name sandbox
$ datapin remote add https://zenodo.org --name zenodo --token-value "$ZENODO_TOKEN"
$ datapin remote add https://demo.dataverse.org/dataverse/mylab --name dv --kind dataverse
$ datapin remote add https://api.figshare.com --name fig --kind figshare
$ datapin remote ls                 # names, kinds, URLs, and whether each has a token
$ datapin remote rm sandbox         # forget the remote and delete its stored token
```

| Flag | Meaning |
|------|---------|
| `--name <name>` | Required. The name datasets refer to. |
| `--kind invenio\|figshare\|dataverse` | Archive backend (default `invenio`). Also accepts the workspace kinds `dir\|s3\|sftp`. |
| `--token-value <tok>` | Store an API token for this remote now. |
| `--no-keychain` | Write the token to `~/.config/datapin/tokens/<name>` (mode `0600`) instead of the keychain. |
| `--no-verify` | Skip the connectivity probe (offline, or an instance that answers unusually). |

Per-remote token lookup order: `DATAPIN_TOKEN_<NAME>` environment variable →
OS keychain → `~/.config/datapin/tokens/<name>`.

Backend differences that are visible to you:

- **`invenio`** — Zenodo and any InvenioRDM instance. Per-version DOIs under a
  stable concept DOI. Unchanged files are imported server-side on a new
  version. Files over 100 MiB upload in parts (multipart `M` transfer)
  automatically.
- **`figshare`** — per-version `.vN` DOIs under a stable base DOI. A new
  version is the mutable account draft, so there is no server-side files
  import.
- **`dataverse`** — **one DOI for all versions** (the landing page carries a
  version picker). A collection alias rides on the remote URL as
  `https://host/dataverse/<alias>`, defaulting to `root`. Requires
  `contact_email` in the dataset metadata — Dataverse's citation block
  mandates a Point of Contact.

### `datapin onboard`

Guided, interactive setup for publishing a dataset — the easiest way to start.
It detects what is already configured and resumes at the first missing piece,
so it is safe to re-run:

1. **Choose where to publish** — the Zenodo sandbox is option 1 and the default
   (rehearse the whole flow there; its DOIs are fake and its records
   disposable), then Zenodo, any InvenioRDM instance, Dataverse, or Figshare.
   Existing remotes are offered for reuse; a new one is probed and its token
   stored.
2. **Select the files** for the dataset — a collapsible file-tree of the things
   git doesn't track (data, models, artifacts).
3. **Describe it** — title, creators with checksum-validated ORCIDs, and a
   license you choose explicitly (CC0-1.0 is suggested, CC-BY-4.0 is the named
   alternative, "decide later" is allowed; datapin never fills one in). A
   contact e-mail is required when the target is Dataverse.
4. **Optionally add a workspace remote** (`dir`/`s3`/`sftp`) for mutable
   intermediate results that need versioned sync but no DOI.

It writes `.datapin/datapin.toml`, lints the metadata, and stops there — run
`datapin check <slug>` and then `datapin publish <slug>` to mint a DOI.
Requires an interactive terminal; for scripting, use `datapin remote add` and
edit the manifest directly.

```console
$ datapin onboard
$ datapin onboard --no-keychain                           # headless token storage
$ datapin onboard --osf                                   # legacy OSF wizard (deprecated)
$ datapin onboard --osf --project abc12 --remote-base /inputs
```

### `datapin check [<slug>]`

Lint dataset metadata before you publish. **Errors are exactly what `publish`
will refuse**; warnings are FAIR nudges. Exit code 0 when there are no errors
(warnings allowed), 1 otherwise — so it drops straight into CI.

```console
$ datapin check                    # every dataset
$ datapin check counts             # one dataset
$ datapin check --fair             # also run an F-UJI FAIR assessment
```

Errors: a missing title, no creators, a malformed ORCID (ISO 7064 checksum), a
`license` that is not an SPDX id (with a did-you-mean), a related identifier
that is blank or carries a non-DataCite `relation`, a file set that exceeds the
backend's record cap.
Warnings: no description, no keywords, no ORCID for a creator, **no license**
(drafting without one is fine — publishing is not), an NC/ND license, a
zero-byte file.

`--fair` runs a full FAIR assessment of each published DOI through an
[F-UJI](https://www.f-uji.net) server. F-UJI probes the public record, so it
needs a resolving, non-sandbox DOI. The hosted service requires credentials:
set `DATAPIN_FUJI_URL`, `DATAPIN_FUJI_USER`, `DATAPIN_FUJI_PASS` (for it or a
self-hosted instance).

### `datapin publish [<slug>]`

Promote a dataset's current files to a published, immutable, DOI-carrying
record on its archive remote. With no slug, every dataset with publishable
changes is processed.

```console
$ datapin publish counts --dry-run     # the full plan, nothing uploaded
$ datapin publish counts               # plan → PUBLIC/PERMANENT confirm → transaction
$ datapin publish counts --reserve     # upload + reserve the DOI, do not publish yet
$ datapin publish counts --force       # publish over a remote version the pin hasn't seen
$ datapin publish counts --yes         # skip the prompt (required in --output=json)
```

**Publishing is PERMANENT and PUBLIC.** A published version cannot be edited
or deleted, and its DOI resolves forever. The confirmation plan spells out the
remote, the visibility, the license, and every file that will move.

What happens inside one publish (it is a transaction — a failure before the
publish step discards the draft, so a crashed run leaves no half-record):

1. Open a draft: create the record, or open an idempotent new version.
2. Import the previous version's files server-side, where the backend supports
   it (`invenio`).
3. Plan per file key: upload / replace / keep / remove. Only changed bytes
   transfer; files over 100 MiB go multipart on `invenio`.
4. Refresh the metadata from the manifest.
5. Publish, then atomically re-pin the manifest to the new record, version, and
   DOIs.

Two rules worth knowing before your first run:

- **A license is required to publish.** `check` only warns about a missing
  license, because drafting without one is reasonable; `publish` refuses. A
  licenseless publish is not neutral — the backend would apply its own default
  and grant rights you never chose. datapin never fills a license in and
  drivers never substitute one: your SPDX id is resolved against the target's
  own license registry, and an id the instance does not offer is a loud error
  listing what it does offer.
- **`contact_email` is required for Dataverse remotes**, which mandate a Point
  of Contact in the citation block. Other backends ignore the field.

`--reserve` is for the manuscript-first workflow: everything uploads and the
DOI is reserved, but the record stays a draft (and survives the run) so you can
cite the DOI in a paper and publish for real later.

### `datapin versions <slug>` / `pull <slug>` / `open <slug>`

```console
$ datapin versions counts              # the archive version chain: version, record, DOI, pin
$ datapin pull counts                  # download the pinned published version
$ datapin pull counts --latest         # fetch the archive's latest and re-pin to it
$ datapin open counts                  # the record's landing page, in your browser
```

A pinned `pull` verifies every downloaded file against the pin and **fails hard
if the archive's checksum contradicts it** rather than quietly delivering
different bytes. `--latest` is how you deliberately move the pin.

On a Dataverse remote every row of `versions` shows the same DOI — that
backend has one DOI for the whole dataset, with a version picker on the landing
page.

### `datapin cite <slug>` / `datapin export <slug>`

```console
$ datapin cite counts                  # APA-style text, via DOI content negotiation
$ datapin cite counts --bibtex         # BibTeX instead
$ datapin export counts                # both files, into the repository root
$ datapin export counts --datapackage  # datapackage.json (Data Package v2)
$ datapin export counts --ro-crate     # ro-crate-metadata.json (RO-Crate 1.2)
$ datapin export counts --dir metadata/
```

`cite` asks DataCite to format the citation for the minted DOI. Sandbox DOIs
(prefix `10.5072`) never resolve, so for those the citation is rendered locally
from the manifest metadata. `export` emits existing standards; datapin never
invents a metadata schema.

### `datapin site build | preview | publish`

Render a static documentation site from the manifest — your markdown pages
under `[[site.pages]]`, plus a generated, citation-ready landing page per
dataset (DOI links, file checksums, schema.org JSON-LD for Google Dataset
Search) — and deploy it as a single orphan commit force-pushed to `gh-pages`
(no workflow file, no site history).

```console
$ datapin site build                             # render into <repo>/public
$ datapin site build --out /tmp/site
$ datapin site preview                           # build + serve on localhost:8383
$ datapin site preview --addr localhost:9000 --out /tmp/site
$ datapin site publish                           # build + push to gh-pages, enable Pages
$ datapin site publish --github-token "$GH_PAT"
```

`site build` is fully offline and deterministic — landing-page citations render
from the manifest, no network. `site publish` finds a GitHub token via
`--github-token` → `GITHUB_TOKEN` → `GH_TOKEN` → `gh auth token`; without one
the push uses your git credential helper and the first-run Pages toggle is
skipped with a note.

## Workspace remotes

The other half of a lab's workflow: results that are not ready for a DOI but
still need to travel and still need history. Run the analysis on the cluster,
`datapin push` the outputs, `git clone` the repo on your laptop,
`datapin pull --workspace`, and you have the same bytes.

```console
$ datapin remote add /mnt/lab-share --name nas --kind dir
$ datapin remote add s3://minio.lab:9000/bucket/prefix --name obj --kind s3 \
    --token-value "$ACCESS_KEY:$SECRET_KEY"
$ datapin remote add sftp://user@cluster.example.edu/scratch/proj --name hpc --kind sftp
```

| Kind | URL form | Credentials |
|------|----------|-------------|
| `dir` | a plain filesystem path (`/mnt/lab-share`) — mounted NAS | none |
| `s3` | `s3://<endpoint>/<bucket>[/<prefix>]` (`?insecure=true` for plain-HTTP MinIO, `?region=…`) | per-remote token as `ACCESSKEY:SECRETKEY`, else `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` |
| `sftp` | `sftp://user@host[:port]/base/path` | `SSH_AUTH_SOCK` agent → `~/.ssh/id_ed25519`/`id_rsa` → `DATAPIN_SFTP_PASSWORD`; host keys must verify against `~/.ssh/known_hosts` |

The endpoint lives in the URL because the audience is institutional
MinIO/R2/object stores, not AWS defaults. Point a dataset at a workspace with
`workspace = "<name>"`, or set `default_workspace` under `[project]`.

```console
$ datapin push counts                  # dataset files → workspace remote (journaled)
$ datapin pull counts --workspace      # fetch the current workspace bytes
$ datapin versions counts/counts.h5    # one file's journal: pushes, reverts, recoverability
$ datapin revert counts/counts.h5 --to 2 --reason "batch 3 was mislabeled"
$ datapin gc --keep 1                  # reclaim archived versions
```

**Journal versioning, on every kind.** Before an overwrite, the superseded
bytes are archived server-side under `.datapin/versions/<key>/<md5>`, and an
append-only journal under `.datapin/journal/` narrates every push and revert.
The invariant, enforced by a test: **every version datapin wrote is
revertible** — until `datapin gc` reclaims it.

- A `revert` is a **new** journal event, never a rewrite: the version you
  regretted stays retrievable, and `--reason` records why. Run
  `datapin pull <slug> --workspace` afterwards to update the local copy.
- `datapin gc --keep N` (default 3) reclaims archived versions per file on
  every workspace remote the manifest uses; the current version never counts
  against the budget. The journal still lists reclaimed versions, which report
  themselves unrecoverable rather than vanishing.
- **Out-of-band overwrites are archived, not just detected.** If someone `scp`s
  over a tracked file, the foreign bytes are archived under their own content
  address before the push proceeds, and the event records the fact.

`datapin status` shows dataset rows alongside files, so one command covers both
tracks: `NOT_PUBLISHED`, `IN_SYNC`, `MISSING`, `AHEAD` (local changes since the
published version), `REMOTE_NEWER` (the archive moved), `DIVERGED`.

## Scripting with JSON

Every command accepts `--output=json`, writing structured JSON to stdout
(progress bars suppressed, color forced off, logging silenced unless you pass
`-v`):

```console
$ datapin publish counts --output=json --yes
$ datapin check --output=json
$ datapin versions counts --output=json
$ datapin remote ls --output=json
$ datapin status --output=json
[{"path":"counts","kind":"dataset","state":"IN_SYNC","declared_version":2}]
```

In JSON mode there are no prompts, so the commands that write remote data need
their intent stated explicitly: `publish` requires `--yes` (or `--force` /
`--reserve` / `--dry-run`), bare `datapin push` requires `--force`, and
`datapin rm` / `datapin wiki rm` require `--yes`.

## Global flags

| Flag | Description |
|------|-------------|
| `--token <token>` | OSF personal access token (overrides env/config/keychain). Archive and workspace remotes use per-remote tokens instead. |
| `--output text\|json` | Output format (default `text`) |
| `--color auto\|always\|never` | Colorize output (default `auto`: on only at a TTY, off under `--output=json`/`--quiet`/`NO_COLOR`) |
| `--verbose`, `-v` | Increase log verbosity (repeatable: `-v` debug, `-vv` HTTP traces + timestamps, `-vvv` max) |
| `--progress-bar`, `-p` | Show live progress bars for transfers (default: log lines) |
| `--quiet`, `-q` | Suppress progress and non-error output (logs drop to errors only; conflicts with `-v`) |
| `--version` | Print the version |

`--jobs`/`-j` is not global: the manifest-scanning commands (`status`, `sync`,
bare `push`/`pull`) accept it to bound how many entries are checked against the
remote concurrently (default 8).

## Logging and output streams

`datapin` writes **results to stdout and activity to stderr**, so the two
compose cleanly:

- **stdout** — the machine/result surface: `ls`/`status`/`versions`/`projects`
  tables, `info`/`set` fields, `open`'s fallback URL, and every
  `--output=json` payload.
- **stderr** — a leveled activity log (what the tool is doing): the remote-scan
  phase, per-file transfers, skips, and mutation confirmations. Colorized on a
  TTY; capture it separately with `2>run.log`.

By default you see high-level activity (`INFO`); add `-v`/`-vv`/`-vvv` for more
detail, or `--quiet` for errors only. Transfers show a one-line summary by
default — pass `-p` for a live progress bar.

`datapin` also cancels cleanly: Ctrl-C aborts in-flight requests and never
leaves a half-downloaded file behind. It exits non-zero on any error, so it
composes in scripts and CI.

## Legacy: the OSF surface

datapin began life as [`gosf`](https://github.com/BU-Neuromics/gosf), a
maintained Go replacement for the Python `osfclient`, and it still speaks to
the [Open Science Framework](https://osf.io). **OSF has announced it is
sunsetting its projects service.** datapin's OSF support is therefore
**frozen** — it works, it is still tested, and it receives no new investment —
and it will be **removed in the major release after the OSF shutdown**. Do not
start new projects here.

### `datapin migrate` — the exit ramp

```console
# GUID mode: export any OSF project (anonymous works for public projects)
$ datapin migrate abc12                   # into the current directory
$ datapin migrate abc12 ./cortex-rnaseq   # into a fresh directory
$ datapin migrate abc12 --components      # each component as its own dataset

# Manifest mode: convert a repo whose manifest already tracks OSF, in place
$ datapin migrate
$ datapin migrate --dataset raw=data/raw/**   # override the grouping (repeatable)

# Either mode: print the full plan and write nothing
$ datapin migrate abc12 --dry-run

# Manifest mode only: skip the TTY confirmation before the manifest rewrite
$ datapin migrate --yes                   # GUID mode never prompts
```

`migrate` turns an OSF project into a datapin project: files downloaded and
MD5-verified, wiki pages exported as markdown site pages, node metadata mapped
to a dataset metadata skeleton, and `[[datasets]]` scaffolded in the manifest.

- **GUID mode** exports node `abc12` into `dest`: every file under
  `osfstorage` with its structure preserved, every wiki page to
  `docs/<page>.md`, a fresh manifest with one dataset (contributors become
  name-only creators, and an `IsDerivedFrom` related identifier points at the
  osf.io origin), and a `MIGRATED.md` provenance breadcrumb. Components are
  skipped with a notice unless `--components` recurses the whole tree.
- **Manifest mode** (no GUID) completes your local copies first — `MISSING` /
  `BEHIND` / `REMOTE_NEWER` entries are fetched through the same safety gates
  as `datapin sync`, and a `DIVERGED` entry fails the run before any transfer —
  then groups `[[files]]` into `[[datasets]]` (one per top-level directory by
  default), converts `[[wikis]]` into site pages, and rewrites the manifest
  atomically with the OSF sections removed.

What OSF cannot supply — a license, ORCIDs, a contact e-mail — is written as a
literal `TODO` marker. That is deliberate: `TODO` is not an SPDX id, so
`datapin check` errors on it and `datapin publish` refuses, which means an
unreviewed migration can never publish by accident. Re-running is idempotent:
files whose MD5 already matches are skipped, and metadata you have edited is
preserved per dataset slug. Nothing is uploaded and no DOI is minted —
publishing stays a separate, deliberate step. Under `--output=json` there are
no prompts and exit code 1 signals that TODOs remain (the export itself
succeeded).

### OSF path syntax

OSF locations are written as `<guid>[:<path>]`:

```
abc12                         # the project/component with GUID abc12
abc12:/data/results/file.csv  # a file inside OSF Storage
abc12:/data                   # a folder inside OSF Storage
abc12/xyz34:/path             # path inside component xyz34 of project abc12
```

A **GUID** is the 5-character identifier from an OSF URL
(`https://osf.io/abc12/` → `abc12`). Components (sub-projects) are addressed
as `parent/child:/path`.

### OSF authentication

Public projects can be read without authentication; private data and any write
need a personal access token from <https://osf.io/settings/tokens/> (grant e.g.
`osf.full_write`).

```console
$ datapin auth login          # prompts, stores in the OS keychain
$ datapin auth login --no-keychain   # ~/.config/datapin/token, mode 0600
$ datapin auth status         # who you are and where the token came from
$ datapin auth logout         # remove the token file and (best-effort) the keychain entry
```

Token priority: `--token` flag → `OSF_TOKEN` environment variable →
`~/.config/datapin/token` → OS keychain. With none of them, datapin runs
unauthenticated (public projects only). The token is never printed in logs or
error output. On HPC nodes without a keychain, `export OSF_TOKEN=…` is enough.

### OSF commands

| Command | What it does |
|---------|--------------|
| `datapin init <project-id>` | Create/update `.datapin/datapin.toml` with `[project].id` |
| `datapin add <local> [<project>:]<remote>` | Track a file (or a directory, recursively) as a `[[files]]` entry; the remote path mirrors the local one if omitted |
| `datapin status` | Sync state of every manifest entry (`--no-check-remote`, `--jobs`) |
| `datapin sync` | Reconcile every entry that has one correct action |
| `datapin ls <project>[:<path>]` | List files and folders |
| `datapin pull <project>[:<path>] [dest]` | Download a file or a folder tree |
| `datapin push <src> <project>:<path>` | Upload a file or directory |
| `datapin rm <project>:<path>` | Delete a file or folder (`--yes`, `--dry-run`) |
| `datapin versions <project>:<path>` | A file's version history, newest first (files only) |
| `datapin mkdir <project>:<path>` | Create a folder (the parent must exist) |
| `datapin mv <src> <dest>` / `datapin cp <src> <dest>` | Move/rename or copy, within or across projects (`--conflict=keep\|replace\|warn`) |
| `datapin info <project>` | Project/component metadata |
| `datapin projects` | The projects and components you can access (needs auth) |
| `datapin set <project>` | Update `--title`, `--description`, `--category`, `--tags` (only the flags you pass) |
| `datapin open <project>[:<path>]` | Open it in your browser |
| `datapin wiki …` | The project wiki — see below |

```console
$ datapin pull abc12:/data/results.csv            # → ./results.csv
$ datapin pull abc12:/data/ ./local-copy          # download the folder tree
$ datapin pull abc12:/data/counts.h5 --version=2  # a specific historical version
$ datapin pull abc12:/data/ data/ --track-only    # register entries, transfer nothing
$ datapin push ./figures/ abc12:/manuscript/figures/
$ datapin push data.csv abc12:/data/data.csv --conflict=overwrite
```

Both directions are **idempotent**: a transfer whose content already matches is
skipped, so no redundant version is minted and no needless download happens.
Other flags worth knowing: `--dry-run` on `push`/`pull`/`rm`/`mkdir`/`mv`/`cp`,
`--no-track` to transfer without touching the manifest, and `--track-only` to
adopt a large remote subtree into the manifest before downloading it (the
entries land as `MISSING`, and a plain `datapin sync` then fetches them — this
is how remote files that nothing tracks become visible to `sync`, which only
ever visits manifest entries).

For an *explicit* `push <src> <dest>`, `--conflict` decides what happens when
the file already exists: `skip` (default), `overwrite` (a new version), or
`rename` (`name_1.ext`, `name_2.ext`, …).

### `[[files]]` / `[[wikis]]` entries

```toml
[project]
id = "abc12"          # default OSF project GUID

[[files]]
local   = "data/counts.h5"        # path relative to repo root
remote  = "/data/counts.h5"       # path within OSF Storage
version = 3                       # pinned OSF version; 0 = not yet pushed
md5     = "d41d8cd98f00b204e…"    # MD5 of the pinned version
project = "xyz89"                 # optional per-entry override of [project].id

[[wikis]]
local   = "docs/home.md"   # markdown file, relative to repo root
page    = "home"           # wiki page name on OSF
version = 3                # pinned wiki version; 0 = not yet pushed
md5     = "…"              # MD5 of the pinned version's content
```

A `local` path may appear in only one of `[[files]]`, `[[wikis]]`, and
`[[datasets]]`. There is **no per-entry direction**: an entry says *what* is
tracked, and how it moves is decided per transfer from the file's state.
Manifests written by datapin ≤ 1.9 carry a `direction` key; it is ignored with
a warning on load and dropped the next time datapin writes the file. No
migration needed.

### File states and the safety gates

Every OSF entry is classified by comparing three values — **L**ocal, the pinned
**B**aseline (`version` + `md5`), and the **R**emote latest:

| State | Meaning |
|-------|---------|
| `IN_SYNC` | L = B, R = B |
| `PIN_ONLY` | Local already equals the remote latest, but the pin is stale/absent → record the pin, no transfer |
| `MISSING` | The local file does not exist |
| `BEHIND` | Local matches an *older* remote version |
| `AHEAD_OF_MANIFEST` | Only local changed since the baseline |
| `REMOTE_NEWER` | Only the remote changed since the baseline |
| `DIVERGED` | Both changed, and local matches no remote version — unsafe |
| `NOT_PUSHED` | `version = 0` and nothing on the remote to compare |

Safety comes entirely from the state at the moment of the action; nothing
recorded in the manifest steers a transfer:

| L vs B | R vs B | `sync` | `push` | `pull` |
|--------|--------|--------|--------|--------|
| L=B | R=B | no-op | no-op | no-op |
| unpinned, L=R | — | pin, no transfer | pin | pin |
| local absent | exists | download + pin | skip | download + pin |
| L≠B | R=B | report, exit 1 | real update (confirm) | skip unless `--force` |
| L=B | R≠B | fast-forward, re-pin | skip unless `--force` (rollback) | fast-forward, re-pin |
| L≠B | R≠B | fail hard, needs `--resolve` | needs `--resolve=ours` | needs `--resolve=theirs` |

```console
$ datapin status                     # read-only; exit 0 only if everything is IN_SYNC
$ datapin sync                       # reconcile everything with a clear answer
$ datapin sync --dry-run             # preview
$ datapin sync --force               # discard local edits, restoring from OSF
$ datapin sync --resolve=theirs      # resolve diverged entries by taking the remote
$ datapin push                       # bare: publish local work, with a per-file plan
$ datapin push --yes                 # skip the prompt for safe pushes
```

- `MISSING` downloads unconditionally — writing a file that does not exist
  destroys nothing.
- `AHEAD_OF_MANIFEST` is the one state `sync` will not guess at: the same
  difference means "publish this" for a generated output and "throw this away"
  for an edited input, and no hash comparison distinguishes them. Say which you
  meant with the verb — `datapin push` publishes it, `datapin pull --force`
  (or `datapin sync --force`) discards it. `sync` reports it and exits
  non-zero without transferring.
- `DIVERGED` fails hard in a pre-flight pass **before any bytes move**, so a
  bulk run never applies a half-resolved state. `--resolve=ours|theirs` is the
  only way through; plain `--force` will not do it.
- `--force` on `sync`/`pull` discards local modifications; on `push` it
  authorizes a deliberate rollback over a newer remote version and bypasses the
  prompt. `--yes` bypasses the prompt for *safe* pushes only.
- Bare `datapin push` prints a rich per-file plan (project title, visibility
  with a loud warning when public, per-file action, sizes, MD5s) and asks for
  confirmation on a TTY. In `--output=json` mode `--force` is mandatory, and a
  non-TTY run without `--yes`/`--force` refuses rather than hang.

### OSF wikis

An OSF project's wiki is a flat namespace of versioned markdown pages,
addressed as `<project>:<page>` (the part after the colon is a page **name**,
not a path; it may contain spaces, and defaults to `home`).

```console
$ datapin wiki ls abc12                          # list pages
$ datapin wiki get abc12 | less                  # print the home page
$ datapin wiki get abc12:protocol protocol.md    # write a page to a file (--force to overwrite)
$ datapin wiki get abc12:home --version=2        # a historical version
$ datapin wiki push docs/home.md abc12:home      # create or add a version (--dry-run)
$ datapin wiki versions abc12:home               # version history
$ datapin wiki mv abc12:draft "Final Protocol"   # rename
$ datapin wiki rm abc12:scratch --yes            # delete
$ datapin wiki open abc12:home                   # open in the browser
$ datapin wiki add docs/home.md abc12:home       # track as a [[wikis]] entry
```

`wiki push` creates the page if it does not exist, otherwise mints a new
version; an identical re-push is skipped. The `home` page cannot be renamed or
deleted. A tracked page (`wiki add`) reconciles through exactly the same states
and gates as a file, so `datapin status` and `datapin sync` treat it as a
first-class row (`"kind": "wiki"` in JSON).

One wrinkle: OSF **normalizes** wiki content on save (CRLF → LF, surrounding
whitespace trimmed), so datapin compares a canonical form rather than raw
bytes. A local file that differs from the page only in line endings or a
trailing newline still counts as in sync, and re-pushing it is a no-op — do not
diff raw bytes against what you pushed.

### Migrating from gosf

Existing gosf setups keep working: a legacy `.gosf/gosf.toml` manifest is found
and read automatically (read-only — rename it to write), `~/.config/gosf`
config and tokens are read when the datapin ones are absent, and `GOSF_*`
environment variables are accepted with a deprecation warning. To migrate a
repo:

```console
$ mv .gosf .datapin && mv .datapin/gosf.toml .datapin/datapin.toml
$ datapin auth login   # re-store your token under datapin
```

Then run `datapin migrate` to move the data itself off OSF.

## For coding agents

`datapin` ships a [skills.sh](https://skills.sh) agent skill so AI coding
agents can understand and invoke it without hand-written instructions.

Install the skill in your project (supported by Claude Code, GitHub Copilot,
Codex, and 38+ other agents):

```console
npx skills add BU-Neuromics/datapin
```

The skill covers installation, remotes and tokens, the manifest, the publish
and workspace workflows, every command, and common recipes. The source lives in
[`skills/datapin/SKILL.md`](./skills/datapin/SKILL.md), and CI asserts that it
names every command and flag the CLI actually has.

## Documentation

- [`ROADMAP.md`](./ROADMAP.md) — shipped releases and the ladder to v1.0
- [`CHANGELOG.md`](./CHANGELOG.md) — what changed, per release
- [`docs/reboot-plan.md`](./docs/reboot-plan.md) — architecture and phases
- [`docs/decisions.md`](./docs/decisions.md) — every implementation decision, with rationale
- [`docs/zenodo-notes.md`](./docs/zenodo-notes.md) — verified Zenodo/InvenioRDM API behaviors
- [`docs/osf-api.md`](./docs/osf-api.md) — OSF REST/Waterbutler notes
- [`CLAUDE.md`](./CLAUDE.md) — the development guide

## Development

```console
go build -o datapin .     # build
go test ./...             # run tests
go test -race ./...       # with the race detector
go test -tags integration -count=1 ./integration/...
go vet ./...              # static checks
gofmt -l .                # formatting check (should print nothing)
golangci-lint run ./...   # lint (must print 0 issues)
```

This project follows **test-driven development**: write a failing test first,
then the code that makes it pass; every bug fix starts with a regression test.
See [`CLAUDE.md`](./CLAUDE.md) for the full development guide.

## License

See [LICENSE](./LICENSE).
