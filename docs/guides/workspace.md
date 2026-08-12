---
title: Cluster to laptop
---

# Cluster to laptop, with history

**You are here because:** the analysis runs on the cluster, you look at results
on your laptop, and the files are too big for git. They are not finished data —
they change daily, they do not deserve a DOI, and you still need to know which
version produced which figure.

**You will end with:** a workspace remote holding your results, versioned by an
append-only journal, reachable from any machine that can clone the repository —
and the ability to `revert` to any version you ever pushed.

**Time:** ten minutes to set up, then two commands per hop.

---

## Workspace or archive?

Use a **workspace** remote when the data is in flight:

| | Workspace | Archive |
|---|---|---|
| Kinds | `dir`, `s3`, `sftp` | `invenio`, `dataverse`, `figshare` |
| Verb | `datapin push <slug>` | `datapin publish <slug>` |
| Identity | a name | a DOI, resolving forever |
| Metadata | none required | title, creators, license required |
| Mutability | overwrite freely | published versions immutable |
| History | append-only journal, `revert` to any version | version chain + concept DOI |
| Cost of a mistake | push again | permanent, public |

The same dataset can have both — `archive = "zenodo"` and `workspace = "hpc"`.
Push all week, publish once.

## 1. Choose a kind

Three drivers, one journal scheme. Pick by what your institution gives you:

```console
# A mounted filesystem: lab NAS, a shared /projectnb, anything with a path.
$ datapin remote add /mnt/lab-share/rnaseq --name nas --kind dir

# Object storage: institutional MinIO, Cloudflare R2, any S3 API.
$ datapin remote add s3://minio.lab.edu:9000/datasets/rnaseq --name obj --kind s3 \
    --token-value "$ACCESS_KEY:$SECRET_KEY"

# Any machine you can ssh to.
$ datapin remote add sftp://you@cluster.example.edu/scratch/you/rnaseq \
    --name hpc --kind sftp
```

Credentials differ per kind, and this is where setup usually stalls:

| Kind | URL form | Credentials |
|---|---|---|
| `dir` | a plain path — `/mnt/lab-share/rnaseq` | none; filesystem permissions are the whole story |
| `s3` | `s3://<endpoint>/<bucket>[/<prefix>]` — add `?insecure=true` for plain-HTTP MinIO, `?region=…` where it matters | the per-remote token as `ACCESSKEY:SECRETKEY`, else `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` |
| `sftp` | `sftp://user@host[:port]/base/path` | `SSH_AUTH_SOCK` agent → `~/.ssh/id_ed25519`/`id_rsa` → `DATAPIN_SFTP_PASSWORD` |

Two things to know before you debug the wrong layer:

- **The S3 endpoint lives in the URL**, not in a config file or an AWS profile.
  The audience here is institutional MinIO and R2, not AWS defaults, so there is
  no region-guessing: say where the bucket is.
- **SFTP verifies host keys against `~/.ssh/known_hosts`** and fails if the host
  is unknown. That is deliberate — an unverified host key on a data transfer is
  not a warning-level problem. `ssh` to the host once by hand to record the key,
  then retry.

## 2. Point the dataset at it

```toml
[project]
default_workspace = "hpc"      # every dataset that names no workspace of its own

[[datasets]]
slug      = "counts"
workspace = "hpc"              # optional per-dataset override
archive   = "zenodo"           # a dataset can have both tracks
  [[datasets.files]]
  local = "results/counts.h5"
  [[datasets.files]]
  local = "results/qc/summary.tsv"
```

A workspace push needs no metadata at all — no title, no license, no ORCIDs.
That is the point: the ceremony belongs at publication, not at every hop.

## 3. The loop

On the cluster, after a run:

```console
$ datapin push counts
$ git add .datapin/datapin.toml && git commit -m "results: batch 4" && git push
```

On the laptop:

```console
$ git pull
$ datapin pull counts --workspace
```

`--workspace` is required on `pull` and is the whole difference between the two
tracks: without it, `datapin pull counts` fetches the *published archive*
version. With it, you get the current workspace bytes.

That is the loop. Everything below is what the journal buys you.

## 4. What the journal gives you

Before any overwrite, the superseded bytes are archived on the remote under
`.datapin/versions/<key>/<md5>`, and an append-only journal under
`.datapin/journal/` records every push and revert. The invariant, enforced by a
test in datapin's own suite: **every version datapin wrote is revertible** —
until you `gc` it away.

Look at one file's history:

```console
$ datapin versions counts/counts.h5
```

The argument is `<slug>/<key>` — the **key**, not the local path. A file with no
explicit `key` gets its local basename, so `results/counts.h5` is addressed as
`counts/counts.h5`. Keys written by `datapin onboard` are full local paths, and
those may contain slashes: `counts/results/qc/summary.tsv` splits at the *first*
slash, so the slug is `counts` and the key is `results/qc/summary.tsv`. When in
doubt, `datapin status` and the manifest both show the keys in play.

Each row is a journal event: what happened, when, the content hash, and whether
that version is still recoverable.

Go back to version 2:

```console
$ datapin revert counts/counts.h5 --to 2 --reason "batch 3 was mislabeled"
$ datapin pull counts --workspace        # update the local copy
```

A revert is a **new journal event, never a rewrite**. The version you regretted
stays retrievable — you can revert back — and `--reason` records why, which is
the difference between a data directory and a data record six months later.
`revert` changes the remote; run `pull --workspace` afterwards to bring the
local copy in line.

### Someone `scp`ed over a tracked file

That happens on shared filesystems. datapin detects it on the next push and
**archives the foreign bytes** under their own content address before
proceeding, recording the fact in the journal. Nothing is silently destroyed,
and the event tells you it happened — which is how you find out at all.

### Reclaiming space

```console
$ datapin gc --keep 1        # keep 1 archived version per file (default 3)
```

`gc` runs across every workspace remote the manifest uses. The current version
never counts against the budget, so `--keep 1` still leaves you a rollback.
Reclaimed versions do not vanish from the journal — they stay listed and report
themselves **unrecoverable**, so a `revert --to 2` that can no longer work tells
you so instead of failing obscurely.

**`gc` is the only destructive command in this workflow.** Everything else adds.

## 5. When the results are finished

Nothing needs to move between tracks. Fill in the metadata and publish the same
dataset:

```console
$ datapin check counts       # now the metadata matters
$ datapin publish counts     # → DOI
```

The workspace history stays where it is. Read
[Your first DOI](../first-doi/) for the metadata you will need.

## What to do next

- Publishing the finished version → [Your first DOI](../first-doi/)
- Driving this from a job script → [Scripting and CI](../automation/)
- A push or pull failed → [Troubleshooting](../troubleshooting/)
