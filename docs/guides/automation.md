---
title: Scripting and CI
---

# datapin in a script or a CI job

**You are here because:** you want a pipeline to publish or sync data — a
Snakemake rule, a Slurm job, a GitHub Actions workflow — with no terminal to
prompt and no keychain to unlock.

**You will end with:** commands that never block on a prompt, machine-readable
output, and meaningful exit codes.

**The one rule to internalize:** in JSON mode there are no prompts, so anything
that writes remote data demands that you state your intent with a flag. That is
not friction; it is the reason an unattended run cannot publish something
permanent by accident.

---

## 1. Get the token in without a keychain

CI runners and compute nodes have no OS keychain. Use the environment variable,
which is the highest-priority source in the ladder:

```bash
export DATAPIN_TOKEN_ZENODO="$ZENODO_TOKEN"     # for the remote named "zenodo"
```

The suffix is the **remote name, uppercased**. Full lookup order per remote:

1. `DATAPIN_TOKEN_<NAME>` environment variable
2. the OS keychain
3. `~/.config/datapin/tokens/<name>` (mode `0600`)

The two ladders are independent — do not conflate them:

| | Archive + workspace remotes | Legacy OSF |
|---|---|---|
| Source | `DATAPIN_TOKEN_<NAME>` → keychain → token file | `--token` → `OSF_TOKEN` → `~/.config/datapin/token` → keychain |
| Scope | one token per named remote | one token for OSF |

Per-kind credential notes: `s3` reuses the token slot as
`ACCESSKEY:SECRETKEY` (falling back to `AWS_ACCESS_KEY_ID` /
`AWS_SECRET_ACCESS_KEY`), and `sftp` uses `SSH_AUTH_SOCK` →
`~/.ssh/id_ed25519`/`id_rsa` → `DATAPIN_SFTP_PASSWORD`, with host keys verified
against `~/.ssh/known_hosts`.

If a remote's config is not on the runner at all, register it in the job — it is
one idempotent command, and `--no-keychain` keeps it off a keychain that does not
exist:

```bash
datapin remote add https://zenodo.org --name zenodo --no-keychain \
  --token-value "$ZENODO_TOKEN"
```

Tokens are never written to `config.toml` and never echoed into logs or error
output, so `config.toml` is safe to commit and a failed run is safe to paste
into an issue.

## 2. State your intent explicitly

In `--output=json` mode there are no prompts, so:

| Command | Required flag |
|---|---|
| `datapin publish` | `--yes` (or `--force` / `--reserve` / `--dry-run`) |
| bare `datapin push` (OSF manifest mode) | `--force` |
| `datapin rm`, `datapin wiki rm` | `--yes` |

A non-TTY run that omits them **refuses rather than hangs**, which is what you
want at 3 a.m. in a queue.

```bash
datapin publish counts --output=json --yes
```

Note the asymmetry, and that it is deliberate: `--yes` bypasses the confirmation
for *safe* actions. `--force` additionally authorizes publishing over a remote
version your pin has not seen. Neither one resolves a `DIVERGED` entry — only
`--resolve=ours|theirs` does that.

## 3. Read the output, not the log

`--output=json` puts structured JSON on **stdout**; activity logging goes to
**stderr** and is silenced in JSON mode unless you pass `-v`. So the two never
interleave and you can capture them separately:

```bash
datapin publish counts --output=json --yes >result.json 2>run.log
```

`publish` emits an array of objects, one per dataset:

```json
[{"slug":"counts","state":"PUBLISHED","record":"585302","version":2,
  "doi":"10.5281/zenodo.585302","concept_doi":"10.5281/zenodo.585299",
  "dry_run":false}]
```

`state` is the field to branch on: `PUBLISHED` (a new version exists),
`IN_SYNC` (nothing to publish — not an error), `DRY_RUN`, or `RESERVED` (uploaded
with a reserved DOI, still a draft). A run that publishes nothing because
nothing changed is a success, so check `state`, not just the exit code.

`check` emits `{slug, issues[], fair?}` per dataset:

```bash
datapin check --output=json | jq -e '[.[].issues[] | select(.severity=="error")] | length == 0'
```

