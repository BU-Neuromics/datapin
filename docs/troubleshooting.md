---
title: Troubleshooting
---

# Troubleshooting

datapin's errors are deliberately loud: when a layer can contradict the pin, it
fails rather than guessing. So most of what looks like a failure here is datapin
refusing to do something irreversible without your say-so.

Find your message below. Each entry says what it means, why datapin is strict
about it, and the one command that resolves it.

- [Publish refuses: a license is required](#publish-refuses-a-license-is-required)
- [Dataverse requires contact_email](#dataverse-requires-contact-email)
- [A sandbox DOI does not resolve](#a-sandbox-doi-does-not-resolve)
- [resource_type is not in the instance's vocabulary](#resource-type-is-not-in-the-instances-vocabulary)
- [The license id is not offered by the target](#the-license-id-is-not-offered-by-the-target)
- [`DIVERGED` — both sides moved](#diverged--both-sides-moved)
- [`AHEAD_OF_MANIFEST` and a non-zero exit](#ahead-of-manifest-and-a-non-zero-exit)
- [A pinned pull fails on a checksum](#a-pinned-pull-fails-on-a-checksum)
- [The remote has a newer version than the pin](#the-remote-has-a-newer-version-than-the-pin)
- [Publish timed out but may have succeeded](#publish-timed-out-but-may-have-succeeded)
- [A large upload fails partway (multipart)](#a-large-upload-fails-partway-multipart)
- [Dataverse: dataset is locked](#dataverse-dataset-is-locked)
- [Authentication failures](#authentication-failures)
- [OSF: 429 Too Many Requests](#osf-429-too-many-requests)
- [The published site does not appear](#the-published-site-does-not-appear)
- [Workspace remote problems](#workspace-remote-problems)
- [Manifest not found](#manifest-not-found)
- [Still stuck](#still-stuck)

---

## Publish refuses: a license is required

```
dataset "counts" has no license — publishing requires an explicit license choice.
Set license in [datasets.metadata] (an SPDX id): CC0-1.0 dedicates the data to
the public domain (the common choice for research data), CC-BY-4.0 requires
attribution.
```

**This is not a bug, and `check` passing is not a contradiction.** `check` only
*warns* about a missing license, because drafting without one is reasonable.
`publish` refuses, because publishing grants rights permanently and there is no
neutral default: with no license, the backend applies **its own**, granting
rights you never chose, on data you cannot unpublish.

```toml
  [datasets.metadata]
  license = "CC0-1.0"      # or CC-BY-4.0, or any SPDX id the target offers
```

datapin never fills a license in and no driver ever substitutes one. If you are
genuinely undecided, that is a reason not to publish yet — `datapin publish
--reserve` uploads and reserves the DOI while leaving the record a draft.

## Dataverse requires contact_email

Dataverse's citation block mandates a Point of Contact, so a Dataverse publish
without one is refused.

```toml
  [datasets.metadata]
  contact_email = "data@university.edu"
```

Other backends ignore the field, so setting it is never wrong.

## A sandbox DOI does not resolve

You published to `sandbox.zenodo.org` or `demo.dataverse.org`, and the DOI
(prefix `10.5072`, or a demo prefix) 404s at doi.org.

**Expected. This is what makes a rehearsal safe.** Sandbox and demo instances
mint DOIs that are never registered with the DOI system. Consequences:

- `datapin cite` cannot use DOI content negotiation, so it renders the citation
  **locally** from your manifest metadata instead. The output is still useful for
  checking the shape of the citation.
- `datapin check --fair` cannot run: F-UJI probes the *public record* over the
  network and needs a resolving, non-sandbox DOI. It is skipped with a warning.

When you publish the same dataset for real, remember the sandbox record and the
production record are **different records**. Clear the rehearsal's pins first —
`record`, `concept`, `concept_doi`, `version_doi` to `""` and `version` to `0` —
or datapin will try to open a new version of a record that does not exist on the
production instance.

## resource_type is not in the instance's vocabulary

`datapin check` errors because the instance's probed resource-type vocabulary
does not offer the value you set. Publishing would be rejected, so this is
caught before the upload.

Take a value from the list in the error message. To see what an instance offers,
or to refresh a vocabulary that has grown since you added the remote:

```console
$ datapin remote probe inst
```

If the remote was added with `--no-verify`, no vocabulary was ever stored and
`check` cannot validate the field at all. Probe it, or accept that this
particular check is not running.

## The license id is not offered by the target

A loud, typed error listing what the target *does* offer. datapin resolves your
SPDX id against the target's own registry — Dataverse's `/api/licenses`,
Figshare's license vocabulary, InvenioRDM's lowercased-SPDX ids — and never
substitutes a different license or lets the backend's default apply.

Institutional Dataverse installations often offer a short list. Pick from the
error's list, or ask the repository administrators to add the license you want.

## `DIVERGED` — both sides moved

Your local file changed **and** the remote changed, and local content matches no
remote version. There is no safe automatic answer: one side's work would be lost
either way.

Divergence is detected in a **pre-flight pass before any bytes move**, so a bulk
run never leaves you half-resolved. Plain `--force` will not get through it —
that is deliberate, because `--force` means "I accept losing local changes",
which is not the same decision.

```console
$ datapin sync --resolve=ours      # take local: push it as a new version
$ datapin sync --resolve=theirs    # take remote: download and re-pin
```

Before choosing, look at what you would discard. For an archive dataset:
`datapin versions <slug>` and `datapin pull <slug> --latest` into a scratch
directory. For OSF: `datapin pull <project>:<path> /tmp/theirs`.

## `AHEAD_OF_MANIFEST` and a non-zero exit

`datapin sync` reports the entry, transfers nothing, and exits 1.

**This is the one state `sync` will not guess at**, and the exit code is a
prompt, not a failure. Only your local copy moved since the pin — and the same
difference means "publish this" for a generated result and "throw this away" for
an accidentally edited input. No hash comparison can tell those apart.

Say which you meant with the verb:

```console
$ datapin push                 # this local change is the new truth — publish it
$ datapin pull --force         # this local change was a mistake — discard it
$ datapin sync --force         # same, across the whole manifest
```

It is a reporting no-op rather than a hard error because both outcomes are
recoverable (local work, or an unwanted remote version), so it must not abort a
bulk run.

## A pinned pull fails on a checksum

`datapin pull <slug>` verifies every downloaded file against the manifest pin
and **fails hard when the archive's checksum contradicts it**, rather than
quietly writing different bytes.

If that happens, the pin and the archive genuinely disagree. Usually the pin
refers to a different version than you think — someone published a new version
and committed the manifest, or you are on a branch with an older pin.

```console
$ datapin versions counts          # what exists, and where your pin sits
$ datapin pull counts --latest     # deliberately move the pin to the latest
```

`--latest` is the only way to move a pin on pull, and that is the point: the pin
is a contract, so overriding it is an explicit act, never a fallback.

## The remote has a newer version than the pin

```
the remote has version 3 but the manifest pins version 2 — pull the newer
version first (datapin pull counts --latest) or --force to publish on top of it
```

A collaborator published, or you published from another checkout. Two answers:

```console
$ datapin pull counts --latest     # adopt the archive's latest, then redo your work
$ datapin publish counts --force   # publish yours on top; the skipped version stays published
```

`--force` does not delete anything — the version you published past remains
citable and resolvable. It just means "I know my pin is behind and I mean this".

## Publish timed out but may have succeeded

Zenodo can return a 504 on publish **while having published successfully**
(zenodo#2131). datapin therefore never blind-retries a publish; it reconciles by
re-reading the record.

If a run dies at this step, check reality before doing anything else:

```console
$ datapin versions counts
```

If the version is there, the publish worked — `datapin pull counts --latest`
re-pins your manifest to it and there is nothing left to do. Re-running
`publish` would attempt an unnecessary new version.

## A large upload fails partway (multipart)

Files over 100 MiB use InvenioRDM's multipart (`M`) transfer on `invenio`
remotes, uploading parts to pre-authorized URLs.

- **If the instance rejects the multipart transfer at registration**, datapin
  falls back to a single PUT automatically. No bytes are read before that point,
  so the fallback costs nothing.
- **If a part upload fails**, datapin deletes the pending file entry so the draft
  stays publishable, and the error surfaces. Re-run `datapin publish` — the
  transaction re-plans from scratch, and files already uploaded intact are kept
  rather than re-sent.

If part uploads are denied by a proxy or storage layer in your environment,
force the single-PUT path by turning the capability off for that remote:

```toml
# ~/.config/datapin/config.toml
[remotes.inst.caps]
multipart_upload = false
```

Hand-edited caps always win over probed values. A single PUT of a very large
file is more fragile on a flaky link, so prefer fixing the proxy if you can.

## Dataverse: dataset is locked

Dataverse locks a dataset during asynchronous work — tabular file ingest,
finalizing a publish — and refuses API calls with a 403 while locked.

datapin already polls until the lock clears. You only see an error when it does
not clear within the bound:

```
dataset doi:10.5072/FK2/ABCDEF is still locked (Ingest) after waiting — try again later
```

That is genuinely "try again later": the lock is the server working. Tabular
ingest of a large CSV can take a long time. Re-run the same command — the
publish transaction is designed to be re-runnable — and consider uploading large
tabular data in a format Dataverse does not ingest (e.g. `.tsv.gz`) if ingest is
not something you want.

## Authentication failures

**First, work out which of the two independent ladders you are in.** Conflating
them is the most common cause of "but I set the token".

Archive and workspace remotes use a **per-remote** token:

1. `DATAPIN_TOKEN_<NAME>` (the remote name, uppercased)
2. the OS keychain
3. `~/.config/datapin/tokens/<name>`

```console
$ datapin remote ls        # the AUTH column says "token set" or "no token"
```

If it says `no token`, nothing else is the problem. Re-add with
`--token-value`, or export `DATAPIN_TOKEN_<NAME>`.

Legacy **OSF** uses a separate ladder: `--token` → `OSF_TOKEN` →
`~/.config/datapin/token` → keychain. A raw 401/403 on an OSF read is wrapped
into an actionable message; `datapin auth status` reports who you are and where
the token came from.

Other things worth knowing:

- **A locked keychain looks like no token.** `config.LoadToken` returns empty
  for a locked keychain exactly as for a deliberate anonymous run. On headless
  and HPC machines, prefer the environment variable or `--no-keychain`.
- **Tokens are never printed** in logs or error output, so `-vv` output and
  pasted errors are safe to share.
- **Scopes matter.** A Zenodo token needs `deposit:write` and
  `deposit:actions`; an OSF token needs e.g. `osf.full_write` for any write.
- **Reads work unauthenticated** on public data (`pull`/`ls`/`info`/`status`/
  `versions`), so a read that works while a write fails is a scope or token
  problem, not connectivity.

## OSF: 429 Too Many Requests

OSF throttles at roughly **100 requests/hour unauthenticated** and 10,000/day
authenticated.

Authenticating is the fix, and usually the whole fix — it raises the ceiling by
two orders of magnitude:

```console
$ datapin auth login
```

datapin already minimizes requests (100 items per page, memoized directory
listings, skipping version history when a listing settles the question) and
retries `429`/`502`/`503`/`504` while honouring `Retry-After` exactly. A wait
longer than 30 seconds is **declined rather than truncated** — the quota is
genuinely spent, and retrying early only earns another 429. Wait it out.

Note that throttling is never mistaken for absence: a 429 is an error, not "this
file is not on the remote". That distinction matters, because "absent" would
classify an unpinned entry as `NOT_PUSHED` and make `sync` upload it.

## The published site does not appear

`datapin site publish` force-pushes an orphan commit to the **`gh-pages`**
branch. Two things then have to be true.

**1. Pages must be serving `gh-pages` at the root.** If Pages was already
switched on for another source — say `main` at `/` — the API call to enable it
returns 409 and the site keeps serving the old source. datapin warns:

```
site pushed to gh-pages, but GitHub Pages is serving main instead — the
published site will not appear until you change the source to the gh-pages
branch (root) in Settings → Pages
```

Fix it in **Settings → Pages → Build and deployment → Branch → `gh-pages` / `/
(root)`**, or with the API:

```console
$ gh api -X PUT repos/OWNER/REPO/pages \
    -f 'build_type=legacy' -f 'source[branch]=gh-pages' -f 'source[path]=/'
```

**2. A token must be available** for the Pages toggle, via `--github-token` →
`GITHUB_TOKEN` → `GH_TOKEN` → `gh auth token`. Without one the push still works
through your git credential helper, but the toggle is skipped with a note — so a
first publish leaves Pages off entirely.

Other site issues:

- **`nothing to build — add [[site.pages]] or [[datasets]] to the manifest`** —
  the site is generated *from the manifest*; it does not walk your `docs/`
  directory. List each page under `[[site.pages]]`.
- **Links between pages 404.** Each page is published at `/<slug>/`, so link to
  `../other-slug/`, not to `other.md`. Slugs default to the basename without its
  extension.
- **Raw HTML in a page is escaped.** By design — a generated site should not be
  an XSS vector by default.
- Check the result locally before pushing: `datapin site preview`.

## Workspace remote problems

**SFTP: host key verification failed.** Host keys must verify against
`~/.ssh/known_hosts`, and an unknown host is a hard failure — an unverified host
key on a data transfer is not a warning-level problem. `ssh` to the host once by
hand to record the key, then retry.

**SFTP: authentication failed.** The ladder is `SSH_AUTH_SOCK` agent →
`~/.ssh/id_ed25519`/`id_rsa` → `DATAPIN_SFTP_PASSWORD`. In a batch job there is
usually no agent, so either point at a key or set the password variable.

**S3: connection or region errors.** The endpoint lives in the **URL**
(`s3://endpoint/bucket/prefix`), not in an AWS profile. Add `?insecure=true` for
plain-HTTP MinIO and `?region=…` where the store cares. Credentials are the
per-remote token as `ACCESSKEY:SECRETKEY`, falling back to
`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`.

**`revert --to N` says the version is unrecoverable.** `datapin gc` reclaimed it.
Reclaimed versions stay listed in the journal and report themselves
unrecoverable rather than vanishing — which is why you get a clear message
instead of an obscure failure. There is no recovery; `gc` is the one destructive
command in the workspace workflow. Raise `--keep` (default 3) if you need a
longer tail.

**Someone overwrote a tracked file out of band.** Handled, not fatal: the
foreign bytes are archived under their own content address before the push
proceeds, and the journal records that it happened. `datapin versions
<slug>/<key>` shows the event.

## Manifest not found

datapin walks up from the current directory looking for `.datapin/datapin.toml`.
If you are outside the repository, or it was never created, you get a
not-found error.

```console
$ datapin onboard              # create one interactively
```

A legacy `.gosf/gosf.toml` is found and loaded **read-only** — `Save` refuses it
with a migration hint, so any command that would rewrite the manifest fails
until you convert:

```console
$ mv .gosf .datapin && mv .datapin/gosf.toml .datapin/datapin.toml
```

## Still stuck

Get the detail before you file anything:

```console
$ datapin <command> -vv 2>run.log
```

`-vv` adds HTTP-level traces plus timestamps and source locations on stderr.
Tokens are never included, so the log is safe to attach.

Then open an issue at
<https://github.com/BU-Neuromics/datapin/issues> with the command, the `-vv`
log, and your `datapin --version`. If it involves a specific instance, say which
software and version it runs — institutional InvenioRDM and Dataverse
deployments vary, and that is usually the first question.
