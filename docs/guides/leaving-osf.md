---
title: Leaving OSF
---

# Leaving OSF

**You are here because:** the Open Science Framework is sunsetting its projects
service, and your data is on it.

**You will end with:** every file and wiki page on local disk and MD5-verified,
a datapin manifest with your data grouped into publishable datasets, and a
metadata skeleton that **cannot publish by accident** until you have reviewed
it.

**Time:** minutes plus download. Nothing is uploaded and no DOI is minted —
`migrate` gets you off OSF; publishing stays a separate, deliberate step.

datapin's OSF support is frozen: it works, it is tested, and it will be removed
in the major release after the shutdown. `datapin migrate` is the exit ramp.

---

## Which mode do you need?

**GUID mode** — you have an OSF project and no datapin manifest. This is most
people:

```console
$ datapin migrate abc12 ./cortex-rnaseq
```

**Manifest mode** — you already track OSF files in `.datapin/datapin.toml`
(`[[files]]`/`[[wikis]]` entries) and want that repository converted in place:

```console
$ datapin migrate
```

Either way, look before you leap:

```console
$ datapin migrate abc12 --dry-run     # the full plan, writes nothing
```

## GUID mode: export a project

The GUID is the five characters in the OSF URL — `https://osf.io/abc12/` →
`abc12`. Public projects work without authentication; for private data,
`datapin auth login` first (or `export OSF_TOKEN=…` on a cluster).

```console
$ datapin migrate abc12                   # into the current directory
$ datapin migrate abc12 ./cortex-rnaseq   # into a fresh directory
$ datapin migrate abc12 --components      # each component as its own dataset
```

What you get:

- **Every file** under `osfstorage`, structure preserved, each one MD5-verified
  against what OSF reported. A mismatch is an error, not a warning.
- **Every wiki page** as `docs/<page>.md`, wired into `[[site.pages]]` so
  `datapin site build` renders them.
- **A dataset skeleton** from the node metadata: the title, the description,
  contributors as name-only creators, and an `IsDerivedFrom` related identifier
  pointing back at the osf.io URL — so the record says where it came from.
- **A fresh manifest** and a `MIGRATED.md` provenance breadcrumb.

**Components are skipped with a notice unless you pass `--components`.** If your
project keeps data in sub-projects, that flag is the difference between
exporting your data and exporting an empty shell. Check the notice.

## Manifest mode: convert in place

With no GUID, `migrate` converts the repository you are standing in. It
completes your local copies *first*, through the same safety gates as
`datapin sync`:

- `MISSING`, `BEHIND`, and `REMOTE_NEWER` entries are fetched.
- A `DIVERGED` entry **fails the run before any transfer** — local and remote
  both moved and no version matches, so no automatic answer is safe. Resolve it
  with `datapin sync --resolve=ours|theirs`, then re-run `migrate`.

Then it groups `[[files]]` into `[[datasets]]` — one dataset per top-level
directory by default — converts `[[wikis]]` into site pages, and rewrites the
manifest atomically with the OSF sections removed.

Override the grouping when per-directory is wrong (the flag is repeatable):

```console
$ datapin migrate --dataset raw=data/raw/** --dataset processed=results/**
```

Manifest mode asks for confirmation before the rewrite; `--yes` skips the
prompt. GUID mode never prompts — it only writes into a destination directory.

## The TODO markers are the safety feature

OSF cannot supply a license, ORCIDs, or a contact e-mail, so `migrate` writes a
literal `TODO`:

```toml
  [datasets.metadata]
  title   = "Cortical RNA-seq"
  license = "TODO"          # ← not an SPDX id
  contact_email = "TODO"
```

That is deliberate. `TODO` is not an SPDX identifier, so `datapin check` errors
on it and `datapin publish` refuses. **An unreviewed migration cannot publish by
accident.** Under `--output=json`, exit code 1 signals that TODOs remain — the
export itself still succeeded.

Re-running is idempotent: files whose MD5 already matches are skipped, and
metadata you have edited is preserved per dataset slug. So the intended loop is:
migrate, edit metadata, `datapin check`, migrate again if you left something
behind.

## What to review before publishing

Run `datapin check` and work the list, but these five need a human, not a
linter:

1. **The license.** OSF did not record one in a form datapin can trust. Pick it
   deliberately — see
   [the license gate](../troubleshooting/#publish-refuses-a-license-is-required).
2. **Creator names and order.** OSF contributors become name-only creators.
   Citation order is the authorship question; fix it now, because a published
   version is immutable.
3. **ORCIDs.** Add them. datapin validates the ISO 7064 checksum, so a typo is
   caught rather than published.
4. **What actually belongs in the dataset.** An OSF project is a workspace and
   probably holds scratch files, superseded drafts, and duplicates. The default
   grouping mirrors your directories, not your intent.
5. **`contact_email`** if you are heading for Dataverse, which requires a Point
   of Contact.

Then:

```console
$ datapin check
$ datapin publish <slug>
```

Read [Your first DOI](../first-doi/) for the publish flow itself — and rehearse
on the Zenodo sandbox first. A migrated dataset is exactly the case where a
rehearsal pays: you are publishing metadata you did not write.

## Coming from gosf

datapin began as `gosf`. A legacy `.gosf/gosf.toml` is found and read
automatically, read-only. To convert the repository:

```console
$ mv .gosf .datapin && mv .datapin/gosf.toml .datapin/datapin.toml
$ datapin auth login          # re-store the token under datapin
$ datapin migrate             # then move the data itself off OSF
```

`~/.config/gosf` config and tokens are read when the datapin ones are absent,
and `GOSF_*` environment variables still work with a deprecation warning.

## What to do next

- The data is not ready for a DOI → [Cluster to laptop](../workspace/) puts it
  on a workspace remote instead.
- Publishing to your university's repository →
  [Institutional repositories](../institutional/)
- Something failed → [Troubleshooting](../troubleshooting/)
