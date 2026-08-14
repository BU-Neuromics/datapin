# datapin documentation

The [README](../README.md) is the command reference — every command, flag, and
manifest field. This directory holds the task-shaped material: read it here on
GitHub, or in your checkout.

## Guides

Each one is a single sitting.

| Guide | When you want it |
|---|---|
| [Your first DOI](./guides/first-doi.md) | You have finished data and need something citable. |
| [Publishing a new version](./guides/new-version.md) | The data changed after you published it. |
| [Cluster to laptop](./guides/workspace.md) | Results that need to travel and keep history, but not a DOI. |
| [Leaving OSF](./guides/leaving-osf.md) | OSF is sunsetting its projects service and you need out. |
| [Institutional repositories](./guides/institutional.md) | Your target is your university's InvenioRDM or Dataverse, not Zenodo. |
| [Scripting and CI](./guides/automation.md) | Publishing from a pipeline, with no terminal to prompt. |

If you have a results folder and a deadline, read
[**Your first DOI**](./guides/first-doi.md) and nothing else.

## When something goes wrong

- [**Troubleshooting**](./troubleshooting.md) — every error datapin raises on
  purpose, what it means, and the one command that resolves it.

Offline, `datapin man` prints a man page generated from the live command tree
(`datapin man > /usr/local/share/man/man1/datapin.1`, then `man datapin`).

## Project history and internals

These are for contributors, not users:

- [`../ROADMAP.md`](../ROADMAP.md) — shipped releases and the ladder to v1.0
- [`../CHANGELOG.md`](../CHANGELOG.md) — what changed, per release
- [`reboot-plan.md`](./reboot-plan.md) — architecture and phases
- [`decisions.md`](./decisions.md) — every implementation decision, with rationale
- [`datapin-handoff.md`](./datapin-handoff.md) — operational decisions D1–D10
- [`zenodo-notes.md`](./zenodo-notes.md) — verified Zenodo/InvenioRDM API behaviors
- [`osf-api.md`](./osf-api.md) — OSF REST/Waterbutler notes (legacy surface)
- [`../CONTRIBUTING.md`](../CONTRIBUTING.md) — how to propose a change
- [`../CLAUDE.md`](../CLAUDE.md) — the development guide
