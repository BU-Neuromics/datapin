# datapin — pin, sync, and publish research data

## Project overview

`datapin` is a single-binary Go CLI that keeps a project's data files
verifiably in sync with remote storage via a committed manifest with git-like
safety gates. It is the reboot of `gosf` (an OSF client) into a multi-backend
FAIR data publication tool: workspace remotes (OSF today) keep the mutable
push/pull/sync workflow; archive backends (Zenodo/InvenioRDM first) will add
DOI-minting `publish`. Distributed to researchers; CLI-only (no SDK scope).

Architecture and roadmap: [`docs/reboot-plan.md`](./docs/reboot-plan.md)
(§2.4 Zenodo API, §4.1–4.7 architecture, §6 testing, §8 phases).
Settled decisions D1–D10 — do not relitigate:
[`docs/datapin-handoff.md`](./docs/datapin-handoff.md).

**Module path:** `github.com/BU-Neuromics/datapin`
**Binary name:** `datapin`
**CLI framework:** Cobra + Viper
**Releases:** restart at `v0.1.0` under the datapin name (gosf reached v2.1)

Backward compatibility with gosf (kept until migration completes): a legacy
`.gosf/gosf.toml` manifest is found and loaded **read-only** (`manifest.Save`
refuses it with a migration hint), `~/.config/gosf` config/token stores are
read when datapin's are absent (writes always target datapin paths), and
`GOSF_*` env vars are accepted with a deprecation warning (`internal/env`).

## Command structure

```
datapin ls       <project>[:<path>]
datapin pull     <project>[:<path>] [dest]
datapin push     <src> <project>:<path>
datapin rm       <project>:<path>
datapin versions <project>:<path>
datapin projects
datapin info     <project>
datapin auth login
datapin auth status
datapin auth logout
datapin open     <project>[:<path>]
datapin add      <local-path> <project>:<remote-path>
datapin status
datapin sync
datapin wiki ls       <project>
datapin wiki get      <project>[:<page>] [dest]
datapin wiki push     <src.md> <project>[:<page>]
datapin wiki rm       <project>:<page>
datapin wiki mv       <project>:<old> <new-name>
datapin wiki versions <project>:<page>
datapin wiki open     <project>[:<page>]
datapin wiki add      <local.md> [<project>:]<page>
```

Wiki page addressing: `<project>:<page-name>`. The part after the colon is a
**wiki page name** (a flat namespace, not a path) and may contain spaces; where
optional it defaults to `home`. Component addressing (`abc12/xyz34:page`) works
as for files.

Path convention: `abc12:/data/results/file.csv`
- 5-char alphanumeric before the colon = OSF project/component GUID
- After the colon = path within OSF Storage
- Components (sub-projects) addressable as `abc12/xyz34:/path`

## Auth design

Priority order: `--token` flag > `OSF_TOKEN` env var > config file > OS keychain

- Absent all: unauthenticated mode (public projects only), same code paths
- Never echo token in logs or error output
- Store via go-keyring; plaintext fallback in config for headless/HPC
- Config file: `~/.config/datapin/config.toml`

## OSF API

The OSF REST/Waterbutler specifics that used to live here moved to
[`docs/osf-api.md`](./docs/osf-api.md). Architecture direction (backend
adapter interface, workspace vs archive remotes, Zenodo/InvenioRDM) is in
[`docs/reboot-plan.md`](./docs/reboot-plan.md); operational decisions in
[`docs/datapin-handoff.md`](./docs/datapin-handoff.md).

## Project structure

```
datapin/
├── cmd/
│   ├── root.go              # root command, global flags, version
│   ├── ls.go
│   ├── pull.go              # --version=<n> flag for specific version download
│   ├── push.go              # bare push selects by state; manifest update on push
│   ├── rm.go
│   ├── versions.go          # datapin versions <project>:<path>
│   ├── projects.go
│   ├── info.go
│   ├── auth.go
│   ├── open.go
│   ├── add.go               # datapin add — add entry to .datapin/datapin.toml
│   ├── status.go            # datapin status — show manifest sync status
│   ├── sync.go              # datapin sync — push/pull; processPushEntry/processPullEntry gates
│   ├── onboard.go           # datapin onboard — guided setup (auth → project → pick files)
│   ├── wiki.go              # datapin wiki command group + shared helpers (parseWikiTarget, findWikiPage, friendlyWikiError)
│   ├── wiki_ls.go           # datapin wiki ls
│   ├── wiki_get.go          # datapin wiki get (stdout/dest, --version)
│   ├── wiki_versions.go     # datapin wiki versions
│   ├── wiki_open.go         # datapin wiki open
│   ├── wiki_push.go         # datapin wiki push (create/new-version, idempotent skip)
│   ├── wiki_rm.go           # datapin wiki rm
│   ├── wiki_mv.go           # datapin wiki mv (rename)
│   ├── wiki_add.go          # datapin wiki add — [[wikis]] manifest entry
│   ├── wiki_manifest.go     # fetchWikiRemoteState + wikiScanCache + canSkipWikiHistory (scan)
│   ├── wiki_sync.go         # executeWikiEntry, wikiEntryPlan, atomic write
│   ├── gate.go              # state-based safety: syncAction + sync/push/pullDecision, divergenceError, entryPlan
│   ├── scan.go              # shared concurrent manifest scan (sync/push/pull)
│   ├── prompt.go            # printPushPlan, confirmation/TTY helpers
│   ├── auth_helpers.go      # friendlyAuthError (401/403 → auth hint)
│   └── manifest_helpers.go  # computeLocalMD5, localFileMatches, fileVersionsToRemote, latestRemoteVersion
├── internal/
│   ├── client/
│   │   ├── osf.go          # JSON:API metadata client
│   │   ├── wiki.go         # wiki API surface (List/Get/Create/Rename/Delete + IsWikiDisabled)
│   │   └── waterbutler.go  # file transfer client; Upload returns UploadResult
│   ├── resolver/
│   │   ├── path.go         # path string → Waterbutler URLs
│   │   └── cache.go        # per-run memoizing FileLister (dedupes dir listings)
│   ├── manifest/
│   │   ├── manifest.go     # Load, Save, FindManifest, Entry, Manifest types
│   │   └── status.go       # ClassifyFile, FileState, RemoteVersion
│   ├── config/
│   │   └── config.go       # config file + keychain + env
│   ├── update/
│   │   └── update.go       # cached "new release available" check
│   ├── gitutil/
│   │   └── candidates.go   # local push candidates (git-untracked; fs-walk fallback)
│   ├── picker/
│   │   ├── tree.go         # pure file-tree model (check/partial, collapse, flatten)
│   │   └── picker.go       # bubbletea view over the tree (onboard file selection)
│   ├── log/
│   │   └── log.go          # leveled stderr logger (slog + custom human handler)
│   └── output/
│       ├── format.go       # human-readable vs --output=json
│       ├── style.go        # color init + Green/Red/Yellow/Cyan/Bold/Dim helpers
│       └── table.go        # ANSI-safe aligned table renderer (Cell, RenderTable)
├── go.mod
├── .goreleaser.yaml
├── CLAUDE.md
└── main.go
```