`status` emits one row per tracked thing, with `kind` distinguishing them:

```json
[{"path":"counts","kind":"dataset","state":"IN_SYNC","declared_version":2}]
```

Colour is forced off in JSON mode as a hard invariant — machine output is never
coloured — so you never have to strip ANSI codes.

## 4. Use the exit codes

Everything exits non-zero on error. Two exit codes carry specific meaning and
are the ones worth branching on:

| Command | 0 | 1 |
|---|---|---|
| `datapin check` | no errors (warnings allowed) | at least one error — `publish` would refuse |
| `datapin status` | everything `IN_SYNC` | anything else, including `PIN_ONLY` and `DIVERGED` |
| `datapin migrate --output=json` | export done, no TODOs left | export succeeded, metadata TODOs remain |

`check` is the CI gate. It errors on exactly what `publish` refuses, so it
catches a metadata problem before you spend the upload:

```yaml
- name: Validate dataset metadata
  run: datapin check --output=json
```

`status` exiting 1 is normal for a repository with unpublished changes — do not
treat it as failure unless in-sync is what you are asserting.

## 5. A worked example

A GitHub Actions job that lints on every PR and publishes on a tag:

```yaml
name: data
on:
  pull_request:
  push:
    tags: ["data-v*"]

jobs:
  data:
    runs-on: ubuntu-latest
    env:
      DATAPIN_TOKEN_ZENODO: ${{ secrets.ZENODO_TOKEN }}
      DATAPIN_NO_UPDATE_CHECK: "1"
    steps:
      - uses: actions/checkout@v4

      - name: Install datapin
        run: curl -fsSL https://raw.githubusercontent.com/BU-Neuromics/datapin/main/install.sh | bash

      # Errors here are exactly what publish would refuse.
      - name: Lint metadata
        run: datapin check --output=json

      - name: Show the plan
        run: datapin publish --output=json --dry-run

      - name: Publish
        if: startsWith(github.ref, 'refs/tags/data-v')
        run: datapin publish counts --output=json --yes >publish.json

      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: datapin-output
          path: "*.json"
```

Two things this deliberately does **not** do:

- **It does not publish on every push.** Publishing is permanent and public;
  gate it on something a human did on purpose, like a tag.
- **It does not commit the re-pinned manifest back.** `publish` rewrites
  `.datapin/datapin.toml` with the new record, version, and DOIs, and that
  rewrite is the record of what you published. Either commit it from the job
  deliberately, or publish from a checkout you then commit by hand — but do not
  let it be discarded with the runner.

## 6. Flags for unattended runs

| Flag / variable | Why |
|---|---|
| `--output=json` | structured stdout, no prompts, no colour, logs silenced |
| `--quiet` / `-q` | errors only (conflicts with `-v`) |
| `-v` / `-vv` | debug detail / HTTP traces — for a failing job, `-vv` on stderr is the thing to read |
| `--dry-run` | the full plan without moving bytes; on `publish` it satisfies the JSON intent requirement |
| `--jobs N` / `-j N` | bound remote-scan concurrency on `status`/`sync`/bare `push`/`pull` (default 8) |
| `DATAPIN_NO_UPDATE_CHECK=1` | suppress the daily "new release available" check |

The update check is already off under `--output=json`, `--quiet`, a non-TTY
stderr, and a `dev` build — set the variable anyway to be explicit about a job
that should make no incidental network calls.

Progress bars are opt-in (`-p`) and only draw on an interactive, non-JSON
stderr, so a CI log gets clean one-line summaries without your doing anything.

## 7. Cancellation

Ctrl-C — and the `SIGTERM` a scheduler sends at a wall-clock limit — cancels
in-flight requests and cleans up: a partially downloaded file is removed, and a
publish interrupted before its final step discards the draft rather than leaving
a half-record. A job killed by the queue does not leave you a mess to reconcile.

## What to do next

- Something failed and you want the meaning of the message →
  [Troubleshooting](../troubleshooting/)
- Teaching an AI coding agent the CLI: `npx skills add BU-Neuromics/datapin`
