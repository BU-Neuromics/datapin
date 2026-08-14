---
title: Institutional repositories
---

# Publishing to an institutional InvenioRDM or Dataverse

**You are here because:** your funder, university, or data-management plan says
the data goes in *your institution's* repository, not Zenodo.

**You will end with:** that instance registered as a remote, its actual
capabilities recorded, and a dataset published to it.

**The short version:** it is the same `remote add` → `check` → `publish` flow.
What differs is that an institutional instance is not zenodo.org, and the places
it differs are the places a publish fails.

---

## 1. Identify what you are talking to

Ask your data librarian for the base URL, or read it off the repository's own
landing page:

| Software | URL to register | Kind |
|---|---|---|
| InvenioRDM | the site root — `https://data.university.edu` | `invenio` (the default) |
| Dataverse | the **collection** URL — `https://dataverse.university.edu/dataverse/mylab` | `dataverse` |
| Figshare (institutional) | `https://api.figshare.com` | `figshare` |

You need a personal access token from your account on that instance. On
InvenioRDM it is under *Applications → Personal access tokens*; on Dataverse it
is *API Token* in your account page.

**Dataverse: the collection alias rides on the URL.** `/dataverse/mylab` names
the collection your datasets are created in, and it defaults to `root` if you
omit it — which is usually not where you have deposit rights. Get this right
before you wonder why creation is refused.

## 2. Register it, and let it be probed

```console
$ datapin remote add https://data.university.edu --name inst \
    --token-value "$INST_TOKEN"

$ datapin remote add https://dataverse.university.edu/dataverse/mylab \
    --name dv --kind dataverse --token-value "$DV_TOKEN"
```

`remote add` **probes the instance once** and records what it declared under
`[remotes.<name>.caps]` in `~/.config/datapin/config.toml`. No later command
re-probes, so this is a one-time cost and an offline-friendly design.

What is genuinely probed:

- **The resource-type vocabulary** — the set of `resource_type` values this
  instance offers. `datapin check` errors on one it does not, because publishing
  would be rejected anyway.
- **The transfer model** — whether multipart upload is supported, inferred from
  the file schema the instance serves. A large upload falls back to a single PUT
  if the instance turns out to reject the multipart transfer.

What is **not** probed, because no InvenioRDM endpoint exposes it: the
**per-record file count and size limits**. datapin uses the documented Zenodo
profile (100 files, 50 GB) as a default and says so. If your instance differs,
edit that same caps table by hand — hand-edited values always win and are never
overwritten by a probe.

```toml
# ~/.config/datapin/config.toml
[remotes.inst.caps]
max_files_per_record = 500            # your instance's real limit, from your librarian
max_file_size        = 214748364800   # 200 GB, in bytes
```

Every field in that table means "not probed, not overridden" when absent — so
absent is not the same as zero or false, and deleting a line restores the
driver's own profile rather than setting a limit of nothing. The `probed_at`
timestamp is informational: it tells you how stale the values are.

Re-probe later — a vocabulary that grew, or a remote added with `--no-verify`:

```console
$ datapin remote probe inst
```

A failed probe leaves the stored values untouched, so a re-probe cannot make
things worse.

### When the probe fails

An instance behind SSO, a staging server with a self-signed certificate, or an
unusual API gateway may not answer the probe. Add it anyway and keep every
default:

```console
$ datapin remote add https://data.university.edu --name inst --no-verify \
    --token-value "$INST_TOKEN"
```

Then expect the two probed things to be unknown: `check` cannot validate
`resource_type` against a vocabulary it never read, and multipart support falls
back conservatively. Both are safe defaults; neither is as good as a probe.

## 3. Metadata the instance requires

The DataCite floor — title, creators, license — is the same everywhere. Beyond
it:

### `resource_type` must be in the instance's vocabulary

```toml
  [datasets.metadata]
  resource_type = "dataset"
```

Institutional instances curate this list, and a value Zenodo accepts may be
absent from yours. `datapin check` catches it against the probed vocabulary and
lists what the instance does offer — take one from that list.

### Dataverse requires `contact_email`

```toml
  [datasets.metadata]
  contact_email = "data@university.edu"
```

Dataverse's citation block mandates a Point of Contact. Other backends ignore
the field, so setting it is never wrong.

### The license must exist in the target's registry

This is the difference that surprises people. datapin resolves your SPDX id
against the target's **own** license registry — Dataverse's `/api/licenses`,
Figshare's license vocabulary, InvenioRDM's lowercased-SPDX vocabulary ids — and
an id the instance does not offer is a **loud, typed error listing what it does
offer**. It is never silently substituted, and the backend's default is never
allowed to apply.

Institutional Dataverse installations frequently offer a short list — often just
`CC0-1.0` and a local custom license. If your `CC-BY-4.0` is refused, the
instance genuinely does not have it; pick from the list in the error or ask for
it to be added.

## 4. Rehearse, then publish

Both ecosystems run public demo instances. Use them exactly as you would use the
Zenodo sandbox:

- Dataverse: <https://demo.dataverse.org> — DOIs use a non-resolving demo prefix.
- InvenioRDM: your instance may have a staging deployment; ask.

```console
$ datapin remote add https://demo.dataverse.org/dataverse/mylab \
    --name dvdemo --kind dataverse --token-value "$DEMO_TOKEN"
$ datapin check counts
$ datapin publish counts --dry-run
$ datapin publish counts
```

Then repoint the dataset at the real remote, clear the pins from the rehearsal
(`record`, `concept`, `concept_doi`, `version_doi` to `""` and `version` to `0`),
and publish for real.

## 5. What looks different afterwards

### Dataverse: one DOI for every version

`Caps.PerVersionDOI` is false for Dataverse — the dataset has **one DOI across
all versions**, with a version picker on the landing page. So:

- `datapin versions counts` shows the same DOI on every row. Correct, not a bug.
- There is no separate concept DOI to cite, because the one DOI already plays
  that role.
- To cite a specific version you cite the DOI plus the version number, per the
  repository's guidance.

### Figshare: `.vN` DOIs and no server-side import

Figshare mints `article.vN`-style version DOIs under a stable base DOI, and a
new version is the mutable account draft — there is **no server-side files
import**, so every file uploads again on every version. Budget bandwidth
accordingly for large datasets.

### Dataverse locks

Dataverse locks a dataset during ingest and finalization. A publish arriving
mid-lock is refused; the lock clears on its own. See
[dataset is locked](../troubleshooting.md#dataverse-dataset-is-locked).

## A caveat worth stating plainly

Zenodo/InvenioRDM is the most heavily exercised backend: datapin's fake for it
is verified against fixtures captured from the real service, and a live test
tier runs the whole publish lifecycle against sandbox.zenodo.org on every push
to `dev`. The Dataverse and Figshare fakes encode **documented** behavior. Treat
your first real run against either as a verification spike: use a demo instance,
read what comes back, and check the landing page against what you expected.

## What to do next

- The publish flow itself → [Your first DOI](./first-doi.md)
- Publishing from a pipeline → [Scripting and CI](./automation.md)
- An error you do not recognize → [Troubleshooting](../troubleshooting.md)
