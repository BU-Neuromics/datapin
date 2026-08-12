---
title: Your first DOI
---

# Your first DOI

**You are here because:** you have a folder of results and a paper deadline,
and a reviewer or a journal wants the data deposited somewhere citable.

**You will end with:** a published, immutable archive record, a DOI that
resolves forever, a paste-ready citation, and a manifest committed to git that
pins every byte you published.

**Time:** about twenty minutes, most of it upload.

The whole flow is rehearsed on a sandbox first. That is not optional caution —
publishing to production Zenodo is permanent and public, and there is no undo.

---

## 0. Before you start

You need:

- **datapin installed.** `datapin --version` should print a version.
- **A sandbox account** at [sandbox.zenodo.org](https://sandbox.zenodo.org),
  with a personal access token from *Applications → Personal access tokens*
  (grant `deposit:write` and `deposit:actions`). The sandbox is a **separate
  service** from zenodo.org with its own account and its own token.
- **A git repository** containing (or beside) your data. datapin writes its
  manifest into `.datapin/datapin.toml` in the repository root and expects that
  file to be committed.
- **The files you actually mean to publish**, in a directory you can point at.
  Archives are not scratch space: publish the finished artifacts, not the whole
  working tree.

A note on file sizes: Zenodo accepts 50 GB per record by default, and datapin
uploads anything over 100 MiB in parts. If your dataset is much larger than
that, ask the repository for a quota increase before you start rather than
halfway through.

## 1. Register the sandbox as a remote

```console
$ datapin remote add https://sandbox.zenodo.org --name sandbox \
    --token-value "$SANDBOX_TOKEN"
```

datapin stores the token in your OS keychain — or, with `--no-keychain`, in
`~/.config/datapin/tokens/sandbox` at mode `0600` for headless and HPC nodes —
and probes the instance once to learn what it supports. Tokens never go into
`config.toml`, which stays safe to commit.

`remote add` does not prompt for a token: pass `--token-value`, or supply it
later through the environment as `DATAPIN_TOKEN_SANDBOX` (the remote name,
uppercased). Confirm what landed:

```console
$ datapin remote ls
NAME     KIND     URL                         AUTH
sandbox  invenio  https://sandbox.zenodo.org  token set
```

If that says `no token`, publishing will fail on authentication and nothing
else. Fix it before you upload.

## 2. Describe the dataset

Run the wizard:

```console
$ datapin onboard
```

It walks four steps — pick the remote (your `sandbox` is offered for reuse),
pick the files from a collapsible tree of everything git does not track, then
the metadata: title, creators with ORCIDs, and a license. It writes
`.datapin/datapin.toml` and stops.

`onboard` needs a real terminal. If you are on a machine without one, write the
manifest by hand instead — it is short:

```toml
[project]
default_archive = "sandbox"

[[datasets]]
slug = "counts"                       # your local handle for this dataset

  [datasets.metadata]
  title       = "Aligned RNA-seq count matrices for 48 cortical samples"
  description = "Gene-level counts produced by the pipeline in this repository."
  license     = "CC0-1.0"
  keywords    = ["RNA-seq", "cortex", "human"]

  [[datasets.metadata.creators]]
  name  = "Labadorf, Adam"
  orcid = "0000-0002-1825-0097"

  [[datasets.files]]
  local = "results/counts.h5"

  [[datasets.files]]
  local = "results/samples.tsv"
```

Three fields deserve a moment of thought, because they are the ones you cannot
fix after publishing:

- **`title`** is what appears in a citation. Write the title of the *dataset*,
  not of the paper.
- **`license`** is required to publish, and datapin will never pick one for
  you. `CC0-1.0` (public domain dedication) is the conventional choice for
  research data and the one that scores best on reuse; `CC-BY-4.0` requires
  attribution. Whatever you choose, choose it deliberately — see
  [the license gate](../troubleshooting/#publish-refuses-a-license-is-required)
  for why datapin refuses to guess.
- **`creators`** are the people credited by the citation. `name` is
  `Family, Given`. Add ORCIDs: they are the difference between a citation that
  aggregates to your record and one that does not.

## 3. Lint before you publish

```console
$ datapin check counts
```

`check` errors on exactly what `publish` will refuse, and warns about
everything else. Exit code is 0 when there are no errors, so it drops straight
into CI. Fix the errors now — an error here is an error you would otherwise hit
after uploading gigabytes.

The warnings are worth reading too. "No keywords" and "no ORCID for a creator"
are the two that most affect whether anyone ever finds the dataset.

## 4. Rehearse: publish to the sandbox

Look at the plan first. Nothing uploads:

```console
$ datapin publish counts --dry-run
```

Then do it for real, on the sandbox:

```console
$ datapin publish counts
```

datapin prints the plan — the remote, the visibility, the license, and every
file that will move — states that this is **PUBLIC AND PERMANENT**, and waits
at a `[y/N]` prompt. Answer `y` and it runs the transaction: open a draft,
upload the changed bytes, refresh the metadata, publish, then atomically re-pin
the manifest. A failure anywhere before the publish step discards the draft, so
an interrupted run leaves no half-record behind.

When it finishes you have a DOI with the sandbox prefix `10.5072`:

```console
$ datapin versions counts
$ datapin open counts          # the landing page, in your browser
```

**Open that landing page and read it as a stranger would.** This is the whole
point of the rehearsal. Is the title comprehensible without the paper? Are the
authors right and in the right order? Is the description enough for someone to
know whether the data answers their question? Are the files the ones you meant,
under names that mean something?

`datapin cite counts` will render a citation locally here — sandbox DOIs
[never resolve](../troubleshooting/#a-sandbox-doi-does-not-resolve), which is
exactly what makes them safe to rehearse with.

## 5. Publish for real

Add production Zenodo as a second remote, with its own token from
[zenodo.org](https://zenodo.org/account/settings/applications/tokens/new/):

```console
$ datapin remote add https://zenodo.org --name zenodo
```

Point the dataset at it. The sandbox record and the real one are different
records, so clear the sandbox pins — this dataset is being published fresh:

```toml
[[datasets]]
slug    = "counts"
archive = "zenodo"        # was: the project default_archive = "sandbox"
record  = ""              # clear the sandbox pins
concept = ""
concept_doi = ""
version = 0
version_doi = ""
```

Then publish:

```console
$ datapin publish counts
```

Read the confirmation plan properly this time. It says PUBLIC and PERMANENT
because it is: you cannot edit or delete a published version, and the DOI
resolves forever.

## 6. Commit the pins, then cite

```console
$ datapin cite counts                  # APA-style, via DOI content negotiation
$ datapin cite counts --bibtex         # for your bibliography
$ git add .datapin/datapin.toml && git commit -m "data: publish counts v1"
```

**Commit the manifest.** It now records the record id, the concept DOI, the
version DOI, and the checksum of every published file. That commit is what
makes the claim "this paper's figures came from these exact bytes" verifiable —
anyone who clones the repository can run `datapin pull counts` and get them.

Two DOIs come back, and the difference matters in a paper:

- The **version DOI** points at exactly what you published today. Use it when
  you mean *these bytes* — a figure, a reproduction, a specific analysis.
- The **concept DOI** points at the dataset across all versions, resolving to
  the newest. Use it in a data-availability statement where you want readers
  to find the current data.

## What to do next

- Data changed? → [Publishing a new version](../new-version/)
- Check how the record scores against FAIR: `datapin check --fair`
  (needs a resolving, non-sandbox DOI and an F-UJI server).
- Give the dataset a human-readable home page:
  `datapin site build && datapin site preview`.
- Publishing from a pipeline instead of by hand? →
  [Scripting and CI](../automation/)
