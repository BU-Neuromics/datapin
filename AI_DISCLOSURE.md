# AI Disclosure

This document describes how generative AI tools were used in the creation of
`datapin` and the role played by the human author.

## What was built with AI assistance

`datapin` was developed through an extended pair-programming session between
**Adam Labadorf** (BU Neuromics, Boston University) and
**Claude Code** (Anthropic's AI coding assistant, claude-sonnet-4-6).

Practically all of the source code — Go packages, command implementations,
tests, CI configuration, and documentation — was written by Claude Code in
response to direction from Adam. The AI assistant also proposed and implemented
the overall architecture, suggested library choices, diagnosed bugs, and drafted
this documentation.

## What the human author contributed

Adam Labadorf's role was that of **technical director and product owner**:

- **Conceived the project** — identified the gap left by the unmaintained Python
  `osfclient`, defined the scope as a single-binary Go CLI, and chose the target
  audience (researchers on HPC systems).
- **Made all product decisions** — determined which commands to include, the
  `datapin.toml` manifest design, conflict-handling semantics, token priority chain,
  JSON output contract, and the six-state sync model.
- **Set and enforced development standards** — mandated test-driven development
  (red-green-refactor), required regression tests for every bug fix, established
  the branching model, and reviewed each pull request before merging.
- **Provided architectural guidance** — chose the two-tier OSF API design
  (JSON:API metadata + Waterbutler file transfer), specified the interface
  boundaries between packages, and decided on testability requirements.
- **Reviewed and approved all code** — read diffs, caught issues, redirected
  approaches that didn't fit the project's goals, and merged or rejected each
  change.
- **Will maintain the project** — future bug fixes, feature additions, and
  security patches will continue under Adam's stewardship.

## Posture on AI-generated code

We believe transparency about AI involvement in open-source projects is
important. Researchers and contributors deserve to know the provenance of the
code they are building on or contributing to.

This project's use of AI assistance does not diminish its quality commitments:
the codebase is fully tested, statically analysed, and reviewed. All code is
held to the same standard regardless of who (or what) wrote the first draft.

If you have questions about any part of the implementation, open an issue —
the human maintainer is responsible for the code and will respond.
