---
name: datapin
description: "Use when working with the Open Science Framework (OSF) for research data management. Invoke when: the project contains a .datapin/datapin.toml manifest (or a legacy .gosf/gosf.toml from datapin's previous life as gosf); the user mentions OSF, osf.io, or osfclient; the task involves syncing, pushing, or pulling research data files with an OSF project; the task involves an OSF project wiki or its markdown pages; or you need to inspect, manage, or automate files stored in OSF Storage. Covers the full datapin CLI: manifest management (datapin init / add / status / sync), file transfer (datapin pull / push / rm), storage management (datapin mkdir / mv / cp), project navigation (datapin ls / info / projects / versions / open / set), project wikis (datapin wiki ls / get / push / rm / mv / versions / open / add), authentication (datapin auth), archive remotes for FAIR data publication to Zenodo/InvenioRDM (datapin remote add / ls / rm), DOI-minting dataset publication (datapin publish), metadata linting and FAIR assessment (datapin check), standard metadata export (datapin export), citations (datapin cite), static documentation sites with dataset landing pages (datapin site build / preview / publish), and journal-versioned workspace remotes on directories, S3, or SFTP for cluster-to-laptop sync (datapin push / pull --workspace / revert / gc)."
metadata:
  version: "0.1.0"
---

# datapin — Open Science Framework CLI

