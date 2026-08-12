---
title: Publishing a new version
---

# Publishing a new version

**You are here because:** you already published a dataset, the data has since
changed — a bug in the pipeline, a sample added, a reviewer's request — and you
need the new data citable without orphaning the old.

**You will end with:** a second published version, a fresh version DOI, the
same concept DOI still pointing at the newest, and the old version still
resolving for anyone who cited it.

**The short version:** edit the files, then run `datapin publish counts` again.
Everything below is what that does and where it can stop you.

---

## What a new version actually is

A published version is immutable. "Editing" a published dataset means adding a
version alongside it. Both remain retrievable forever, and the old DOI keeps
resolving — that is the point of a DOI, and the reason you cannot simply
overwrite.

Three archive backends model versions differently, and it shows:

| Backend | Version DOIs | New version means |
|---|---|---|
| `invenio` (Zenodo, InvenioRDM) | one per version, under a stable **concept DOI** | a new draft; unchanged files are imported server-side, so they do not re-upload |
| `figshare` | a `.vN` DOI per version under a stable base DOI | the mutable account draft; **no** server-side import, so every file uploads again |
| `dataverse` | **one DOI for all versions** | a new version behind the same DOI, with a version picker on the landing page |

On `dataverse`, `datapin versions counts` shows the same DOI on every row. That
is correct, not a bug: there is nothing else to show.

## 1. See what changed

```console
$ datapin status
```

Dataset rows report a state against the published pin:

- `IN_SYNC` — local files match the published version. Nothing to publish.
- `AHEAD` — local files changed since the published version. This is the normal
  state before publishing v2.
- `REMOTE_NEWER` — the archive has a version your manifest has not seen (a
  collaborator published, or you published from another machine).
- `MISSING` — a pinned file is not on disk. `datapin pull counts` restores it.
- `DIVERGED` — both moved independently.

Then read the plan. This is the step worth not skipping:

```console
$ datapin publish counts --dry-run
```

The plan is per file key, and each key gets one of four actions:

- **upload** — a new file that was not in the previous version.
- **replace** — the key exists but the content changed. Only these bytes move.
- **keep** — unchanged; on `invenio` it is imported server-side and costs no
  bandwidth.
- **remove** — the key was in the previous version and is no longer in the
  manifest. **It will not be in the new version.** A removal you did not intend
  is the single most common surprise here: it usually means a file was renamed,
  or a `key` changed, not that you meant to drop data.

## 2. Publish

```console
$ datapin publish counts
```

Same transaction as the first time: open an idempotent new version, import the
previous version's files, move only changed bytes, refresh the metadata,
publish, re-pin the manifest atomically.

Then commit:

```console
$ git add .datapin/datapin.toml && git commit -m "data: publish counts v2"
```

## 3. What re-pinning changed in the manifest

After a successful publish, exactly these fields move:

```toml
[[datasets]]
slug        = "counts"
record      = "585302"                    # ← the NEW version's record id
concept     = "585299"                    # unchanged: the version group
concept_doi = "10.5281/zenodo.585299"     # unchanged: resolves to newest
version     = 2                           # ← incremented
version_doi = "10.5281/zenodo.585302"     # ← the NEW version DOI
  [[datasets.files]]
  local = "results/counts.h5"
  md5   = "9f2c…"                         # ← re-pinned to what was published
```

`record` and `version_doi` now name v2. `concept`/`concept_doi` never change —
that is what makes them safe to cite in a data-availability statement.

**Which DOI goes in the paper?** The version DOI when you mean *these exact
bytes* (a figure, a reproduction). The concept DOI when you mean *this dataset*
and want readers to land on the current version. If you have already published
a paper citing v1's version DOI, that citation stays valid and keeps resolving
to v1 — which is the whole reason not to overwrite.

## 4. Metadata changes

Editing `[datasets.metadata]` and publishing again carries the new metadata onto
the new version. The published v1 record keeps v1's metadata: archives do not
retroactively rewrite published records, and neither does datapin.

A typo in a published title is therefore not fixable by datapin. Correct it in
the next version, or ask the repository's curators — on Zenodo, metadata edits
to a published record are a support request, not an API call.

## When it stops you

### `REMOTE_NEWER` — the archive moved without you

Someone published a version your manifest does not know about, or you published
from another checkout. Publishing on top of that blindly would branch the
history, so datapin refuses.

Reconcile first:

```console
$ datapin versions counts        # see the real chain and where your pin sits
$ datapin pull counts --latest   # adopt the archive's latest and re-pin to it
```

`--latest` is the deliberate way to move the pin. Then re-apply your changes and
publish.

If you are certain your local files are what the next version should be and the
remote version is one you want to supersede:

```console
$ datapin publish counts --force
```

`--force` means "publish over a remote version my pin has not seen". It does
not delete anything — the version you skipped past stays published and citable.

### `DIVERGED`

Both sides moved and your local content matches no published version. datapin
fails before any bytes move. Decide which side is right:
`datapin pull counts --latest` to take the archive's, or edit the local files
until they are what you mean and publish with `--force`.

### The publish 504s but the version appears anyway

Zenodo can time out on publish while having succeeded. datapin never blind-
retries a publish; it reconciles by re-reading the record. If a run dies here,
run `datapin versions counts` before doing anything else — the version may
already exist, in which case `datapin pull counts --latest` re-pins you to it
and there is nothing more to publish.

## The manuscript-first workflow

If you need to cite the DOI *in the paper you are still writing*:

```console
$ datapin publish counts --reserve
```

Everything uploads and the DOI is reserved, but the record stays a draft — and
unlike a failed run, a `--reserve` draft is not discarded. Put the reserved DOI
in the manuscript, and publish for real when the paper is accepted.

## What to do next

- Give each version a readable landing page: `datapin site build`.
- Automate it from a pipeline → [Scripting and CI](../automation/)
- Something else went wrong → [Troubleshooting](../troubleshooting/)
