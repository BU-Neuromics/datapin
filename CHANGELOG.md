# Changelog

All notable changes to this project will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

> **Rename notice (2026-08-10):** this project was rebooted from `gosf` to
> **`datapin`** (see `docs/reboot-plan.md`). Versioning restarts at `v0.1.0`
> under the datapin name; the `[1.x]`/`[2.x]` entries below record the tool's
> previous life as gosf and are kept verbatim (their release links point at
> the original gosf repository).

## [2.1.0] - 2026-07-31

Rate-limit resilience. Projects with more than a handful of tracked files were
hitting `429 Too Many Requests`, and gosf did nothing to avoid, absorb, or
explain it. Nothing in this release is breaking; upgrading needs no changes.

If you have been seeing 429s, run `gosf auth status` first — OSF allows roughly
100 requests/hour anonymously against 10,000/day with a token, and gosf used to
fall back to anonymous silently (see the last entry under **Changed**).

### Fixed

- **A throttled request was indistinguishable from "this file is not on the
  remote"** (#86). `fetchRemoteState` swallowed every resolve failure and
  returned "no remote state", so a 429 mid-scan classified an unpinned entry as
  `NOT_PUSHED` — and since #81 that means `sync` **uploads it**, minting a
  redundant version of a file that was already there, byte-identical. The
  resolver now returns a typed `NotFoundError` for genuine absence, and anything
  else (throttling, an unreachable API, a permission error) fails the scan
  loudly instead of producing a plan built on wrong states.