## Key UX requirements

- **Opt-in** progress bars on pull/push (`schollz/progressbar`) via
  `--progress-bar`/`-p`; the default is log-style start/finish lines (see the
  Logging section). `waterbutler.Upload/Download` take a `showProgress bool`
  gated by `progressBarEnabled()`.
- `--dry-run` on push, pull, rm
- `--output=json` on all commands for scripting
- `--conflict=skip|overwrite|rename` on push (default: skip)
- `--quiet` suppresses progress/non-error output (drops logging to errors only)
- `--verbose`/`-v` (repeatable) raises log verbosity; see the Logging section.
- Proper non-zero exit codes on errors
- **Update check** (`internal/update`): after each command, `update.MaybeNotify`
  prints a one-line "new release available" notice to stderr when the installed
  version is behind the latest GitHub release. Best-effort and cached — it hits
  the releases API at most once/day (`~/.config/datapin/update_check.json`), uses a
  short timeout, and never blocks. Gated off under `--quiet`, `--output=json`,
  non-TTY stderr, a `dev` build, a Ctrl-C'd run, and when `DATAPIN_NO_UPDATE_CHECK`
  is set. The gate (`shouldNotify`) and semver compare (`newerAvailable`) are
  pure/unit-tested; the checker's HTTP/cache/clock are injectable.
- Colorized output (`fatih/color`). Indeterminate waits no longer use a spinner;
  they emit an INFO activity log line instead (the `briandowns/spinner` dependency
  and `internal/output/spinner.go` were removed in the logging sweep). Color is
  resolved once in `root.go`'s `PersistentPreRunE`
  via `output.InitColor`: on only when stdout is a TTY, forced off under
  `--output=json` (hard invariant — machine output is never colored), `--quiet`,
  and `NO_COLOR`. Global `--color=auto|always|never` overrides. All styling flows
  through the `internal/output` helpers, so it degrades to plain automatically.

### Colorized tables

`text/tabwriter` measures width in bytes and misaligns once ANSI codes are
present, so colored tables (`ls`, `status`, `versions`, `projects`) use
`output.RenderTable(header, [][]output.Cell)` instead: each `Cell{Text, Style}`
is padded on its *plain* text, then the `Style` func colors the padded cell, so
columns line up identically with color on or off. The final column is never
padded (no trailing whitespace). `output.NewTabWriter`/`PrintHeader` were removed.

### `--output=json` contract

Every command supports `--output=json`. Result types live in
`internal/output/result.go` so the contract is explicit and unit-tested.
JSON goes to stdout; progress bars are suppressed in JSON mode.

| Command | JSON shape |
|---------|-----------|
| `ls` | array of file objects (`[]` when empty, never `null`); each file object carries its content hashes under `attributes.extra.hashes.{md5,sha256}` (empty hashes omitted, so folders carry none) |
| `info` | the node object |
| `projects` | array of node objects |
| `open` | `{"url": "..."}` (does not launch a browser) |
| `pull` | `{"downloaded": [{"path","size"}], "dry_run": bool}` |
| `push` | `{"uploaded": [{"path","action"}], "dry_run": bool}` where action ∈ upload\|overwrite\|rename\|skip |
| `rm` | `{"node","path","kind","dry_run"}` — requires `--yes` (no interactive prompt in JSON mode) |
| `versions` | `{"versions": [{"version","date_created","size","contributor"}]}` — `[]` when empty |
| `wiki ls` | array of `{id, name, version, size, date_modified}` (`[]` when empty) |
| `wiki get` | `{project, page, version, size, content}` |
| `wiki push` | `{project, page, action, version, dry_run}` — action ∈ create\|update\|skip |
| `wiki rm` | `{node, page, dry_run}` — requires `--yes` (as `rm`) |
| `wiki mv` | `{node, from, to, dry_run}` |
| `wiki versions` | same shape as `versions` |
| `wiki add` | `{entries: [{local, page, project, version, md5}], manifest_created}` |
| `status`/`sync` items | each carries `"kind": "file"\|"wiki"` |

