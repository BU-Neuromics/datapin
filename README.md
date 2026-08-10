# datapin

[![CI](https://github.com/BU-Neuromics/datapin/actions/workflows/ci.yml/badge.svg)](https://github.com/BU-Neuromics/datapin/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/BU-Neuromics/datapin)](https://github.com/BU-Neuromics/datapin/releases)

`datapin` is a fast, single-binary command-line tool for pushing and pulling files
to and from the [Open Science Framework](https://osf.io) (OSF). It is a
maintained replacement for the Python `osfclient`, distributed as a static
binary with no runtime dependencies.

```console
$ datapin pull abc12:/data/results.csv
$ datapin push ./figures/ abc12:/manuscript/figures/
$ datapin ls abc12:/data
$ datapin wiki push docs/home.md abc12:home
```

## Features

- **Single binary** — no Python, no virtualenv; drop it on an HPC node and go.
- **Push, pull, list, remove** files in OSF Storage, plus project metadata.
- **Token auth** stored securely in your OS keychain (with a plaintext fallback
  for headless systems).
- **Scriptable** — `--output=json` on every command.
- **Safe** — `--dry-run` on push/pull/rm, conflict handling on push, a rich
  confirmation before bulk pushes, and state-based gates that refuse to silently
  clobber diverged files.
- **Idempotent** — pushing or pulling a file that already matches is a no-op; no
  redundant versions, no needless downloads.
- **Progress bars** on transfers and **colorized output** (both auto-off when not
  a TTY or under `--quiet`/`--output=json`; controllable with `--color`).
- **Ctrl-C aware** — cancels in-flight transfers cleanly and never leaves
  half-downloaded files behind.
- **Sync manifest** — declare files in `.datapin/datapin.toml` and keep them in sync
  with `datapin sync`; CI-friendly status with `datapin status`.
- **Project wikis** — read, write, and sync a project's versioned markdown wiki
  pages (`datapin wiki`), including manifest-driven sync of local `.md` files.

## For coding agents

`datapin` ships a [skills.sh](https://skills.sh) agent skill so AI coding agents can
understand and invoke it without hand-written instructions.

Install the skill in your project (supported by Claude Code, GitHub Copilot,
Codex, and 38+ other agents):

```console
npx skills add BU-Neuromics/datapin
```

The skill covers installation, authentication, path syntax, the
`.datapin/datapin.toml` manifest, every command, and common workflows. The source
lives in [`skills/datapin/SKILL.md`](./skills/datapin/SKILL.md).

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

## Authentication

Most public projects can be read without authentication. To access private
projects or to upload, you need a personal access token.

1. Create a token at <https://osf.io/settings/tokens/> (grant the scopes you
   need, e.g. `osf.full_write`).
2. Log in:

   ```console
   $ datapin auth login
   Enter your OSF personal access token: ********
   Logged in as Ada Lovelace (a1b2c)
   ```

3. Check status anytime:

   ```console
   $ datapin auth status
   Logged in as: Ada Lovelace (a1b2c)
   Token from:   OS keychain
   ```

### Where the token comes from

`datapin` resolves a token from the first source that has one:

| Priority | Source |
|----------|--------|
| 1 | `--token` flag |
| 2 | `OSF_TOKEN` environment variable |
| 3 | Token file (`~/.config/datapin/token`) |
| 4 | OS keychain |

If none are set, `datapin` runs unauthenticated (public projects only).

### Headless / HPC systems

If there's no OS keychain available, store the token in a local token file instead:

```console
datapin auth login --no-keychain
```

The token is written to `~/.config/datapin/token` with mode `0600`. This file is
separate from `config.toml` so the config file remains safe to commit to version
control.

Or skip persistent storage entirely and use the environment variable:

```console
export OSF_TOKEN=your-token-here
datapin ls abc12
```

The token is never printed in logs or error output.

### Logging out

```console
datapin auth logout
```

Removes the token file and (best-effort) the OS keychain entry. If the keychain
is locked or unavailable, logout still succeeds and prints a warning — the token
file, which datapin controls directly, is always removed.

## Path syntax

OSF locations are written as `<guid>[:<path>]`:

```
abc12                         # the project/component with GUID abc12
abc12:/data/results/file.csv  # a file inside OSF Storage
abc12:/data                   # a folder inside OSF Storage
abc12/xyz34:/path             # path inside component xyz34 of project abc12
```

- A **GUID** is the 5-character identifier from an OSF URL
  (`https://osf.io/abc12/` → `abc12`).
- The part after `:` is a path within the project's **OSF Storage** provider.
- Components (sub-projects) are addressed as `parent/child:/path`.

## Commands

### `datapin onboard`

Guided, interactive setup — the easiest way to start. It detects your current
state and resumes at the right step, so it's safe to re-run:

1. **Authenticate** (offered if you're not logged in).
2. **Attach a project** — type a GUID or pick from your project list.
3. **Select files to push** — a collapsible file-tree of the things git doesn't
   track (data, models, artifacts); check individual files or whole directories.

It records your picks in `.datapin/datapin.toml` and stops there; run `datapin sync` to
upload. Requires an interactive terminal.

```console
$ datapin onboard
$ datapin onboard --project abc12 --remote-base /inputs   # skip the prompts
```

### `datapin ls <project>[:<path>]`

List files and folders.

```console
$ datapin ls abc12:/data
NAME          SIZE      MODIFIED
results/      —         2024-02-02 12:00
notes.txt     1.2 KB    2024-01-15 09:30
```

### `datapin pull <project>[:<path>] [dest]`

Download a file or an entire folder tree.

```console
$ datapin pull abc12:/data/results.csv            # → ./results.csv
$ datapin pull abc12:/data/results.csv out.csv    # → ./out.csv
$ datapin pull abc12:/data/ ./local-copy          # download the folder tree
$ datapin pull abc12: --dry-run                   # preview a whole-project pull
$ datapin pull abc12:/data/counts.h5 --version=2  # download a specific version
```

A pull is **idempotent**: if the local file already matches the remote (same
MD5), the download is skipped. With no path argument, `datapin pull` downloads every
tracked entry that is missing locally or behind the remote; locally modified
files are reported and left alone unless `--force` is given.

Flags:
- `--version=<n>` — download a specific historical version instead of the latest.
  Only valid for single-file targets; errors for directory pulls.
- `--force` — overwrite a locally-modified file with the tracked version.
- `--track-only` — record the matched files in `.datapin/datapin.toml` without
  transferring any bytes, so a large remote can be adopted and reviewed before it
  is downloaded. The entries land as `MISSING`; a plain `datapin sync` then fetches
  them. (`sync` only ever visits entries in the manifest, so this is how remote
  files that nothing tracks become visible to it.)
- `--resolve=theirs` — resolve a diverged file (changed both locally and
  remotely) by taking the remote copy.
- `--dry-run` — list what would be downloaded without writing any files.

```console
$ datapin pull abc12:/data/ data/ --track-only   # register 900 files, download none
$ datapin status                                 # review what you just adopted
$ datapin sync                                   # now fetch them
```

### `datapin push <src> <project>:<path>`

Upload a file or directory. With a trailing slash, the destination is treated
as a folder and the source filename is kept.

```console
$ datapin push results.csv abc12:/data/results.csv
$ datapin push ./figures/  abc12:/manuscript/figures/
$ datapin push data.csv    abc12:/data/data.csv --conflict=overwrite
```

A push is **idempotent**: uploading a file whose bytes already match the remote
is skipped rather than minting a redundant version.

Conflict handling for an *explicit* push (`--conflict`, default `skip`):

| Mode | Behaviour when a file already exists |
|------|--------------------------------------|
| `skip` | Leave the existing file untouched (default) |
| `overwrite` | Replace it with the local file (new version) |
| `rename` | Upload as `name_1.ext`, `name_2.ext`, … |

With no arguments, `datapin push` publishes every tracked file that holds local work
the remote does not have — files modified since they were last synced, and files
never pushed. Because that writes remote data, it prints a per-file plan
(project title + visibility, the action per file, sizes and MD5s) and asks for
confirmation:

- `--yes` — skip the prompt for a safe push (new files, real updates).
- `--force` — also authorize a *rollback* (overwriting a newer remote version).
- `--resolve=ours` — resolve a diverged file by taking the local copy.
- In `--output=json` mode, `--force` is required (there is no prompt).

### `datapin rm <project>:<path>`

Delete a file or folder. Prompts for confirmation unless `--yes` is given.

```console
$ datapin rm abc12:/data/old.csv
Delete /data/old.csv from project abc12? [y/N]: y
Deleted /data/old.csv

$ datapin rm abc12:/scratch/ --yes
```

### `datapin projects`

List the projects and components you can access (requires auth).

### `datapin info <project>`

Show project/component metadata.

### `datapin open <project>[:<path>]`

Open the project or file in your web browser.

### `datapin versions <project>:<path>`

List all versions of a file, newest first.

```console
$ datapin versions abc12:/data/counts.h5
VERSION  DATE                  SIZE     CONTRIBUTOR
3        2024-03-01 09:15 UTC  14.2 MB  ada@example.com
2        2024-02-10 14:30 UTC  13.8 MB  ada@example.com
1        2024-01-20 08:00 UTC  12.1 MB  ada@example.com
```

### `datapin init <project-id>`

Create or update `.datapin/datapin.toml` in the current directory, setting the default
project GUID. Existing `[[files]]` entries are preserved.

```console
$ datapin init abc12
```

### `datapin mkdir <project>:<path>`

Create a folder in OSF Storage (the parent folder must already exist).

### `datapin mv <src> <dest>` / `datapin cp <src> <dest>`

Move/rename or copy a file or folder within or across projects
(`--conflict=keep|replace|warn`).

### `datapin set <project>`

Update a project's title, description, category, and/or tags (only the flags you
pass are changed).

### `datapin add <local-path> [<project>:]<remote-path>`

Track a file (or directory, recursively) in the `.datapin/datapin.toml` manifest
(creates the manifest if absent). `datapin pull` does the same for files it
downloads. Neither fixes which way the file will move later — that is decided per
transfer, from the file's state. If the remote path is omitted it mirrors the
local path.

```console
$ datapin add results/model.pkl abc12:/results/model.pkl
$ datapin add data/raw/ abc12:/data/raw/        # add a directory, one entry per file
$ datapin add notes.md                          # remote mirrors the local path
```

If the file already exists on OSF its current version and MD5 are recorded in
the manifest automatically. Files larger than 50 MB get a `.gitignore` tip.

### `datapin status`

Show the sync state of every file in `.datapin/datapin.toml`.

```console
$ datapin status
STATUS    LOCAL PATH             VER   DETAIL
✓         data/counts.h5         v3
AHEAD     results/model.pkl      v1    locally modified — push to publish, pull --force to discard
BEHIND    shared/ref.csv         v2    remote is v3
pull  ≡         inputs/ref.csv         —     identical to remote v2, unpinned — run sync
push  DIVERGED  notes/summary.md       v1    local and remote both changed since v1
push  ·         outputs/report.pdf     —     never pushed
```

Status is read-only — it reports, it never mutates the manifest. It also
content-compares **unpinned** (`version = 0`) entries against the remote, so an
already-identical file shows `≡`/`PIN_ONLY` rather than a blanket "never pushed".

Exit code 0 when everything is `IN_SYNC`; exit code 1 otherwise — useful in CI.

Flags:
- `--no-check-remote` — skip remote version lookups (faster; cannot detect
  `BEHIND` or `REMOTE_NEWER`).

### `datapin sync`

Reconcile files with OSF according to the manifest. `sync` takes the one correct
action for each file's state and reports the states that do not have one. It is
non-interactive and fails hard (before transferring anything) on a diverged file.

```console
$ datapin sync                       # reconcile everything with a clear answer
$ datapin sync --dry-run             # preview without making changes
$ datapin sync --force               # discard local edits, restoring from OSF
$ datapin sync --resolve=theirs      # resolve diverged files by taking remote
```

Actions are chosen from the file's state, comparing **L**ocal, the pinned
**B**aseline, and the **R**emote latest:

| State | Action |
|-------|--------|
| `IN_SYNC` | ✓ skip |
| `PIN_ONLY` (local already matches remote) | record the pin, no transfer |
| `MISSING` | download it, pin — no flag needed; nothing local is at risk |
| `BEHIND` | fast-forward to the remote's latest, re-pin |
| `REMOTE_NEWER` (only remote changed) | fast-forward to the remote's latest, re-pin |
| `NOT_PUSHED` (file exists) | upload it, pin |
| `NOT_PUSHED` (missing) | skip — nothing local, nothing remote |
| `AHEAD_OF_MANIFEST` (only local changed) | **report only**; `sync` exits non-zero |
| `DIVERGED` (both changed) | **fail hard** — needs `--resolve=ours\|theirs` |

`AHEAD_OF_MANIFEST` is the one state `sync` will not guess at: the same
difference means "publish this" for a generated output and "throw this away" for
an edited input, and no hash comparison distinguishes them. Say which you meant
with the verb — `datapin push` publishes it, `datapin pull --force` (or
`datapin sync --force`) discards it.

Flags:
- `--force` — discard local modifications, restoring the tracked version from
  OSF. Does **not** cover divergence.
- `--resolve=ours\|theirs` — resolve diverged entries (`ours` keeps local,
  `theirs` keeps remote).
- `--dry-run` — show what would happen without making any changes.
- `--no-check-remote` — skip remote version lookups (faster; cannot detect
  `BEHIND` or `REMOTE_NEWER`).

### `datapin wiki`

Manage a project's wiki — the versioned markdown pages attached to it. Pages are
addressed as `<project>:<page>`; the page name is a flat namespace (not a path),
may contain spaces, and defaults to `home` where optional.

```console
$ datapin wiki ls abc12                          # list pages
$ datapin wiki get abc12 | less                  # print the home page
$ datapin wiki get abc12:protocol protocol.md    # write a page to a file
$ datapin wiki push docs/home.md abc12:home      # create or update a page
$ datapin wiki versions abc12:home               # version history
$ datapin wiki mv abc12:draft "Final Protocol"   # rename
$ datapin wiki rm abc12:scratch --yes            # delete
$ datapin wiki open abc12:home                   # open in the browser
```

`datapin wiki push` creates the page if it does not exist, otherwise mints a new
version; an identical re-push is skipped (no redundant version). The `home` page
cannot be renamed or deleted.

**Syncing wiki pages with local markdown.** Track a markdown file as a wiki page
and it syncs like any other file:

```console
$ datapin wiki add docs/home.md abc12:home
$ datapin status        # shows the wiki row alongside files
$ datapin sync          # reconciles the page from its state, like any other entry
```

Wiki entries live under `[[wikis]]` in the manifest and reconcile through the
same pinned-baseline safety model as files (`PIN_ONLY`, `REMOTE_NEWER`,
`DIVERGED`, `--force`, `--resolve=ours|theirs`). Note that OSF normalizes wiki
content on save (CRLF line endings become LF and surrounding whitespace is
trimmed), so datapin compares a canonical form rather than raw bytes — a local file
that differs from the wiki only in line endings or a trailing newline still counts
as in sync, and pushing it again is a no-op.

## Sync manifest (`.datapin/datapin.toml`)

The manifest lives at `.datapin/datapin.toml` in your repository root (datapin walks up
from the current directory to find it). Create it with `datapin init <project-id>`,
or let `datapin add` / `datapin pull` create it for you. It declares which OSF files
belong to the project:

```toml
[project]
id = "abc12"          # default OSF project GUID

[[files]]
local   = "data/counts.h5"        # path relative to repo root
remote  = "/data/counts.h5"       # path within OSF Storage
version = 3                       # pinned OSF version; 0 = not yet pushed
md5     = "d41d8cd98f00b204e..."  # MD5 of the pinned version

[[files]]
local   = "results/model.pkl"
remote  = "/results/model.pkl"
version = 1
md5     = "0cc175b9c0f1b6..."

[[files]]
local   = "data/ref.csv"
remote  = "/data/ref.csv"
version = 0          # not yet pushed — md5 left blank
md5     = ""
project = "xyz89"    # per-entry project override

[[wikis]]
local   = "docs/home.md"   # markdown file, relative to repo root
page    = "home"           # wiki page name on OSF
version = 3                # pinned wiki version; 0 = not yet pushed
md5     = "…"              # MD5 of the pinned version's content
```

`[[wikis]]` entries track wiki pages the same way `[[files]]` track storage
files (a `local` path may appear in only one of the two). The manifest is
updated automatically when you run `datapin sync` or `datapin push` — you rarely need
to edit it by hand.

There is **no per-entry direction**. An entry says *what* is tracked; how it
moves is decided per transfer from the file's state. Manifests written by
datapin ≤ 1.9 carry a `direction` key on every entry: it is ignored with a warning
on load and dropped the next time datapin writes the file. No migration is needed.

## Scripting with JSON

Every command accepts `--output=json`, writing structured JSON to stdout
(progress bars are suppressed automatically):

```console
$ datapin ls abc12:/data --output=json
[{"id":"...","attributes":{"name":"results","kind":"folder", ...}}, ...]

$ datapin push data.csv abc12:/data/data.csv --output=json
{"uploaded": [{"path": "/data/data.csv", "action": "upload"}], "dry_run": false}

$ datapin status --output=json
[{"path":"data/counts.h5","kind":"file","state":"IN_SYNC","declared_version":3}]

$ datapin sync --output=json
[{"path":"results/model.pkl","state":"AHEAD_OF_MANIFEST","declared_version":1,"action_taken":"push"}]

$ datapin versions abc12:/data/counts.h5 --output=json
{"versions": [{"version":3,"date_created":"2024-03-01T09:15:00","size":14900000,"contributor":"ada@example.com"}]}

$ datapin add data/new.csv abc12:/data/new.csv --output=json
{"entries":[{"local":"data/new.csv","remote":"/data/new.csv","project":"abc12","version":0,"md5":""}],"manifest_created":false}

$ datapin wiki ls abc12 --output=json
[{"id":"...","name":"home","version":3,"size":128,"date_modified":"2024-03-01T09:15:00"}]

$ datapin wiki get abc12:home --output=json
{"project":"abc12","page":"home","version":3,"size":128,"content":"# Home\n..."}

$ datapin wiki push docs/home.md abc12:home --output=json
{"project":"abc12","page":"home","action":"update","version":4,"dry_run":false}
```

The wiki commands follow the same JSON conventions: `wiki push` reports
`"action"` ∈ `create|update|skip`, `wiki versions` matches `datapin versions`, and
`wiki mv`/`wiki add` emit `{node,from,to,dry_run}` / `{entries,manifest_created}`.
In `datapin status` / `datapin sync` output, each item carries `"kind":"file"|"wiki"`
so mixed manifests are unambiguous.

In JSON mode, `datapin rm` and `datapin wiki rm` require `--yes` (there is no
interactive prompt).

## Global flags

| Flag | Description |
|------|-------------|
| `--token <token>` | Use this OSF token (overrides env/config/keychain) |
| `--output text\|json` | Output format (default `text`) |
| `--color auto\|always\|never` | Colorize output (default `auto`: on only at a TTY, off under `--output=json`/`--quiet`/`NO_COLOR`) |
| `--verbose`, `-v` | Increase log verbosity (repeatable: `-v` debug, `-vv` HTTP traces + timestamps, `-vvv` max) |
| `--progress-bar`, `-p` | Show live progress bars for transfers (default: log lines) |
| `--jobs`, `-j` | Files to scan against the remote concurrently on `sync`/`status` (default 8) |
| `--quiet`, `-q` | Suppress progress and non-error output (logs drop to errors only; conflicts with `-v`) |
| `--version` | Print the version |

## Logging and output streams

`datapin` writes **results to stdout and activity to stderr**, so the two compose
cleanly:

- **stdout** — the machine/result surface: `ls`/`status`/`versions`/`projects`
  tables, `info`/`set` fields, and every `--output=json` payload.
- **stderr** — a leveled activity log (what the tool is doing): the remote-scan
  phase, per-file transfers, skips, and mutation confirmations. Colorized on a
  TTY; capture it separately with `2>run.log`.

By default you see high-level activity (`INFO`); add `-v`/`-vv`/`-vvv` for more
detail, or `--quiet` for errors only. Transfers show a one-line summary by
default — pass `-p` for the classic live progress bar. In `--output=json` mode
stdout stays pure JSON and activity logging is silenced unless you pass `-v`.

## Exit codes

`datapin` exits non-zero on any error, so it composes cleanly in scripts and CI.

## Development

```console
go build -o datapin .     # build
go test ./...          # run tests
go test -race ./...    # run tests with the race detector
go vet ./...           # static checks
gofmt -l .             # formatting check (should print nothing)
```

This project follows **test-driven development**: write a failing test first,
then the code that makes it pass; every bug fix starts with a regression test.
See [`CLAUDE.md`](./CLAUDE.md) for the full development guide, architecture, and
the OSF API notes.

## License

See [LICENSE](./LICENSE).