- **`Retry-After` was ignored** (#86). OSF sends it on every 429; gosf had no
  retry, backoff, or wait of any kind, so a scan simply died. Throttled and
  transient-gateway responses are now retried up to 3 times, honouring the
  server's `Retry-After` exactly. A wait longer than 30s is declined rather than
  truncated — the quota is genuinely spent, and retrying early would only earn
  another 429 — so the run fails immediately with the number instead of hanging.
  The wait is context-aware: Ctrl-C during a rate-limit pause still aborts.

### Changed

- **Up to 10× fewer API requests** (#86). OSF's JSON:API serves 10 items per
  page by default and caps at 100; gosf never sent `page[size]`, so a folder of
  87 files cost 9 requests instead of 1. Every paginated endpoint (files, nodes,
  versions, wikis) now asks for the maximum, idempotently — `links.next` already
  carries the size forward, and a duplicate key would let the server choose.
  Measured against the fake: listing 87 files went from 10 requests to 2, and a
  40-file manifest scan from 5 to 2.
- A 429 now produces an actionable message instead of `OSF API 429: …`. It names
  whether the run was authenticated, since OSF allows roughly 100 requests/hour
  anonymously against 10,000/day with a token.
- Manifest-scanning commands warn when they are running unauthenticated.
  `config.LoadToken` returns `""` for a locked keychain or a mistyped variable
  exactly as it does for a deliberate anonymous run, so a user could burn the
  100/hour allowance never knowing why.

### Internal

- `fakeosf` now paginates like real OSF (10 per page by default, `page[size]`
  honoured and capped at 100) and counts listing requests. It previously
  returned every item in one page with `next: nil`, so **no test had ever
  exercised the multi-page walk** — the code path this release changes was
  entirely unguarded.
- CI now checks the agent skill against the CLI (`cmd/skill_doc_test.go`): every
  command path and flag must appear in `skills/gosf/SKILL.md`, and every
  top-level command in its frontmatter `description`. Deliberate omissions need
  an allowlist entry with a reason. Nothing verified this before, which is how
  the drift below went unnoticed across two releases.

### Documentation

- **The agent skill was missing the entire `gosf wiki` command group**, which
  shipped in v1.9.0 — nine subcommands, the `[[wikis]]` manifest table, and the
  content-canonicalization rule. The frontmatter `description` omitted wikis
  too, so the skill was never even triggered for wiki tasks. Added, along with a
  wiki workflow. An audit of every documented flag against the binary also fixed
  `--jobs` being listed as global (it is per-command, and now on bare
  `push`/`pull` as well), incomplete `pull`/`push` synopses, and `--force` being
  described with one meaning when it means "discard local modifications" on
  `sync`/`pull` and "authorize a rollback" on `push`. Skill version 1.1.0 →
  2.0.0.

## [2.0.0] - 2026-07-30

A major release because `direction` is gone from the manifest, along with the
refusals it caused. **Existing manifests keep working untouched** — the key is
accepted and ignored with a one-time warning, then dropped the next time gosf
writes the file. The breaking changes to watch for are in tooling around gosf,
not in the manifest: `gosf status --output=json` no longer emits `direction`,
`gosf wiki add --direction` is removed, `gosf sync` no longer publishes locally
modified files, and `gosf sync --force` has a new meaning. See **Migrating from
1.x** at the end of this entry.

### Fixed

- **`gosf sync` silently refused to create files that exist on OSF but not
  locally** (#81). A tracked file that was missing from the working tree was
  skipped with `missing locally, skipping`, and no flag — `--force` included —
  changed that: an entry recorded for pushing was never routed to the code that
  downloads. A plain `gosf sync` now restores it. Two related refusals are gone
  with it: `--resolve` was rejected unless it happened to match the entry's
  recorded direction (so the divergence message recommended a command `sync`
  then refused), and a `both` entry with a newer remote hit a rollback refusal
  instead of fast-forwarding.
- `gosf pull` over a subtree replaced whole manifest entries, flipping tracked
  outputs to `direction=pull` and quietly stopping them from being published
  again. Pulling no longer changes how an entry behaves afterwards.

### Changed

- **BREAKING: `direction` is removed from the manifest** (#81, finishes #38, and
  the cause of the bug above).
  Every state has exactly one correct action, derived from comparing local
  content, the pinned baseline, and the remote at the moment of the transfer;
  the two states that are genuinely ambiguous are reported for you to resolve
  rather than guessed at from a field recorded weeks earlier. **No migration is
  needed:** a `direction` key in an existing manifest is accepted and ignored
  with a one-time warning on load, and dropped the next time gosf writes the
  file. `gosf wiki add --direction` is removed; `gosf status` drops its `DIR`
  column and the `direction` field from `--output=json`.
- **BREAKING:** `gosf sync` reports `AHEAD_OF_MANIFEST` instead of pushing it, and exits
  non-zero. A locally modified file is work to publish for a generated output
  and a scratch edit to discard for an input, and no hash comparison tells them
  apart — so say which with the verb: `gosf push` publishes it, `gosf pull
  --force` (or `gosf sync --force`) discards it. `sync --force` now means
  "discard local modifications" rather than "authorize a rollback".
- Bare `gosf push` and `gosf pull` select entries by state rather than by a
  manifest field: push publishes what is modified or never pushed, pull fetches
  what is missing or behind. A push that would merely bury a newer remote
  version is skipped rather than failing the whole run (`--force` still performs
  the rollback deliberately).
- Bare `gosf push` and `gosf pull` now scan the manifest concurrently through
  the caching resolver, like `sync` and `status`, and accept `--jobs`/`-j`.

### Added

- `gosf pull --track-only` registers a remote subtree in `.gosf/gosf.toml`
  without transferring any bytes, so a large project can be adopted and reviewed
  before it is downloaded. The entries land as `MISSING` and a plain `gosf sync`
  fetches them. `sync` only ever visits entries in the manifest, so this is how
  remote files that nothing tracks become visible to it.

### Documentation

- The wiki feature was missing from three at-a-glance spots that undersold it:
  the README intro examples, the Features list, and the JSON scripting section.
  Added, along with a note on the shared JSON conventions and the `status`/`sync`
  `"kind"` field.

### Migrating from 1.x

**Your manifest needs no edits.** `.gosf/gosf.toml` keeps loading as-is; the
retired `direction` key produces one warning per run and is dropped the next time
gosf writes the file (any `sync`/`push`/`pull` that changes a pin, or
`add`/`wiki add`/`init`). Read-only commands like `status` warn but never
rewrite, so run `gosf init <project-id>` if you want to clear the warning
deliberately — it rewrites in place with no other effect. Note that the rewrite
re-marshals the file, so hand-written comments and column alignment are lost.

Check these four things instead:

1. **Scripts parsing `gosf status --output=json`** — the `direction` field is
   gone. Items still carry `path`, `kind`, `state`, `declared_version`, and
   `remote_latest_version`.
2. **Anything invoking `gosf wiki add --direction=…`** — the flag no longer
   exists and the command will now fail with `unknown flag`.
3. **Pipelines that relied on `gosf sync` to publish outputs** — it no longer
   does. `sync` reports locally modified files and exits non-zero; use
   `gosf push` (with `--yes` when unattended) to publish. `gosf sync` in CI as a
   drift check now behaves like `gosf status` for that state.
4. **Anything passing `--force` to `gosf sync`** — it used to authorize pushing
   over a newer remote version; it now means "discard local modifications and
   restore from OSF". If you wanted the old behavior, that is `gosf push
   --force`.

One capability is genuinely gone: `direction = "pull"` could mark a file that
must never be republished, and nothing in the manifest replaces it. In practice
an unmodified file stays `IN_SYNC` and never enters a push plan, and interactive
`gosf push` prints a per-file plan and prompts first — so the exposure is
unattended `gosf push --yes`/`--force` over a locally modified input. If that
matters for your pipeline, the check has to live in the pipeline for now
(a `--only <glob>` selector is the intended fix, tracked separately).

## [1.9.0] - 2026-07-16

### Added

- **`gosf wiki` — OSF project wikis as first-class synced content** (#66). A new
  command group manages the versioned markdown pages attached to a project:
  - Read: `wiki ls`, `wiki get` (stdout by default, optional dest, `--version=n`),
    `wiki versions`, `wiki open`.
  - Write: `wiki push` (create a page or mint a new version; skips identical
    content), `wiki rm` (confirmation + `--yes` like `gosf rm`), `wiki mv`
    (rename). The `home` page cannot be renamed or deleted (refused client-side).
  - Sync: `wiki add` tracks a markdown file as a `[[wikis]]` manifest entry, and
    `gosf status` / `gosf sync` / manifest-driven `push`/`pull` reconcile wiki
    pages through the **same state machine and gate matrix as files** — pinned
    baseline, `PIN_ONLY`, `REMOTE_NEWER`, `DIVERGED`, `--force`, and
    `--resolve=ours|theirs`. Since OSF exposes no content hash for wiki versions,
    gosf computes MD5s from the page content, fetching only what classification
    needs. `status`/`sync` JSON items gain a `"kind": "file"|"wiki"` field.
  - Reads work anonymously on public projects; a disabled wiki addon produces an
    actionable message. OSF normalizes wiki content on save (CRLF→LF, surrounding
    whitespace trimmed) and serves content as `text/markdown`, so gosf sends the
    right `Accept` header and compares a **canonical** form (not raw bytes) — a
    local file differing only in line endings or a trailing newline still counts
    as in sync, and re-pushing it is a no-op. The live tier asserts the canonical
    round trip and idempotency.

## [1.8.0] - 2026-07-10

### Changed

- **Much faster `gosf sync` / `gosf status` remote scan** (#63). Classifying a
  manifest against OSF was O(files × depth) sequential metadata calls; on large
  manifests this took minutes. Three composing optimizations bring it down to
  seconds: directory listings are memoized per run (files sharing a folder list
  it once, not once each), the scan runs concurrently (new `--jobs`/`-j`, default
  8), and per-file version history is skipped when the directory listing's latest
  version already settles the classification (OSF reports `current_version` and
  the latest MD5 in listings). The skip is proven to classify identically to
  fetching the full history, and falls back to fetching it when needed.

### Fixed

- **`gosf sync` no longer 409s on a `version = 0` entry whose remote file already
  exists** (#62). Such an entry now reconciles against the resolved remote —
  recording the pin when content is identical (`PIN_ONLY`), or pushing a new
  version when it differs — instead of blindly issuing a create that OSF rejects
  with `409 Conflict`. The create-vs-update choice is now keyed on whether the
  remote file exists, not on the manifest `version`.

## [1.7.0] - 2026-07-10

### Added

- **Leveled activity logging** (#59, #60). gosf now reports what it's doing as it
  works — most visibly, `gosf sync`'s remote-scan phase prints `scanning remote
  n/total` progress instead of appearing to hang. A repeatable `-v`/`--verbose`
  flag raises verbosity: default shows high-level activity, `-v` adds per-item
  detail, `-vv` adds HTTP traces (with timestamps + source), `-vvv` is maximum.
  `--quiet` drops to errors only (and conflicts with `-v`). Logs are colorized
  and written to stderr, built on the standard library's `log/slog`.

### Changed

- **Progress bars are now opt-in** (#59). The default for transfers is concise
  log lines (`↑ pushed …`, `↓ pulled …`); pass `--progress-bar`/`-p` for the
  classic live bar. Spinners for indeterminate waits are replaced by activity log
  lines.
- **stdout/stderr split** (#59, #60) — **potentially breaking for scripts.**
  gosf now reserves **stdout for result data** (the `ls`/`status`/`versions`/
  `projects` tables, `info`/`set` fields, `open`'s fallback URL, and every
  `--output=json` payload) and routes **all activity to stderr**. Per-file
  transfer summaries and the `add`/`init`/`cp`/`mv`/`mkdir`/`rm` confirmations
  (e.g. `Created …`, `Added …`) moved from stdout to stderr logs; in text mode
  those commands now print nothing to stdout. `--output=json` keeps stdout pure
  JSON and silences stderr logging unless `-v` is passed. Scripts that parsed
  human confirmations from stdout should read stderr or switch to `--output=json`.
  (`gosf auth login`/`status`/`logout` keep their confirmation lines on stdout —
  that text is the command's result.)

### Removed

- The `briandowns/spinner` dependency, replaced by activity logging (#60).

## [1.6.0] - 2026-07-09

### Added

- **`gosf onboard`** (#57) — an interactive, resumable setup wizard. It detects your current state and starts at the right step: authenticate, attach an OSF project (type a GUID or pick from your projects), and choose local files to push via a collapsible file-tree checkbox UI (defaulting to the files git doesn't track). It records `direction=push` entries in `.gosf/gosf.toml` and stops; run `gosf sync` to upload. Requires an interactive terminal.
- **New-release notifications** (#56) — gosf now tells you when a newer release is available. The check is cached (hits the GitHub releases API at most once a day), best-effort, and suppressed under `--quiet`, `--output=json`, non-interactive use, and when `GOSF_NO_UPDATE_CHECK` is set.

### Fixed

- `gosf status` / `gosf sync` in a directory without a manifest now give an actionable error pointing at `gosf init` (rather than only `gosf add`) (#55).

### Documentation

- README and the `skills/gosf/SKILL.md` agent skill were refreshed to match current behavior: the manifest lives at `.gosf/gosf.toml`, the state-based sync model (`PIN_ONLY`/`DIVERGED`, `--resolve`, `--force`), the `--color` flag, and the `init`/`mkdir`/`mv`/`cp`/`set`/`onboard` commands; the README now leads with the coding-agents section (#53).

## [1.5.0] - 2026-07-09

### Added

- Colorized output and progress spinners (#42). Status glyphs, headers, errors, and the PUBLIC push warning are now colored; indeterminate waits (resolve/list/versions) show a spinner. A global `--color=auto|always|never` flag controls it; color is off automatically under `--output=json`, `--quiet`, `NO_COLOR`, and when stdout is not a TTY.

### Fixed

- **Pushing a new file into a non-root subfolder returned `404 Not Found`** (#45). OSF's osfstorage addresses folders by opaque object ID, not by name; gosf built upload URLs from folder names. New-file uploads now use the parent folder's ID-based upload link (root uploads were unaffected).
- **`gosf mkdir` into a subfolder had the same `404`** (#46), fixed the same way.
- **`gosf versions` reported version `0` for every version, and manifest status version comparisons (`REMOTE_NEWER`/`BEHIND`) were skewed** (#49). The OSF versions endpoint carries the version number as the JSON:API resource `id`, not an attribute; gosf now reads it correctly.
- **`gosf auth logout` failed on a locked or unavailable OS keychain** (headless/HPC) even though the token lived in a file (#48). Logout is now best-effort on the keychain: it always removes the token file, warns on a keychain error, and only fails on a real file-removal error.

### Internal

- New live-OSF integration test tier (`-tags live`) exercising a real private project, a `dev` integration branch with live CI, merged unit+integration coverage measurement (`make cover`), and broadened error-path test coverage (#43, #44, #47, #48, #50).

## [1.4.0] - 2026-07-08

### Added

- **State-based sync safety** (#38): `direction` is now a *default intent* rather than a hard lock. Safety comes from state-based gates at the moment of a destructive action, comparing local (L), the pinned baseline (B), and the remote latest (R).
- Idempotent transfers: `gosf pull` and `gosf push` skip the byte transfer when the local file already matches the remote MD5. Manifest entries that are already byte-identical to the remote are pinned without any transfer (the new `PIN_ONLY` state), so registering already-present inputs no longer mints redundant remote versions or requires hand-editing `.gosf/gosf.toml`.
- `gosf status` now content-compares unpinned (`version = 0`) entries against the remote and reports `PIN_ONLY` / `BEHIND` / `AHEAD` instead of a blanket "never pushed"; it remains read-only. New `DIVERGED` state for entries where both sides changed since the baseline.
- Rich `gosf push` confirmation: bare push prints a per-file plan (header with project title and PUBLIC/PRIVATE visibility, a loud warning when public, `local → remote` action, size, and MD5) and a summary, then prompts for confirmation. `--yes` bypasses the prompt for safe pushes; `--force` also authorizes a remote-newer rollback. In `--output=json` mode `--force` is mandatory (same rule as `gosf rm`).
- `--resolve=ours|theirs` on `push` / `pull` / `sync` to resolve a `DIVERGED` entry explicitly (`ours` keeps local, `theirs` keeps remote). Divergence otherwise fails hard with a diagnostic naming the three states and the exact resolution commands.
- Anonymous reads for public projects (#38): `pull` / `ls` / `info` / `status` / `versions` attempt the fetch unauthenticated and only need a token for private data; a 401/403 is turned into an actionable "run 'gosf auth login' or set OSF_TOKEN" message.
- `gosf ls --output=json` now includes each file's content hashes under `attributes.extra.hashes.{md5,sha256}` (#35).

### Changed

- `gosf sync` runs a divergence/rollback pre-flight before any transfer, so a bulk run never applies a partial, half-resolved state. On a remote-newer pull entry it now fast-forwards and re-pins to the latest version.
- Bare `gosf push` now requires confirmation before writing remote bytes; non-interactive callers must pass `--yes` or `--force` (breaking change for scripts that relied on silent bare push).

### Fixed

- `gosf versions` no longer sends `embed=user`, which the OSF v2 versions endpoint rejects with a 400 (#34).
- `gosf pull` to an explicit alternate destination is a plain download and no longer refuses or re-tracks the existing entry (#36).
- Fresh-manifest creation on `pull`/`push` wrote to `.gosf/.gosf/gosf.toml`; it now correctly writes `.gosf/gosf.toml`.

## [1.3.0] - 2026-06-10

### Changed

- The sync manifest moved from `gosf.toml` to `.gosf/gosf.toml`, grouping gosf's project-level files under a dedicated `.gosf/` directory (following the convention of `.github/`, `.vscode/`, etc.). `gosf init` now creates the `.gosf/` directory automatically, and `FindManifest` walks up looking for `.gosf/gosf.toml`.

  **Migration:** there is no fallback to the old location. Existing users must move their manifest: `mkdir -p .gosf && git mv gosf.toml .gosf/gosf.toml` (or `mv` if not tracked).

## [1.2.0] - 2026-06-05

### Added

- `gosf init <project-id>` — create or update `gosf.toml` with a default project GUID; idempotent
- `gosf pull` bare form (no arguments) — download all `direction=pull|both` manifest entries that are missing or behind
- `gosf pull` auto-tracking — after a successful download, writes/updates the entry in `gosf.toml` with `direction=pull`, version, and MD5; opt out with `--no-track`
- `gosf push` bare form (no arguments) — upload all `direction=push|both` manifest entries that are ahead of manifest or not yet pushed
- `gosf push` auto-tracking — after a successful upload, writes/updates the entry in `gosf.toml` with `direction=push`, version, and MD5; opt out with `--no-track`
- scp-style path semantics on `gosf pull`, `gosf push`, and `gosf add`: trailing slash on source copies contents only; no trailing slash copies the directory itself; omitted dest mirrors the source path
- `gosf add` directory recursion — `gosf add data/dir abc12:/remote/` walks the directory and creates one manifest entry per file
- `gosf add` no-dest form — `gosf add local/path/file.txt` mirrors to remote `/local/path/file.txt`

### Changed

- `gosf sync` now processes **both** push-eligible and pull-eligible entries by default; the `--pull-new` flag has been removed
- `gosf add` direction is always `push`; the `--direction` flag has been removed
- `gosf push` and `gosf pull` refuse to operate on entries whose manifest `direction` conflicts (e.g. pushing a `direction=pull` file) with a clear error message
- Duplicate remote-tracking guard: pushing or pulling to a remote path already tracked under a different local path is a hard error

### Fixed

- `gosf sync` now requires `[project].id` to be set in `gosf.toml`; an empty project ID produces an actionable error pointing to `gosf init`
- `gosf pull` with default dest `"."` now correctly mirrors the remote path instead of treating `.` as a directory name

## [1.1.0] - 2026-06-05

### Added

- `gosf mv <src> <dest>` — rename or move a file/folder within or across OSF projects; uses Waterbutler rename action when src/dest share the same folder, move action otherwise; `--conflict=warn|replace|keep`
- `gosf cp <src> <dest>` — copy a file/folder within or across OSF projects; `--conflict=keep|replace|warn` (default: keep)
- `gosf mkdir <project>:<path>` — create a folder in OSF Storage; `--dry-run`
- `gosf set <project>` — update node title, description, category, and/or tags via `PATCH /v2/nodes/{id}/`; only supplied flags are sent; `--output=json` returns the updated node

## [1.0.1] - 2026-06-05

### Fixed

- `gosf pull` returned 403 on files whose `links.download` URL is at `osf.io` (e.g. `https://osf.io/download/…/`) rather than directly at `files.osf.io`. The `CheckRedirect` policy stripped the `Authorization` header on any host change, including OSF-internal redirects (`osf.io` → `files.osf.io`). The policy now only strips auth when redirecting outside OSF infrastructure (`*.osf.io`), preserving credentials across OSF-internal hops while still preventing token leakage to third-party storage backends (S3, GCS).

## [1.0.0] - 2026-06-05

Initial public release.

### Added

**Commands**
- `gosf ls` — list files and folders in an OSF project or subfolder
- `gosf pull` — download a file or entire folder tree; `--version=N` for historical versions
- `gosf push` — upload a file or directory; `--conflict=skip|overwrite|rename`
- `gosf rm` — delete a file or folder with confirmation prompt; `--yes` to skip
- `gosf projects` — list all accessible OSF projects (requires auth)
- `gosf info` — show project/component metadata
- `gosf open` — construct the OSF web URL and open it in a browser
- `gosf versions` — list all versions of a file, newest first
- `gosf add` — add a file to the `gosf.toml` sync manifest
- `gosf status` — show manifest sync state for each tracked file (exit 1 if anything is out of sync)
- `gosf sync` — push/pull tracked files according to manifest directions; `--pull-new` to download missing files
- `gosf auth login / status / logout` — store and manage OSF personal access tokens

**Core features**
- Two-tier OSF API integration: JSON:API metadata (`api.osf.io`) + Waterbutler file transfer (`files.osf.io`)
- `gosf.toml` sync manifest with per-file `direction` (`push`, `pull`, or `both`) and pinned version/MD5
- Six file states in `gosf status`: `IN_SYNC`, `MISSING`, `BEHIND`, `AHEAD_OF_MANIFEST`, `REMOTE_NEWER`, `NOT_PUSHED`
- Manifest updated automatically after successful `gosf push` and `gosf sync`
- `--output=json` on every command for scripting; JSON goes to stdout, progress to stderr
- `--dry-run` on `push`, `pull`, `rm`, `sync`
- `--quiet` / `-q` to suppress progress bars and informational output
- Progress bars on all file transfers via `schollz/progressbar`
- Signal-aware context: Ctrl-C cancels in-flight HTTP requests and removes partial download files
- Token priority chain: `--token` flag → `OSF_TOKEN` env var → token file → OS keychain
- Plaintext token fallback in `~/.config/gosf/token` (mode `0600`) for headless/HPC systems (via `--no-keychain`); kept separate from `config.toml` so the config file is safe to commit to version control
- Token never printed in logs or error output

**Distribution and CI**
- Static single-binary releases for Linux, macOS, Windows on amd64 and arm64
- GoReleaser pipeline publishing cross-platform archives + checksums to GitHub Releases
- GitHub Actions CI: `gofmt`, `go vet`, race-detector test suite, `golangci-lint v2`, cross-compile matrix
- End-to-end integration test suite with an in-process fake OSF + Waterbutler server (36 tests)
- One-liner installers: `curl … | bash` for Linux/macOS and `irm … | iex` for Windows; verifies SHA-256 checksums and supports `GOSF_INSTALL_DIR` override
- [skills.sh](https://skills.sh) agent skill (`skills/gosf/SKILL.md`) for AI coding agents; install with `npx skills add BU-Neuromics/gosf`