### Logging and verbosity (`internal/log`)

Activity/status is emitted through a leveled logger (`internal/log`, built on
stdlib `log/slog` with a custom human handler) that writes **colorized lines to
stderr** — stdout stays reserved for machine/result output (tables, `--output=json`,
`open`'s URL, `info` fields). The verbosity ladder is a repeatable `-v/--verbose`
count, resolved by the pure `resolveLevel(verbosity, quiet)`:

| Flags     | Level  | Shows |
|-----------|--------|-------|
| (default) | INFO   | high-level activity ("scanning remote 12/50", "↑ pushed x v1→v2") + results |
| `-v`      | DEBUG  | per-item detail (resolved IDs, upload URLs, version counts) |
| `-vv`     | TRACE  | HTTP-level traces; also adds a timestamp + source location to every line |
| `-vvv`    | TRACE2 | maximum detail |
| `--quiet` | ERROR  | errors only (overrides verbosity; `--quiet -v` is rejected) |

Custom slog levels `LevelTrace` (-8) and `LevelTrace2` (-12) extend the built-ins.
Call sites use `log.Infof/Debugf/Warnf/Errorf/Tracef/Trace2f`. `log.Init` is called
once in `root.go`'s `PersistentPreRunE`; `SetWriter` redirects to a buffer in tests.
`--output=json` silences logs by default (so stderr stays clean for scripts) unless
`-v` is passed — see the pure `logQuiet(quiet, jsonMode, verbosity)`.

**Progress bars are opt-in** via `--progress-bar`/`-p`; the default is
log-style start/finish lines. `showProgressBar(progressFlag, quiet, jsonMode,
stderrTTY)` (pure) gates the live bar — only drawn when `-p` is set on an
interactive, human (non-json/quiet) stderr. Waterbutler's `Upload`/`Download`
take a `showProgress bool`; call sites pass `progressBarEnabled()`.

Rollout: **complete across every command.** All activity/status/notice output —
query-command spinners (now INFO log lines), the explicit-form `push`/`pull`
streamers, and the `add`/`init`/`cp`/`mv`/`mkdir`/`rm` mutation confirmations —
routes through `internal/log` on stderr. The stream contract is: **result data
stays on stdout** (`ls`/`status`/`versions`/`projects` tables, `info`/`set` node
fields, `open`'s fallback URL, and every `--output=json` payload) while
**everything else is a stderr log**. `auth login`/`status`/`logout` keep their
human confirmation lines on stdout (that text *is* the command's result).

### Cancellation

`Execute` installs a `signal.NotifyContext` (SIGINT/SIGTERM) and runs via
`ExecuteContext`. Commands use `cmd.Context()`, so Ctrl-C cancels in-flight
HTTP requests and aborts transfers. A failed download removes its partial file.

## Sync manifest (.datapin/datapin.toml)

### Schema

```toml
[project]
id = "abc12"          # default project GUID for entries that omit project field

[[files]]
local   = "data/raw/counts.h5"    # path relative to repo root
remote  = "/data/raw/counts.h5"   # path within OSF Storage
version = 3                       # pinned OSF version number; 0 = not yet pushed
md5     = "d41d8cd98f00b204e9800998ecf8427e"  # MD5 of pinned version; "" if version=0
project = "xyz89"                 # optional per-entry override of [project].id
```

**There is no `direction` field** (removed in #81, which finished #38). What a
transfer should do is decided at the moment of the transfer from the L/B/R
comparison; a standing per-entry default could only block transfers that were
unambiguously safe. Manifests written by datapin ≤1.9 still carry the key: `Load`
counts it via the pure `LegacyDirectionCount`, warns once, and ignores it, and
`Save` drops it.

Wiki pages are tracked with `[[wikis]]` entries, which mirror `[[files]]` but
address a named wiki page instead of a storage path:

```toml
[[wikis]]
local     = "docs/home.md"   # markdown file, relative to repo root
page      = "home"           # wiki page name on OSF
version   = 3                # pinned wiki version identifier; 0 = not yet pushed
md5       = "…"              # MD5 of the pinned version's content, computed by datapin
project   = "xyz89"          # optional per-entry override
```

Validation on load:
- No duplicate `local` paths — **across `[[files]]` and `[[wikis]]` together**.
- No duplicate `(project, remote)` pairs; no duplicate `(project, page)` pairs.
- Wiki page names obey OSF rules (non-blank, no `/`, ≤100 chars).
- Every entry must resolve a project (own field or `[project].id`).

Wiki entries flow through the same `ClassifyFile` state machine and gate matrix
via `WikiEntry.BaselineEntry()` (which exposes the pinned `version`+`md5` as an
`Entry`). Remote versions for a wiki are built by `fetchWikiRemoteState`, which
hashes fetched content: it fetches the latest once and skips older-version
hashing when local matches latest or the pinned baseline (`canSkipWikiHistory`,
the wiki analogue of `canSkipVersionHistory`), memoized per project by
`wikiScanCache`. `datapin status`, `datapin sync`, and manifest-driven push/pull treat
wiki entries as first-class rows; a wiki "transfer" is a metadata-API call
(push = create page / new version; pull = atomic local file write via
`writeFileAtomic`).

### File states

`ClassifyFile(entry, localMD5, remoteVersions, noCheckRemote)` in `internal/manifest/status.go`
compares three values — **L** = local, **B** = pinned baseline (`version`+`md5`),
**R** = remote latest — and reports how many sides diverged from the baseline:

| State              | Meaning |
|--------------------|---------|
| `IN_SYNC`          | L = B, R = B |
| `MISSING`          | Local file does not exist |
| `BEHIND`           | Local MD5 matches an older remote version (safe to pull) |
| `AHEAD_OF_MANIFEST`| L ≠ B, R = B — only local moved (a real local update) |
| `REMOTE_NEWER`     | L = B, R ≠ B — only remote moved (safe fast-forward for pull) |
| `PIN_ONLY`         | Local content already equals remote latest but the pin is stale/absent → record the pin, **no transfer** |
| `DIVERGED`         | L ≠ B **and** R ≠ B and local matches no remote version → unsafe, fail hard |
| `NOT_PUSHED`       | version = 0 **and** the remote path does not exist |

Unpinned entries (version = 0) are **content-compared** against the remote when it
exists, so an already-identical file classifies as `PIN_ONLY` (not `NOT_PUSHED`).
`NOT_PUSHED` now means only "version = 0 and nothing on the remote to compare".

When `--no-check-remote`: only IN_SYNC, MISSING, AHEAD_OF_MANIFEST, NOT_PUSHED are
possible (the remote-comparing states need network).

### Rate limiting and request efficiency (issue #86)

OSF throttles: roughly **100 requests/hour unauthenticated** and **10,000/day
authenticated**, signalled by `429` plus a `Retry-After` header.

- **`page[size]=100` on every paginated endpoint.** OSF's JSON:API defaults to
  10 items per page and caps at 100, so an unqualified listing costs up to 10×
  the requests it needs. `withPageSize` (pure, in `internal/client/osf.go`) is
  applied inside each pagination loop, which covers initial URLs, folder
  `related` hrefs, and `links.next` alike; an existing `page[size]` is left
  alone so the key is never duplicated.
- **Bounded retry** (`internal/client/retry.go`). `doGet` retries `429`/`502`/
  `503`/`504` up to `maxRetries`, honouring `Retry-After` exactly (seconds or
  HTTP-date). A wait longer than `maxRetryDelay` is **declined, not truncated**:
  the quota is genuinely spent, and retrying early only earns another 429. The
  wait goes through `OSFClient.sleep`, injectable in tests and context-aware so
  Ctrl-C during a throttle still aborts.
- **Throttling is never mistaken for absence.** `resolver.NotFoundError` +
  `resolver.IsNotFound` distinguish "this path is not on the remote" from "the
  request failed". `fetchRemoteState` returns `(nil, nil, nil)` only for the
  former; everything else is an error. This matters because "absent" classifies
  an unpinned entry as `NOT_PUSHED`, which under #81 makes `sync` upload it — so
  folding a 429 into that answer caused spurious re-uploads.
- **`friendlyAPIError(err, authenticated)`** maps a 429 to an actionable
  message, and `warnUnauthenticated` warns scanning commands that are anonymous
  (`config.LoadToken` returns `""` for a locked keychain just as it does for a
  deliberate anonymous run).
- **`fakeosf` paginates** (10 default, `page[size]` honoured, capped at 100) and
  exposes `ListRequests()`, so the request-count reduction is asserted rather
  than assumed. It previously returned everything in one page with `next: nil`.

What is *not* available: OSF has no bulk/multi-get endpoint, and `?path=` on the
files endpoint returned the wrong item for a nested path when probed — it is not
a safe substitute for the tree walk.

### Scan performance (sync / status pass 1)

Classifying a manifest against the remote is the dominant cost of `sync`/`status`
(OSF's metadata API is ~1–3s per request). Three optimizations keep it fast:

- **Memoized directory listings** — `resolver.NewCachingLister` wraps the client
  for the duration of one command so the many files that share directories don't
  each re-walk (and re-list) the same folders. `singleflight` collapses concurrent
  identical fetches, so each unique listing hits the network exactly once.
- **Bounded concurrency** — pass 1 classifies entries through an `errgroup` worker
  pool (`--jobs`/`-j`, default `defaultScanJobs` = 8), overlapping round-trips.
  Only classification is parallel; the pre-flight and transfer passes stay serial.
- **Skip version history when the listing settles it** — OSF returns
  `attributes.current_version` (latest number) and `attributes.extra.hashes.md5`
  (latest MD5) in a directory listing. When local content equals the remote latest,
  or a pinned entry's local still equals its baseline, classification cannot depend
  on older versions, so `fetchRemoteState` skips the per-file `GetFileVersions`
  call and synthesizes a latest-only `RemoteVersion`. The decision is the pure,
  unit-tested `canSkipVersionHistory`; `TestCanSkipVersionHistory_EquivalentToFullHistory`
  proves the synthetic slice classifies identically to the full history. The skip
  requires `current_version > 0`; if OSF omits it, datapin falls back to fetching
  history (`TestLive_ListingCarriesCurrentVersion` guards the assumption).

### State-based safety (gate matrix)

Safety comes entirely from state-based gates evaluated at the moment of a
destructive action, keyed on how many sides diverged from the baseline. Nothing
recorded in the manifest steers a transfer:

| L vs B | R vs B | `sync` | `push` | `pull` |
|--------|--------|--------|--------|--------|
| L=B | R=B | no-op | no-op | no-op |
| unpinned, L=R | — | pin, no transfer (`PIN_ONLY`) | pin | pin |
| local absent | exists | download + pin (`MISSING`) | · skip | download + pin |
| L≠B | R=B | report, exit 1 (`AHEAD`) | real update (confirm) | skip unless `--force` |
| L=B | R≠B | fast-forward, re-pin | skip unless `--force` (rollback) | fast-forward, re-pin |
| L≠B | R≠B | `DIVERGED` → fail hard, `--resolve` | `DIVERGED` → `--resolve=ours` | `DIVERGED` → `--resolve=theirs` |

The three policies are the pure, table-tested `syncDecision`/`pushDecision`/
`pullDecision` in `cmd/gate.go`; they return a `syncAction`, and the single
executor `executeEntry` (`cmd/sync.go`, `executeWikiEntry` for pages) performs
it. That split is why a file and a wiki page in the same state now reconcile the
same way, and why `sync`, `push`, and `pull` cannot drift apart.

- **Idempotent transfers**: explicit `pull`/`push` skip the byte transfer when the
  local file already matches the remote MD5 (`localFileMatches`,
  `redundantOverwrite` in `cmd/`). Manifest-driven transfers pin without
  transferring in the `PIN_ONLY` state (`pinEntry` in `cmd/sync.go`).
- **`--force`** on `sync`/`pull` discards local modifications, restoring the
  tracked version from OSF; on `push` it authorizes a deliberate rollback over a
  newer remote version. It does **not** cover divergence.
- **`--yes`** bypasses the push confirmation prompt for *safe* actions (new file,
  real update) without authorizing a rollback.
- **`--resolve=ours|theirs`** is the only way through a `DIVERGED` entry: `ours`
  takes local (push a new version), `theirs` takes remote (download + re-pin).
  Divergence is detected in a pre-flight pass before any bytes move, so a bulk
  `sync`/`push` never applies a partial, half-resolved state.
- Helpers live in `cmd/gate.go` (`divergenceError`, `latestRemoteVersionInfo`,
  `pushActionLabel`, `needsPushConfirmation`, `summarizePush`, `validateResolve`,
  `entryPlan`) and `cmd/prompt.go` (`printPushPlan`, `isInteractive`).

### MD5 sourcing

- **Local**: stream file through `crypto/md5` (never read fully into memory).
- **Remote**: `data.attributes.extra.hashes.md5` from the versions list API response, and from Waterbutler upload response (`UploadResult.MD5`).
- Compare local MD5 against **all** remote version MD5s (not just declared version) to distinguish BEHIND from AHEAD_OF_MANIFEST.

### `internal/manifest/` package

- `manifest.go`: `Load`, `Save` (atomic temp+rename), `FindManifest` (walks up from cwd), `Entry.ResolveProject`.
- `status.go`: `FileState`, `RemoteVersion`, `ClassifyFile`.
- `IsNotFound(err)` checks for `NotFoundError` from `FindManifest`.

### `internal/client/` changes

- `FileVersionAttributes` now includes `Extra.Hashes.MD5` from `attributes.extra.hashes.md5`.
- `WaterbutlerClient.Upload` returns `(UploadResult, error)` — `UploadResult` carries `Version int` and `MD5 string` from the Waterbutler response.

### `datapin add` (`cmd/add.go`)

```
datapin add <local-path> <project>:<remote-path>
```
- Creates .datapin/datapin.toml if absent.
- Errors if local path already in manifest.
- Fetches remote version+MD5 if file exists; writes version=0, md5="" otherwise.
- Prints .gitignore tip for local files >50 MB.

### `datapin status` (`cmd/status.go`)

- Computes local MD5 for each entry.
- Fetches remote versions unless `--no-check-remote` — **including for unpinned
  (version = 0) entries**, so an already-identical file reports `PIN_ONLY` instead
  of a blanket "never pushed". Status is **read-only**: it reports, never mutates.
- Tabular output: STATUS / LOCAL PATH / VER / DETAIL.
- Exit code 0 only if all entries are `IN_SYNC` (`statusIsInSync`); exit code 1
  otherwise (CI-friendly). `PIN_ONLY` and `DIVERGED` count as not-in-sync.
- `--output=json` emits array of `{path, kind, state, declared_version, remote_latest_version}`.

### `datapin sync` (`cmd/sync.go`)

Non-interactive. Three passes — `scanEntries`/`scanWikiEntries` classify all,
`syncDecision` picks each entry's action, a divergence **pre-flight** fails hard
before any transfer, then `executeEntry`/`executeWikiEntry` run.

| State               | `sync` action |
|---------------------|---------------|
| IN_SYNC             | ✓ skip |
| PIN_ONLY            | pin, no transfer |
| MISSING             | download latest + pin — **no flag required** |
| BEHIND              | fast-forward to latest, re-pin |
| REMOTE_NEWER        | fast-forward to latest, re-pin |
| NOT_PUSHED (exists) | upload, set manifest |
| NOT_PUSHED (absent) | · skip — nothing anywhere |
| AHEAD_OF_MANIFEST   | report only, no transfer; run exits 1 (`--force` → restore) |
| DIVERGED            | fail hard, `--resolve=ours\|theirs` honored as given |

`AHEAD_OF_MANIFEST` is a **reporting no-op with a non-zero exit**, not a hard
error: unlike `DIVERGED` it only risks local work (on restore) or an unwanted
remote version (on push), both recoverable, so it must not fail a whole bulk run.
`MISSING` downloads unconditionally — writing a file that does not exist destroys
nothing, and requiring a flag for it was the bug in #81.

Flags: `--force`, `--resolve=ours|theirs`, `--dry-run`, `--no-check-remote`,
`--jobs`/`-j`.

### `datapin push` manifest integration

- **Bare `datapin push`** (manifest-driven) runs classify → pre-flight → confirm →
  execute. A push that writes remote bytes (new file / new version) prints a rich
  per-file plan (header with project title + PUBLIC/PRIVATE and a loud warning when
  public, per-file `local → remote` + action + size + MD5, and a summary line) and
  prompts for confirmation on a TTY. `--yes`/`--force` bypass the prompt; in
  `--output=json` mode `--force` is **mandatory** (same rule as `datapin rm`), and a
  non-TTY run without `--yes`/`--force` refuses rather than hang.
  Bare push selects entries by state (`pushDecision`): AHEAD / NOT_PUSHED with
  local content / PIN_ONLY, plus REMOTE_NEWER and BEHIND under `--force` (a
  deliberate rollback). An entry whose local content is already on the remote
  carries no work to publish, so it is skipped rather than failing the run.
- **Explicit `datapin push <src> <project>:<path>`** keeps the `--conflict`
  behavior; it additionally skips an overwrite that would merely re-mint identical
  bytes.
- After a successful push, `UploadResult.Version > 0` → update manifest atomically.

### Anonymous reads

`pull`/`ls`/`info`/`status`/`versions` attempt the fetch unauthenticated (empty
token is a valid client) and only need a token for private data. A raw 401/403 on
a read is wrapped by `friendlyAuthError` (`cmd/auth_helpers.go`) into an
actionable "run 'datapin auth login' or set OSF_TOKEN" message. `push`/`sync`/
`projects` still require a token up front.

### `datapin onboard` (`cmd/onboard.go`)

Interactive, resumable guided setup (TTY-only; errors under `--output=json` or a
non-TTY). Detects state and enters at the first unsatisfied phase: **auth**
(offered, not required — `runLogin`, shared with `datapin auth login`) → **project**
(attach a GUID: `--project`, a numbered pick from `client.GetUserNodes`, or typed)
→ **select** (candidates from `internal/gitutil.Candidates`, the bubbletea tree
picker in `internal/picker`, remote base via `--remote-base`/prompt) → writes
manifest entries and **stops**, pointing at `datapin sync`. Pure helpers
(`untrackedCandidates`, `remotePath`) and the tree model are unit-tested; the
guard paths (non-TTY / `--output=json`) are integration-tested; and the full
interactive flow is driven end-to-end over a **pseudo-terminal** in
`integration/onboard_pty_test.go` (`creack/pty`). Because lipgloss/bubbletea
query the terminal (OSC 11 background, CPR, DA1) and a bare PTY isn't an emulator,
the test's reader answers those queries; it skips if a PTY can't be allocated.
New deps: `charmbracelet/bubbletea` + `lipgloss` + `creack/pty` (pinned to keep
the go 1.24 toolchain).

### Exit code handling

`exitCodeError` in `cmd/status.go` carries a numeric exit code without printing an error message.
`Execute()` in `cmd/root.go` handles it: `errors.As(err, &exitErr)` → `os.Exit(exitErr.code)`.
All other errors are printed to stderr and exit 1. `rootCmd.SilenceErrors = true` prevents Cobra's double-printing.

## Development notes

- Build: `go build -o datapin .`
- Test: `go test ./...` (and `go test -race ./...`)
- Format check: `gofmt -l .` (must print nothing)
- Vet: `go vet ./...`
- The OSF API requires no auth for public projects; token elevates to private

### Agent-skill parity (`cmd/skill_doc_test.go`)

`skills/datapin/SKILL.md` is a **shipped artifact** — installed into coding agents
via skills.sh — and its frontmatter `description` is what decides whether the
skill loads for a task at all. It has no compiler and drifted silently: the whole
`datapin wiki` group shipped in v1.9.0 and went undocumented for two releases,
description included, so agents asked about an OSF wiki were never offered the
skill.

`cmd/skill_doc_test.go` walks the real cobra tree and asserts:

1. every command path (`datapin wiki add`, …) appears in the skill body;
2. every non-hidden flag, global and per-command, appears in the skill;
3. every top-level command name appears in the frontmatter `description`.

Deliberate omissions go in `undocumentedCommands` / `undocumentedFlags` / the
`excused` map **with a reason** — the point is to force a decision, not to be
unskippable. It runs in the normal `go test -race ./...` CI step, so adding a
command or flag without documenting it fails the build. When you add either,
update the skill in the same PR.

### Test tiers

1. **Unit** — pure functions and HTTP clients against `httptest` (`go test ./...`).
2. **Integration** (`-tags integration`, `integration/`) — the built binary driven
   against the in-process `fakeosf` server. Fast, hermetic, runs in CI.
3. **Live** (`-tags live`, `integration/live/`) — the built binary against a **real**
   private OSF project. Compiled only under `-tags live`; each test skips unless
   `OSF_TEST_TOKEN` + `OSF_TEST_PROJECT` (+ optional `OSF_TEST_COMPONENT`) are set.
   Tests write under a unique `/datapin-ci-<nano>-<pid>/` folder and delete it on
   cleanup, so they are repeatable and leave no residue. Run privately:
   ```
   OSF_TEST_TOKEN=… OSF_TEST_PROJECT=… OSF_TEST_COMPONENT=… \
     go test -tags live -count=1 -v ./integration/live/...
   ```
   The `fakeosf` server encodes our *assumptions*; the live tier catches where real
   OSF/Waterbutler diverges (e.g. the cross-project new-version push 404, captured
   by the skipped `TestLive_ComponentPushNewVersion` regression test).

### Coverage

`make cover` reports **merged unit + integration** coverage. Integration/live tests
drive the compiled binary as a subprocess, so plain `go test -cover` misses them
(and undercounts `cmd`). The harness builds the binary with `-cover` and points it
at `GOCOVERDIR` when `DATAPIN_COVERDIR` is set; `go tool covdata` then merges the
subprocess profiles with the unit `-coverprofile` into one number and
`coverage/coverage.txt`. Real baseline: `cmd` ~75%, `internal/*` 84–98%.

### Branch model

`dev` is the integration branch and the repo default: **PRs target `dev`** and get
the full non-live CI. `main` is the release branch; promote `dev` → `main` once the
live suite is green.

### CI / Release

- `.github/workflows/ci.yml` runs on every push to `main`/`dev` and every PR:
  gofmt check, `go vet`, `go test -race`, integration tests, a build, and a
  cross-compile matrix over linux/darwin/windows × amd64/arm64. The Go version is
  read from `go.mod` via `go-version-file`, so bumping the toolchain is a one-line
  change.
- `.github/workflows/live.yml` runs the live suite on **push to `dev`** and manual
  `workflow_dispatch` (never on PRs). Serialized via a `live-osf` concurrency group.
  Requires repo secret `OSF_TEST_TOKEN` and repo variables `OSF_TEST_PROJECT` /
  `OSF_TEST_COMPONENT`; skips gracefully if the token is absent.
- `.github/workflows/release.yml` runs on a `v*` tag push and invokes
  GoReleaser (`release --clean`) using `.goreleaser.yaml` to build and publish
  the cross-platform archives + checksums to a GitHub Release.
- To cut a release: tag `vX.Y.Z` and push the tag. The version is injected into
  the binary via `-ldflags -X .../cmd.version=<version>`.
- Validate the release config locally with `goreleaser check` and
  `goreleaser build --snapshot --clean --single-target`.
- `golangci-lint` **is** wired into CI (a required check, golangci-lint-action
  v8 / lint v2.5.0, configured by `.golangci.yml`). Run it locally before pushing:
  `golangci-lint run ./...` must print `0 issues`. `errcheck` is active, so check
  or explicitly discard (`_, _ = w.Write(...)`) every returned error, and don't
  leave unused symbols (`unused`).

---

## Development strategy

### Test-driven development (REQUIRED)

This project follows test-driven development. The rule is non-negotiable:

> **Write the failing test first. Watch it fail. Then write the code that makes
> it pass.**

Concretely, for every change — a new command, a bug fix, a new helper:

1. **Red** — Write a test that encodes the desired behaviour. Run it; confirm it
   fails for the *expected* reason (not a compile error in the test itself).
2. **Green** — Write the minimum production code to make the test pass.
3. **Refactor** — Clean up with the test as a safety net.

**Bug fixes start with a regression test.** Before fixing any bug, write a test
that reproduces it and fails. The fix is done when that test goes green. This is
how we prevent the same class of bug twice. If a bug shipped, it means a test
was missing — add it.

**No production logic lands without a test that exercises it.** The only
exceptions are thin glue that cannot fail in isolation (e.g. a Cobra `RunE` that
only wires flags to an already-tested function, or a `main()`). Push the real
logic *out* of those glue layers and into tested functions.

#### What this means in practice

- **Pure functions** (`ParseTarget`, `RootUploadURL`, `AppendUploadName`,
  `FormatSize`, `findFreeName`, `splitPath`, `buildOSFWebURL`): test directly with
  table tests.
- **HTTP clients** (`internal/client`): test against `httptest.Server`. The
  client base URLs are injectable fields so tests point them at the test server.
  Cover happy path, pagination, and every error status the command maps.
- **Path resolution** (`internal/resolver`): the `Resolver` depends on a
  `FileLister` interface, not the concrete client. Test `ListDir` with a mock
  that returns canned `FileItem` trees — no network.
- **Config** (`internal/config`): use `keyring.MockInit()` and a temp
  `XDG_CONFIG_HOME`/`os.UserConfigDir` so tests never touch the real keychain or
  the developer's config. Cover the full token priority chain.
- **Command glue** (`cmd`): keep `RunE` bodies thin. Extract decision logic into
  functions in `internal/` and test those. Filesystem-touching behaviour
  (download cleanup, dest path resolution) is tested with `t.TempDir()`.

#### Testability rules for new code

- Any new dependency on the network, filesystem, clock, or keychain must be
  reachable behind an interface or an injectable field so it can be faked.
- Never call `os.Exit` outside `cmd.Execute`; return errors so they're testable.
- Prefer returning values over printing; a function that builds a string is
  testable, one that writes to `os.Stdout` is not (without capture).

### Branching model

All work happens on feature branches cut from `dev`. One branch per logical
group of commands. Branch naming: `claude/<slug>`. Open a PR, get it merged,
delete the branch, pull main, repeat.

### Definition of done (per command)

A command is considered done when:
1. **A test was written first and failed before the implementation existed**
2. It compiles cleanly (`go build`)
3. It passes `go test ./...`, and the new logic is covered by tests
4. It produces correct output against the live OSF API (verified manually or
   with a recorded HTTP fixture)
5. `--output=json` emits valid, parseable JSON
6. Non-zero exit code on all error paths
7. Help text (`--help`) is complete

Stub commands (`return fmt.Errorf("not yet implemented")`) do **not** land in
`main`. Each PR must take stubs to done.

### Implementation order and rationale

Commands are ordered by dependency and complexity. Each tier must be stable
before the next.

#### Tier 1 — metadata-only (no Waterbutler)

These commands only talk to `api.osf.io`. They can be implemented and tested
without touching the file-transfer layer.

| Command | Key work |
|---------|----------|
| `info` | `GET /nodes/{id}/`, format metadata display |
| `projects` | `GET /users/me/nodes/` with pagination, tabular output |
| `open` | Construct OSF web URL, open in OS browser |

#### Tier 2 — file transfer (Waterbutler)

Requires a working `internal/client/waterbutler.go`. Implement Waterbutler
client first, then commands in dependency order.

| Command | Key work |
|---------|----------|
| `pull` | Waterbutler download, streaming to disk, progress bar, recursive folder walk |
| `push` | Waterbutler upload (new + overwrite paths), `--conflict` logic, directory walk |
| `rm` | Waterbutler DELETE, confirmation prompt, `--dry-run` |

### Waterbutler client design

`internal/client/waterbutler.go` exposes three operations:

```
Download(ctx, downloadURL, destPath string, size int64, quiet bool) error
Upload(ctx, srcPath, uploadURL string, quiet bool) error
Delete(ctx, deleteURL string) error
```

**Upload URL construction** (used by `push` to upload new files):

`osfstorage` addresses folders by **opaque object ID, not by name**, so upload
URLs must never be built from a folder-name path (doing so 404s on real OSF for
any non-root subfolder — the bug the live tier caught). Instead:

- **Root:** `client.RootUploadURL(nodeID)` → `…/providers/osfstorage/`, then
  `client.AppendUploadName(base, filename)`.
- **Subfolder:** resolve the parent folder via the metadata API and use its
  `FileLinks.Upload` (already an ID-based Waterbutler URL), then
  `AppendUploadName`. `cmd.folderUploadBase` encapsulates root-vs-subfolder.

For *overwriting* an existing file, PUT directly to the file's `FileLinks.Upload`
(the correct versioned, ID-based URL from the metadata API).

`mkdir` uses the same ID-based approach: `folderUploadBase` for the parent, then
`client.AppendFolderName(base, name)` (kind=folder), PUT via `CreateFolder`.

**Redirect handling**: Waterbutler redirects downloads to S3 (or other backend).
Strip the `Authorization` header when following redirects to a different host.

### Edge cases to handle per command

**`info`**
- Invalid GUID → APIError 404, friendly message
- Private project without auth → APIError 403

**`projects`**
- No token → 401, tell user to run `datapin auth login`
- Paginate all pages before printing

**`open`**
- Path `/` → `https://osf.io/{nodeID}/`
- File or folder → `https://osf.io/{nodeID}/files/osfstorage{path}`
- Print URL with `--quiet` on headless systems (fallback if browser fails)

**`pull`**
- File target → download single file
- Folder target (or `/`) → walk full tree, preserve relative structure under `dest`
- `dest` defaults to `.` (current dir); `dest` for a single file defaults to `./filename`
- Skip existing files by default (no overwrite flag needed for pull)
- `--dry-run` lists what would be downloaded
- `--version=<n>` downloads a specific historical version; validates version exists before transferring
- `--version` is invalid for directory/tree targets (errors early)
- `--track-only` records manifest entries for everything the target matches and
  transfers nothing (version + MD5 come from the version listing, the only source
  available with no local copy — `pullSession.pinFor`). Conflicts with
  `--no-track` and requires a path argument. Entries land as `MISSING`, so a
  plain `datapin sync` fetches them; this is how remote files that nothing tracks
  become visible to `sync`, which only ever iterates manifest entries.

**`push`**
- Split dest path into `parentDir` + `filename`; fail if parent doesn't exist in OSF
- Check for existing file in parent dir before uploading
- `--conflict=skip` (default): print notice and skip if exists
- `--conflict=overwrite`: PUT to existing file's `links.upload`
- `--conflict=rename`: append `_1`, `_2`, … until name is free
- If `src` is a directory, walk it and upload all files (maintaining relative paths)
- `--dry-run` shows what would be uploaded without uploading

**`rm`**
- Resolve path → `FileLinks.Delete`
- Print what will be deleted; require `--yes` or interactive confirmation unless `--dry-run`
- `--dry-run` prints path without deleting

**`versions`**
- Requires a specific file path (not a folder or project root)
- Folder target → error: "versions only applies to files"
- Missing path → error: "versions requires a specific file path"
- Resolves via `resolver.Resolve` to get the file's OSF ID, then calls `GetFileVersions`
- Contributor resolution (best-effort): `email_primary` > `full_name` > user GUID
- `--output=json` emits `{"versions": [{"version","date_created","size","contributor"}]}`

### OSF file versioning

Every PUT to an existing file's `links.upload` URL creates a new numbered version.
Versions are immutable once created.

`GetFileVersions(fileID)` calls `GET /v2/files/{id}/versions/` and returns
`[]FileVersion` sorted newest-first. The OSF versions endpoint does not expose a
user relationship (requesting `embed=user` returns a 400), so contributor info is
generally unavailable from this endpoint; `FileVersion.Contributor()` resolves to
`email_primary` → `full_name` → GUID when embedded user data happens to be present,
and returns an empty string otherwise.

`RevisionURL(downloadURL, n)` appends `?revision=n` to a Waterbutler download URL,
fetching a specific historical version. Used by `pull --version=<n>`.

Two distinct upload paths in `push`:
- **New file**: PUT to the parent folder's ID-based upload URL
  (`folderUploadBase` + `AppendUploadName`) → 201 Created
- **Update (overwrite)**: PUT to `existing.Links.Upload` → 200 OK, creates new version

### Adding a new OSF API endpoint to `osf.go`

1. Add response struct(s) near the top of the file alongside related types
2. Add the method to `*OSFClient`
3. Use `c.getJSON(ctx, url, &result)` for single-item GETs
4. Use `c.listXFromURL(ctx, url)` pattern (with a typed page struct) for paginated lists

### Adding new Waterbutler operations

Add methods to `*WaterbutlerClient` in `internal/client/waterbutler.go`.
Keep auth header stripping on cross-host redirects in place for all GET requests.