`datapin` is a single-binary CLI that pins, syncs, and publishes research
data: workspace file sync against the Open Science Framework, and
DOI-minting dataset publication to Zenodo/InvenioRDM archives. It syncs files with the
[Open Science Framework](https://osf.io) (OSF); it began life as `gosf`, a
replacement for the unmaintained Python `osfclient`, and legacy `.gosf`
manifests, `~/.config/gosf` tokens, and `GOSF_*` env vars are still read
(read-only, with deprecation warnings).

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
<https://github.com/BU-Neuromics/datapin/releases> and put `datapin` on `PATH`. It is a
static binary with no runtime dependencies — suitable for HPC nodes.

## Authentication

Public projects are readable without auth. A token is needed for private
projects or any write. Tokens are resolved in priority order:
1. `--token` flag
2. `OSF_TOKEN` environment variable
3. `~/.config/datapin/token` (written by `datapin auth login --no-keychain`)
4. OS keychain (written by `datapin auth login`)

Check status: `datapin auth status`  ·  Log in: `datapin auth login`  ·  Log out: `datapin auth logout`

On HPC/headless systems without a keychain, use `OSF_TOKEN` or `--no-keychain`.
`datapin auth logout` is best-effort on the keychain: it always removes the token
file and only warns if the keychain is unavailable.

## Path syntax

```
abc12                         # project/component GUID (5 chars)
abc12:/data/results.csv       # file inside OSF Storage
abc12:/data/                  # folder inside OSF Storage
abc12/xyz34:/path             # path inside component xyz34 of project abc12
```

GUIDs appear in OSF URLs: `https://osf.io/abc12/` → `abc12`.

## .datapin/datapin.toml manifest

The manifest declares which files belong to the project and how they flow. It
lives at `.datapin/datapin.toml`; datapin walks up from the current directory to find it.
Create it with `datapin init <project-id>`, or let `datapin add`/`datapin pull` create it.

```toml
[project]
id = "abc12"          # default project GUID for all entries

[[files]]
local   = "data/counts.h5"       # path relative to repo root
remote  = "/data/counts.h5"      # path in OSF Storage
version = 3                      # pinned OSF version; 0 = not yet pushed
md5     = "d41d8cd98f00b204..."  # MD5 of pinned version; "" if version=0
project = "xyz89"                # optional: override [project].id for this entry
```

Wiki pages are tracked with `[[wikis]]` entries, which mirror `[[files]]` but
address a named wiki page instead of a storage path:

```toml
[[wikis]]
local   = "docs/home.md"   # markdown file, relative to repo root
page    = "home"           # wiki page name on OSF (flat namespace, may contain spaces)
version = 3                # pinned wiki version; 0 = not yet pushed
md5     = "…"              # MD5 of the pinned version's content, computed by datapin
project = "xyz89"          # optional: override [project].id for this entry
```

A `local` path may appear in only one of `[[files]]` and `[[wikis]]`. Wiki
entries flow through the same states, gates, and `--force`/`--resolve` handling
as files.

**There is no per-entry direction.** What a transfer should do is decided at the
moment of the transfer, by comparing local content, the pinned baseline, and the
remote (see the state table below). Manifests written by datapin ≤ 1.9 still carry a
`direction` key: it is ignored with a warning on load and dropped the next time
datapin writes the file. No migration is needed.

**File states** (from `datapin status`), comparing Local / pinned Baseline / Remote:

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

`datapin status` is read-only and exits 0 only if all files are `IN_SYNC`, 1
otherwise — safe to use in CI. It content-compares unpinned (`version=0`) entries
against the remote instead of blindly reporting "never pushed".

## Command reference

### Archive remotes (Zenodo / InvenioRDM)

Named archive remotes are where datasets publish (DOI-minting backends —
distinct from the OSF workspace project). Stored in
`~/.config/datapin/config.toml`; tokens live in the OS keychain or
`~/.config/datapin/tokens/<name>`, resolved as `DATAPIN_TOKEN_<NAME>` env
var → keychain → token file.

```bash
datapin remote add <url> --name <name> [--kind invenio|figshare|dataverse] [--token-value <tok>] [--no-verify] [--no-keychain]
datapin remote ls [--output=json]        # list remotes and whether each has a token
datapin remote rm <name>                 # remove a remote and its stored token
```

Kinds: `invenio` (Zenodo and any InvenioRDM instance), `figshare`
(per-version .vN DOIs, no files-import), `dataverse` (one DOI for all
versions; a collection alias may ride on the URL as
https://host/dataverse/<alias>, default root). `remote add` probes the
URL to confirm it answers like the expected API (`--no-verify` skips). Zenodo sandbox
(https://sandbox.zenodo.org) and production (https://zenodo.org) are
separate services with separate accounts and tokens — add both as remotes
when rehearsing a publish. Sandbox DOIs (prefix `10.5072`) do not resolve.

### Workspace remotes (cluster→laptop intermediate results)

Datasets can also sync to a mutable **workspace** remote — no DOIs, no
metadata ceremony — for the "run on the cluster, pull on the laptop"
workflow. Every workspace remote gets the same journal versioning: before
an overwrite, the superseded bytes archive server-side under
`.datapin/versions/`, and an append-only journal under `.datapin/journal/`
narrates every push and revert. **Every version datapin wrote is
revertible** (until `gc` reclaims it).

```bash
datapin remote add /mnt/lab-share --name nas --kind dir           # mounted path
datapin remote add s3://minio.lab:9000/bucket/prefix --name s3 --kind s3   # token: "ACCESS:SECRET"
datapin remote add sftp://user@cluster/scratch/proj --name hpc --kind sftp # ssh-agent/keys/known_hosts
datapin push <slug>                       # dataset files → workspace (journaled)
datapin pull <slug> --workspace           # fetch current workspace bytes
datapin versions <slug>/<key>             # one file's journal: pushes, reverts, recoverability
datapin revert <slug>/<key> --to <n> [--reason <why>]   # journaled restore (history never rewrites)
datapin gc [--keep N]                     # reclaim archived versions (default keeps 3 per file)
```

Set `workspace = "<name>"` on a dataset (or `default_workspace` under
`[project]`). An unpublished dataset with a workspace pulls from it by
default. Out-of-band overwrites (scp over a tracked file) are detected,
flagged in the journal, and their bytes archived rather than lost.

### Datasets and publishing (FAIR data publication)

A `[[datasets]]` entry in the manifest groups files into one publishable
record. `datapin publish` promotes a dataset's current files to a
published, immutable, DOI-carrying version on its archive remote —
**permanent and public**; rehearse on a sandbox remote first. Unchanged
files are carried over server-side (no re-upload); only changed content
transfers. Every publish prints the DOI and a paste-ready citation.

```bash
datapin publish [<slug>] [--dry-run] [--yes] [--force] [--reserve] [--output=json]
datapin versions <slug>                  # archive version chain with DOIs
datapin pull <slug> [--latest]           # fetch published bytes (pinned version; --latest re-pins)
datapin open <slug>                      # record landing page on the archive (browser)
```

- `--reserve` uploads and reserves the DOI but does NOT publish — the DOI
  can go into a manuscript first; publish again without --reserve to go live.
- `--force` publishes on top of a remote version the manifest has not seen
  (otherwise refused, the dataset analogue of REMOTE_NEWER).
- `--yes` is mandatory for JSON/non-interactive publishes.
- Publishing needs metadata: at minimum `title` and one `creators` entry in
  `[datasets.metadata]` (name = "Family, Given"). License and keywords are
  strongly recommended (CC0-1.0 suggested for data).

Metadata quality and standard exports:

```bash
datapin check [<slug>] [--fair]          # lint metadata: DataCite floor, SPDX, ORCID checksums, relation types; exit 1 on errors
datapin export <slug> [--datapackage] [--ro-crate] [--dir <path>]  # write datapackage.json / ro-crate-metadata.json (default: both)
datapin cite <slug> [--bibtex]           # paste-ready citation via DOI content negotiation (local fallback for sandbox DOIs)
```

`check` errors are exactly what `publish` refuses; warnings are FAIR
nudges (missing description/keywords/ORCIDs, NC/ND licenses). `check
--fair` runs an F-UJI assessment of the published DOI (needs a resolving,
non-sandbox DOI; configure `DATAPIN_FUJI_URL`/`_USER`/`_PASS`).

### Documentation site

`datapin site` renders a static site from the manifest — markdown pages
under `[[site.pages]]` plus a generated, citation-ready landing page per
dataset (DOI links, file checksums, schema.org JSON-LD for Google Dataset
Search) — and deploys it as a single orphan commit force-pushed to the
`gh-pages` branch (no workflow file, no site history).

```bash
datapin site build [--out <dir>]                 # render into <repo>/public (or --out)
datapin site preview [--addr localhost:8383] [--out <dir>]    # build + serve locally
datapin site publish [--github-token <tok>] [--out <dir>]     # build + push to gh-pages, enable Pages
```

Site config in the manifest:

```toml
[site]
title    = "Cortical RNA-seq"
base_url = "https://org.github.io/repo"   # for sitemap + absolute JSON-LD URLs
repo     = "org/repo"                     # gh-pages target; default: git origin
[[site.pages]]
local = "docs/index.md"                   # slug defaults to the basename
```

`site publish` finds a GitHub token via --github-token → `GITHUB_TOKEN` →
`GH_TOKEN` → `gh auth token`; without one the push uses your git
credential helper and the first-run Pages toggle is skipped with a note.

Manifest shape:

```toml
[project]
default_archive = "sandbox"        # remote name from `datapin remote add`

[[datasets]]
slug = "counts"                    # local handle: publish/versions/pull use it
record = ""                        # filled by the first publish
version = 0
  [datasets.metadata]
  title = "Aligned RNA-seq count matrices"
  license = "CC0-1.0"
  [[datasets.metadata.creators]]
  name = "Labadorf, Adam"
  orcid = "0000-0002-…"
  [[datasets.files]]
  local = "results/counts.h5"      # key defaults to the basename
```

### Manifest commands

```bash
datapin onboard [--project <guid>] [--remote-base <path>]  # interactive guided setup (TTY only)
datapin init <project-id>                              # create/update .datapin/datapin.toml
datapin add <local-path> [<project>:]<remote-path>     # track file(s) (dir = recursive)
datapin status [--no-check-remote] [--jobs=N] [--output=json]   # show sync state of all entries
datapin sync [--force] [--resolve=ours|theirs] [--dry-run] [--no-check-remote] [--jobs=N] [--output=json]
```

`datapin onboard` is a resumable, interactive wizard (auth → attach a project → pick
git-untracked files to push via a tree checkbox UI). It writes manifest entries
and stops; run `datapin sync` to upload. TTY only — for scripting/agents, use
`init` + `add` + `sync` directly.

`datapin add` registers a local file; `datapin pull` registers what it downloads. Both
are just ways to get an entry into the manifest — neither fixes which way the
file will move later. If the remote path is omitted it mirrors the local path.

`datapin sync` reconciles every entry that has one unambiguous answer, and reports
the ones that do not:

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

### Storage management

```bash
datapin mkdir <project>:<path>          # create a folder (parent must exist)
datapin mv <src> <dest>                 # move/rename within or across projects
datapin cp <src> <dest>                 # copy within or across projects
```

`mv`/`cp` accept `--conflict=keep|replace|warn`.

### Project navigation

```bash
datapin ls <project>[:<path>] [--output=json]     # list files/folders
datapin info <project> [--output=json]            # project metadata
datapin projects [--output=json]                  # list accessible projects (needs auth)
datapin versions <project>:<path> [--output=json] # list file versions (files only)
datapin open <project>[:<path>] [--output=json]   # open in browser (or print URL)
datapin set <project> [--title ...] [--description ...] [--category ...] [--tags ...]
```

## Common workflows

### Set up a new project and pull inputs

```bash
datapin init abc12                                  # 1. create .datapin/datapin.toml
datapin pull abc12:/data/ ml/data/                  # 2. download + track
datapin status                                      # 3. verify → all ✓ IN_SYNC
```

Pulling files that are already present locally and identical is a no-op that just
records the pin — no redundant downloads.

### Push locally modified outputs

```bash
datapin add results/model.pkl abc12:/results/model.pkl   # track it (once)
datapin status                                           # AHEAD → local has unpublished work
datapin push --dry-run                                   # preview
datapin push --yes                                       # publish (skip the prompt)
```

### Resolve a divergence

```bash
# datapin sync failed hard: notes.md changed both locally and on OSF.
datapin sync --resolve=theirs   # take remote (discard local), or
datapin sync --resolve=ours     # take local  (discard remote)
```

### Keep a wiki page in the repo

```bash
datapin wiki add docs/home.md abc12:home   # track it (pins the remote if it exists)
datapin status                             # wiki row appears alongside files
datapin sync                               # reconcile like any other entry
```

One-shot, without tracking: `datapin wiki push docs/home.md abc12:home`. To read a
page, `datapin wiki get abc12:home` prints it to stdout.

### Check whether everything is in sync (CI)

```bash
datapin status --no-check-remote   # fast: no remote API calls
datapin status                     # full: checks BEHIND / REMOTE_NEWER / DIVERGED
# exits 0 if all IN_SYNC, 1 otherwise
```

## Global flags

| Flag | Description |
|------|-------------|
| `--token <tok>` | OSF token (overrides env/file/keychain) |
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
is never colorized. In `--output=json` mode, `datapin rm` and a bytes-writing
`datapin push` both require an explicit flag (`--yes` / `--force`) — there is no
prompt.

## Constraints to respect

- A `DIVERGED` file (changed both locally and remotely) blocks `sync`/`push`/`pull`
  until you pass `--resolve=ours|theirs`; plain `--force` will not override it.
- `--force` means different things by command, and both are a deliberate,
  one-sided discard: on `sync`/`pull` it discards local modifications and
  restores the tracked version from OSF; on `push` it authorizes a rollback
  (burying a newer remote version) and bypasses the confirmation prompt.
- Bare `datapin push` and `datapin rm` require `--yes`/`--force` in `--output=json` mode.
- `datapin versions` works on files, not folders.
- New files/folders upload into the parent folder resolved from OSF (osfstorage
  addresses folders by ID); the parent must exist (`datapin mkdir` first if needed).
- The manifest is updated atomically after every successful push, pull, or sync.
